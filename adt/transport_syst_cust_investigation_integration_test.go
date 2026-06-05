//go:build integration

// Investigation tests for issue #63: GetTransportRequests returns empty on
// S/4HANA systems that use KORRDEV=SYST/CUST instead of KORRDEV=K.
//
// Background: the standard ADT endpoint GET /sap/bc/adt/cts/transportrequests
// with Accept: application/vnd.sap.adt.transportorganizertree.v1+xml silently
// returns an empty <tm:root/> on systems where all transport requests use
// KORRDEV=SYST or KORRDEV=CUST. The endpoint is hard-coded server-side
// (in CL_ADT_CTS_MANAGEMENT) to return only KORRDEV=K (classic workbench
// corrections). On some S/4HANA systems, K is no longer used at all.
//
// These tests probe four hypotheses derived from ABAP source analysis of
// IF_CTS_ADT_TM_CONSTANTS and CL_CTS_ADT_TM_CONFIG_HANDLER on S4U:
//
//  1. GapDocumentation: confirms that GetTransportRequests returns fewer
//     transports than E070 contains. Does not fail — documents the bug.
//
//  2. AlternativeAcceptHeader: tests Accept: application/vnd.sap.adt.transportorganizer.v1+xml
//     (CO_SAP_TM_CONTENT_TYPE, without "tree") against the list endpoint.
//
//  3. AllTypeQueryParams: tests CamelCase query parameters discovered in
//     CL_CTS_ADT_TM_CONFIG_HANDLER — WorkbenchRequests, CustomizingRequests,
//     TransportOfCopies, Modifiable — in various combinations.
//
//  4. CombinedHypothesis: non-tree accept header + all type params together.
//
// Run against S4U:
//
//	SAP_INTEGRATION_SYSTEMS=S4U go test -tags=integration -v -run TestGetTransportRequests_SystCust ./adt/...
//
// See: https://github.com/Hochfrequenz/adtler/issues/63
package adt

import (
	"context"
	"encoding/xml"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"testing"

	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"
)

// issue63System pairs a system name with its internal *httpClient for
// issue-63 investigation tests. Using *httpClient (not the Client interface)
// gives access to doRead for raw endpoint probing.
type issue63System struct {
	name string
	c    *httpClient
}

// issue63Systems loads all SAP systems permitted by the SAP_INTEGRATION_SYSTEMS
// (or SAP_INTEGRATION_SYSTEM / default_system) whitelist. It mirrors the
// selection logic of eachSystem in integration_helpers_test.go but returns
// *httpClient values for direct HTTP probing.
func issue63Systems(t *testing.T) []issue63System {
	t.Helper()

	paths := []string{os.Getenv("SAP_CONFIG_FILE")}
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, home+"/.config/sap-mcp/systems.json")
	}
	var cfg *sapmcpconfig.Config
	for _, p := range paths {
		if p == "" {
			continue
		}
		c, err := sapmcpconfig.Load(p)
		if err == nil {
			cfg = c
			break
		}
	}
	if cfg == nil || len(cfg.Systems) == 0 {
		t.Skip("issue63: no SAP config — set SAP_CONFIG_FILE or place systems.json under ~/.config/sap-mcp/")
		return nil
	}

	allowed := make(map[string]bool)
	if raw := strings.TrimSpace(os.Getenv("SAP_INTEGRATION_SYSTEMS")); raw != "" {
		for _, name := range strings.Split(raw, ",") {
			if name = strings.TrimSpace(name); name != "" {
				allowed[name] = true
			}
		}
	} else if s := strings.TrimSpace(os.Getenv("SAP_INTEGRATION_SYSTEM")); s != "" {
		allowed[s] = true
	} else if cfg.DefaultSystem != "" {
		allowed[cfg.DefaultSystem] = true
	}

	var names []string
	for name := range cfg.Systems {
		if allowed[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		t.Skip("issue63: no systems matched SAP_INTEGRATION_SYSTEMS whitelist")
		return nil
	}

	out := make([]issue63System, 0, len(names))
	for _, name := range names {
		sys := cfg.Systems[name]
		sys.TLSSkipVerify = true
		out = append(out, issue63System{
			name: name,
			c:    NewClient(sys).(*httpClient),
		})
	}
	return out
}

