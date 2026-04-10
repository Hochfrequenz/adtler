# Discovery-First Content Negotiation (Issue #35)

**Status:** Design
**Date:** 2026-04-10
**Branch:** `refactor/35-discovery-content-negotiation`
**Related issues:** Hochfrequenz/adtler#35, Hochfrequenz/adtler#12

## Problem

The discovery infrastructure (`parseDiscovery`, `NegotiateContentType`, `acceptHeaderForURI`) exists in `adt/discovery.go` and `adt/repository.go`, but is only used for object metadata reads (`GetObjectInfo`, `DeleteObject`). Source operations, ETag fetching, and ATC checks hardcode Accept/Content-Type headers, which causes failures on systems that advertise different API versions via discovery.

Concrete symptoms already observed:

- **#9** (fixed) CLAS `ResolveETag` → `GetSource` → 400 on S/4
- **#14** (fixed) DTEL/DOMA `GetSource` hardcodes `/source/main` → 404
- **#15** (fixed) TABL ETag charset mismatch → 412
- **#12** (open, `blocked:sap-investigation`) RunATCCheck HTTP 500 on R/3, possibly wrong API version

Every new object type added to the hardcoded `objectTypeAcceptHeaders` map is a potential mismatch with what the server actually supports. S/4 and ECC advertise different API versions via discovery — the infrastructure to handle this already exists, it just needs to be wired in.

## Goals

1. Source operations (`GetSource`, `SetSource`, `GetIncludeSource`, `SetIncludeSource`) drive Accept/Content-Type from discovery data, with a hardcoded fallback.
2. ETag fetching (`ResolveETag` / `FetchETag`) uses discovery-aware Accept headers consistently.
3. `RunATCCheck` drives its Content-Type from discovery for the ATC endpoint.
4. Discovery is loaded eagerly on the first request of any kind, not just the first mutation.
5. No behavioural regression on systems where discovery is empty or the endpoint is missing from the cache — all call sites fall back to the existing hardcoded defaults.

## Non-goals

- Restructuring the ETag charset handling itself. The existing 412 retry in `SetSource` stays. A separate enhancement issue will track "normalize ETag charset at receive time".
- Removing the `objectTypeAcceptHeaders` map. It remains as the fallback.
- Changing the discovery XML parser.

## Architecture

### Current state

```
doRequest ──► (if CSRF token empty) fetchCSRFToken ──► parseDiscovery ──► c.discovery
                                                                             │
                                                                             ▼
                             acceptHeaderForURI (metadata reads only) ◄─ NegotiateContentType
                                                                             ▲
                                                                             │
                                                       GetSource / SetSource / RunATCCheck
                                                       (hardcoded, never reach here)
```

### Target state

```
doRequest ──► (if CSRF token empty) fetchCSRFToken ──► parseDiscovery ──► c.discovery
                                                                             │
                                             ┌───────────────────────────────┤
                                             ▼                               ▼
                        acceptHeaderForURI (metadata, ETag)        sourceContentType (source ops)
                                             │                               │
                                             └─────────► NegotiateContentType ◄─── RunATCCheck
                                                                   │
                                                                   ▼
                                                 hardcoded fallback map (objectTypeAcceptHeaders)
```

## Component changes

### 1. Eager discovery (`adt/client.go`)

`doRequest` already checks the CSRF token before every request. `fetchCSRFToken` already parses discovery from the same response. The only thing to verify: no code path skips the CSRF fetch for read-only requests. If any path exists that sends a GET without ensuring `c.csrfToken != "" || discovery is loaded`, fix it so discovery is always populated on the first request of any kind.

**Acceptance:** A unit test that issues a single `GetSource` against a mock server sees two HTTP calls: (1) CSRF/discovery fetch, (2) the actual GET — and the second call's Accept header reflects the discovery-advertised type.

### 2. `sourceContentType` (`adt/source.go`)

New unexported helper:

```go
// sourceContentType returns the Accept/Content-Type to use for source
// operations on the given endpoint. It consults discovery first and falls
// back to "text/plain" if discovery has no entry for the endpoint.
func (c *httpClient) sourceContentType(endpoint string) string {
    return c.NegotiateContentType(endpoint,
        []string{"text/plain", "text/plain; charset=utf-8"},
        "text/plain")
}
```

