//go:build integration

package adt_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
)

// TestADTError_EnqueueLockProperties_MultiSystem verifies issue #56: before
// the fix, ADTError discarded the <properties> map (and the T100 message-key
// it carries) on every exception, regardless of body content. A same-object
// lock collision from a second session is a reliable, system-independent way
// to trigger SAP's structured "currently editing" exception
// (ExceptionResourceNoAccess, T100KEY EU/510 — see aibap.mcp#378) without
// depending on any object-specific fixture state.
//
// Two independent adt.Client instances against the same system (distinct
// HTTP sessions/cookie jars) simulate the two-user collision that a single
// session cannot reproduce.
func TestADTError_EnqueueLockProperties_MultiSystem(t *testing.T) {
	ctx := context.Background()

	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			holder := sys.Client
			contender := adt.NewClient(sys.Config)

			handle, err := holder.LockObject(ctx, testReportURI)
			if err != nil {
				t.Fatalf("[%s] first-session LockObject failed: %v", sys.Name, err)
			}
			defer func() {
				if err := holder.UnlockObject(context.Background(), testReportURI, handle); err != nil {
					t.Logf("[%s] WARNING: cleanup UnlockObject failed: %v", sys.Name, err)
				}
			}()

			_, err = contender.LockObject(ctx, testReportURI)
			if err == nil {
				t.Fatalf("[%s] expected second-session LockObject to fail while the first session holds the lock", sys.Name)
			}

			var adtErr *adt.ADTError
			if !errors.As(err, &adtErr) {
				t.Fatalf("[%s] expected *adt.ADTError, got %T: %v", sys.Name, err, err)
			}
			t.Logf("[%s] StatusCode=%d Type=%q Properties=%v T100KeyID=%q T100KeyNo=%q T100Vars=%v",
				sys.Name, adtErr.StatusCode, adtErr.Type, adtErr.Properties, adtErr.T100KeyID, adtErr.T100KeyNo, adtErr.T100Vars)

			if adtErr.Type == "" {
				t.Skipf("[%s] error carried no structured Type (legacy/plain-text body) — cannot assert Properties here (message: %q)", sys.Name, adtErr.Message)
			}

			// This is the bug's exact failure path: pre-#56, Properties was
			// always nil no matter what the body contained, so this would
			// have failed on every system whose modern exception envelope
			// carries T100KEY data for this collision.
			if len(adtErr.Properties) == 0 {
				t.Fatalf("[%s] Type=%q carried no <properties> — regression for #56 (message: %q)", sys.Name, adtErr.Type, adtErr.Message)
			}

			if user, object, ok := adtErr.IsEnqueueLock(); ok {
				t.Logf("[%s] IsEnqueueLock: user=%q object=%q", sys.Name, user, object)
			} else {
				t.Logf("[%s] IsEnqueueLock: no match (Type=%q T100KeyID=%q T100KeyNo=%q) — SAP used a different exception variant for this collision on this system",
					sys.Name, adtErr.Type, adtErr.T100KeyID, adtErr.T100KeyNo)
			}
		})
	}
}
