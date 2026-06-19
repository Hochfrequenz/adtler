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
// transport's objects into those restored, skipped, and failed.
type RollbackResult struct {
	Restored []RollbackEntry `json:"restored"`
	Skipped  []RollbackEntry `json:"skipped"`
	Failed   []RollbackEntry `json:"failed"`
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

// RollbackTransport restores every source object in a transport to its version
// immediately before the transport. For each R3TR PROG/CLAS/INTF/FUGR object it
// reads the version history, finds the pre-transport version, and writes that
// source back (lock → set → activate). Non-source objects (TABL, DTEL, …) and
// non-R3TR entries are skipped. Objects created by the transport (no prior
// version) are reported as failed.
//
// This is destructive: it overwrites current source with historical versions.
func (c *httpClient) RollbackTransport(ctx context.Context, transport string) (*RollbackResult, error) {
	objects, err := c.GetTransportObjects(ctx, transport)
	if err != nil {
		return nil, fmt.Errorf("reading transport objects: %w", err)
	}

	// On S/4HANA, version history (VRSD) records the task number rather than the
	// request number. Fetch all task numbers so findPreTransportVersion can match
	// either the request or any of its tasks.
	transports := []string{transport}
	if tasks, taskErr := c.GetTransportTasks(ctx, transport); taskErr == nil {
		transports = append(transports, tasks...)
	}

	result := &RollbackResult{}
	for _, obj := range objects {
		if obj.PgmID != "R3TR" {
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
		if err := c.rollbackObject(ctx, objectURI, transports); err != nil {
			result.Failed = append(result.Failed, RollbackEntry{
				Type: obj.Type, Name: obj.Name, Reason: err.Error(),
			})
		} else {
			result.Restored = append(result.Restored, RollbackEntry{
				Type: obj.Type, Name: obj.Name,
			})
		}
	}
	return result, nil
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
		set[t] = true
	}
	seenTransport := false
	restoreURI := ""
	for _, v := range versions {
		if set[v.Transport] {
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

// rollbackObject restores a single object's source to its pre-transport version.
// transports is the request number plus all its task numbers (to handle both R/3
// and S/4, which record different identifiers in version history).
func (c *httpClient) rollbackObject(ctx context.Context, objectURI string, transports []string) error {
	versions, err := c.GetVersionHistory(ctx, objectURI)
	if err != nil {
		return fmt.Errorf("get version history: %w", err)
	}

	restoreURI, err := findPreTransportVersion(versions, transports)
	if err != nil {
		return err
	}

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
		_ = c.UnlockObject(ctx, objectURI, lockHandle)
		return fmt.Errorf("get current source: %w", err)
	}

	// transports[0] is the request number; pass it as corrNr so both R/3 and S/4
	// accept the write (S/4 rejects SetSource when corrNr is absent).
	if _, err := c.SetSource(ctx, objectURI, oldSource, lockHandle, transports[0], current.ETag); err != nil {
		_ = c.UnlockObject(ctx, objectURI, lockHandle)
		return fmt.Errorf("set source: %w", err)
	}
	// Unlock before activation: on S/4, the ESRDIRE session lock left by
	// SetSource blocks ActivateObjects if not released first.
	_ = c.UnlockObject(ctx, objectURI, lockHandle)

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
