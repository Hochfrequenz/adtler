package adt_test

import (
	"context"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"
)

const (
	preauditClassURI  = "/sap/bc/adt/oo/classes/zcl_example"
	preauditClassName = "ZCL_EXAMPLE"
)

// preauditAnswer is the shape SAP_BASIS 750 answers the first activation POST
// with for a class whose parts are inactive and recorded in a request (#279):
// a transport-only entry, the class, one of its methods, and an unrelated
// inactive program of the same user.
const preauditAnswer = `<?xml version="1.0" encoding="utf-8"?><ioc:inactiveObjects xmlns:ioc="http://www.sap.com/abapxml/inactiveCtsObjects">` +
	`<ioc:entry><ioc:object/><ioc:transport ioc:user="USERA" ioc:linked="false"><ioc:ref adtcore:uri="/sap/bc/adt/vit/wb/object_type/%20%20%20%20rq/object_name/AAAK900001" adtcore:type="/RQ" adtcore:name="AAAK900001" xmlns:adtcore="http://www.sap.com/adt/core"/></ioc:transport></ioc:entry>` +
	`<ioc:entry><ioc:object ioc:user="USERA" ioc:deleted="false"><ioc:ref adtcore:uri="/sap/bc/adt/oo/classes/zcl_example" adtcore:type="CLAS/OC" adtcore:name="ZCL_EXAMPLE" adtcore:packageName="ZPKG" xmlns:adtcore="http://www.sap.com/adt/core"/></ioc:object><ioc:transport/></ioc:entry>` +
	`<ioc:entry><ioc:object ioc:user="USERA" ioc:deleted="false"><ioc:ref adtcore:uri="/sap/bc/adt/oo/classes/zcl_example/source/main#type=CLAS%2FOM;name=METHOD_A" adtcore:type="CLAS/OM/public" adtcore:name="ZCL_EXAMPLE                   METHOD_A" adtcore:parentUri="/sap/bc/adt/oo/classes/zcl_example" xmlns:adtcore="http://www.sap.com/adt/core"/></ioc:object><ioc:transport ioc:user="USERA" ioc:linked="true"><ioc:ref adtcore:uri="/sap/bc/adt/vit/wb/object_type/%20%20%20%20rq/object_name/AAAK900002" adtcore:type="/RQ" adtcore:name="AAAK900002" xmlns:adtcore="http://www.sap.com/adt/core"/></ioc:transport></ioc:entry>` +
	`<ioc:entry><ioc:object ioc:user="USERA" ioc:deleted="false"><ioc:ref adtcore:uri="/sap/bc/adt/programs/programs/zother" adtcore:type="PROG/P" adtcore:name="ZOTHER" xmlns:adtcore="http://www.sap.com/adt/core"/></ioc:object><ioc:transport/></ioc:entry>` +
	`</ioc:inactiveObjects>`

type sentRef struct {
	URI       string `xml:"uri,attr"`
	Type      string `xml:"type,attr"`
	Name      string `xml:"name,attr"`
	ParentURI string `xml:"parentUri,attr"`
}

type sentActivation struct {
	preaudit bool
	refs     []sentRef
}

