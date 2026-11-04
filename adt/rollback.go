package adt

import (
	"context"
	"fmt"
	"strings"
)

// RollbackEntry is one object processed by RollbackTransport.
type RollbackEntry struct {
	Type   string `json:"type"`
	Name   string `json:"name"`
	Reason string `json:"reason,omitempty"`
}

// RollbackResult reports the outcome of RollbackTransport, partitioning the
// transport's objects into those restored, skipped, failed and created, and,
// with RollbackOptions.DeleteCreatedObjects, deleted.
type RollbackResult struct {
	Restored []RollbackEntry `json:"restored"`
	Skipped  []RollbackEntry `json:"skipped"`
	Failed   []RollbackEntry `json:"failed"`
	// Created lists the objects the transport created (see
	// RollbackTransportWithOptions for how that is decided) that were left in
	// place: because DeleteCreatedObjects was off, or because a restore failed
	// and deleting them could break the object that was not restored.
	Created []RollbackEntry `json:"created"`
	// Deleted lists the objects the transport created that were deleted.
	Deleted []RollbackEntry `json:"deleted"`
}

// RollbackOptions changes what RollbackTransportWithOptions does beyond
// restoring source.
type RollbackOptions struct {
	// DeleteCreatedObjects deletes the objects the transport created, instead
	// of reporting them in RollbackResult.Created. The deletion is recorded in
	// the rolled-back request, like the restored source. It cannot be undone.
	DeleteCreatedObjects bool
}

// rollbackSourceTypes is the set of object types RollbackTransport restores
// source for. DDIC types (TABL, DTEL, ...) are intentionally excluded and
// skipped — they have no source to roll back the same way.
var rollbackSourceTypes = map[string]bool{
	"PROG": true,
	"CLAS": true,
	"INTF": true,
	"FUGR": true,
}

// Reasons recorded on RollbackResult.Created entries.
const (
	rollbackReasonCreatedKept       = "created by this transport; left in place (DeleteCreatedObjects is off)"
	rollbackReasonCreatedRestoreErr = "created by this transport; not deleted because a restore in this rollback failed and the unrestored object may still use it"
	rollbackReasonCreatedSubObjects = "created by this transport; not deleted because the transport has sub-object (LIMU) entries that were not rolled back and may still use it"
)

// RollbackTransport is RollbackTransportWithOptions with zero options: it
// restores source and deletes nothing.
func (c *httpClient) RollbackTransport(ctx context.Context, transport string) (*RollbackResult, error) {
	return c.RollbackTransportWithOptions(ctx, transport, RollbackOptions{})
}

