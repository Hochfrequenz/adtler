package adt

import (
	"errors"
	"strings"
	"testing"
)

const wrongInputDataMsg = "Resource ZCL_TEST: wrong input data for processing"

// TestParseADTError_XMLEnvelope verifies the layer-2 path: when the body
// is the standard ADT framework <ExceptionText><message>…</message></ExceptionText>
// envelope, the message is extracted verbatim.
func TestParseADTError_XMLEnvelope(t *testing.T) {
	body := strings.NewReader(`<?xml version="1.0" encoding="utf-8"?>
<exc:ExceptionText xmlns:exc="http://www.sap.com/abapxml/types/communicationframework">
  <message>Resource ZCL_TEST: wrong input data for processing</message>
</exc:ExceptionText>`)
	err := parseADTError(400, body)
	var adtErr *ADTError
	if !errors.As(err, &adtErr) {
		t.Fatalf("expected *ADTError, got %T: %v", err, err)
	}
	if adtErr.StatusCode != 400 {
		t.Errorf("StatusCode: got %d, want 400", adtErr.StatusCode)
	}
	if adtErr.Message != wrongInputDataMsg {
		t.Errorf("Message: got %q", adtErr.Message)
	}
}

// TestParseADTError_HTMLBody regression-tests adtler#13:
// Before the fix, parseADTError dumped the entire HTML body (page chrome,
// CSS, base64 SAP-logo PNG, several KB) into ADTError.Message. After the
// fix, only the user-facing fragments — the errorTextHeader and detailText
// paragraphs — are extracted. The test asserts:
//   - none of the page chrome (DOCTYPE, <style>, <body>) leaks through
//   - all of the relevant text fragments survive
//   - the resulting message is short (under 200 bytes for a typical page)
func TestParseADTError_HTMLBody(t *testing.T) {
	htmlBody := `<!DOCTYPE html>
<html>
<head>
  <meta http-equiv="content-type" content="text/html; charset=windows-1252" />
  <title>Application Server Error</title>
  <style>
    html { font-family: "72", "72full", Helvetica, sans-serif, Arial; font-size: 14px; }
    body { margin: 0; padding: 0; background: #f7f7f7; }
    .errorTextHeader { font-size: 24px; font-weight: bold; }
    .detailText { font-size: 14px; }
    /* … several KB of CSS removed for the test fixture … */
  </style>
</head>
<body>
  <div class="content">
    <div class="valigned">
      <div class="centerText">
        <p class="errorTextHeader">500 Internal Server Error</p>
        <p class="detailText">Internal error code 8.</p>
        <p class="detailText">Server time: 2026-04-09 00:33:26</p>
      </div>
      <img src="data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAOgAAACQCAY..."/>
    </div>
  </div>
</body>
</html>`
	err := parseADTError(500, strings.NewReader(htmlBody))
	var adtErr *ADTError
	if !errors.As(err, &adtErr) {
		t.Fatalf("expected *ADTError, got %T: %v", err, err)
	}
	if adtErr.StatusCode != 500 {
		t.Errorf("StatusCode: got %d, want 500", adtErr.StatusCode)
	}

	msg := adtErr.Message
	// Positive: every relevant fragment must survive.
	wantContains := []string{
		"500 Internal Server Error",
		"Internal error code 8",
		"Server time: 2026-04-09 00:33:26",
	}
	for _, want := range wantContains {
		if !strings.Contains(msg, want) {
			t.Errorf("Message should contain %q, got: %q", want, msg)
		}
	}
	// Negative: no page chrome, no CSS, no PNG.
	wantAbsent := []string{
		"<!DOCTYPE",
		"<style>",
		"font-family",
		"<body>",
		"data:image/png",
		"iVBORw0",
	}
	for _, absent := range wantAbsent {
		if strings.Contains(msg, absent) {
			t.Errorf("Message should NOT contain %q, got: %q", absent, msg)
		}
	}
	// Length: a real SAP error page is several KB; the parsed summary
	// should be a short, single-line(ish) human-readable string.
	if len(msg) > 200 {
		t.Errorf("Message should be short (<200 bytes), got %d: %q", len(msg), msg)
	}
}

