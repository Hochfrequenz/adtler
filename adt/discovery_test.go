package adt

import "testing"

const testPkgV2 = "application/vnd.sap.adt.packages.v2+xml"
const testPkgV1 = "application/vnd.sap.adt.packages.v1+xml"

func TestParseDiscovery(t *testing.T) {
	xml := `<?xml version="1.0" encoding="utf-8"?>
<app:service xmlns:app="http://www.w3.org/2007/app" xmlns:atom="http://www.w3.org/2005/Atom">
  <app:workspace>
    <atom:title>Package</atom:title>
    <app:collection href="/sap/bc/adt/packages">
      <atom:title>Package</atom:title>
      <app:accept>application/vnd.sap.adt.packages.v2+xml</app:accept>
      <app:accept>application/vnd.sap.adt.packages.v1+xml</app:accept>
    </app:collection>
  </app:workspace>
  <app:workspace>
    <atom:title>Check</atom:title>
    <app:collection href="/sap/bc/adt/checkruns">
      <atom:title>Check</atom:title>
    </app:collection>
  </app:workspace>
</app:service>`

	result := parseDiscovery([]byte(xml))

	if len(result) != 1 {
		t.Fatalf("expected 1 endpoint with accepts, got %d", len(result))
	}

	accepts := result["/sap/bc/adt/packages"]
	if len(accepts) != 2 {
		t.Fatalf("expected 2 accepts for /sap/bc/adt/packages, got %d", len(accepts))
	}
	if accepts[0] != testPkgV2 {
		t.Errorf("first accept: got %q", accepts[0])
	}
	if accepts[1] != testPkgV1 {
		t.Errorf("second accept: got %q", accepts[1])
	}
}

func TestNegotiateContentType(t *testing.T) {
	c := &httpClient{
		discovery: map[string][]string{
			"/sap/bc/adt/packages": {
				testPkgV2,
				testPkgV1,
			},
		},
	}

	// Prefer v2, system has v2
	got := c.NegotiateContentType("/sap/bc/adt/packages",
		[]string{testPkgV2, testPkgV1},
		"application/xml")
	if got != testPkgV2 {
		t.Errorf("expected v2, got %q", got)
	}

	// Endpoint not in discovery → fallback
	got = c.NegotiateContentType("/sap/bc/adt/unknown",
		[]string{"application/vnd.sap.adt.foo.v2+xml"},
		"application/xml")
	if got != "application/xml" {
		t.Errorf("expected fallback, got %q", got)
	}

	// Prefer v3 but system only has v2/v1 → should pick v2 as second choice
	got = c.NegotiateContentType("/sap/bc/adt/packages",
		[]string{"application/vnd.sap.adt.packages.v3+xml", testPkgV2},
		"application/xml")
	if got != testPkgV2 {
		t.Errorf("expected v2 fallback, got %q", got)
	}
}

func TestAcceptHeaderForURI_UsesDiscovery(t *testing.T) {
	// System only supports v1 for packages (older ECC)
	c := &httpClient{
		discovery: map[string][]string{
			"/sap/bc/adt/packages": {testPkgV1},
		},
	}

	// Hardcoded default is v2, but discovery only has v1 → should use v1
	got := c.acceptHeaderForURI("/sap/bc/adt/packages/Z_MY_PKG")
	if got != testPkgV1+", application/xml" {
		t.Errorf("expected v1 from discovery, got %q", got)
	}
}

func TestAcceptHeaderForURI_FallsBackToHardcoded(t *testing.T) {
	// No discovery data → use hardcoded defaults
	c := &httpClient{}

	got := c.acceptHeaderForURI("/sap/bc/adt/packages/Z_MY_PKG")
	want := "application/vnd.sap.adt.packages.v2+xml, application/xml"
	if got != want {
		t.Errorf("expected hardcoded fallback %q, got %q", want, got)
	}
}

func TestAcceptHeaderForURI_UnknownURI(t *testing.T) {
	c := &httpClient{}
	got := c.acceptHeaderForURI("/sap/bc/adt/something/unknown")
	if got != "application/xml" {
		t.Errorf("expected generic xml, got %q", got)
	}
}

// TestAcceptHeaderForURI_FUGRInclude regression-tests adtler#17:
// S/4 returns HTTP 406 when GetObjectInfo for a function group include URI
// sends the parent group's Accept header (functions.groups.v3+xml). The
// include sub-resource needs functions.fincludes.v2+xml. Without the
// special case in acceptHeaderForURI, the longest-prefix loop returns the
// parent type for both the bare FUGR and its includes — wrong for includes.
func TestAcceptHeaderForURI_FUGRInclude(t *testing.T) {
	c := &httpClient{}
	want := "application/vnd.sap.adt.functions.fincludes.v2+xml, application/xml"

	got := c.acceptHeaderForURI("/sap/bc/adt/functions/groups/zmy_fg/includes/lzmy_fgtop")
	if got != want {
		t.Errorf("FUGR include: got %q, want %q", got, want)
	}

	// Also verify with a different (uppercase, namespaced) include name.
	got = c.acceptHeaderForURI("/sap/bc/adt/functions/groups/ZMY_FG/includes/LZMY_FGUXX")
	if got != want {
		t.Errorf("uppercase FUGR include: got %q, want %q", got, want)
	}
}

// TestAcceptHeaderForURI_FUGRBare guards the FUGR include fix from over-
// reaching: the bare function group URI must still return the *group* type
// (functions.groups.v3+xml), not the include type. The include special-case
// must only fire when "/includes/" is actually present in the path.
func TestAcceptHeaderForURI_FUGRBare(t *testing.T) {
	c := &httpClient{}
	want := "application/vnd.sap.adt.functions.groups.v3+xml, application/xml"

	got := c.acceptHeaderForURI("/sap/bc/adt/functions/groups/zmy_fg")
	if got != want {
		t.Errorf("bare FUGR: got %q, want %q", got, want)
	}
}

// TestAcceptHeaderForURI_RAPTypes covers the three object kinds adtler#65
// added. The Accept header is the whole story for a service binding: asking
// its URI for "application/xml" — which is what the fallback below returns
// for an unknown prefix — answers 406, and the issue read that 406 as a
// missing endpoint. The path was right all along.
//
// Media types measured against SAP S/4HANA on-premise (SAP_BASIS 816,
// S4CORE 109) on 2026-09-22, and cross-checked against the app:accept
// entries the system's own /sap/bc/adt/discovery document publishes for
// these collections.
func TestAcceptHeaderForURI_RAPTypes(t *testing.T) {
	c := &httpClient{}
	cases := []struct {
		name string
		uri  string
		want string
	}{
		{
			"behavior definition",
			"/sap/bc/adt/bo/behaviordefinitions/zbd",
			"application/vnd.sap.adt.blues.v1+xml, application/xml",
		},
		{
			"service definition",
			"/sap/bc/adt/ddic/srvd/sources/zsd",
			"application/vnd.sap.adt.ddic.srvd.v1+xml, application/xml",
		},
		{
			"service binding",
			"/sap/bc/adt/businessservices/bindings/zsb",
			"application/vnd.sap.adt.businessservices.servicebinding.v2+xml, application/xml",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := c.acceptHeaderForURI(tc.uri); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