// RollbackTransportWithOptions restores every source object in a transport to
// its version immediately before the transport. For each R3TR
// PROG/CLAS/INTF/FUGR object it reads the version history, finds the
// pre-transport version, and writes that source back (lock → set → activate).
// Non-source objects (TABL, DTEL, …) and non-R3TR entries are skipped.
//
// An object counts as created by the transport when its version history is
// not empty and every entry in it carries the request or one of its tasks
// (for a class: every entry of every include). Such an object has nothing to
// restore. It is reported in Created, or, with opts.DeleteCreatedObjects,
// deleted and reported in Deleted. An object with any entry outside the
// transport, including a local entry without a request, is never deleted. An
// object without any version history (for example one created but never
// activated) cannot be told apart from one whose history is missing, so it is
// reported as failed, not as created. Deletion covers the same source types
// as the restore; a DDIC object the transport created stays Skipped.
//
// Before deleting, the object's TADIR source system must be the request's
// system (the first three characters of the request number). An object
// imported from elsewhere — a second development system, an add-on, a
// modified SAP object — gets no version on import by default, so a change in
// this request leaves a history that looks created; it stays in Created. So
// does an object whose source system cannot be read. What the rule still
// cannot see is an object that existed in this system before the transport
// without any version outside it, such as a local ($TMP) object without
// versions that was then moved into a transportable package under this
// request. Deleting it destroys its pre-transport state, which is one reason
// deletion is opt-in.
//
// Deletion runs after every restore, so restored code no longer references
// the objects about to be deleted, and only when every part of the transport
// was rolled back: nothing is deleted when a restore failed or when the
// transport has sub-object (LIMU) entries, which are skipped (#134), since
// the code left unrestored may still use a created object. Created objects
// that use each other are deleted in rounds until a round deletes nothing;
// what is left is reported as failed with the last error SAP gave. An object
// SAP reports as not found is counted as deleted. The deletion is recorded in
// the rolled-back request (as corrNr), the same request the restored source
// is written to.
//
// A transport recorded at sub-object granularity (e.g. a LIMU/METH row for a
// method, rather than the R3TR class that owns it) matches neither the R3TR
// filter nor rollbackSourceTypes, so every one of its entries is Skipped and
// the call returns an all-skipped RollbackResult with a nil error — not a
// signal that nothing needed restoring. Whether a transport is recorded this
// way is a property of what SAP wrote into it, not of which system family
// produced it or which path read it back (a live comparison of the ADT XML
// path and the E071 query path on the same S/4 request found them agreeing on
// granularity row-for-row — see getTransportObjectsViaQuery's doc comment).
// Mapping sub-object types to their carrying R3TR object was considered and
// deferred: see https://github.com/Hochfrequenz/adtler/issues/134.
//
// This is destructive: it overwrites current source with historical versions.
//
// Once SAP answers a request with 401, nothing more is sent (#222): reading the
// task numbers returns the error, and every object not yet processed is
// reported as failed with that error, without a request of its own.
func (c *httpClient) RollbackTransportWithOptions(ctx context.Context, transport string, opts RollbackOptions) (*RollbackResult, error) {
	objects, err := c.GetTransportObjects(ctx, transport)
	if err != nil {
		return nil, fmt.Errorf("reading transport objects: %w", err)
	}

	// On S/4HANA, version history (VRSD) records the task number rather than the
	// request number. Fetch all task numbers so findPreTransportVersion can match
	// either the request or any of its tasks.
	transports := []string{transport}
	tasks, taskErr := c.GetTransportTasks(ctx, transport)
	switch {
	case taskErr == nil:
		transports = append(transports, tasks...)
	case isUnauthorized(taskErr):
		return nil, fmt.Errorf("reading transport tasks: %w", taskErr)
	}

	result := &RollbackResult{}
	var created []createdObject
	var rejected error  // the 401 that ended processing, if any
	subObjects := false // a LIMU entry was skipped, so part of the transport was not rolled back
	for _, obj := range objects {
		if obj.PgmID != "R3TR" {
			if obj.PgmID == "LIMU" {
				subObjects = true
			}
			result.Skipped = append(result.Skipped, RollbackEntry{
				Type: obj.Type, Name: obj.Name, Reason: "not R3TR",
			})
			continue
		}
		if !rollbackSourceTypes[obj.Type] {
			result.Skipped = append(result.Skipped, RollbackEntry{
				Type: obj.Type, Name: obj.Name, Reason: "non-source object type",
			})
			continue
		}
		objectURI, err := ObjectURI(obj.Type, obj.Name)
		if err != nil {
			result.Skipped = append(result.Skipped, RollbackEntry{
				Type: obj.Type, Name: obj.Name, Reason: err.Error(),
			})
			continue
		}
		if rejected != nil {
			result.Failed = append(result.Failed, RollbackEntry{
				Type: obj.Type, Name: obj.Name, Reason: "not attempted: " + rejected.Error(),
			})
			continue
		}
		isCreated, err := c.rollbackOrClassify(ctx, objectURI, transports)
		switch {
		case err != nil:
			if isUnauthorized(err) {
				rejected = err
			}
			result.Failed = append(result.Failed, RollbackEntry{
				Type: obj.Type, Name: obj.Name, Reason: err.Error(),
			})
		case isCreated:
			created = append(created, createdObject{
				entry: RollbackEntry{Type: obj.Type, Name: obj.Name}, uri: objectURI,
			})
		default:
			result.Restored = append(result.Restored, RollbackEntry{
				Type: obj.Type, Name: obj.Name,
			})
		}
	}

	switch {
	case len(created) == 0:
	case !opts.DeleteCreatedObjects:
		result.Created = createdEntries(created, rollbackReasonCreatedKept)
	case rejected != nil:
		for _, o := range created {
			o.entry.Reason = "created by this transport; not deleted: " + rejected.Error()
			result.Failed = append(result.Failed, o.entry)
		}
	case len(result.Failed) > 0:
		result.Created = createdEntries(created, rollbackReasonCreatedRestoreErr)
	case subObjects:
		result.Created = createdEntries(created, rollbackReasonCreatedSubObjects)
	default:
		local := c.keepLocalOrigin(ctx, created, transport, result)
		c.deleteCreatedObjects(ctx, local, transport, result)
	}
	return result, nil
}

// createdObject is an object RollbackTransportWithOptions found created by the
// transport, with the error of its last delete attempt.
type createdObject struct {
	entry      RollbackEntry
	uri        string
	lastErr    error
	notRetried bool // a 401 stopped the rollback before this object's next attempt
}