// TestGetTransportRequests_SystCust_GapDocumentation_Integration documents the
// gap between E070 and GetTransportRequests on each configured system.
// It queries E070 directly via RunQuery and compares the KORRDEV distribution
// against what GetTransportRequests returns. Logs findings without failing —
// zero ADT results is the documented bug, not a test failure.
func TestGetTransportRequests_SystCust_GapDocumentation_Integration(t *testing.T) {
	for _, sys := range issue63Systems(t) {
		sys := sys
		t.Run(sys.name, func(t *testing.T) {
			ctx := context.Background()
			client := NewClient(sys.c.cfg)

			// E070: count modifiable transports per KORRDEV type.
			e070, err := client.RunQuery(ctx,
				"SELECT KORRDEV, COUNT( TRKORR ) AS CNT FROM E070 WHERE TRSTATUS = 'D' GROUP BY KORRDEV ORDER BY CNT DESCENDING",
				50)
			if err != nil {
				t.Logf("E070 query failed: %v", err)
			} else {
				t.Log("E070 KORRDEV distribution (TRSTATUS=D):")
				korrdevIdx, cntIdx := -1, -1
				for i, col := range e070.Columns {
					switch col.Name {
					case "KORRDEV":
						korrdevIdx = i
					case "CNT":
						cntIdx = i
					}
				}
				for _, row := range e070.Rows {
					korrdev, cnt := "", ""
					if korrdevIdx >= 0 && korrdevIdx < len(row) {
						korrdev = row[korrdevIdx]
					}
					if cntIdx >= 0 && cntIdx < len(row) {
						cnt = row[cntIdx]
					}
					t.Logf("  KORRDEV=%-6q  count=%s", korrdev, cnt)
				}
			}

			// ADT standard endpoint: current behavior.
			transports, err := client.GetTransportRequests(ctx, "", "D")
			if err != nil {
				t.Logf("GetTransportRequests failed: %v", err)
			} else {
				t.Logf("GetTransportRequests returned %d transports (TRSTATUS=D)", len(transports))
				if len(transports) == 0 {
					t.Log("  → zero results: confirms issue #63 on this system")
				}
				for i, tr := range transports {
					if i >= 5 {
						t.Logf("  ... and %d more", len(transports)-5)
						break
					}
					t.Logf("  [%d] %s owner=%s status=%s %q", i, tr.Number, tr.Owner, tr.Status, tr.Description)
				}
			}
		})
	}
}

// TestGetTransportRequests_SystCust_AlternativeAcceptHeader_Integration probes
// whether Accept: application/vnd.sap.adt.transportorganizer.v1+xml
// (CO_SAP_TM_CONTENT_TYPE from IF_CTS_ADT_TM_CONSTANTS, without "tree") returns
// SYST/CUST requests that the transportorganizertree variant omits.
func TestGetTransportRequests_SystCust_AlternativeAcceptHeader_Integration(t *testing.T) {
	const acceptFull = "application/vnd.sap.adt.transportorganizer.v1+xml"

	for _, sys := range issue63Systems(t) {
		sys := sys
		t.Run(sys.name, func(t *testing.T) {
			ctx := context.Background()

			resp, err := sys.c.doRead(ctx, "/sap/bc/adt/cts/transportrequests", map[string]string{
				"Accept": acceptFull,
			})
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()

			body, _ := io.ReadAll(resp.Body)
			t.Logf("HTTP %d  Content-Type: %s", resp.StatusCode, resp.Header.Get("Content-Type"))
			if resp.StatusCode != http.StatusOK {
				t.Logf("non-200 response — accept header not supported on the list endpoint")
				t.Logf("body (first 500 chars): %.500s", body)
				return
			}

			t.Logf("response body (first 1000 chars): %.1000s", body)
			count := countXMLRequestElements(body)
			t.Logf("found %d <*:request> elements", count)
			if count > 0 {
				t.Log("HYPOTHESIS A CONFIRMED: non-tree accept header returns transports")
				logTransportNumbersFromXML(t, body)
			} else {
				t.Log("hypothesis A rejected: still empty with this accept header")
			}
		})
	}
}

