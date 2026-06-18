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
		if err := c.rollbackObject(ctx, objectURI, transport); err != nil {
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
// preceding the given transport in the version history. The history is ordered
// newest-first, so the entry after the transport's own version is the object's
// state before the transport changed it. It errors if the transport is absent
// from the history, or if no earlier version exists (the object was created by
// this transport).
func findPreTransportVersion(versions []VersionInfo, transport string) (string, error) {
	seenTransport := false
	for _, v := range versions {
		if v.Transport == transport {
			seenTransport = true
			continue
		}
		if seenTransport {
			return v.ContentURI, nil
		}
	}
	if !seenTransport {
		return "", fmt.Errorf("transport %s not found in version history", transport)
	}
	return "", fmt.Errorf("no version before transport %s (object may have been created by this transport)", transport)
}

// rollbackObject restores a single object's source to its pre-transport version.
func (c *httpClient) rollbackObject(ctx context.Context, objectURI, transport string) error {
	versions, err := c.GetVersionHistory(ctx, objectURI)
	if err != nil {
		return fmt.Errorf("get version history: %w", err)
	}

	restoreURI, err := findPreTransportVersion(versions, transport)
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
	defer func() { _ = c.UnlockObject(ctx, objectURI, lockHandle) }()

	current, err := c.GetSource(ctx, objectURI)
	if err != nil {
		return fmt.Errorf("get current source: %w", err)
	}

	if _, err := c.SetSource(ctx, objectURI, oldSource, lockHandle, "", current.ETag); err != nil {
		return fmt.Errorf("set source: %w", err)
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
