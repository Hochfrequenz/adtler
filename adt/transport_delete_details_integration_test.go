//go:build integration && transport

package adt_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Hochfrequenz/adtler/adt"
)

// TestDeleteTransport_Details_Integration creates a workbench request, deletes
// it and checks that DeleteTransport reported what it read just before (#243):
// owner, description, category, state and the one task SAP creates with the
// request. The creation date comes from E070CREATE, which not every request
// has a row in, so a zero Created is accepted and only logged.
func TestDeleteTransport_Details_Integration(t *testing.T) {
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			c := sys.Client
			ctx := context.Background()
			description := "adtler #243 " + time.Now().Format("150405")
			number, err := c.CreateTransport(ctx, adt.CreateTransportOptions{Description: description, Package: testPackage})
			if err != nil {
				t.Fatalf("CreateTransport failed: %s", errKind(err))
			}

			deleted, err := c.DeleteTransport(ctx, number)
			if err != nil {
				t.Fatalf("DeleteTransport failed: %s (the request may need a manual cleanup)", errKind(err))
			}
			if deleted.DetailsErr != nil {
				t.Errorf("DetailsErr is set: %s", errKind(deleted.DetailsErr))
			}
			if deleted.Number != number {
				t.Error("Number: differs from the number CreateTransport returned")
			}
			if deleted.Owner == "" {
				t.Error("Owner is empty")
			}
			if deleted.Description != description {
				t.Errorf("Description: got %q, want %q", deleted.Description, description)
			}
			// An ADT answer in the worklist shape carries no tm:type, so an empty
			// Type is logged, not failed; a Type that is there must be K.
			if deleted.Type == "" {
				t.Log("Type is empty: the ADT answer carries no tm:type on this system")
			} else if deleted.Type != adt.TransportTypeWorkbench {
				t.Errorf("Type: got %q, want %q", deleted.Type, adt.TransportTypeWorkbench)
			}
			if deleted.State != adt.TransportStateModifiable {
				t.Errorf("State: got %v (raw %q), want modifiable", deleted.State, deleted.RawStatus)
			}
			if len(deleted.Tasks) != 1 {
				t.Errorf("Tasks: got %d, want 1", len(deleted.Tasks))
			}
			t.Logf("LastChanged known: %t, Created known: %t", !deleted.LastChanged.IsZero(), !deleted.Created.IsZero())
		})
	}
}

// errKind names the category of err without its text, which can carry request
// numbers or URLs of the system: the integration output is published.
func errKind(err error) string {
	var adtErr *adt.ADTError
	if errors.As(err, &adtErr) {
		return fmt.Sprintf("ADT error, status %d, type %q", adtErr.StatusCode, adtErr.Type)
	}
	return "error that is not an ADTError"
}
