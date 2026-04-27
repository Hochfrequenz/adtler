//go:build integration

package adt_test

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"

	"github.com/Hochfrequenz/adtler/adt"
)

// testPackage is the SAP package that holds all persistent integration test objects.
// It must exist on the target system before running integration tests.
// Source and installation instructions: https://github.com/Hochfrequenz/Z_ADT_MCP_TEST
const testPackage = "Z_ADT_MCP_TEST"

// Test fixture URIs — all objects live in testPackage (S4) or $TMP (ECC).
// Created automatically by TestMain via setupFixtures. See also fixtures_integration_test.go.
const (
	testReportURI    = "/sap/bc/adt/programs/programs/Z_ADT_MCP_TEST_REPORT"
	testSynWarnURI   = "/sap/bc/adt/programs/programs/Z_ADT_MCP_TEST_SYNWARN"
	testInterfaceURI = "/sap/bc/adt/oo/interfaces/ZIF_ADT_MCP_TEST"
	testClassURI     = "/sap/bc/adt/oo/classes/ZCL_ADT_MCP_TEST_UNITS"
	testClassNoTests = "/sap/bc/adt/oo/classes/ZCL_ADT_MCP_TEST_NOUNITS"
)

// integrationConfig builds a SAPSystem. It tries, in order:
//  1. JSON config file (same as MCP server) + SAP_INTEGRATION_SYSTEM env var
//  2. Fallback env vars: SAP_INTEGRATION_HOST, SAP_INTEGRATION_USER, etc.
//
// JSON paths searched: SAP_CONFIG_FILE env var, ~/.config/sap-mcp/systems.json
func integrationConfig() sapmcpconfig.SAPSystem {
	// Try JSON config first
	if cfg, ok := integrationConfigFromFile(); ok {
		return cfg
	}
	// Fallback to legacy env vars
	return sapmcpconfig.SAPSystem{
		Host:          strings.TrimSpace(os.Getenv("SAP_INTEGRATION_HOST")),
		User:          strings.TrimSpace(os.Getenv("SAP_INTEGRATION_USER")),
		Password:      os.Getenv("SAP_INTEGRATION_PASSWORD"),
		Client:        os.Getenv("SAP_INTEGRATION_CLIENT"),
		TLSSkipVerify: true,
	}
}

func integrationConfigFromFile() (sapmcpconfig.SAPSystem, bool) {
	paths := []string{os.Getenv("SAP_CONFIG_FILE")}
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, home+"/.config/sap-mcp/systems.json")
	}

	var cfg *sapmcpconfig.Config
	for _, p := range paths {
		if p == "" {
			continue
		}
		var err error
		cfg, err = sapmcpconfig.Load(p)
		if err == nil {
			break
		}
	}
	if cfg == nil {
		return sapmcpconfig.SAPSystem{}, false
	}

	// Pick system: SAP_INTEGRATION_SYSTEM env var, or default_system from config
	systemName := os.Getenv("SAP_INTEGRATION_SYSTEM")
	if systemName == "" {
		systemName = cfg.DefaultSystem
	}

	// Safety guard: only run tests against explicitly allowed systems.
	// Set SAP_INTEGRATION_SYSTEMS (comma-separated) to whitelist; if unset,
	// only the default system is allowed.
	if allowed := os.Getenv("SAP_INTEGRATION_SYSTEMS"); allowed != "" {
		found := false
		for _, s := range strings.Split(allowed, ",") {
			if strings.TrimSpace(s) == systemName {
				found = true
				break
			}
		}
		if !found {
			return sapmcpconfig.SAPSystem{}, false
		}
	}

	sys, ok := cfg.Systems[systemName]
	if !ok {
		return sapmcpconfig.SAPSystem{}, false
	}
	sys.TLSSkipVerify = true
	return sys, true
}

// newIntegrationClient creates a real ADT client from environment variables.
// Do not log the returned client or its config — they contain credentials.
func newIntegrationClient(t *testing.T) adt.Client {
	t.Helper()
	cfg := integrationConfig()
	if cfg.Host == "" {
		t.Skip("No SAP config found — set SAP_CONFIG_FILE or SAP_INTEGRATION_HOST")
	}
	if cfg.User == "" {
		t.Fatal("SAP user not configured — check YAML config or SAP_INTEGRATION_USER")
	}
	if cfg.Password == "" {
		t.Fatal("SAP password not configured — check YAML config or SAP_INTEGRATION_PASSWORD")
	}
	return adt.NewClient(cfg)
}

// integrationSystem pairs a logical system name (the key in systems.json) with
// a ready-to-use adt.Client built from that system's credentials.
type integrationSystem struct {
	Name   string
	Client adt.Client
}

