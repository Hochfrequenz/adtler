package adt

import (
	"errors"
	"strings"
	"testing"
)

// TestParseADTError_XMLEnvelope verifies the layer-1 path: when the body
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
	if adtErr.Message != "Resource ZCL_TEST: wrong input data for processing" {
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

// TestParseADTError_PlainText verifies the layer-3 fallback for non-XML,
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
