package adtxml

import (
	"encoding/xml"
	"strings"
	"testing"
)

func TestBreakpointsRequest_Marshal(t *testing.T) {
	req := BreakpointsRequest{
		NSDebug:       "http://www.sap.com/adt/debugger",
		NSCore:        "http://www.sap.com/adt/core",
		Scope:         "external",
		DebuggingMode: "user",
		RequestUser:   "U",
		TerminalID:    "T1",
		IdeID:         "ide1",
		Breakpoints: []BreakpointRequest{
			{Kind: "line", ClientID: "0", URI: "/src#start=3,0", Type: "PROG/P", Name: "ZTEST"},
			{Kind: "line", URI: "/src#start=4,0"},
		},
	}
	out, err := xml.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		`<dbg:breakpoints xmlns:dbg="http://www.sap.com/adt/debugger" xmlns:adtcore="http://www.sap.com/adt/core" scope="external" debuggingMode="user" requestUser="U" terminalId="T1" ideId="ide1">`,
		`<breakpoint kind="line" clientId="0" adtcore:uri="/src#start=3,0" adtcore:type="PROG/P" adtcore:name="ZTEST"></breakpoint>`,
		// Empty clientId, type and name are omitted, not sent empty.
		`<breakpoint kind="line" adtcore:uri="/src#start=4,0"></breakpoint>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s\nin %s", want, got)
		}
	}
	if strings.Contains(got, "syncMode") {
		t.Errorf("unset SyncMode serialized: %s", got)
	}
}

func TestBreakpointsResponse_Unmarshal(t *testing.T) {
	raw := `<?xml version="1.0" encoding="utf-8"?>` +
		`<dbg:breakpoints xmlns:dbg="http://www.sap.com/adt/debugger" xmlns:adtcore="http://www.sap.com/adt/core">` +
		`<breakpoint kind="line" clientId="1" errorKind="invalidPosition" errorMessage="Cannot create a breakpoint at this position" nonAbapFlavour=""/>` +
		`<breakpoint kind="line" clientId="0" id="BP1" nonAbapFlavour="" adtcore:uri="/src#start=3" adtcore:type="PROG/P" adtcore:name="ZTEST"/>` +
		`</dbg:breakpoints>`
	var resp BreakpointsResponse
	if err := xml.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(resp.Breakpoints) != 2 {
		t.Fatalf("breakpoints: got %d, want 2", len(resp.Breakpoints))
	}
	if bp := resp.Breakpoints[0]; bp.ClientID != "1" || bp.ErrorKind != "invalidPosition" || bp.ErrorMessage == "" || bp.ID != "" {
		t.Errorf("[0]: %+v", bp)
	}
	if bp := resp.Breakpoints[1]; bp.ClientID != "0" || bp.ID != "BP1" || bp.URI != "/src#start=3" || bp.Type != "PROG/P" || bp.Name != "ZTEST" {
		t.Errorf("[1]: %+v", bp)
	}
}