// TestParseADTError_HTMLWithoutSAPLayout verifies the fallback for HTML
// bodies that don't match the expected SAP error layout — we still don't
// dump the whole page, but we tell the caller it was HTML.
func TestParseADTError_HTMLWithoutSAPLayout(t *testing.T) {
	body := strings.NewReader(`<!DOCTYPE html>
<html><body><h1>Some other HTML page</h1></body></html>`)
	err := parseADTError(500, body)
	var adtErr *ADTError
	if !errors.As(err, &adtErr) {
		t.Fatalf("expected *ADTError, got %T: %v", err, err)
	}
	if !strings.Contains(adtErr.Message, "HTML error page") {
		t.Errorf("Message should signal HTML response, got: %q", adtErr.Message)
	}
	if strings.Contains(adtErr.Message, "Some other HTML page") {
		t.Errorf("Message should not dump page content, got: %q", adtErr.Message)
	}
}

// TestParseADTError_PlainText verifies the layer-4 fallback for non-XML,
// non-HTML bodies (e.g. an SAP server emitting a bare error string). The
// existing behaviour is preserved.
func TestParseADTError_PlainText(t *testing.T) {
	body := strings.NewReader("  Some plain error message  \n")
	err := parseADTError(500, body)
	var adtErr *ADTError
	if !errors.As(err, &adtErr) {
		t.Fatalf("expected *ADTError, got %T: %v", err, err)
	}
	if adtErr.Message != "Some plain error message" {
		t.Errorf("Message: got %q, want trimmed plain text", adtErr.Message)
	}
}

