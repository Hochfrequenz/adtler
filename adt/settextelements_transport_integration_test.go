//go:build integration && transport

package adt_test

import (
	"context"
	"testing"
	"time"

	"github.com/Hochfrequenz/adtler/adt"
)

// TestSetTextElements_WithTransport_MultiSystem exercises the
// transport-required path of SetTextElements: writing text elements to an
// object in a transport-managed package requires the transport to be
// passed as ?corrNr= in the URL — without it S/4 returns
// 400 ExceptionParameterNotFound.
//
// Strategy per system: create a fresh transport, create a disposable
// program in the transport-managed test package using that transport,
// seed source via SetSource, activate, then lock the textelements URI
// (S/4 binds the textelements lock to a different enqueue resource than
// the program lock), write text elements with the transport, verify via
// GetTextElements, unlock, and clean up (delete the object and release
// the transport).
//
// Skips on R/3 ECC where the textelements endpoint is not exposed for
// this object kind.
//
// Build tag: `integration && transport` — this test creates a real
// transport request. Run with: `go test -tags='integration transport'`.
func TestSetTextElements_WithTransport_MultiSystem(t *testing.T) {
	ctx := context.Background()
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			name := timestampedName("Z_TXTR_")
			programURI := "/sap/bc/adt/programs/programs/" + name
			textElementsURI := "/sap/bc/adt/textelements/programs/" + name

			transport, err := sys.Client.CreateTransport(ctx, "K", "DUM",
				"adtler #45 SetTextElements integration test", testPackage)
			if err != nil {
				t.Fatalf("[%s] CreateTransport: %v", sys.Name, err)
			}
			t.Logf("[%s] created transport %s", sys.Name, transport)
			t.Cleanup(func() {
				if err := sys.Client.ReleaseTransportWithTasks(context.Background(), transport); err != nil {
					t.Logf("[%s] release transport %s: %v (manual cleanup may be needed)", sys.Name, transport, err)
				}
			})

			if err := sys.Client.CreateObject(ctx, "PROG", name, testPackage,
				"adtler #45 integration test", transport); err != nil {
				t.Fatalf("[%s] CreateObject(%s, %s): %v", sys.Name, name, testPackage, err)
			}
			t.Cleanup(func() {
				_ = sys.Client.DeleteObject(context.Background(), programURI, "", transport)
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
			seedSource := "REPORT " + name + ".\n" +
				"PARAMETERS p_test TYPE string.\n" +
				"WRITE: TEXT-001.\n"
			if _, err := sys.Client.SetSource(ctx, programURI, seedSource, progLock, transport, src.ETag); err != nil {
				_ = sys.Client.UnlockObject(ctx, programURI, progLock)
				t.Fatalf("[%s] SetSource (seed): %v", sys.Name, err)
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

			injectedSymbol := adt.TextSymbol{
				Key:       "001",
				Text:      "adtler #45 transport test " + time.Now().Format("15:04:05"),
				MaxLength: 50,
			}
			injectedSelection := adt.SelectionText{Name: "P_TEST", Text: "test parameter"}

			textLock, err := sys.Client.LockObject(ctx, textElementsURI)
			if err != nil {
				if isEndpointUnavailable(err) {
					t.Skipf("[%s] textelements endpoint unavailable: %v", sys.Name, err)
				}
				t.Fatalf("[%s] LockObject(textelements): %v", sys.Name, err)
			}

			err = sys.Client.SetTextElements(ctx, programURI,
				[]adt.TextSymbol{injectedSymbol},
				[]adt.SelectionText{injectedSelection},
				textLock, transport)
			if err != nil {
				_ = sys.Client.UnlockObject(ctx, textElementsURI, textLock)
				if isEndpointUnavailable(err) {
					t.Skipf("[%s] textelements endpoint unavailable: %v", sys.Name, err)
				}
				t.Fatalf("[%s] SetTextElements (with transport %s): %v", sys.Name, transport, err)
			}
			if err := sys.Client.UnlockObject(ctx, textElementsURI, textLock); err != nil {
				t.Fatalf("[%s] UnlockObject(textelements): %v", sys.Name, err)
			}

			after, err := sys.Client.GetTextElements(ctx, programURI)
			if err != nil {
				t.Fatalf("[%s] GetTextElements (verify): %v", sys.Name, err)
			}
			t.Logf("[%s] read-back: %d symbols, %d selections", sys.Name, len(after.Symbols), len(after.Selections))
			if !containsSymbol(after.Symbols, injectedSymbol) {
				t.Errorf("[%s] expected symbol %+v in read-back; got %+v",
					sys.Name, injectedSymbol, after.Symbols)
			}
			if !containsSelection(after.Selections, injectedSelection) {
				t.Errorf("[%s] expected selection %+v in read-back; got %+v",
					sys.Name, injectedSelection, after.Selections)
			}
		})
	}
}

func timestampedName(prefix string) string {
	return prefix + time.Now().Format("150405")
}
