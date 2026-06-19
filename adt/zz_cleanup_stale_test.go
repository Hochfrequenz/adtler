//go:build integration && transport

package adt_test

import (
	"context"
	"os"
	"testing"
)

// TestInspectTransport prints all tasks and objects in a transport.
// Set TR_NUMBER to the transport to inspect, then run once.
func TestInspectTransport(t *testing.T) {
	trNumber := os.Getenv("TR_NUMBER")
	if trNumber == "" {
		t.Skip("TR_NUMBER not set")
	}
	client := newIntegrationClient(t)
	ctx := context.Background()

	tasks, err := client.GetTransportTasks(ctx, trNumber)
	if err != nil {
		t.Fatalf("GetTransportTasks: %v", err)
	}
	t.Logf("transport %s tasks: %v", trNumber, tasks)

	objects, err := client.GetTransportObjects(ctx, trNumber)
	if err != nil {
		t.Fatalf("GetTransportObjects: %v", err)
	}
	for _, obj := range objects {
		t.Logf("object: pgmid=%s type=%s name=%s", obj.PgmID, obj.Type, obj.Name)
	}
}

// TestRemoveStuckObjectFromTransport removes Z_ADT_MCP_ROLLBACK_TST from the
// stuck transport task. Run once with TASK_NUMBER=<task> TR_NUMBER=<request>.
func TestRemoveStuckObjectFromTransport(t *testing.T) {
	taskNumber := os.Getenv("TASK_NUMBER")
	trNumber := os.Getenv("TR_NUMBER")
	if taskNumber == "" || trNumber == "" {
		t.Skip("TASK_NUMBER and TR_NUMBER not set")
	}
	client := newIntegrationClient(t)
	ctx := context.Background()

	const objName = "Z_ADT_MCP_ROLLBACK_TST"
	if err := client.RemoveFromTransport(ctx, taskNumber, trNumber, "R3TR", "PROG", objName, "PROG/P", ""); err != nil {
		t.Fatalf("RemoveFromTransport: %v", err)
	}
	t.Logf("removed %s from task %s (transport %s)", objName, taskNumber, trNumber)

	// Also try removing Z_ADT_MCP_RELVERIFY_TST which also appeared in the inspect output
	const objName2 = "Z_ADT_MCP_RELVERIFY_TST"
	if err := client.RemoveFromTransport(ctx, taskNumber, trNumber, "R3TR", "PROG", objName2, "PROG/P", ""); err != nil {
		t.Logf("RemoveFromTransport %s: %v (may not be in this task)", objName2, err)
	} else {
		t.Logf("removed %s from task %s", objName2, taskNumber)
	}
}