// eachSystem returns one entry per SAP system the test run is allowed to hit,
// so a single test function can validate behaviour against R/3 and S/4 in one
// go via t.Run sub-tests:
//
//	for _, sys := range eachSystem(t) {
//		sys := sys
//		t.Run(sys.Name, func(t *testing.T) {
//			result, err := sys.Client.GetMessageClass(ctx, "00")
//			...
//		})
//	}
//
// The system list is the intersection of:
//  1. Systems present in the JSON config (SAP_CONFIG_FILE or
//     ~/.config/sap-mcp/systems.json).
//  2. A whitelist resolved in this order:
//     a. SAP_INTEGRATION_SYSTEMS (plural, comma-separated) — explicit
//     multi-system whitelist
//     b. SAP_INTEGRATION_SYSTEM (singular) — compat with newIntegrationClient
//     c. cfg.DefaultSystem from the JSON config
//
// **The helper t.Skips and never returns to the caller** when no JSON config
// is reachable or when no whitelisted system exists in the config. Callers
// can therefore rely on the returned slice always being non-empty when
// execution continues past this call. This intentionally does NOT fall back
// to the legacy SAP_INTEGRATION_HOST env vars — those only describe a single
// system and the whole point of eachSystem is parametrization.
//
// The helper does NOT verify that whitelisted systems have credentials
// configured; if a system entry has no User/Password the first HTTP call
// against its client will fail loudly. This matches newIntegrationClient's
// behaviour for misconfigured systems.
func eachSystem(t *testing.T) []integrationSystem {
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
		t.Skip("eachSystem: no SAP JSON config found — set SAP_CONFIG_FILE or place systems.json under ~/.config/sap-mcp/")
	}

	// Whitelist resolution, in order of precedence:
	//
	//  1. SAP_INTEGRATION_SYSTEMS (comma-separated, plural) — explicit
	//     multi-system whitelist. Empty/whitespace-only entries are skipped.
	//  2. SAP_INTEGRATION_SYSTEM (singular) — single-system whitelist for
	//     compatibility with newIntegrationClient. A developer who already
	//     uses the singular var to point newIntegrationClient at one system
	//     gets the same single-system run from eachSystem.
	//  3. cfg.DefaultSystem from the JSON config.
	//
	// `allowed` is always non-nil after this block; the helper t.Skips below
	// if neither resolves to anything. This guarantees we never silently run
	// against every system in the config when none was explicitly requested
	// — that's the safety posture newIntegrationClient maintains.
	allowed := make(map[string]bool)
	if raw := strings.TrimSpace(os.Getenv("SAP_INTEGRATION_SYSTEMS")); raw != "" {
		for _, name := range strings.Split(raw, ",") {
			if name = strings.TrimSpace(name); name != "" {
				allowed[name] = true
			}
		}
	} else if singular := strings.TrimSpace(os.Getenv("SAP_INTEGRATION_SYSTEM")); singular != "" {
		allowed[singular] = true
	} else if cfg.DefaultSystem != "" {
		allowed[cfg.DefaultSystem] = true
	}

	// Deterministic sub-test names so failing -run filters reproduce the
	// same order on every machine.
	names := make([]string, 0, len(cfg.Systems))
	for name := range cfg.Systems {
		if !allowed[name] {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)

	if len(names) == 0 {
		t.Skip("eachSystem: no systems matched the SAP_INTEGRATION_SYSTEMS whitelist")
	}

	systems := make([]integrationSystem, 0, len(names))
	for _, name := range names {
		sys := cfg.Systems[name]
		sys.TLSSkipVerify = true
		systems = append(systems, integrationSystem{
			Name:   name,
			Client: adt.NewClient(sys),
		})
	}
	return systems
}

// setupDisposableReport creates a $TMP program with the given name and initial
// source, activates it, and registers cleanup to delete it after the test.
// Returns the object URI. No transport is needed for $TMP objects.
func setupDisposableReport(t *testing.T, client adt.Client, name, initialSource string) string {
	t.Helper()
	ctx := context.Background()
	objectURI := "/sap/bc/adt/programs/programs/" + name

	err := client.CreateObject(ctx, "PROG", name, "$TMP",
		fmt.Sprintf("Integration test (%s)", time.Now().Format("2006-01-02")), "")
	if err != nil {
		// Object may already exist from a previous aborted run — try to use it.
		if _, infoErr := client.GetObjectInfo(ctx, objectURI); infoErr != nil {
			t.Fatalf("CreateObject %s failed and object does not exist: %v", name, err)
		}
		t.Logf("object %s already exists, reusing", name)
	}

	// Set initial source so the object is in a known state.
	lockHandle, err := client.LockObject(ctx, objectURI)
	if err != nil {
		t.Fatalf("LockObject for setup of %s failed: %v", name, err)
	}
	src, err := client.GetSource(ctx, objectURI)
	if err != nil {
		_ = client.UnlockObject(ctx, objectURI, lockHandle)
		t.Fatalf("GetSource for setup of %s failed: %v", name, err)
	}
	_, err = client.SetSource(ctx, objectURI, initialSource, lockHandle, "", src.ETag)
	if err != nil {
		_ = client.UnlockObject(ctx, objectURI, lockHandle)
		t.Fatalf("SetSource for setup of %s failed: %v", name, err)
	}
	_ = client.UnlockObject(ctx, objectURI, lockHandle)

	// Activate so tests start from a known-good active state.
	result, err := client.ActivateObjects(ctx, []string{objectURI})
	if err != nil {
		t.Fatalf("ActivateObjects for setup of %s failed: %v", name, err)
	}
	if !result.Success {
		t.Fatalf("activation of %s failed: %d messages", name, len(result.Messages))
	}

	t.Cleanup(func() {
		if err := client.DeleteObject(context.Background(), objectURI, "", ""); err != nil {
			t.Logf("WARNING: cleanup failed to delete %s: %v", name, err)
		}
	})

	return objectURI
}
