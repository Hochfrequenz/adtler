# adtler 🦅

[![Unittests](https://github.com/Hochfrequenz/adtler/actions/workflows/test.yml/badge.svg)](https://github.com/Hochfrequenz/adtler/actions/workflows/test.yml)
[![coverage](https://github.com/Hochfrequenz/adtler/actions/workflows/coverage.yml/badge.svg)](https://github.com/Hochfrequenz/adtler/actions/workflows/coverage.yml)
[![golangci-lint](https://github.com/Hochfrequenz/adtler/actions/workflows/golangci-lint.yml/badge.svg)](https://github.com/Hochfrequenz/adtler/actions/workflows/golangci-lint.yml)

Go client library for SAP **A**BAP **D**evelopment **T**ools (ADT).
For a Python wrapper around the SAP COM GUI, check [**sapsucker**](https://github.com/Hochfrequenz/sapsucker).

`adtler` provides a typed Go interface to the SAP ADT REST API: read and write
ABAP source, manage transports, run syntax checks and ATC, lock and activate
objects, browse the repository, and more. It is the runtime that powers
[`mcp-server-abap`](https://github.com/Hochfrequenz/mcp-server-abap) and is
designed to be reusable in CLI tools, CI pipelines, and other Go integrations.
Unlike many ADT clients, `adtler` includes proper integration tests against both
SAP ECC R/3 and SAP S/4HANA.

## Install

```bash
go get github.com/Hochfrequenz/adtler/adt
```

## Quick start

```go
package main

import (
    "context"
    "fmt"

    "github.com/Hochfrequenz/adtler/adt"
    sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"
)

func main() {
    sys := sapmcpconfig.SAPSystem{
        Host:     "https://sap.example.com",
        Client:   "100",
        User:     "DEVELOPER",
        Password: "secret",
    }
    client := adt.NewClient(sys)

    src, err := client.GetSource(context.Background(), "/sap/bc/adt/programs/programs/ZHELLO/source/main")
    if err != nil {
        panic(err)
    }
    fmt.Println(src.Source)
}
```

For multi-system setups (e.g. dev/test/prod), use `adt.NewClientsFromConfig`
plus `adt.NewClientRegistry` — see [`adt/`](./adt) for details.

## Packages

| Package | Purpose |
|---------|---------|
| [`adt/`](./adt) | HTTP client for the SAP ADT REST API. All operations: source, transports, locks, activation, syntax check, ATC, unit tests, ... |
| [`adt/adtxml/`](./adt/adtxml) | XML marshalling types used by `adt/` |
| [`adt/custexport/`](./adt/custexport) | SAP customizing-table export (SQLite/JSON) |
| [`auth/`](./auth) | OAuth2 PKCE flow and on-disk token storage |

## Configuration

Credentials live in JSON files matching the schema defined by
[`sap-mcp-config`](https://github.com/Hochfrequenz/sap-mcp-config). The
default token store path is `~/.config/sap-adt/tokens.json`.

## Testing

### Unit tests

```bash
go test ./...
```

Unit tests use `httptest` to mock the SAP server and run on every PR. They
don't need credentials or network access.

### Integration tests

Integration tests are gated by the `integration` build tag and only run when
explicitly requested:

```bash
go test -tags=integration ./adt/...
```

They hit a real SAP system and need credentials. The recommended setup is a
JSON config file matching the
[`sap-mcp-config`](https://github.com/Hochfrequenz/sap-mcp-config) schema —
the same file `mcp-server-abap` and the MCP server CLI use:

```bash
# Default location, picked up automatically:
~/.config/sap-mcp/systems.json

# Or override per-run:
export SAP_CONFIG_FILE=/path/to/custom/systems.json
```

The repo also requires the
[`Z_ADT_MCP_TEST`](https://github.com/Hochfrequenz/Z_ADT_MCP_TEST) ABAP
package on the target system for fixtures used by some integration tests.

#### Single-system tests

`newIntegrationClient(t)` returns one client based on the
`SAP_INTEGRATION_SYSTEM` env var (or the config's `default_system`). Use it
when a test only needs to validate behaviour against one system at a time.

#### Multi-system tests

`eachSystem(t)` iterates over every system in the
`SAP_INTEGRATION_SYSTEMS` whitelist (comma-separated, e.g.
`SAP_INTEGRATION_SYSTEMS=hfq,s4u`) and yields one
`{Name, Client}` per system. Use it when a test needs to validate the same
operation against both R/3 and S/4 — for example, header / vendor-MIME-type
fixes that pass on R/3's lenient handler but fail on S/4's strict one:

```go
//go:build integration

func TestSomething_MultiSystem_Integration(t *testing.T) {
    ctx := context.Background()
    for _, sys := range eachSystem(t) {
        sys := sys
        t.Run(sys.Name, func(t *testing.T) {
            result, err := sys.Client.GetMessageClass(ctx, "00")
            // assertions...
        })
    }
}
```

Resolution order for the whitelist (first match wins):
1. `SAP_INTEGRATION_SYSTEMS` (plural, comma-separated) — explicit multi-system list
2. `SAP_INTEGRATION_SYSTEM` (singular) — single-system, compat with `newIntegrationClient`
3. `cfg.DefaultSystem` from the JSON config

Both helpers `t.Skip` cleanly when no JSON config is reachable, so
`go test -tags=integration ./...` is safe to run on a developer machine
without SAP credentials configured — the integration tests just don't
execute.

#### Running the heavy regression sweep

Before tagging a release, run integration tests against **both** R/3 and S/4
to catch system-specific bugs:

```bash
SAP_INTEGRATION_SYSTEMS=hfq,s4u go test -tags=integration -v ./adt/...
```

Where `hfq` and `s4u` are the keys of your R/3 and S/4 entries in
`systems.json`.

## Status

`adtler` was extracted from
[`mcp-server-abap`](https://github.com/Hochfrequenz/mcp-server-abap) and
preserves the full git history of the `adt/` and `auth/` packages. The API is
stable insofar as it is the same code that has been running in production
behind `mcp-server-abap` — but the standalone library is new, so expect minor
adjustments before a `v1.0.0` tag.

## License

[MIT](./LICENSE)