// TestADTError_Error_WithType verifies that when Type is populated, Error()
// includes it in parentheses after the status code.
func TestADTError_Error_WithType(t *testing.T) {
	e := &ADTError{
		StatusCode: 423,
		Namespace:  "com.sap.adt",
		Type:       "ExceptionResourceLocked",
		Message:    "Object is locked by user X",
	}
	got := e.Error()
	want := "SAP ADT error 423 (ExceptionResourceLocked): Object is locked by user X"
	if got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

// TestADTError_Error_WithoutType verifies that when Type is empty, Error()
// preserves the legacy format exactly.
func TestADTError_Error_WithoutType(t *testing.T) {
	e := &ADTError{StatusCode: 500, Message: "Internal server error"}
	got := e.Error()
	want := "SAP ADT error 500: Internal server error"
	if got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

// TestParseADTError_ExcExceptionEnvelope verifies the new layer 1: when the
// body is the modern <exc:exception> shape with namespace, type, and message
// children, all three are extracted into ADTError.
func TestParseADTError_ExcExceptionEnvelope(t *testing.T) {
	body := strings.NewReader(`<?xml version="1.0" encoding="UTF-8"?>
<exc:exception xmlns:exc="http://www.sap.com/abapxml/types/communicationframework">
  <namespace id="com.sap.adt"/>
  <type id="ExceptionResourceWrongData"/>
  <message lang="EN">Resource ZCL_TEST: wrong input data for processing</message>
</exc:exception>`)
	err := parseADTError(400, body)
	var adtErr *ADTError
	if !errors.As(err, &adtErr) {
		t.Fatalf("expected *ADTError, got %T: %v", err, err)
	}
	if adtErr.StatusCode != 400 {
		t.Errorf("StatusCode: got %d, want 400", adtErr.StatusCode)
	}
	if adtErr.Namespace != "com.sap.adt" {
		t.Errorf("Namespace: got %q, want %q", adtErr.Namespace, "com.sap.adt")
	}
	if adtErr.Type != "ExceptionResourceWrongData" {
		t.Errorf("Type: got %q, want %q", adtErr.Type, "ExceptionResourceWrongData")
	}
	if adtErr.Message != wrongInputDataMsg {
		t.Errorf("Message: got %q", adtErr.Message)
	}
}

// TestParseADTError_ExcExceptionMissingNamespace verifies that when the
// modern envelope omits <namespace>, Type and Message are still extracted
// and Namespace stays empty.
func TestParseADTError_ExcExceptionMissingNamespace(t *testing.T) {
	body := strings.NewReader(`<?xml version="1.0" encoding="UTF-8"?>
<exc:exception xmlns:exc="http://www.sap.com/abapxml/types/communicationframework">
  <type id="ExceptionResourceWrongData"/>
  <message lang="EN">Some message</message>
</exc:exception>`)
	err := parseADTError(400, body)
	var adtErr *ADTError
	if !errors.As(err, &adtErr) {
		t.Fatalf("expected *ADTError, got %T: %v", err, err)
	}
	if adtErr.Namespace != "" {
		t.Errorf("Namespace: got %q, want empty", adtErr.Namespace)
	}
	if adtErr.Type != "ExceptionResourceWrongData" {
		t.Errorf("Type: got %q, want %q", adtErr.Type, "ExceptionResourceWrongData")
	}
	if adtErr.Message != "Some message" {
		t.Errorf("Message: got %q", adtErr.Message)
	}
}

// TestParseADTError_ExcExceptionMissingType verifies that when the modern
// envelope omits <type>, Namespace and Message are still extracted and Type
// stays empty.
func TestParseADTError_ExcExceptionMissingType(t *testing.T) {
	body := strings.NewReader(`<?xml version="1.0" encoding="UTF-8"?>
<exc:exception xmlns:exc="http://www.sap.com/abapxml/types/communicationframework">
  <namespace id="com.sap.adt"/>
  <message lang="EN">Some message</message>
</exc:exception>`)
	err := parseADTError(400, body)
	var adtErr *ADTError
	if !errors.As(err, &adtErr) {
		t.Fatalf("expected *ADTError, got %T: %v", err, err)
	}
	if adtErr.Namespace != "com.sap.adt" {
		t.Errorf("Namespace: got %q, want %q", adtErr.Namespace, "com.sap.adt")
	}
	if adtErr.Type != "" {
		t.Errorf("Type: got %q, want empty", adtErr.Type)
	}
	if adtErr.Message != "Some message" {
		t.Errorf("Message: got %q", adtErr.Message)
	}
}

// TestParseADTError_ExcExceptionMissingMessage verifies that an
// <exc:exception> body without a <message> child falls through to the
// plain-text layer (preserves the existing "empty message means try the
// next layer" semantics shared with the legacy parser).
func TestParseADTError_ExcExceptionMissingMessage(t *testing.T) {
	body := `<?xml version="1.0" encoding="UTF-8"?>
<exc:exception xmlns:exc="http://www.sap.com/abapxml/types/communicationframework">
  <namespace id="com.sap.adt"/>
  <type id="ExceptionResourceWrongData"/>
</exc:exception>`
	err := parseADTError(400, strings.NewReader(body))
	var adtErr *ADTError
	if !errors.As(err, &adtErr) {
		t.Fatalf("expected *ADTError, got %T: %v", err, err)
	}
	// Without a <message>, layer 1 is skipped. Layers 2 and 3 don't match
	// either, so we fall through to layer 4 with the trimmed raw body.
	// Namespace/Type stay empty because layer 1 didn't claim the body.
	if adtErr.Namespace != "" {
		t.Errorf("Namespace: got %q, want empty (layer 1 should not have claimed)", adtErr.Namespace)
	}
	if adtErr.Type != "" {
		t.Errorf("Type: got %q, want empty (layer 1 should not have claimed)", adtErr.Type)
	}
	if !strings.Contains(adtErr.Message, "<exc:exception") {
		t.Errorf("Message: expected raw XML body in plain-text fallback, got %q", adtErr.Message)
	}
}

// TestParseADTError_LegacyEnvelopeNoNamespaceOrType verifies that the legacy
// <ExceptionText> path leaves Namespace and Type empty (regression guard).
func TestParseADTError_LegacyEnvelopeNoNamespaceOrType(t *testing.T) {
	body := strings.NewReader(`<?xml version="1.0" encoding="utf-8"?>
<exc:ExceptionText xmlns:exc="http://www.sap.com/abapxml/types/communicationframework">
  <message>Resource ZCL_TEST: wrong input data for processing</message>
</exc:ExceptionText>`)
	err := parseADTError(400, body)
	var adtErr *ADTError
	if !errors.As(err, &adtErr) {
		t.Fatalf("expected *ADTError, got %T: %v", err, err)
	}
	if adtErr.Namespace != "" {
		t.Errorf("Namespace: got %q, want empty (legacy form has no namespace)", adtErr.Namespace)
	}
	if adtErr.Type != "" {
		t.Errorf("Type: got %q, want empty (legacy form has no type)", adtErr.Type)
	}
	if adtErr.Message != wrongInputDataMsg {
		t.Errorf("Message: got %q", adtErr.Message)
	}
}
