//go:build integration

package adt_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
)

const fakeTransportNumber = "AAAK900001"

// fakeFixtureClient answers the calls setupFixtures and cleanupFixtures make and counts the
// transport calls. It embeds adt.Client, so a call it does not override panics: the tests
// also prove that nothing else is called. It contacts no SAP system.
type fakeFixtureClient struct {
	adt.Client
	missing        map[string]bool // fixture URIs GetObjectInfo reports as not found
	nonLocalFails  bool            // CreateObject fails for every package but $TMP
	setSourceFails bool
	createdTr      int
	releasedTr     int
	deletedTr      int
	deletedObjects int
	createdObjects int
	// rec and the two flags stand for the 401 recorder of the real client: the call fails and
	// marks the credential rejected.
	rec                     *rejectionRecorder
	rejectOnCreateTransport bool
	rejectOnDeleteObject    bool
	rejectOnBrowse          bool
	rejectOnGetObjectInfo   bool
	objectInfoCalls         int
}

func (f *fakeFixtureClient) BrowsePackage(context.Context, string) ([]adt.ObjectInfo, error) {
	if f.rejectOnBrowse {
		f.rec.rejected.Store(true)
		return nil, errors.New("unauthorized")
	}
	return nil, nil
}

func (f *fakeFixtureClient) GetObjectInfo(_ context.Context, uri string) (*adt.ObjectInfo, error) {
	f.objectInfoCalls++
	if f.rejectOnGetObjectInfo {
		f.rec.rejected.Store(true)
		return nil, errors.New("unauthorized")
	}
	if f.missing[uri] {
		return nil, errors.New("not found")
	}
	return &adt.ObjectInfo{}, nil
}

func (f *fakeFixtureClient) CreateTransport(context.Context, adt.CreateTransportOptions) (string, error) {
	f.createdTr++
	if f.rejectOnCreateTransport {
		f.rec.rejected.Store(true)
		return "", errors.New("unauthorized")
	}
	return fakeTransportNumber, nil
}

func (f *fakeFixtureClient) CreateObject(_ context.Context, _, _, pkg, _, _ string) error {
	f.createdObjects++
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
	if f.setSourceFails {
		return "", errors.New("write refused")
	}
	return "", nil
}
func (f *fakeFixtureClient) ActivateObjects(context.Context, []string) (*adt.ActivationResult, error) {
	return &adt.ActivationResult{Success: true}, nil
}
func (f *fakeFixtureClient) DeleteObject(context.Context, string, string, string) error {
	f.deletedObjects++
	if f.rejectOnDeleteObject {
		f.rec.rejected.Store(true)
		return errors.New("unauthorized")
	}
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
	created, tr, err := setupFixtures(context.Background(), c, &rejectionRecorder{})
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
			created, tr, err := setupFixtures(context.Background(), c, &rejectionRecorder{})
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

// A fixture whose source write failed exists already, so the teardown has to delete it before
// it releases the request that records it.
func TestSetupFixtures_SourceWriteFails_ReturnsTheCreatedFixture(t *testing.T) {
	c := &fakeFixtureClient{
		missing:        map[string]bool{testFixtures[0].objectURI: true},
		setSourceFails: true,
	}
	created, tr, err := setupFixtures(context.Background(), c, &rejectionRecorder{})
	if err == nil {
		t.Fatal("setupFixtures succeeded although the source write failed")
	}
	if len(created) != 1 || tr.number() != fakeTransportNumber || !tr.holdsObjects {
		t.Errorf("created %d fixtures, request %q, holdsObjects %v; want 1, %s, true", len(created), tr.number(), tr.holdsObjects, fakeTransportNumber)
	}
}

// A 401 is final (#222): after the request creation was rejected, setup sends nothing more.
func TestSetupFixtures_RejectedTransportCreation_StopsSetup(t *testing.T) {
	rec := &rejectionRecorder{}
	c := &fakeFixtureClient{
		rec:                     rec,
		missing:                 map[string]bool{testFixtures[0].objectURI: true},
		rejectOnCreateTransport: true,
	}
	_, _, err := setupFixtures(context.Background(), c, rec)
	if err == nil {
		t.Fatal("setupFixtures succeeded after a rejected credential")
	}
	if c.createdObjects != 0 {
		t.Errorf("CreateObject was called %d times after the 401", c.createdObjects)
	}
}

// A 401 on the package browse or the existence check ends setup before anything else is sent.
func TestSetupFixtures_RejectedLookup_StopsSetup(t *testing.T) {
	for _, tc := range []struct {
		name   string
		client func(*rejectionRecorder) *fakeFixtureClient
	}{
		{"package browse", func(r *rejectionRecorder) *fakeFixtureClient {
			return &fakeFixtureClient{rec: r, rejectOnBrowse: true}
		}},
		{"existence check", func(r *rejectionRecorder) *fakeFixtureClient {
			return &fakeFixtureClient{rec: r, rejectOnGetObjectInfo: true}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := &rejectionRecorder{}
			c := tc.client(rec)
			if _, _, err := setupFixtures(context.Background(), c, rec); err == nil {
				t.Fatal("setupFixtures succeeded after a rejected credential")
			}
			if c.objectInfoCalls > 1 || c.createdTr != 0 || c.createdObjects != 0 {
				t.Errorf("%d existence checks, %d requests, %d objects created after the 401; want at most the one that failed, 0, 0", c.objectInfoCalls, c.createdTr, c.createdObjects)
			}
		})
	}
}

// A 401 during the teardown stops the remaining deletions and leaves the request alone.
func TestCleanupFixtures_RejectionDuringDeletion_StopsTeardown(t *testing.T) {
	rec := &rejectionRecorder{}
	c := &fakeFixtureClient{rec: rec, rejectOnDeleteObject: true}
	two := []fixtureObject{testFixtures[0], testFixtures[1]}
	cleanupFixtures(context.Background(), c, two, &lazyTransport{nr: fakeTransportNumber, holdsObjects: true}, rec)
	if c.deletedObjects != 1 || c.releasedTr != 0 || c.deletedTr != 0 {
		t.Errorf("deleted %d objects, released %d, deleted %d requests; want 1, 0, 0", c.deletedObjects, c.releasedTr, c.deletedTr)
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
		{"rejected credential touches nothing", one, &lazyTransport{nr: fakeTransportNumber, holdsObjects: true}, true, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &fakeFixtureClient{}
			rec := &rejectionRecorder{}
			rec.rejected.Store(tc.rejected)
			cleanupFixtures(context.Background(), c, tc.created, tc.tr, rec)
			if c.releasedTr != tc.wantReleased || c.deletedTr != tc.wantDel {
				t.Errorf("released %d, deleted %d requests; want %d, %d", c.releasedTr, c.deletedTr, tc.wantReleased, tc.wantDel)
			}
			wantObjects := len(tc.created)
			if tc.rejected {
				wantObjects = 0
			}
			if c.deletedObjects != wantObjects {
				t.Errorf("deleted %d objects, want %d", c.deletedObjects, wantObjects)
			}
		})
	}
}
