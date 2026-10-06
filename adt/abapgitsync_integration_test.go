//go:build integration

package adt_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
)

// TestListAbapGitRepos_Integration lists the repositories the abapGit sync
// companion knows. Systems without the companion are skipped. Only the count
// is logged, never repository names or URLs.
func TestListAbapGitRepos_Integration(t *testing.T) {
	ctx := context.Background()
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			list, err := sys.Client.ListAbapGitRepos(ctx)
			if errors.Is(err, adt.ErrAbapGitSyncNotInstalled) {
				t.Skip("companion not installed")
			}
			if err != nil {
				t.Fatalf("ListAbapGitRepos failed: %v", err)
			}
			if list.Count != len(list.Repos) {
				t.Errorf("Count = %d, want len(Repos) = %d", list.Count, len(list.Repos))
			}
			t.Logf("[%s] repos: %d", sys.Name, list.Count)
		})
	}
}

// TestPullAbapGitRepo_Integration pulls one repository without confirmations.
// The repository identifier comes from ADTLER_ABAPGIT_PULL_TEST_REPO (the test
// skips when it is unset), an optional transport from
// ADTLER_ABAPGIT_PULL_TEST_TRANSPORT. Only the status and the log-entry count
// are logged.
func TestPullAbapGitRepo_Integration(t *testing.T) {
	repo := os.Getenv("ADTLER_ABAPGIT_PULL_TEST_REPO")
	if repo == "" {
		t.Skip("ADTLER_ABAPGIT_PULL_TEST_REPO not set")
	}
	transport := os.Getenv("ADTLER_ABAPGIT_PULL_TEST_TRANSPORT")

	ctx := context.Background()
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			res, err := sys.Client.PullAbapGitRepo(ctx, adt.AbapGitPullRequest{
				Repo:      repo,
				Transport: transport,
			})
			if errors.Is(err, adt.ErrAbapGitSyncNotInstalled) {
				t.Skip("companion not installed")
			}
			if err != nil {
				t.Fatalf("PullAbapGitRepo failed: %v", err)
			}
			switch res.Status {
			case adt.AbapGitStatusPulled:
			case adt.AbapGitStatusNeedsConfirmation:
				t.Logf("[%s] confirmations required: %d", sys.Name, len(res.ConfirmationsRequired))
			default:
				t.Errorf("unexpected status %q", res.Status)
			}
			t.Logf("[%s] status=%s log entries=%d", sys.Name, res.Status, len(res.Log))
		})
	}
}
