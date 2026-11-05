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

// rollbackSourceTypes is the set of R3TR object types RollbackTransport
// restores source for. DDIC types (TABL, DTEL, ...) are intentionally excluded and
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
	rollbackReasonCreatedSubObjects = "created by this transport; not deleted because part of the transport was not rolled back (a sub-object entry without source, or a created function module or function group include) and may still use it"
	rollbackReasonCreatedSubObject  = "created by this transport; left in place (a rollback deletes only TADIR objects, not function modules or function group includes)"
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
// Non-source objects (TABL, DTEL, …) are skipped.
//
// A transport usually records only the parts of an object it changed: a
// LIMU METH entry for a method rather than R3TR CLAS for the class, which is
// what a released workbench request normally holds (#134). Such an entry is
// mapped to the unit of source that has its own version history (see
// resolveRollbackTarget): a method or section to its class, a program part
// to the main program, standalone include or function group include, a
// function module to itself. Each unit is restored once however many entries
// lead to it, and is reported under that unit's type and name. A sub-object
// without source (screen, GUI status, text pool) is Skipped; a CORR entry, a
// release marker rather than an object, is left out of the result.
//
// A class is restored include by include: the global source (sections and
// methods) from the history of its main include, and each local include
// from its own, written back to that include. An include the transport did
// not touch is left alone. Rolling back one method therefore restores the
// whole global class source to its state before the transport.
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
// was rolled back: nothing is deleted when a restore failed, when a
// sub-object entry was skipped, or when the transport created a function
// module or function group include (which a rollback never deletes), since
// the code left unrestored may still use a created object. Created objects
// that use each other are deleted in rounds until a round deletes nothing;
// what is left is reported as failed with the last error SAP gave. An object
// SAP reports as not found is counted as deleted. The deletion is recorded in
// the rolled-back request (as corrNr), the same request the restored source
// is written to.
//
// This is destructive: it overwrites current source with historical versions.
//
// Once SAP answers a request with 401, nothing more is sent (#222): reading the
// task numbers returns the error, and every entry not yet resolved and every
// object not yet processed is reported as failed with that error, without a
// request of its own.
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
	targets, partial, rejected := c.rollbackTargets(ctx, objects, result)

	var created []createdObject
	for _, tg := range targets {
		obj := tg.entry
		if rejected != nil {
			result.Failed = append(result.Failed, RollbackEntry{
				Type: obj.Type, Name: obj.Name, Reason: "not attempted: " + rejected.Error(),
			})
			continue
		}
		isCreated, err := c.rollbackOrClassify(ctx, tg.uri, transports)
		switch {
		case err != nil:
			if isUnauthorized(err) {
				rejected = err
			}
			result.Failed = append(result.Failed, RollbackEntry{
				Type: obj.Type, Name: obj.Name, Reason: err.Error(),
			})
		case isCreated && !tg.tadir:
			// A function module or function group include is not deleted by
			// a rollback, so it stays as it is, and so does everything it
			// may use.
			partial = true
			result.Created = append(result.Created, RollbackEntry{
				Type: obj.Type, Name: obj.Name, Reason: rollbackReasonCreatedSubObject,
			})
		case isCreated:
			created = append(created, createdObject{entry: obj, uri: tg.uri})
		default:
			result.Restored = append(result.Restored, obj)
		}
	}

	switch {
	case len(created) == 0:
	case !opts.DeleteCreatedObjects:
		result.Created = append(result.Created, createdEntries(created, rollbackReasonCreatedKept)...)
	case rejected != nil:
		for _, o := range created {
			o.entry.Reason = "created by this transport; not deleted: " + rejected.Error()
			result.Failed = append(result.Failed, o.entry)
		}
	case len(result.Failed) > 0:
		result.Created = append(result.Created, createdEntries(created, rollbackReasonCreatedRestoreErr)...)
	case partial:
		result.Created = append(result.Created, createdEntries(created, rollbackReasonCreatedSubObjects)...)
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
	writes, err := planRestore(versions, transports)
	if err != nil {
		return false, err
	}
	return false, c.restoreVersion(ctx, objectURI, writes, transports[0])
}

// sourceWrite is one source a restore writes back: the version at contentURI,
// into the class include named include, or into the object's main source
// when include is "" or classIncludeMain.
type sourceWrite struct {
	include    string
	contentURI string
}

// planRestore picks the version to write back for each part of the object
// the transport changed.
//
// A class keeps a separate history per include (VersionInfo.Include), so
// each include is matched on its own and written back to itself: the
// pre-transport version of one include is never the previous state of
// another. An include the transport did not touch is left alone. If an
// include the transport touched has no earlier version (the transport
// created it, e.g. the first local test class), nothing is written, because
// writing back the other includes alone would leave a class that may not
// compile.
func planRestore(versions []VersionInfo, transports []string) ([]sourceWrite, error) {
	byInclude := make(map[string][]VersionInfo)
	var order []string
	for _, v := range versions {
		if _, ok := byInclude[v.Include]; !ok {
			order = append(order, v.Include)
		}
		byInclude[v.Include] = append(byInclude[v.Include], v)
	}
	var writes []sourceWrite
	for _, include := range order {
		history := byInclude[include]
		if !historyHasTransport(history, transports) {
			continue
		}
		contentURI, err := findPreTransportVersion(history, transports)
		if err != nil {
			if include != "" {
				return nil, fmt.Errorf("include %s: %w", include, err)
			}
			return nil, err
		}
		writes = append(writes, sourceWrite{include: include, contentURI: contentURI})
	}
	if len(writes) == 0 {
		return nil, fmt.Errorf("transport %s not found in version history", transports[0])
	}
	return writes, nil
}