// preauditServer answers activation POSTs with answers in order (the last one
// repeats) and records them; the inactive-objects list is always empty.
func preauditServer(t *testing.T, answers ...string) (*httptest.Server, *[]sentActivation) {
	t.Helper()
	var sent []sentActivation
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case csrfEndpoint:
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
		case activationPath:
			body, _ := io.ReadAll(r.Body)
			var req struct {
				Refs []sentRef `xml:"objectReference"`
			}
			if err := xml.Unmarshal(body, &req); err != nil {
				t.Errorf("activation body: %v", err)
			}
			preaudit, err := strconv.ParseBool(r.URL.Query().Get("preauditRequested"))
			if err != nil {
				t.Errorf("preauditRequested: %v", err)
			}
			sent = append(sent, sentActivation{preaudit: preaudit, refs: req.Refs})
			answer := answers[len(answers)-1]
			if len(sent) <= len(answers) {
				answer = answers[len(sent)-1]
			}
			if answer == serverErrorAnswer {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(answer))
		case inactiveObjectsPath:
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?><ioc:inactiveObjects xmlns:ioc="http://www.sap.com/abapxml/inactiveCtsObjects"/>`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	return srv, &sent
}

// #279: when SAP answers the pre-audit with the inactive parts instead of
// activating, ActivateObjects sends the activation again without the
// pre-audit, for the class with its own type and name plus its listed
// method, and leaves out the transport-only entry and the unrelated program.
func TestActivateObjects_SendsPreauditListBack(t *testing.T) {
	srv, sent := preauditServer(t, preauditAnswer, "")
	defer srv.Close()
	client := adt.NewClient(sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"})

	result, err := client.ActivateObjects(context.Background(), []string{preauditClassURI})
	if err != nil {
		t.Fatalf("ActivateObjects: %v", err)
	}
	if !result.Success {
		t.Errorf("Success = false, messages %+v", result.Messages)
	}
	if len(*sent) != 2 {
		t.Fatalf("activation POSTs: got %d, want 2", len(*sent))
	}
	if !(*sent)[0].preaudit || (*sent)[1].preaudit {
		t.Errorf("preauditRequested: got %v then %v, want true then false", (*sent)[0].preaudit, (*sent)[1].preaudit)
	}
	refs := (*sent)[1].refs
	if len(refs) != 2 {
		t.Fatalf("follow-up refs: got %+v, want the class and its method", refs)
	}
	if refs[0].URI != preauditClassURI || refs[0].Type != "CLAS/OC" || refs[0].Name != preauditClassName {
		t.Errorf("class ref: got %+v", refs[0])
	}
	m := refs[1]
	if m.URI != preauditClassURI+"/source/main#type=CLAS%2FOM;name=METHOD_A" || m.Type != "CLAS/OM/public" ||
		m.Name != "ZCL_EXAMPLE                   METHOD_A" || m.ParentURI != preauditClassURI {
		t.Errorf("method ref: got %+v", m)
	}
}

// #279: errors in the follow-up answer are reported like any other.
func TestActivateObjects_PreauditFollowUpErrorsAreReported(t *testing.T) {
	srv, _ := preauditServer(t, preauditAnswer,
		`<?xml version="1.0" encoding="utf-8"?><chkl:messages xmlns:chkl="http://www.sap.com/abapxml/checklist"><msg objDescr="Class ZCL_EXAMPLE" type="E" line="1" href="`+preauditClassURI+`/source/main#start=8,4"><shortText><txt>Syntax error</txt></shortText></msg></chkl:messages>`)
	defer srv.Close()
	client := adt.NewClient(sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"})

	result, err := client.ActivateObjects(context.Background(), []string{preauditClassURI})
	if err != nil {
		t.Fatalf("ActivateObjects: %v", err)
	}
	if result.Success || len(result.Messages) != 1 || result.Messages[0].Text != "Syntax error" {
		t.Errorf("result: got %+v, want the follow-up's syntax error", result)
	}
}

// Without a pre-audit list there is one POST, as before #279.
func TestActivateObjects_NoFollowUpWithoutPreauditList(t *testing.T) {
	srv, sent := preauditServer(t, "")
	defer srv.Close()
	client := adt.NewClient(sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"})

	if _, err := client.ActivateObjects(context.Background(), []string{preauditClassURI}); err != nil {
		t.Fatalf("ActivateObjects: %v", err)
	}
	if len(*sent) != 1 || !(*sent)[0].preaudit {
		t.Errorf("activation POSTs: got %+v, want one with preauditRequested=true", *sent)
	}
}

// serverErrorAnswer makes preauditServer answer that activation POST with 500.
const serverErrorAnswer = "<500>"

// preauditList wraps refs (each an ioc:ref element's attributes) in a
// pre-audit answer, one entry per ref.
func preauditList(refs ...string) string {
	s := `<?xml version="1.0" encoding="utf-8"?><ioc:inactiveObjects xmlns:ioc="http://www.sap.com/abapxml/inactiveCtsObjects">`
	for _, r := range refs {
		s += `<ioc:entry><ioc:object ioc:user="USERA" ioc:deleted="false"><ioc:ref ` + r + ` xmlns:adtcore="http://www.sap.com/adt/core"/></ioc:object><ioc:transport/></ioc:entry>`
	}
	return s + `</ioc:inactiveObjects>`
}

func activateAndCollect(t *testing.T, uri string, answers ...string) ([]sentActivation, error) {
	t.Helper()
	srv, sent := preauditServer(t, answers...)
	defer srv.Close()
	client := adt.NewClient(sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"})
	_, err := client.ActivateObjects(context.Background(), []string{uri})
	return *sent, err
}

// #279: an entry whose URI is not under the requested object but whose
// parentUri names it is sent back too.
func TestActivateObjects_PreauditFollowUpIncludesParentedEntries(t *testing.T) {
	const part = "/sap/bc/adt/vit/wb/object_type/clasosi/object_name/ZCL_EXAMPLE"
	sent, err := activateAndCollect(t, preauditClassURI,
		preauditList(`adtcore:uri="`+part+`" adtcore:type="CLAS/OSI" adtcore:parentUri="`+preauditClassURI+`"`), "")
	if err != nil {
		t.Fatalf("ActivateObjects: %v", err)
	}
	if len(sent) != 2 || len(sent[1].refs) != 2 || sent[1].refs[1].URI != part {
		t.Fatalf("follow-up: got %+v, want the class and the parented part", sent)
	}
}

// #279: the match is one-way. For a requested function module, the listed
// function group it is nested under stays out; the module's own entry is
// sent back.
func TestActivateObjects_PreauditFollowUpLeavesOutEnclosingObject(t *testing.T) {
	const (
		group  = "/sap/bc/adt/functions/groups/zfg"
		module = group + "/fmodules/zfm"
	)
	sent, err := activateAndCollect(t, module,
		preauditList(`adtcore:uri="`+group+`" adtcore:type="FUGR/F"`, `adtcore:uri="`+module+`" adtcore:type="FUGR/FF"`), "")
	if err != nil {
		t.Fatalf("ActivateObjects: %v", err)
	}
	if len(sent) != 2 || len(sent[1].refs) != 1 || sent[1].refs[0].URI != module || sent[1].refs[0].Type != "FUGR/FF" {
		t.Fatalf("follow-up: got %+v, want only the module, with its type", sent)
	}
}

// #279: a list holding only unrelated objects still gets a follow-up, for the
// requested objects alone.
func TestActivateObjects_PreauditFollowUpWithOnlyUnrelatedEntries(t *testing.T) {
	sent, err := activateAndCollect(t, preauditClassURI,
		preauditList(`adtcore:uri="/sap/bc/adt/programs/programs/zother" adtcore:type="PROG/P"`), "")
	if err != nil {
		t.Fatalf("ActivateObjects: %v", err)
	}
	if len(sent) != 2 || len(sent[1].refs) != 1 || sent[1].refs[0].URI != preauditClassURI {
		t.Fatalf("follow-up: got %+v, want the requested class only", sent)
	}
}

// #279: an HTTP error on the follow-up POST is returned, without a third POST.
func TestActivateObjects_PreauditFollowUpHTTPError(t *testing.T) {
	sent, err := activateAndCollect(t, preauditClassURI, preauditAnswer, serverErrorAnswer)
	if err == nil {
		t.Fatal("expected the follow-up's 500 to surface")
	}
	if len(sent) != 2 {
		t.Errorf("activation POSTs: got %d, want 2", len(sent))
	}
}
