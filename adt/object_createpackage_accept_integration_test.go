//go:build integration

package adt_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
)

// endpointUnavailable reports whether CreatePackage refused because the
// /sap/bc/adt/packages endpoint does not exist on this release. CreatePackage
// answers a 404 with its own guidance string rather than an ADTError, so this
// is a substring match on that fixed prefix and not on a server message.
func endpointUnavailable(err error) bool {
	return err != nil && strings.Contains(err.Error(), "endpoint is not available on this SAP system")
}

// TestCreatePackage_DuplicateSurfacesAsADTError_MultiSystem_Integration is the
// live coverage CreatePackage can have without creating anything: it names a
// package that already exists and asserts the refusal comes back as a typed
// ADTError rather than a silent success or an opaque transport error.
//
// # This is deliberately NOT the regression guard for adtler#149
//
// It cannot be, and no live test can be. Measured on SAP S/4HANA on-premise
// (SAP_BASIS 816, S4CORE 109) on 2026-09-21, by running this exact call with
// and without the Accept header:
//
//	with Accept:     400 ExceptionResourceAlreadyExists
//	without Accept:  400 ExceptionResourceAlreadyExists  (identical)
//
// SAP checks the Accept header *last* — only a request that would otherwise
// have succeeded ever reaches that check and comes back 400
// ExceptionResourceBadRequest ("Accept header missing"). Anything SAP rejects
// earlier, whether for a duplicate name or for an empty adtcore:responsible,
// answers the same with or without the header.
//
// So a live guard for #149 must let the request succeed, which means creating a
// package — and this client cannot currently delete one (adtler#150, an open
// ETag bug here rather than an ADT limitation), so each run would strand one on
// every system until that is fixed. That trade is not worth making
// for a header that TestCreatePackage_SendsAcceptHeader already guards on every
// CI run. TestCreatePackage_Integration is the live check, and it is single-use
// per system: it catches the bug on a system where its package does not exist
// yet, and from the next run onward it can only smoke-test. README.md records
// this under "What this client cannot currently undo".
//
// The POST here cannot modify the package it names — SAP rejects a duplicate
// before reading the rest of the payload — so it is safe against a package the
// test did not create.
func TestCreatePackage_DuplicateSurfacesAsADTError_MultiSystem_Integration(t *testing.T) {
	ctx := context.Background()

	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			// Discover the fixture rather than hardcoding one: any local
			// package will do, and $-prefixed means no transport, software
			// component or transport layer is involved.
			pkgs, err := sys.Client.SearchPackages(ctx, "$Z*", 20)
			if err != nil {
				t.Skipf("[%s] cannot search for a local package to name: %v", sys.Name, err)
			}
			var existing string
			for _, p := range pkgs {
				if strings.HasPrefix(p.Name, "$") {
					existing = p.Name
					break
				}
			}
			if existing == "" {
				t.Skipf("[%s] no local package found to name in the request", sys.Name)
			}

			err = sys.Client.CreatePackage(ctx, existing,
				"adtler duplicate-rejection probe", sys.Config.User, "", "", "")

			if endpointUnavailable(err) {
				t.Skipf("[%s] /sap/bc/adt/packages is not available on this release", sys.Name)
			}

			if err == nil {
				t.Fatalf("[%s] CreatePackage reported success for a package that already "+
					"exists — a duplicate must be refused, not silently accepted", sys.Name)
			}

			var adtErr *adt.ADTError
			if !errors.As(err, &adtErr) {
				t.Fatalf("[%s] duplicate rejection did not surface as an *adt.ADTError, "+
					"so callers cannot tell this case apart from a transport failure: %v",
					sys.Name, err)
			}

			// Asserted on the exception ID, which survives message translation;
			// the message text does not.
			if adtErr.Type != "ExceptionResourceAlreadyExists" {
				t.Fatalf("[%s] expected ExceptionResourceAlreadyExists for a duplicate "+
					"package, got %s (HTTP %d): %q",
					sys.Name, adtErr.Type, adtErr.StatusCode, adtErr.Message)
			}

			t.Logf("[%s] duplicate refused with %s (HTTP %d)",
				sys.Name, adtErr.Type, adtErr.StatusCode)
		})
	}
}