// historyHasTransport reports whether any entry of versions carries the
// request or one of its tasks.
func historyHasTransport(versions []VersionInfo, transports []string) bool {
	for _, v := range versions {
		for _, t := range transports {
			if strings.EqualFold(v.Transport, t) {
				return true
			}
		}
	}
	return false
}

// restoreVersion writes each version in writes back to objectURI under
// request, then activates the object once.
//
// After a 401 it sends nothing more, not even the release of its lock: the
// release would carry the rejected credential as well (#222).
func (c *httpClient) restoreVersion(ctx context.Context, objectURI string, writes []sourceWrite, request string) error {
	sources := make([]string, len(writes))
	for i, w := range writes {
		src, err := c.GetVersionSource(ctx, w.contentURI)
		if err != nil {
			return fmt.Errorf("get version source%s: %w", includeSuffix(w.include), err)
		}
		sources[i] = src
	}

	// Read the main source's ETag before locking. On SAP_BASIS 750 a GET of
	// a class's source between LockObject and SetSource ends the stateful
	// session the lock lives in, and SetSource then fails with 423
	// ExceptionResourceInvalidLockHandle (measured on the ECC system; a
	// program's lock survives it). The lock still guards the write itself.
	etag := ""
	for _, w := range writes {
		if w.include == "" || w.include == classIncludeMain {
			current, err := c.GetSource(ctx, objectURI)
			if err != nil {
				return fmt.Errorf("get current source: %w", err)
			}
			etag = current.ETag
			break
		}
	}

	lockHandle, err := c.LockObject(ctx, objectURI)
	if err != nil {
		return fmt.Errorf("lock: %w", err)
	}

	for i, w := range writes {
		if err := c.writeSource(ctx, objectURI, w.include, sources[i], lockHandle, request, etag); err != nil {
			c.unlockUnlessUnauthorized(ctx, objectURI, lockHandle, err)
			if i > 0 {
				return fmt.Errorf("%w (earlier includes were written back but not activated)", err)
			}
			return err
		}
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

// writeSource writes source into the main source of objectURI, or into its
// class include include, under the held lock. etag is the main source's ETag,
// read before the lock was taken.
func (c *httpClient) writeSource(ctx context.Context, objectURI, include, source, lockHandle, request, etag string) error {
	if include != "" && include != classIncludeMain {
		// A held lock replaces If-Match for includes (see SetIncludeSource).
		if _, err := c.SetIncludeSource(ctx, objectURI, include, source, lockHandle, request, ""); err != nil {
			return fmt.Errorf("set source%s: %w", includeSuffix(include), err)
		}
		return nil
	}
	// request is the request number, not a task; pass it as corrNr so both R/3
	// and S/4 accept the write (S/4 rejects SetSource when corrNr is absent).
	if _, err := c.SetSource(ctx, objectURI, source, lockHandle, request, etag); err != nil {
		return fmt.Errorf("set source: %w", err)
	}
	return nil
}

func includeSuffix(include string) string {
	if include == "" {
		return ""
	}
	return " (include " + include + ")"
}

// unlockUnlessUnauthorized releases lockHandle after cause made a step fail,
// as best effort, unless cause is a 401: the release would carry the rejected
// credential too (#222).
func (c *httpClient) unlockUnlessUnauthorized(ctx context.Context, objectURI, lockHandle string, cause error) {
	if !isUnauthorized(cause) {
		_ = c.UnlockObject(ctx, objectURI, lockHandle)
	}
}

// rollbackTargets resolves the transport's entries to the units of source the
// rollback restores (see resolveRollbackTarget), in transport order and each
// once. Entries without such a unit go to result.Skipped, failed lookups to
// result.Failed. CORR entries (release markers) are left out. partial reports
// that a LIMU entry was skipped or failed, so part of the transport will not
// be rolled back. A 401 ends the lookups (#222) and is returned as rejected;
// the entries not yet looked up go to result.Failed.
func (c *httpClient) rollbackTargets(ctx context.Context, objects []TransportObject, result *RollbackResult) (targets []rollbackTarget, partial bool, rejected error) {
	seen := make(map[string]bool)
	for _, obj := range objects {
		if obj.PgmID == pgmIDCorr {
			continue
		}
		if rejected != nil {
			result.Failed = append(result.Failed, RollbackEntry{
				Type: obj.Type, Name: obj.Name, Reason: "not attempted: " + rejected.Error(),
			})
			continue
		}
		tg, reason, ok, err := c.resolveRollbackTarget(ctx, obj)
		switch {
		case err != nil:
			if isUnauthorized(err) {
				rejected = err
			}
			partial = partial || obj.PgmID == pgmIDLIMU
			result.Failed = append(result.Failed, RollbackEntry{
				Type: obj.Type, Name: obj.Name, Reason: "resolving the object that carries this entry: " + err.Error(),
			})
		case !ok:
			partial = partial || obj.PgmID == pgmIDLIMU
			result.Skipped = append(result.Skipped, RollbackEntry{
				Type: obj.Type, Name: obj.Name, Reason: reason,
			})
		case !seen[strings.ToLower(tg.uri)]:
			seen[strings.ToLower(tg.uri)] = true
			targets = append(targets, tg)
		}
	}
	return targets, partial, rejected
}
