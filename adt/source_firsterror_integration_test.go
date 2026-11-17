//go:build integration

package adt_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestSetSource_FailedRetryKeepsTheFirstError_Integration guards #280 on both
// systems: when the header-delivered write and its ?lockHandle= retry both
// fail, the error must carry the first attempt's answer too. The test writes a
// throwaway $TMP program it holds a lock on, with a lock handle SAP never
// issued, so both attempts are refused and nothing is written. ($TMP, because a write into a
// transportable package without a request is refused before the lock check.)
func TestSetSource_FailedRetryKeepsTheFirstError_Integration(t *testing.T) {
	// A new name per run: an S/4 enqueue orphaned by an aborted run outlives
	// the object and would refuse a create under the same name.
	name := fmt.Sprintf("ZADTLER_280_%06X", time.Now().UnixNano()&0xFFFFFF)
	uri := "/sap/bc/adt/programs/programs/" + strings.ToLower(name)
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			ctx := context.Background()
			if err := sys.Client.CreateObject(ctx, "PROG", name, "$TMP", "adtler #280 guard", ""); err != nil {
				t.Fatalf("CreateObject: %v", err)
			}
			t.Cleanup(func() {
				if err := sys.Client.DeleteObject(context.Background(), uri, "", ""); err != nil {
					t.Errorf("cleanup: DeleteObject left the $TMP program behind: %v", err)
				}
			})
			src, err := sys.Client.GetSource(ctx, uri)
			if err != nil {
				t.Fatalf("GetSource: %v", err)
			}
			// With no lock held, both systems accept a write whatever handle
			// it carries (measured 2026-11-17), so hold a real lock and send
			// a different handle.
			h, err := sys.Client.LockObject(ctx, uri)
			if err != nil {
				t.Fatalf("LockObject: %v", err)
			}
			defer func() { _ = sys.Client.UnlockObject(context.Background(), uri, h) }()
			_, err = sys.Client.SetSource(ctx, uri, src.Source, "ADTLER280NOTISSUED", "", src.ETag)
			if err == nil {
				t.Fatal("SetSource with a lock handle SAP never issued succeeded")
			}
			if !strings.Contains(err.Error(), "first attempt") {
				t.Fatalf("err: got %q, want the first attempt's answer attached", err)
			}
			t.Logf("refused as expected, both answers reported (%d characters)", len(err.Error()))
		})
	}
}
