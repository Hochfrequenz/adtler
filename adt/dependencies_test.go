package adt

import (
	"context"
	"strings"
	"testing"

	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"
)

func TestPoolProgramNames(t *testing.T) {
	if got := fugrPoolProgramName("ZFG"); got != "SAPLZFG" {
		t.Errorf("fugr plain: got %q, want SAPLZFG", got)
	}
	if got := fugrPoolProgramName("/NS/FUGR"); got != "/NS/SAPLFUGR" {
		t.Errorf("fugr namespaced: got %q, want /NS/SAPLFUGR", got)
	}
	// CLAS: padded with '=' to 30 chars + CP.
	if got := classPoolProgramName("ZCL_FOO"); got != "ZCL_FOO"+strings.Repeat("=", 23)+"CP" {
		t.Errorf("class pad: got %q", got)
	}
	if got := classPoolProgramName(strings.Repeat("X", 30)); got != strings.Repeat("X", 30)+"CP" {
		t.Errorf("class >=30: got %q", got)
	}
	// INTF: padded with '=' to 30 chars + IP.
	if got := intfPoolProgramName("ZIF_BAR"); got != "ZIF_BAR"+strings.Repeat("=", 23)+"IP" {
		t.Errorf("intf pad: got %q", got)
	}
}

func TestTabclassToUseType(t *testing.T) {
	cases := map[string]string{
		"TRANSP":  UseTypeTable,
		"INTTAB":  UseTypeStructure,
		"CLUSTER": UseTypeTable,
		"POOL":    UseTypeTable,
		"VIEW":    UseTypeView,
		"WEIRD":   UseTypeUnknown,
	}
	for in, want := range cases {
		if got := tabclassToUseType(in); got != want {
			t.Errorf("tabclassToUseType(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOoRelTypeToUseType(t *testing.T) {
	cases := map[string]string{
		"0": UseTypeInterface,
		"1": UseTypeInterface,
		"2": UseTypeSuperclass,
		"9": UseTypeUnknown,
	}
	for in, want := range cases {
		if got := ooRelTypeToUseType(in); got != want {
			t.Errorf("ooRelTypeToUseType(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestChunkNames(t *testing.T) {
	// Each entry costs len(name)+3 bytes. With maxBytes=20 and 8-char names
	// (cost 11), two names (22) exceed the budget, so they split 1-per-chunk.
	chunks := chunkNames([]string{"NAME0001", "NAME0002", "NAME0003"}, 20)
	if len(chunks) != 3 {
		t.Fatalf("expected 3 chunks for over-budget names, got %d: %v", len(chunks), chunks)
	}
	// Generous budget keeps everything in one chunk.
	chunks = chunkNames([]string{"A", "B", "C"}, 150)
	if len(chunks) != 1 || len(chunks[0]) != 3 {
		t.Errorf("expected 1 chunk of 3, got %v", chunks)
	}
	// Empty input yields no chunks.
	if chunks := chunkNames(nil, 150); len(chunks) != 0 {
		t.Errorf("expected no chunks for empty input, got %v", chunks)
	}
}

func TestGetObjectDependencies_UnsupportedType(t *testing.T) {
	// No HTTP call is made for an unsupported type.
	cfg := sapmcpconfig.SAPSystem{Host: "http://127.0.0.1:0", User: "U", Password: "P", Client: "100"}
	client := NewClient(cfg)
	if _, err := client.GetObjectDependencies(context.Background(), "BOGUS", "X", 200, 3); err == nil {
		t.Fatal("expected error for unsupported object type")
	}
}
