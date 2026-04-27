//go:build integration

package adt_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Hochfrequenz/adtler/adt"
)

// TestSetTextElements_MultiSystem verifies that SetTextElements successfully
// PUTs text symbols and selection texts to a real SAP system (issue #45).
//
// Strategy: per system, create a fresh $TMP report (no transport required —
// that's the whole reason for using $TMP), seed it with source that
// references a parameter so selection texts have something to bind to,
// activate, then write text elements via SetTextElements and verify with
// GetTextElements. The $TMP report is deleted on cleanup.
//
// $TMP avoids the corrNr / transport requirement that S/4 enforces for
// transport-managed packages. The test exercises both PUT endpoints
// (symbols.v1 and selections.v1) on every configured SAP system.
//
// Per system: gracefully skips if the textelements endpoint is unavailable
// (e.g. R/3 ECC may not expose it for all object kinds).
func TestSetTextElements_MultiSystem(t *testing.T) {
	ctx := context.Background()
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			name := fmt.Sprintf("Z_TXT_%d", time.Now().UnixNano()%1_000_000)
			programURI := "/sap/bc/adt/programs/programs/" + name
			textElementsURI := "/sap/bc/adt/textelements/programs/" + name
			source := fmt.Sprintf(
				"REPORT %s.\nPARAMETERS p_test TYPE string.\nWRITE: TEXT-001.\n",
				strings.ToLower(name),
			)

			if err := sys.Client.CreateObject(ctx, "PROG", name, "$TMP", "adtler integration test", ""); err != nil {
				t.Fatalf("[%s] CreateObject: %v", sys.Name, err)
			}
			t.Cleanup(func() {
				_ = sys.Client.DeleteObject(context.Background(), programURI, "", "")
			})

			progLock, err := sys.Client.LockObject(ctx, programURI)
			if err != nil {
				t.Fatalf("[%s] LockObject(program): %v", sys.Name, err)
			}
			src, err := sys.Client.GetSource(ctx, programURI)
			if err != nil {
				_ = sys.Client.UnlockObject(ctx, programURI, progLock)
				t.Fatalf("[%s] GetSource: %v", sys.Name, err)
			}
			if _, err := sys.Client.SetSource(ctx, programURI, source, progLock, "", src.ETag); err != nil {
				_ = sys.Client.UnlockObject(ctx, programURI, progLock)
				t.Fatalf("[%s] SetSource: %v", sys.Name, err)
			}
			if err := sys.Client.UnlockObject(ctx, programURI, progLock); err != nil {
				t.Fatalf("[%s] UnlockObject(program): %v", sys.Name, err)
			}

			actResult, err := sys.Client.ActivateObjects(ctx, []string{programURI})
			if err != nil {
				t.Fatalf("[%s] ActivateObjects: %v", sys.Name, err)
			}
			if !actResult.Success {
				t.Fatalf("[%s] activation failed: %d messages", sys.Name, len(actResult.Messages))
			}

			injectedSymbol := adt.TextSymbol{Key: "001", Text: "adtler integration test", MaxLength: 30}
			injectedSelection := adt.SelectionText{Name: "P_TEST", Text: "test parameter"}

			textLock, err := sys.Client.LockObject(ctx, textElementsURI)
			if err != nil {
				if isEndpointUnavailable(err) {
					t.Skipf("[%s] textelements lock endpoint unavailable: %v", sys.Name, err)
				}
				t.Fatalf("[%s] LockObject(textelements): %v", sys.Name, err)
			}

			err = sys.Client.SetTextElements(ctx, programURI,
				[]adt.TextSymbol{injectedSymbol},
				[]adt.SelectionText{injectedSelection},
				textLock, "")
			if err != nil {
				_ = sys.Client.UnlockObject(ctx, textElementsURI, textLock)
				if isEndpointUnavailable(err) {
					t.Skipf("[%s] textelements endpoint unavailable: %v", sys.Name, err)
				}
				t.Fatalf("[%s] SetTextElements: %v", sys.Name, err)
			}

			if err := sys.Client.UnlockObject(ctx, textElementsURI, textLock); err != nil {
				t.Fatalf("[%s] UnlockObject(textelements): %v", sys.Name, err)
			}

			after, err := sys.Client.GetTextElements(ctx, programURI)
			if err != nil {
				t.Fatalf("[%s] GetTextElements after write: %v", sys.Name, err)
			}
			t.Logf("[%s] read-back: %d symbols, %d selections", sys.Name, len(after.Symbols), len(after.Selections))

			if !containsSymbol(after.Symbols, injectedSymbol) {
				t.Errorf("[%s] expected symbol %+v in read-back; got %+v", sys.Name, injectedSymbol, after.Symbols)
			}
			if !containsSelection(after.Selections, injectedSelection) {
				t.Errorf("[%s] expected selection %+v in read-back; got %+v", sys.Name, injectedSelection, after.Selections)
			}
		})
	}
}

func containsSymbol(haystack []adt.TextSymbol, needle adt.TextSymbol) bool {
	for _, s := range haystack {
		if s.Key == needle.Key && s.Text == needle.Text {
			return true
		}
	}
	return false
}

func containsSelection(haystack []adt.SelectionText, needle adt.SelectionText) bool {
	for _, s := range haystack {
		if s.Name == needle.Name && s.Text == needle.Text {
			return true
		}
	}
	return false
}

// isEndpointUnavailable reports true for the 404 / 405 / 415 family that
// indicates a textelements endpoint isn't exposed for the given object on
// this SAP release.
func isEndpointUnavailable(err error) bool {
	var adtErr *adt.ADTError
	if !errors.As(err, &adtErr) {
		return false
	}
	switch adtErr.StatusCode {
	case 404, 405, 415:
		return true
	}
	return false
}
