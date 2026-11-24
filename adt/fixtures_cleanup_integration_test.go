//go:build integration

package adt_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
)

const (
	fakeTransportNumber = "AAAK900001"
	localPackage        = "$TMP"
)

// fakeFixtureClient answers the calls setupFixtures and cleanupFixtures make and counts the
// transport calls. It embeds adt.Client, so a call it does not override panics: the tests
// also prove that nothing else is called. It contacts no SAP system.
type fakeFixtureClient struct {
	adt.Client
	missing        map[string]bool // fixture URIs GetObjectInfo reports as not found
	nonLocalFails  bool            // CreateObject fails for every package but $TMP
	createdTr      int
	releasedTr     int
	deletedTr      int
	deletedObjects int
}

func (f *fakeFixtureClient) BrowsePackage(context.Context, string) ([]adt.ObjectInfo, error) {
	return nil, nil
}

func (f *fakeFixtureClient) GetObjectInfo(_ context.Context, uri string) (*adt.ObjectInfo, error) {
	if f.missing[uri] {
		return nil, errors.New("not found")
	}
	return &adt.ObjectInfo{}, nil
}

func (f *fakeFixtureClient) CreateTransport(context.Context, adt.CreateTransportOptions) (string, error) {
	f.createdTr++
	return fakeTransportNumber, nil
}

func (f *fakeFixtureClient) CreateObject(_ context.Context, _, _, pkg, _, _ string) error {
	if f.nonLocalFails && pkg != localPackage {
		return errors.New("refused")
	}
	// The fixture exists from now on.
	return nil
}

func (f *fakeFixtureClient) LockObject(context.Context, string) (string, error) { return "h", nil }
func (f *fakeFixtureClient) UnlockObject(context.Context, string, string) error { return nil }
func (f *fakeFixtureClient) GetSource(context.Context, string) (*adt.SourceResult, error) {
	return &adt.SourceResult{}, nil
}
func (f *fakeFixtureClient) SetSource(context.Context, string, string, string, string, string) (string, error) {
	return "", nil
}
func (f *fakeFixtureClient) ActivateObjects(context.Context, []string) (*adt.ActivationResult, error) {
	return &adt.ActivationResult{Success: true}, nil
}
func (f *fakeFixtureClient) DeleteObject(context.Context, string, string, string) error {
	f.deletedObjects++
	return nil
}
func (f *fakeFixtureClient) DeleteTransport(context.Context, string) error {
	f.deletedTr++
	return nil
}
func (f *fakeFixtureClient) ReleaseTransportWithTasks(_ context.Context, nr string) (*adt.ReleaseResult, error) {
	f.releasedTr++
	return &adt.ReleaseResult{Transport: nr}, nil
}

// #241: a run whose fixtures all exist created a request and never released or deleted it.
func TestSetupFixtures_AllFixturesExist_CreatesNoTransport(t *testing.T) {
	c := &fakeFixtureClient{}
	created, tr, err := setupFixtures(context.Background(), c)
	if err != nil {
		t.Fatalf("setupFixtures: %v", err)
	}
	if len(created) != 0 || c.createdTr != 0 || tr.number() != "" {
		t.Errorf("created %d fixtures, %d requests, request %q; want none", len(created), c.createdTr, tr.number())
	}
}

// A missing fixture needs the request only when it lands in a non-local package.
func TestSetupFixtures_MissingFixture_HoldsObjectsOnlyInNonLocalPackage(t *testing.T) {
	for _, tc := range []struct {
		name          string
		nonLocalFails bool
		wantHolds     bool
	}{
		{"created in the test package", false, true},
		{"fell back to $TMP", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &fakeFixtureClient{
				missing:       map[string]bool{testFixtures[0].objectURI: true},
				nonLocalFails: tc.nonLocalFails,
			}
			created, tr, err := setupFixtures(context.Background(), c)
			if err != nil {
				t.Fatalf("setupFixtures: %v", err)
			}
			if len(created) != 1 || c.createdTr != 1 || tr.number() != fakeTransportNumber {
				t.Fatalf("created %d fixtures, %d requests, request %q; want 1, 1, %s", len(created), c.createdTr, tr.number(), fakeTransportNumber)
			}
			if tr.holdsObjects != tc.wantHolds {
				t.Errorf("holdsObjects = %v, want %v", tr.holdsObjects, tc.wantHolds)
			}
		})
	}
}

func TestCleanupFixtures(t *testing.T) {
	one := []fixtureObject{testFixtures[0]}
	for _, tc := range []struct {
		name                  string
		created               []fixtureObject
		tr                    *lazyTransport
		rejected              bool
		wantReleased, wantDel int
	}{
		{"no request", nil, &lazyTransport{}, false, 0, 0},
		{"empty request is deleted", nil, &lazyTransport{nr: fakeTransportNumber}, false, 0, 1},
		{"request without objects is deleted", one, &lazyTransport{nr: fakeTransportNumber}, false, 0, 1},
		{"request with objects is released", one, &lazyTransport{nr: fakeTransportNumber, holdsObjects: true}, false, 1, 0},
		{"rejected credential touches nothing", nil, &lazyTransport{nr: fakeTransportNumber}, true, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &fakeFixtureClient{}
			rec := &rejectionRecorder{}
			rec.rejected.Store(tc.rejected)
			cleanupFixtures(context.Background(), c, tc.created, tc.tr, rec)
			if c.releasedTr != tc.wantReleased || c.deletedTr != tc.wantDel {
				t.Errorf("released %d, deleted %d requests; want %d, %d", c.releasedTr, c.deletedTr, tc.wantReleased, tc.wantDel)
			}
			if c.deletedObjects != len(tc.created) {
				t.Errorf("deleted %d objects, want %d", c.deletedObjects, len(tc.created))
			}
		})
	}
}
