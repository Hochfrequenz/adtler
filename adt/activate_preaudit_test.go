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