// keepLocalOrigin returns the objects of objs whose TADIR source system is
// the system the request belongs to, which is the first three characters of
// the request number. Every other object goes to result.Created, never to
// deletion: an object imported from another system (a second development
// system, an add-on, a modified SAP object) gets no version on import by
// default, so a change to it in this request leaves a history that looks
// created. An object whose origin cannot be read is not deleted either.
// After a 401 nothing more is sent (#222) and every object not yet confirmed
// is reported as failed.
func (c *httpClient) keepLocalOrigin(ctx context.Context, objs []createdObject, request string, result *RollbackResult) []createdObject {
	sid := ""
	if len(request) >= 3 {
		sid = strings.ToUpper(request[:3])
	}
	var local []createdObject
	var rejected error
	for _, o := range objs {
		if rejected != nil {
			o.entry.Reason = "created by this transport; not deleted: " + rejected.Error()
			result.Failed = append(result.Failed, o.entry)
			continue
		}
		src, err := c.tadirSourceSystem(ctx, o.entry.Type, o.entry.Name)
		switch {
		case isUnauthorized(err):
			rejected = err
			o.entry.Reason = "created by this transport; not deleted: " + err.Error()
			result.Failed = append(result.Failed, o.entry)
		case err != nil:
			o.entry.Reason = "created by this transport by its version history; not deleted because its original system could not be read: " + err.Error()
			result.Created = append(result.Created, o.entry)
		case sid == "" || src != sid:
			o.entry.Reason = "version history shows only this transport, but the object originates in system " + src + ", not in the request's system; not deleted"
			result.Created = append(result.Created, o.entry)
		default:
			local = append(local, o)
		}
	}
	return local
}

// tadirSourceSystem reads the source system (TADIR-SRCSYSTEM) of the R3TR
// object objType/name.
func (c *httpClient) tadirSourceSystem(ctx context.Context, objType, name string) (string, error) {
	qr, err := c.RunQuery(ctx, fmt.Sprintf(
		"SELECT SRCSYSTEM FROM TADIR WHERE PGMID = 'R3TR' AND OBJECT = '%s' AND OBJ_NAME = '%s'",
		EscapeValue(objType), EscapeValue(name)), 1)
	if err != nil {
		return "", err
	}
	if len(qr.Rows) == 0 || len(qr.Rows[0]) == 0 {
		return "", fmt.Errorf("no TADIR entry")
	}
	return strings.ToUpper(strings.TrimSpace(qr.Rows[0][0])), nil
}

func createdEntries(objs []createdObject, reason string) []RollbackEntry {
	entries := make([]RollbackEntry, len(objs))
	for i, o := range objs {
		entries[i] = o.entry
		entries[i].Reason = reason
	}
	return entries
}

// deleteCreatedObjects deletes objs in rounds, recording each deletion in
// request, until every object is gone or a round deletes nothing. A created
// object may be in use by another one the same transport created, and SAP may
// refuse to delete it while that one exists; the next round retries it. What
// is left is reported as failed with its last error. After a 401 nothing more
// is sent (#222).
func (c *httpClient) deleteCreatedObjects(ctx context.Context, objs []createdObject, request string, result *RollbackResult) {
	pending := objs
	var rejected error
	for len(pending) > 0 && rejected == nil {
		var next []createdObject
		for _, o := range pending {
			if rejected != nil {
				o.notRetried = true
				next = append(next, o)
				continue
			}
			err := c.DeleteObject(ctx, o.uri, "", request)
			if ClassifyError(err) == ErrorNotFound {
				// Already gone: the state the deletion was to reach.
				err = nil
			}
			if err != nil {
				if isUnauthorized(err) {
					rejected = err
				}
				o.lastErr = err
				next = append(next, o)
				continue
			}
			result.Deleted = append(result.Deleted, o.entry)
		}
		progress := len(next) < len(pending)
		pending = next
		if !progress {
			break
		}
	}
	for _, o := range pending {
		switch {
		case o.lastErr != nil && o.notRetried:
			o.entry.Reason = "created by this transport; delete: " + o.lastErr.Error() + "; not retried: " + rejected.Error()
		case o.lastErr != nil:
			o.entry.Reason = "created by this transport; delete: " + o.lastErr.Error()
		default:
			o.entry.Reason = "created by this transport; delete not attempted: " + rejected.Error()
		}
		result.Failed = append(result.Failed, o.entry)
	}
}

// createdByTransport reports whether versions shows an object the transport
// created: the history is not empty, and every entry in it carries the request
// or one of its tasks. An entry without a request (a local version) counts as
// outside the transport. Matching is case-insensitive, like
// findPreTransportVersion.
func createdByTransport(versions []VersionInfo, transports []string) bool {
	if len(versions) == 0 {
		return false
	}
	set := make(map[string]bool, len(transports))
	for _, t := range transports {
		set[strings.ToUpper(t)] = true
	}
	for _, v := range versions {
		if !set[strings.ToUpper(v.Transport)] {
			return false
		}
	}
	return true
}