`endpoint` is the path prefix from the request URI (e.g. the object's `Href` from discovery). The caller is responsible for passing the discovery-cache key — typically the object URI without query string.

**Used by:** `GetSource`, `SetSource`, `GetIncludeSource`, `SetIncludeSource`.

### 3. `acceptHeaderForURI` reused by ETag operations

`FetchETag` already uses `acceptHeaderForURI`. `ResolveETag` goes through `GetSource`, which after the refactor automatically uses `sourceContentType`. No new code in `lockmap.go` — we verify the chain is correct with tests.

### 4. ATC content negotiation (`adt/atc.go`)

`RunATCCheck` currently hardcodes the POST body Content-Type. After the refactor:

```go
ct := c.NegotiateContentType("/sap/bc/adt/atc/runs", preferred, currentHardcodedDefault)
req.Header.Set("Content-Type", ct)
```

The `preferred` list is the set of ATC content types the client knows how to build. The hardcoded default is whatever the code uses today — guaranteeing no regression on systems where discovery has no ATC entry.

**Acceptance:** An integration test runs `RunATCCheck` against R/3 and S/4. On S/4 it must continue to pass. On R/3 the 500 error may disappear (which would close #12) or may persist (in which case #12 stays open for SAP-side investigation).

### 5. Existing 412 retry in `SetSource`

Unchanged. Lives alongside the new content-type selection.

## Error handling & edge cases

| Case | Behaviour |
|---|---|
| Discovery XML parse fails | `parseDiscovery` returns nil → `NegotiateContentType` returns `defaultCT` → hardcoded fallback |
| Discovery endpoint missing from cache | `NegotiateContentType` returns `defaultCT` → hardcoded fallback |
| Concurrent first request | `c.mu` (existing) serialises CSRF/discovery fetch |
| 412 on `SetSource` | Existing retry path (from #15 fix) runs unchanged |
| ATC 500 on R/3 | If discovery has no ATC entry, fallback = status quo, no regression |

## Testing strategy

### Unit tests (`httptest`)

1. `TestDoRequest_LoadsDiscoveryBeforeFirstRead` — single `GetSource` triggers CSRF+discovery fetch before the actual GET.
2. `TestSourceContentType_UsesDiscovery` — discovery advertises a non-default type for `/source/main` → `GetSource` sends that type in `Accept`.
3. `TestSourceContentType_FallbackWhenDiscoveryEmpty` — empty discovery → hardcoded `text/plain`.
4. `TestSetSource_ContentTypeFromDiscovery` — same as above for PUT.
5. `TestRunATCCheck_ContentTypeFromDiscovery` — discovery advertises ATC v2 → POST uses v2.
6. `TestRunATCCheck_FallbackWhenDiscoveryEmpty` — empty discovery → hardcoded current default.

### Integration tests (`eachSystem(t)`, R/3 + S/4, `-tags=integration`)

1. `TestSourceOperations_DiscoveryDriven_AllObjectTypes` — `GetSource` + `SetSource` cycle for CLAS, PROG, DTEL (S/4 only), DOMA (S/4 only), TABL. Must not return 400/415.
2. `TestResolveETag_AllObjectTypes` — `LockObject` + `ResolveETag` for each object type. ETag must resolve successfully.
3. `TestRunATCCheck_BothSystems` — `RunATCCheck` on R/3 and S/4. Must not return 500. On R/3 this may close #12.

All integration tests use `eachSystem(t)` so they run once per configured system and report per-system results.

## Implementation order

1. Verify/ensure eager discovery load in `doRequest`. Unit test.
2. Add `sourceContentType` helper. Wire into `GetSource`. Unit test.
3. Wire `sourceContentType` into `SetSource`, `GetIncludeSource`, `SetIncludeSource`. Unit tests.
4. Verify `ResolveETag`/`FetchETag` chain. Unit test at the `ResolveETag` boundary.
5. Wire `NegotiateContentType` into `RunATCCheck`. Unit tests.
6. Write integration tests. Run locally if possible, otherwise let the integration-test agent run them via the PR workflow.
7. Open a separate enhancement issue: "normalize ETag charset at receive time" (captures option B from the brainstorm — replace the 412 retry with receive-time normalisation).

## Rollout

Single PR, following the established fix/review/test/merge cycle (CLAUDE.md):

1. Implement on `refactor/35-discovery-content-negotiation`
2. Open PR, link #35 and consumer-side issues where relevant
3. Reviewer agent (fresh context) evaluates correctness
4. CI green (lint, unit, coverage, CodeQL)
5. Integration-test agent runs against R/3 + S/4 via `eachSystem(t)`
6. Remove `needs:integration-test` label, auto-merge squash
