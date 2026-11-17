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

	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config/v2"

	"github.com/Hochfrequenz/adtler/adt"
)

// testPackage is the SAP package that holds all persistent integration test objects.
// It must exist on the target system before running integration tests.
// Source and installation instructions: https://github.com/Hochfrequenz/Z_ADT_MCP_TEST

const testPackage = "Z_ADT_MCP_TEST"

// integrationSupportedLanguages is the language allow-list handed to
// sapmcpconfig.Load, which requires one since v2.
var integrationSupportedLanguages = []string{"DE", "EN"}

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
		cfg, err = sapmcpconfig.Load(p, integrationSupportedLanguages)
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
	return logOffAtEnd(t, adt.NewClient(cfg))
}

// newTmpProgram creates a $TMP program named prefix plus a random suffix,
// registers its deletion as cleanup, and returns its name and lower-case
// object URI. Tests that write source use it instead of the shared fixtures:
// those live in a transportable package, so a write without a request fails
// on S/4 (#300). Register cleanups that must run before the delete (an
// unlock) after calling it.
func newTmpProgram(t *testing.T, c adt.Client, prefix string) (name, uri string) {
	t.Helper()
	name = fmt.Sprintf("%s_%06X", prefix, time.Now().UnixNano()&0xFFFFFF)
	uri = "/sap/bc/adt/programs/programs/" + strings.ToLower(name)
	if err := c.CreateObject(context.Background(), "PROG", name, "$TMP", "adtler throwaway program", ""); err != nil {
		t.Fatalf("CreateObject %s: %v", name, err)
	}
	t.Cleanup(func() {
		if err := c.DeleteObject(context.Background(), uri, "", ""); err != nil {
			t.Errorf("cleanup: DeleteObject left the $TMP program behind: %v", err)
		}
	})
	return name, uri
}

// logOffAtEnd logs the client's SAP session off when the test ends. Logging
// off releases every edit lock the session still holds, so a test that stops
// between LockObject and UnlockObject leaves no lock on a shared fixture for
// the tests after it (#300). It is registered first, so it runs after every
// cleanup the test registers itself. Errors are ignored: a session that is
// already gone holds no locks.
func logOffAtEnd(t *testing.T, c adt.Client) adt.Client {
	t.Helper()
	t.Cleanup(func() { _ = c.Logout(context.Background()) })
	return c
}

// integrationSystem pairs a logical system name (the key in systems.json) with
// a ready-to-use adt.Client built from that system's credentials.
type integrationSystem struct {
	Name   string
	Config sapmcpconfig.SAPSystem
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
	systems, skip := integrationSystemConfigs()
	if skip != "" {
		t.Skip("eachSystem: " + skip)
	}
	for i := range systems {
		systems[i].Client = logOffAtEnd(t, adt.NewClient(systems[i].Config))
	}
	return systems
}

// integrationSystemConfigs resolves the systems an integration run may hit,
// sorted by name, without clients; see eachSystem for the order of
// precedence. When there are none it returns the reason instead. TestMain
// uses it too, outside any test.
func integrationSystemConfigs() ([]integrationSystem, string) {
	paths := []string{os.Getenv("SAP_CONFIG_FILE")}
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, home+"/.config/sap-mcp/systems.json")
	}
	var cfg *sapmcpconfig.Config
	for _, p := range paths {
		if p == "" {
			continue
		}
		c, err := sapmcpconfig.Load(p, integrationSupportedLanguages)
		if err == nil {
			cfg = c
			break
		}
	}
	if cfg == nil || len(cfg.Systems) == 0 {
		return nil, "no SAP JSON config found — set SAP_CONFIG_FILE or place systems.json under ~/.config/sap-mcp/"
	}

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

	names := make([]string, 0, len(cfg.Systems))
	for name := range cfg.Systems {
		if !allowed[name] {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return nil, "no systems matched the SAP_INTEGRATION_SYSTEMS whitelist"
	}

	systems := make([]integrationSystem, 0, len(names))
	for _, name := range names {
		sys := cfg.Systems[name]
		sys.TLSSkipVerify = true
		systems = append(systems, integrationSystem{Name: name, Config: sys})
	}
	return systems, ""
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

// listModifiableRequests lists the logon user's modifiable transport requests,
// newest first, up to adt.MaxTransportPageSize of them.
func listModifiableRequests(ctx context.Context, c adt.Client) ([]adt.TransportRequest, error) {
	page, err := c.GetTransportRequests(ctx, adt.TransportListOptions{
		Filter: adt.TransportFilterModifiable, Limit: adt.MaxTransportPageSize,
	})
	if err != nil {
		return nil, err
	}
	return page.Requests, nil
}