// findPreTransportVersion returns the ContentURI of the version immediately
// preceding any of the given transport identifiers in the version history.
// transports[0] is the request number; subsequent entries are its task numbers.
// The history is ordered newest-first, so the entry after the transport's own
// version is the object's state before the transport changed it. It errors if
// none of the identifiers are found, or if no earlier version exists (the object
// was created by this transport).
//
// S/4HANA records task numbers in VRSD rather than the request number, so callers
// must pass both the request and its tasks to handle both R/3 and S/4.
func findPreTransportVersion(versions []VersionInfo, transports []string) (string, error) {
	set := make(map[string]bool, len(transports))
	for _, t := range transports {
		set[strings.ToUpper(t)] = true
	}
	seenTransport := false
	restoreURI := ""
	for _, v := range versions {
		// Case-insensitive for the same reason matchesTransportNumber
		// (transport.go) is: RollbackTransport passes the caller's transport
		// string through unchanged, while v.Transport comes from the server
		// and is always uppercase. A lowercase caller value used to resolve
		// GetTransportObjects correctly (that path was already
		// case-insensitive) and then fail to match here, so every object came
		// back Failed instead of Restored.
		if set[strings.ToUpper(v.Transport)] {
			seenTransport = true
			continue
		}
		if seenTransport {
			restoreURI = v.ContentURI
			break
		}
	}
	if !seenTransport {
		return "", fmt.Errorf("transport %s not found in version history", transports[0])
	}
	// An empty ContentURI is treated the same as no earlier version at all.
	if restoreURI == "" {
		return "", fmt.Errorf("no version before transport %s (object may have been created by this transport)", transports[0])
	}
	return restoreURI, nil
}

// rollbackOrClassify reads objectURI's version history and either restores
// the object to its pre-transport version or, when the transport created it
// (see createdByTransport), reports created == true and changes nothing.
// transports is the request number plus all its task numbers.
func (c *httpClient) rollbackOrClassify(ctx context.Context, objectURI string, transports []string) (created bool, err error) {
	versions, err := c.GetVersionHistory(ctx, objectURI)
	if err != nil {
		return false, fmt.Errorf("get version history: %w", err)
	}
	if len(versions) == 0 {
		return false, fmt.Errorf("no version history, so whether transport %s created the object cannot be told (it may never have been activated)", transports[0])
	}
	if createdByTransport(versions, transports) {
		return true, nil
	}
	restoreURI, err := findPreTransportVersion(versions, transports)
	if err != nil {
		return false, err
	}
	return false, c.restoreVersion(ctx, objectURI, restoreURI, transports[0])
}

// restoreVersion writes the source at restoreURI back to objectURI under
// request and activates it.
//
// After a 401 it sends nothing more, not even the release of its lock: the
// release would carry the rejected credential as well (#222).
func (c *httpClient) restoreVersion(ctx context.Context, objectURI, restoreURI, request string) error {
	oldSource, err := c.GetVersionSource(ctx, restoreURI)
	if err != nil {
		return fmt.Errorf("get version source: %w", err)
	}

	lockHandle, err := c.LockObject(ctx, objectURI)
	if err != nil {
		return fmt.Errorf("lock: %w", err)
	}

	current, err := c.GetSource(ctx, objectURI)
	if err != nil {
		c.unlockUnlessUnauthorized(ctx, objectURI, lockHandle, err)
		return fmt.Errorf("get current source: %w", err)
	}

	// request is the request number, not a task; pass it as corrNr so both R/3
	// and S/4 accept the write (S/4 rejects SetSource when corrNr is absent).
	if _, err := c.SetSource(ctx, objectURI, oldSource, lockHandle, request, current.ETag); err != nil {
		c.unlockUnlessUnauthorized(ctx, objectURI, lockHandle, err)
		return fmt.Errorf("set source: %w", err)
	}
	// Unlock before activation: on S/4, the ESRDIRE session lock left by
	// SetSource blocks ActivateObjects if not released first. Only a 401
	// stops the rollback here; any other unlock failure is left to show up
	// in the activation, as before.
	if err := c.UnlockObject(ctx, objectURI, lockHandle); isUnauthorized(err) {
		return fmt.Errorf("source written but not activated, unlock: %w", err)
	}

	actResult, err := c.ActivateObjects(ctx, []string{objectURI})
	if err != nil {
		return fmt.Errorf("activate: %w", err)
	}
	if !actResult.Success {
		msgs := make([]string, len(actResult.Messages))
		for i, m := range actResult.Messages {
			msgs[i] = m.Text
		}
		return fmt.Errorf("activation failed: %s", strings.Join(msgs, "; "))
	}
	return nil
}

// unlockUnlessUnauthorized releases lockHandle after cause made a step fail,
// as best effort, unless cause is a 401: the release would carry the rejected
// credential too (#222).
func (c *httpClient) unlockUnlessUnauthorized(ctx context.Context, objectURI, lockHandle string, cause error) {
	if !isUnauthorized(cause) {
		_ = c.UnlockObject(ctx, objectURI, lockHandle)
	}
}