// TestGetTransportRequests_SystCust_AllTypeQueryParams_Integration probes
// whether the CamelCase query parameters from CL_CTS_ADT_TM_CONFIG_HANDLER
// unlock SYST/CUST transport results. Four combinations are tested:
//
//   - WorkbenchRequests=true only (baseline)
//   - CustomizingRequests=true only (targets CUST transports)
//   - all three type flags together (WorkbenchRequests + CustomizingRequests + TransportOfCopies)
//   - all three type flags + Modifiable=true (hypothesis: correct param name for open requests)
func TestGetTransportRequests_SystCust_AllTypeQueryParams_Integration(t *testing.T) {
	type probe struct {
		name   string
		params url.Values
	}
	probes := []probe{
		{
			name:   "WorkbenchOnly",
			params: url.Values{"WorkbenchRequests": {"true"}, "Modifiable": {"true"}},
		},
		{
			name:   "CustomizingOnly",
			params: url.Values{"CustomizingRequests": {"true"}, "Modifiable": {"true"}},
		},
		{
			name:   "AllTypes_Modifiable",
			params: url.Values{"WorkbenchRequests": {"true"}, "CustomizingRequests": {"true"}, "TransportOfCopies": {"true"}, "Modifiable": {"true"}},
		},
		{
			name:   "AllTypes_LegacyStatusD",
			params: url.Values{"WorkbenchRequests": {"true"}, "CustomizingRequests": {"true"}, "TransportOfCopies": {"true"}, "status": {"D"}},
		},
	}

	for _, sys := range issue63Systems(t) {
		sys := sys
		t.Run(sys.name, func(t *testing.T) {
			ctx := context.Background()
			for _, p := range probes {
				p := p
				t.Run(p.name, func(t *testing.T) {
					path := "/sap/bc/adt/cts/transportrequests?" + p.params.Encode()
					resp, err := sys.c.doRead(ctx, path, map[string]string{
						"Accept": "application/vnd.sap.adt.transportorganizertree.v1+xml",
					})
					if err != nil {
						t.Fatalf("request failed: %v", err)
					}
					defer func() { _ = resp.Body.Close() }()

					body, _ := io.ReadAll(resp.Body)
					t.Logf("HTTP %d  params: %s", resp.StatusCode, p.params.Encode())
					if resp.StatusCode != http.StatusOK {
						t.Logf("non-200 — params not accepted")
						return
					}

					count := countXMLRequestElements(body)
					t.Logf("found %d <*:request> elements", count)
					if count > 0 {
						t.Logf("HYPOTHESIS B CONFIRMED: %s returns transports", p.name)
						logTransportNumbersFromXML(t, body)
					} else {
						t.Log("still empty with these params")
					}
				})
			}
		})
	}
}

// TestGetTransportRequests_SystCust_CombinedHypothesis_Integration combines the
// non-tree accept header with all type query parameters. This is the strongest
// hypothesis: both the content-type and the filter params are changed together.
func TestGetTransportRequests_SystCust_CombinedHypothesis_Integration(t *testing.T) {
	for _, sys := range issue63Systems(t) {
		sys := sys
		t.Run(sys.name, func(t *testing.T) {
			ctx := context.Background()

			params := url.Values{
				"WorkbenchRequests":   {"true"},
				"CustomizingRequests": {"true"},
				"TransportOfCopies":   {"true"},
				"Modifiable":          {"true"},
			}
			path := "/sap/bc/adt/cts/transportrequests?" + params.Encode()
			resp, err := sys.c.doRead(ctx, path, map[string]string{
				"Accept": "application/vnd.sap.adt.transportorganizer.v1+xml",
			})
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()

			body, _ := io.ReadAll(resp.Body)
			t.Logf("HTTP %d  Content-Type: %s", resp.StatusCode, resp.Header.Get("Content-Type"))
			t.Logf("response body (first 2000 chars): %.2000s", body)

			if resp.StatusCode != http.StatusOK {
				t.Logf("non-200 response — combined hypothesis rejected")
				return
			}

			count := countXMLRequestElements(body)
			t.Logf("found %d <*:request> elements", count)
			if count > 0 {
				t.Log("COMBINED HYPOTHESIS CONFIRMED: non-tree header + all type params returns transports")
				logTransportNumbersFromXML(t, body)
			} else {
				t.Log("combined hypothesis rejected: still empty")
			}
		})
	}
}

// countXMLRequestElements counts XML start elements whose local name is "request",
// regardless of namespace prefix.
func countXMLRequestElements(data []byte) int {
	count := 0
	d := xml.NewDecoder(strings.NewReader(string(data)))
	for {
		tok, err := d.Token()
		if err != nil {
			break
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "request" {
			count++
		}
	}
	return count
}

// logTransportNumbersFromXML logs the first five transport numbers found in an
// XML response (any element with a "number" attribute).
func logTransportNumbersFromXML(t *testing.T, data []byte) {
	t.Helper()
	d := xml.NewDecoder(strings.NewReader(string(data)))
	logged := 0
	for {
		tok, err := d.Token()
		if err != nil {
			break
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != "request" {
			continue
		}
		for _, attr := range se.Attr {
			if attr.Name.Local == "number" && attr.Value != "" {
				t.Logf("  transport: %s", attr.Value)
				logged++
				if logged >= 5 {
					return
				}
				break
			}
		}
	}
}
