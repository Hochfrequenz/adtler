//go:build integration

package adt_test

import (
	"context"
	"slices"
	"testing"
)

// TestRunUnitTests_InactiveTestInclude_Integration is the live regression
// guard for #212: a run against a class whose test-classes include exists
// only in an inactive version executes no test method, and RunUnitTests must
// then list that include in InactiveURIs — whether the run targets the class
// or the include itself. After activation the same run executes the test.
func TestRunUnitTests_InactiveTestInclude_Integration(t *testing.T) {
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			ctx := context.Background()
			c := sys.Client
			name, uri := newActiveClassPool70(t, c, "ZCL_ADT212")
			include := uri + "/includes/testclasses"

			writeClassPool70(t, c, uri, name, "issue212", true)
			if pending := inactiveUnder(t, c, uri); !slices.ContainsFunc(pending, equalFold(include)) {
				t.Fatalf("precondition: expected the test-classes include to be inactive, got %v", pending)
			}

			for _, target := range []string{uri, include} {
				res, err := c.RunUnitTests(ctx, target, 60)
				if err != nil {
					t.Fatalf("RunUnitTests(%s): %v", target, err)
				}
				t.Logf("inactive run via %s: passed=%d failed=%d alerts=%+v inactive=%d",
					target, res.Passed, res.Failed, res.Alerts, len(res.InactiveURIs))
				if len(res.TestCases) != 0 {
					t.Fatalf("RunUnitTests(%s): expected no executed test while the include is inactive, got %d", target, len(res.TestCases))
				}
				if !slices.ContainsFunc(res.InactiveURIs, equalFold(include)) {
					t.Errorf("RunUnitTests(%s): InactiveURIs = %v, want it to contain %s", target, res.InactiveURIs, include)
				}
			}

			if res, err := c.ActivateObjects(ctx, []string{uri}); err != nil || !res.Success {
				t.Fatalf("activation: result=%+v err=%v", res, err)
			}
			res, err := c.RunUnitTests(ctx, uri, 60)
			if err != nil {
				t.Fatalf("RunUnitTests after activation: %v", err)
			}
			if res.Passed != 1 || res.Failed != 0 {
				t.Errorf("after activation: passed=%d failed=%d, want 1/0", res.Passed, res.Failed)
			}
		})
	}
}
