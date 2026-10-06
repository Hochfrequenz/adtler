package adt_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"
)

// Anonymised shapes of live responses: 816 lists stackPosition etc. first and
// carries debugCursorStackIndex; 750 lists programName first.
const stack816 = `<?xml version="1.0" encoding="utf-8"?><dbg:stack isRfc="false" debugCursorStackIndex="0" isSameSystem="true" xmlns:dbg="http://www.sap.com/adt/debugger" xmlns:adtcore="http://www.sap.com/adt/core">` +
	`<stackEntry stackPosition="1" stackType="ABAP" programName="SAPLAUNIT" includeName="LAUNITU01" line="10" eventType="FUNCTION" eventName="RUN" sourceType="ABAP" systemProgram="true" isActive="false" adtcore:uri="/sap/bc/adt/functions/groups/aunit/fmodules/run/source/main#start=10,0"/>` +
	`<stackEntry stackPosition="2" stackType="ABAP" programName="ZREP" includeName="ZREP" line="14" eventType="METHOD" eventName="TEST_HELLO" sourceType="ABAP" systemProgram="false" isActive="true" adtcore:uri="/sap/bc/adt/programs/programs/zrep/source/main#start=14,0"/>` +
	`</dbg:stack>`

const stack750 = `<?xml version="1.0" encoding="utf-8"?><dbg:stack isRfc="true" isSameSystem="true" xmlns:dbg="http://www.sap.com/adt/debugger" xmlns:adtcore="http://www.sap.com/adt/core">` +
	`<stackEntry programName="SAPLSTFC" includeName="LSTFCU01" line="13" eventType="FUNCTION" eventName="STFC_CONNECTION" stackPosition="9" systemProgram="false" adtcore:uri="/sap/bc/adt/functions/groups/stfc/fmodules/stfc_connection/source/main#start=13,0"/>` +
	`<stackEntry programName="SAPMHTTP" includeName="SAPMHTTP" line="5" eventType="MODULE" eventName="X" stackPosition="1" systemProgram="true"/>` +
	`</dbg:stack>`

func stackServer(t *testing.T, body string) *adt.DebugSession {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			return
		}
		if r.URL.Query().Get("method") == "getStack" {
			if r.Header.Get("X-sap-adt-sessiontype") != "stateful" {
				t.Error("getStack must be stateful")
			}
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(body))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	return adt.NewDebugSession(adt.NewClient(sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}), "U")
}

func TestGetStackFrames_816(t *testing.T) {
	frames, err := stackServer(t, stack816).GetStackFrames(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 2 {
		t.Fatalf("got %d frames", len(frames))
	}
	f, ok := adt.ActiveFrame(frames)
	if !ok || f.Program != "ZREP" || f.Line != 14 || f.EventName != "TEST_HELLO" ||
		f.SourceURI != "/sap/bc/adt/programs/programs/zrep/source/main" || f.SourceLine != 14 || f.SystemProgram {
		t.Errorf("active frame: %+v", f)
	}
}

// 750 has no isActive: the top of the stack (highest stackPosition) is active.
// A frame without adtcore:uri keeps an empty SourceURI.
func TestGetStackFrames_750_NoIsActive(t *testing.T) {
	frames, err := stackServer(t, stack750).GetStackFrames(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	f, ok := adt.ActiveFrame(frames)
	if !ok || f.Position != 9 || f.Include != "LSTFCU01" || f.Line != 13 ||
		f.SourceURI != "/sap/bc/adt/functions/groups/stfc/fmodules/stfc_connection/source/main" {
		t.Errorf("active frame: %+v", f)
	}
	if frames[1].SourceURI != "" || frames[1].SourceLine != 0 || !frames[1].SystemProgram {
		t.Errorf("second frame: %+v", frames[1])
	}
	if _, ok := adt.ActiveFrame(nil); ok {
		t.Error("ActiveFrame(nil) must report false")
	}
}
