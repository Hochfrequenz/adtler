package adt

import "context"

// SystemFlavor distinguishes SAP system generations whose ADT semantics
// differ in ways client code must handle defensively — see SystemFlavor
// method doc.
type SystemFlavor int

const (
	SystemFlavorUnknown SystemFlavor = iota
	SystemFlavorECC                  // R/3, NetWeaver-era ABAP
	SystemFlavorS4                   // S/4HANA
)

func (f SystemFlavor) String() string {
	switch f {
	case SystemFlavorECC:
		return "ECC"
	case SystemFlavorS4:
		return "S4"
	default:
		return "Unknown"
	}
}

// SystemFlavor reports whether the active system is ECC (R/3) or S/4HANA,
// derived from the discovery document already cached for content
// negotiation (see NegotiateContentType). /sap/bc/adt/packages is exposed
// only on S/4HANA and recent ABAP Platform releases — package creation via
// ADT REST 404s without it (CreatePackage) — so its presence in the
// discovery collection list is used as the flavor signal.
//
// Consumers need this because ECC and S/4 diverge in ADT semantics beyond
// content negotiation: most notably, ECC's write handlers do not validate
// lockHandle at all (a bogus or expired handle is silently accepted, no
// enqueue is held, and the lockMap cache can drift undetected — see
// aibap.mcp#377). Callers wanting to defend against that should not trust a
// cached lock handle on SystemFlavorECC the way they can on SystemFlavorS4.
//
// Returns SystemFlavorUnknown only when the discovery fetch itself fails
// (network/auth error, surfaced via err); an empty or malformed discovery
// document is treated as ECC, since ECC systems are the ones known to
// return sparser discovery data.
//
// Detection is an exact match on the collection href
// "/sap/bc/adt/packages" — parseDiscovery drops any collection with no
// <app:accept> children, and a system advertising this collection under a
// different href, or with no accepts listed, would misclassify as ECC. This
// is a deliberate fail-safe direction, not an oversight: #377's consumer
// treats SystemFlavorECC as "don't trust the cached lock handle, relock
// defensively" — the safe-but-wasteful failure mode for an S/4 system
// misread as ECC is an extra LockObject call, not a silently corrupted
// write. A prefix scan would be more robust but was not needed for that
// consumer; revisit if a future caller needs tighter detection.
func (c *httpClient) SystemFlavor(ctx context.Context) (SystemFlavor, error) {
	if err := c.ensureCSRF(ctx); err != nil {
		return SystemFlavorUnknown, err
	}
	c.mu.Lock()
	_, hasPackages := c.discovery["/sap/bc/adt/packages"]
	c.mu.Unlock()
	if hasPackages {
		return SystemFlavorS4, nil
	}
	return SystemFlavorECC, nil
}
