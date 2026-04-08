# adtler 🦅

[![Unittests](https://github.com/Hochfrequenz/adtler/actions/workflows/test.yml/badge.svg)](https://github.com/Hochfrequenz/adtler/actions/workflows/test.yml)
[![coverage](https://github.com/Hochfrequenz/adtler/actions/workflows/coverage.yml/badge.svg)](https://github.com/Hochfrequenz/adtler/actions/workflows/coverage.yml)
[![golangci-lint](https://github.com/Hochfrequenz/adtler/actions/workflows/golangci-lint.yml/badge.svg)](https://github.com/Hochfrequenz/adtler/actions/workflows/golangci-lint.yml)

Go client library for SAP **A**BAP **D**evelopment **T**ools (ADT).

`adtler` provides a typed Go interface to the SAP ADT REST API: read and write
ABAP source, manage transports, run syntax checks and ATC, lock and activate
objects, browse the repository, and more. It is the runtime that powers
[`mcp-server-abap`](https://github.com/Hochfrequenz/mcp-server-abap) and is
designed to be reusable in CLI tools, CI pipelines, and other Go integrations.

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

## Status

`adtler` was extracted from
[`mcp-server-abap`](https://github.com/Hochfrequenz/mcp-server-abap) and
preserves the full git history of the `adt/` and `auth/` packages. The API is
stable insofar as it is the same code that has been running in production
behind `mcp-server-abap` — but the standalone library is new, so expect minor
adjustments before a `v1.0.0` tag.

## License

[MIT](./LICENSE)
