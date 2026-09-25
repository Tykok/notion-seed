// SPDX-License-Identifier: GPL-3.0-or-later

package resources

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/tykok/notion-seed/core/config"
	"github.com/tykok/notion-seed/core/providers/notion/transport"
)

// stubTransport returns one response per called path.
//
// calls records "METHOD /path" — not the path alone — so a test can tell a
// PATCH from a GET on the same resource: Update reads back exactly the paths
// it just wrote.
//
// errs makes it possible to fail one specific call without touching the
// others: without it, a test can only fail ALL the calls of a transport,
// which does not make it possible to check that Update stops at the right
// place.
type stubTransport struct {
	byPath map[string]string
	calls  []string
	errs   map[string]error
}

func (s *stubTransport) Execute(_ context.Context, req transport.APIRequest) (transport.APIResponse, error) {
	key := req.Method + " " + req.Path
	s.calls = append(s.calls, key)
	if err, ok := s.errs[key]; ok {
		return transport.APIResponse{}, err
	}
	return transport.APIResponse{Status: 200, Body: []byte(s.byPath[req.Path])}, nil
}

func TestDatabaseResourceReadFetchesDatabaseThenDataSource(t *testing.T) {
	st := &stubTransport{byPath: map[string]string{
		"/v1/databases/db1": `{"id":"db1","data_sources":[{"id":"ds1","name":"Projects"}]}`,
		"/v1/data_sources/ds1": `{"id":"ds1","title":[{"plain_text":"Projects"}],
			"properties":{"Name":{"id":"title","name":"Name","type":"title"}}}`,
	}}
	// The stub CAPTURES what it receives. Without that, a decode(dsBody, dbBody)
	// call with swapped arguments would pass the test unnoticed.
	var gotDB, gotDS []byte
	decode := func(dbBody, dsBody []byte) (RemoteDatabase, error) {
		gotDB, gotDS = dbBody, dsBody
		return RemoteDatabase{ID: "db1", DataSourceID: "ds1", Name: "Projects", Found: true}, nil
	}
	r := NewDatabaseResource(st, decode)

	got, err := r.Read(context.Background(), "db1")
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if !got.Exists() {
		t.Error("Exists() = false, want true")
	}
	if len(st.calls) != 2 ||
		st.calls[0] != "GET /v1/databases/db1" || st.calls[1] != "GET /v1/data_sources/ds1" {
		t.Errorf("calls = %v, want database then data_source", st.calls)
	}
	if !strings.Contains(string(gotDB), `"object":"database"`) &&
		!strings.Contains(string(gotDB), `"data_sources"`) {
		t.Errorf("decode received %q as dbBody: arguments probably swapped", gotDB)
	}
	if !strings.Contains(string(gotDS), `"properties"`) {
		t.Errorf("decode received %q as dsBody: arguments probably swapped", gotDS)
	}
}

// The drift test between Read's probe and the decoder lives in the mapper
// package (TestProbeAndDecoderAgreeOnDataSourceID): here, it could only inject
// a hard-coded closure, hence guard nothing. The mapper package, on the other
// hand, can import resources and run the REAL decoder.

func TestDatabaseResourceDiffOnAbsentRemoteIsCreate(t *testing.T) {
	r := NewDatabaseResource(nil, nil)
	db := config.Database{
		Key:  "projects",
		Name: "Projects",
		Properties: map[string]config.Property{
			"Name":   {Type: "title"},
			"Budget": {Type: "number", Format: "euro"},
		},
	}

	cs, err := r.Diff(db, nil)
	if err != nil {
		t.Fatalf("Diff() error = %v", err)
	}
	if cs.Kind != KindCreate {
		t.Errorf("Kind = %v, want KindCreate", cs.Kind)
	}
	if cs.Resource != "database.projects" {
		t.Errorf("Resource = %q", cs.Resource)
	}
	if len(cs.Details) != 2 {
		t.Fatalf("details = %d, want 2", len(cs.Details))
	}
	// Deterministic order: properties sorted alphabetically.
	if cs.Details[0].Target != `property "Budget" (number)` {
		t.Errorf("Details[0].Target = %q", cs.Details[0].Target)
	}
}

// Typed-nil trap: a non-nil RemoteState whose Exists() is false must lead to a
// creation just like a nil. Without this test, simplifying the condition to
// `remote == nil` would break nothing.
func TestDatabaseResourceDiffOnNonNilButAbsentRemoteIsAlsoCreate(t *testing.T) {
	r := NewDatabaseResource(nil, nil)
	db := config.Database{Key: "projects", Name: "Projects",
		Properties: map[string]config.Property{"Name": {Type: "title"}}}

	var remote RemoteState = RemoteDatabase{} // non-nil, Found == false
	if remote == nil {
		t.Fatal("the test setup is wrong: remote must be non-nil")
	}
	cs, err := r.Diff(db, remote)
	if err != nil {
		t.Fatalf("Diff() error = %v", err)
	}
	if cs.Kind != KindCreate {
		t.Errorf("Kind = %v, want KindCreate", cs.Kind)
	}
}

func TestDatabaseResourceDiffRejectsWrongDesiredType(t *testing.T) {
	r := NewDatabaseResource(nil, nil)
	if _, err := r.Diff("not a database", nil); err == nil {
		t.Fatal("Diff() error = nil, want a type error")
	}
}

// The Diff method delegates to the package function, which needs no
// resource. That is what lets the diff engine stop building a
// NewDatabaseResource(nil, nil) to call a pure computation.
func TestDiffDelegatesToDatabaseChangeset(t *testing.T) {
	db := config.Database{
		Key:  "projects",
		Name: "Projects",
		Properties: map[string]config.Property{
			"Name":   {Type: "title"},
			"Budget": {Type: "number", Format: "euro"},
		},
	}

	viaMethod, err := NewDatabaseResource(nil, nil).Diff(db, nil)
	if err != nil {
		t.Fatalf("Diff() error = %v", err)
	}
	if viaFunc := DatabaseChangeset(db, nil); !reflect.DeepEqual(viaMethod, viaFunc) {
		t.Errorf("method = %+v, function = %+v: the delegation diverged", viaMethod, viaFunc)
	}
}

// transportFunc lets a test decide by METHOD as well as by path, and return an
// error — which stubTransport cannot do.
type transportFunc func(ctx context.Context, req transport.APIRequest) (transport.APIResponse, error)

func (f transportFunc) Execute(ctx context.Context, req transport.APIRequest) (transport.APIResponse, error) {
	return f(ctx, req)
}

// decodeCreated is the decoder of the creation tests. It is local to the
// package: using mapper.RemoteDatabaseFromJSON would create an import cycle,
// since mapper imports resources. It is the same reason that requires Create
// to take an already serialized []byte.
func decodeCreated(dbBody, dsBody []byte) (RemoteDatabase, error) {
	return RemoteDatabase{
		ID:           "db-new",
		DataSourceID: "ds-new",
		Name:         "Tasks",
		Found:        true,
		Properties: map[string]RemoteProperty{
			"Name": {ID: "title", Type: "title"},
		},
	}, nil
}

// Create must POST then READ BACK. The read-back is not overzealous: it brings
// back the option ids, without which the state is blind to drift, and it
// confronts the result with what had been announced.
func TestCreatePostsThenReadsBack(t *testing.T) {
	var seen []string
	var sentBody []byte
	tr := transportFunc(func(_ context.Context, req transport.APIRequest) (transport.APIResponse, error) {
		seen = append(seen, req.Method+" "+req.Path)
		switch {
		case req.Method == "POST":
			sentBody = req.Body
			return transport.APIResponse{Status: 200, Body: []byte(
				`{"object":"database","id":"db-new","data_sources":[{"id":"ds-new"}]}`)}, nil
		case strings.HasPrefix(req.Path, "/v1/databases/"):
			return transport.APIResponse{Status: 200, Body: []byte(
				`{"object":"database","id":"db-new","archived":false,` +
					`"data_sources":[{"id":"ds-new","name":"Tasks"}]}`)}, nil
		default:
			return transport.APIResponse{Status: 200, Body: []byte(
				`{"object":"data_source","id":"ds-new"}`)}, nil
		}
	})

	r := NewDatabaseResource(tr, decodeCreated)
	got, err := r.Create(context.Background(), []byte(`{"parent":{}}`))
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if got.ID != "db-new" || got.DataSourceID != "ds-new" {
		t.Errorf("ids = %q/%q, want db-new/ds-new", got.ID, got.DataSourceID)
	}
	if got.ReadErr != nil {
		t.Errorf("ReadErr = %v, want nil", got.ReadErr)
	}
	if got.Remote.Name != "Tasks" {
		t.Errorf("Remote.Name = %q, want Tasks", got.Remote.Name)
	}
	if len(seen) != 3 || seen[0] != "POST /v1/databases" {
		t.Errorf("calls = %v, want POST /v1/databases then the two reads", seen)
	}
	if string(sentBody) != `{"parent":{}}` {
		t.Errorf("body = %q, it must go out as is", sentBody)
	}
}

// If the read-back fails AFTER a successful POST, the identity must survive:
// losing it would cost a duplicate on the next apply. The error is reported
// separately, not confused with a creation failure.
func TestCreateKeepsIdentityWhenReadBackFails(t *testing.T) {
	tr := transportFunc(func(_ context.Context, req transport.APIRequest) (transport.APIResponse, error) {
		if req.Method == "POST" {
			return transport.APIResponse{Status: 200, Body: []byte(
				`{"object":"database","id":"db-new","data_sources":[{"id":"ds-new"}]}`)}, nil
		}
		return transport.APIResponse{}, &transport.APIError{Status: 500, NotionCode: "internal_server_error"}
	})

	r := NewDatabaseResource(tr, decodeCreated)
	got, err := r.Create(context.Background(), []byte(`{"parent":{}}`))
	if err != nil {
		t.Fatalf("Create() error = %v, want nil: the creation succeeded", err)
	}
	if got.ID != "db-new" || got.DataSourceID != "ds-new" {
		t.Errorf("ids = %q/%q: the identity must never be lost", got.ID, got.DataSourceID)
	}
	if got.ReadErr == nil {
		t.Error("ReadErr = nil, want the read-back failure")
	}
}

// A failed POST is a creation failure: no identity to keep.
func TestCreateReturnsErrorWhenPostFails(t *testing.T) {
	tr := transportFunc(func(_ context.Context, _ transport.APIRequest) (transport.APIResponse, error) {
		return transport.APIResponse{}, &transport.APIError{Status: 400, NotionCode: "validation_error"}
	})
	r := NewDatabaseResource(tr, decodeCreated)
	got, err := r.Create(context.Background(), []byte(`{}`))
	if err == nil {
		t.Fatal("Create() error = nil, want the API error")
	}
	if got.ID != "" {
		t.Errorf("ID = %q, want empty: nothing was created", got.ID)
	}
}

// A response without an identifier must not pass for a success: what was
// created is unknown, and the message must send the user to check.
func TestCreateRefusesResponseWithoutID(t *testing.T) {
	tr := transportFunc(func(_ context.Context, _ transport.APIRequest) (transport.APIResponse, error) {
		return transport.APIResponse{Status: 200, Body: []byte(`{"object":"database"}`)}, nil
	})
	r := NewDatabaseResource(tr, decodeCreated)
	_, err := r.Create(context.Background(), []byte(`{}`))
	if err == nil {
		t.Fatal("Create() error = nil, want a rejection")
	}
	for _, want := range []string{"identifier", "  → ", "parent page"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message = %q, it must contain %q", err.Error(), want)
		}
	}
}

// Update must write the database BEFORE the data source, then read both back.
// The order is the cheapest failure: a data source failure leaves a name and
// an icon up to date and no data touched.
func TestDatabaseResourceUpdatePatchesDatabaseBeforeDataSource(t *testing.T) {
	st := &stubTransport{byPath: map[string]string{
		"/v1/databases/db1":    `{"id":"db1","data_sources":[{"id":"ds1","name":"Tasks"}]}`,
		"/v1/data_sources/ds1": `{"id":"ds1","title":[{"plain_text":"Tasks"}],"properties":{}}`,
	}}
	decode := func(dbBody, dsBody []byte) (RemoteDatabase, error) {
		return RemoteDatabase{ID: "db1", DataSourceID: "ds1", Name: "Tasks", Found: true}, nil
	}
	r := NewDatabaseResource(st, decode)

	got, err := r.Update(context.Background(), "db1", "ds1",
		[]byte(`{"title":[]}`), []byte(`{"properties":{}}`))
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if !got.DatabaseWritten {
		t.Error("DatabaseWritten = false while dbBody was non-empty")
	}
	// The two PATCHes, then the read-back (database then data source).
	want := []string{
		"PATCH /v1/databases/db1", "PATCH /v1/data_sources/ds1",
		"GET /v1/databases/db1", "GET /v1/data_sources/ds1",
	}
	if !reflect.DeepEqual(st.calls, want) {
		t.Errorf("calls = %v, want %v", st.calls, want)
	}
}

// An empty body skips its endpoint: an update that touches only properties
// has nothing to write on the database.
func TestDatabaseResourceUpdateSkipsTheEndpointWithoutABody(t *testing.T) {
	st := &stubTransport{byPath: map[string]string{
		"/v1/databases/db1":    `{"id":"db1","data_sources":[{"id":"ds1","name":"Tasks"}]}`,
		"/v1/data_sources/ds1": `{"id":"ds1","title":[{"plain_text":"Tasks"}],"properties":{}}`,
	}}
	decode := func(dbBody, dsBody []byte) (RemoteDatabase, error) {
		return RemoteDatabase{ID: "db1", Found: true}, nil
	}
	r := NewDatabaseResource(st, decode)

	got, err := r.Update(context.Background(), "db1", "ds1", nil, []byte(`{"properties":{}}`))
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if got.DatabaseWritten {
		t.Error("DatabaseWritten = true while no database body was passed")
	}
	want := []string{"PATCH /v1/data_sources/ds1", "GET /v1/databases/db1", "GET /v1/data_sources/ds1"}
	if !reflect.DeepEqual(st.calls, want) {
		t.Errorf("calls = %v, want %v", st.calls, want)
	}
}

// If the database PATCH fails, the data source must RECEIVE no call: writing
// it while the name did not go through would break the D4 order that
// guarantees the cheapest failure.
func TestDatabaseResourceUpdateStopsBeforeDataSourceWhenDatabasePatchFails(t *testing.T) {
	st := &stubTransport{
		byPath: map[string]string{
			"/v1/databases/db1":    `{"id":"db1","data_sources":[{"id":"ds1","name":"Tasks"}]}`,
			"/v1/data_sources/ds1": `{"id":"ds1","title":[{"plain_text":"Tasks"}],"properties":{}}`,
		},
		errs: map[string]error{
			"PATCH /v1/databases/db1": &transport.APIError{Status: 400, NotionCode: "validation_error"},
		},
	}
	decode := func(dbBody, dsBody []byte) (RemoteDatabase, error) {
		return RemoteDatabase{ID: "db1", DataSourceID: "ds1", Name: "Tasks", Found: true}, nil
	}
	r := NewDatabaseResource(st, decode)

	got, err := r.Update(context.Background(), "db1", "ds1",
		[]byte(`{"title":[]}`), []byte(`{"properties":{}}`))
	if err == nil {
		t.Fatal("Update() error = nil, want the database PATCH failure")
	}
	if got.DatabaseWritten {
		t.Error("DatabaseWritten = true while the database PATCH failed")
	}
	want := []string{"PATCH /v1/databases/db1"}
	if !reflect.DeepEqual(st.calls, want) {
		t.Errorf("calls = %v, want %v: the data source must not be touched", st.calls, want)
	}
}

// If the data source PATCH fails AFTER the database's succeeded,
// DatabaseWritten must stay true: the name and the icon are written, only the
// properties are not, and the caller must be able to say so.
func TestDatabaseResourceUpdateReportsDatabaseWrittenWhenDataSourcePatchFails(t *testing.T) {
	st := &stubTransport{
		byPath: map[string]string{
			"/v1/databases/db1":    `{"id":"db1","data_sources":[{"id":"ds1","name":"Tasks"}]}`,
			"/v1/data_sources/ds1": `{"id":"ds1","title":[{"plain_text":"Tasks"}],"properties":{}}`,
		},
		errs: map[string]error{
			"PATCH /v1/data_sources/ds1": &transport.APIError{Status: 404, NotionCode: "object_not_found"},
		},
	}
	decode := func(dbBody, dsBody []byte) (RemoteDatabase, error) {
		return RemoteDatabase{ID: "db1", DataSourceID: "ds1", Name: "Tasks", Found: true}, nil
	}
	r := NewDatabaseResource(st, decode)

	got, err := r.Update(context.Background(), "db1", "ds1",
		[]byte(`{"title":[]}`), []byte(`{"properties":{}}`))
	if err == nil {
		t.Fatal("Update() error = nil, want the data source PATCH failure")
	}
	if !got.DatabaseWritten {
		t.Error("DatabaseWritten = false while the database PATCH succeeded")
	}
	want := []string{"PATCH /v1/databases/db1", "PATCH /v1/data_sources/ds1"}
	if !reflect.DeepEqual(st.calls, want) {
		t.Errorf("calls = %v, want %v", st.calls, want)
	}
}

// DatabaseExists reads ONLY the database, never its data source: Task 8 uses
// it to diagnose an archived ancestor after a 404 on the data source PATCH,
// where GET database answers 200 while the data source is unreachable — a
// full Read would fail here and hide the diagnosis.
func TestDatabaseResourceDatabaseExistsOnSuccessIsTrue(t *testing.T) {
	st := &stubTransport{byPath: map[string]string{
		"/v1/databases/db1": `{"id":"db1"}`,
	}}
	r := NewDatabaseResource(st, nil)

	got, err := r.DatabaseExists(context.Background(), "db1")
	if err != nil {
		t.Fatalf("DatabaseExists() error = %v", err)
	}
	if !got {
		t.Error("DatabaseExists() = false, want true")
	}
	want := []string{"GET /v1/databases/db1"}
	if !reflect.DeepEqual(st.calls, want) {
		t.Errorf("calls = %v, want %v: must read ONLY the database", st.calls, want)
	}
}

func TestDatabaseResourceDatabaseExistsOn404IsFalseWithoutError(t *testing.T) {
	st := &stubTransport{
		errs: map[string]error{
			"GET /v1/databases/db1": &transport.APIError{Status: 404, NotionCode: "object_not_found"},
		},
	}
	r := NewDatabaseResource(st, nil)

	got, err := r.DatabaseExists(context.Background(), "db1")
	if err != nil {
		t.Fatalf("DatabaseExists() error = %v, want nil: a 404 just says \"absent\"", err)
	}
	if got {
		t.Error("DatabaseExists() = true, want false")
	}
}

func TestDatabaseResourceDatabaseExistsOnOtherErrorReturnsIt(t *testing.T) {
	wantErr := &transport.APIError{Status: 500, NotionCode: "internal_server_error"}
	st := &stubTransport{
		errs: map[string]error{
			"GET /v1/databases/db1": wantErr,
		},
	}
	r := NewDatabaseResource(st, nil)

	got, err := r.DatabaseExists(context.Background(), "db1")
	if err == nil {
		t.Fatal("DatabaseExists() error = nil, want the error passed up: it is not a 404")
	}
	if got {
		t.Error("DatabaseExists() = true, want false")
	}
}

type otherRemote struct{}

func (otherRemote) Exists() bool { return true }

// Create and Update share this conversion. Read never returns anything other
// than a RemoteDatabase: another type is a notion-seed bug, and the message
// must say so with its action, rather than letting the user look on the
// Notion side.
func TestAsRemoteDatabaseNamesAnUnexpectedTypeAsAnInternalDefect(t *testing.T) {
	_, err := asRemoteDatabase("db-1", otherRemote{})
	if err == nil {
		t.Fatal("asRemoteDatabase() error = nil, want a notion-seed bug")
	}
	for _, want := range []string{"db-1", "unexpected type", "  → ", "notion-seed bug"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message = %q, it must contain %q", err.Error(), want)
		}
	}

	rd, err := asRemoteDatabase("db-1", RemoteDatabase{ID: "db-1"})
	if err != nil || rd.ID != "db-1" {
		t.Errorf("asRemoteDatabase(RemoteDatabase) = %v, %v", rd, err)
	}
}

// The trashing goes out in ONE call, on the database only, with the body
// measured on 2026-09-24. The data source is neither written nor read back.
func TestDatabaseResourceTrashPatchesInTrashOnTheDatabaseOnly(t *testing.T) {
	var calls []string
	var body []byte
	tr := transportFunc(func(_ context.Context, req transport.APIRequest) (transport.APIResponse, error) {
		calls = append(calls, req.Method+" "+req.Path)
		body = req.Body
		return transport.APIResponse{Status: 200, Body: []byte(
			`{"object":"database","id":"db1","archived":true,"in_trash":true}`)}, nil
	})
	r := NewDatabaseResource(tr, nil)

	trashed, err := r.Trash(context.Background(), "db1")
	if err != nil {
		t.Fatalf("Trash() error = %v", err)
	}
	if !trashed {
		t.Error("Trash() = false, want true: the response confirms the trashing")
	}
	if want := []string{"PATCH /v1/databases/db1"}; !reflect.DeepEqual(calls, want) {
		t.Errorf("calls = %v, want %v", calls, want)
	}
	if string(body) != `{"in_trash":true}` {
		t.Errorf("body = %s, want {\"in_trash\":true}", body)
	}
}

// Review Focus #1: a 200 is not a trashing. Only the response says so, and by
// the decoder's rule — the one by which the next plan would classify the
// database.
func TestDatabaseResourceTrashConfirmsOnlyWhatTheResponseSays(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want bool
	}{
		{"neither archived nor in_trash", `{"object":"database","id":"db1","archived":false,"in_trash":false}`, false},
		{"archived only", `{"object":"database","id":"db1","archived":true}`, true},
		{"in_trash only", `{"object":"database","id":"db1","in_trash":true}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr := transportFunc(func(context.Context, transport.APIRequest) (transport.APIResponse, error) {
				return transport.APIResponse{Status: 200, Body: []byte(tc.body)}, nil
			})
			trashed, err := NewDatabaseResource(tr, nil).Trash(context.Background(), "db1")
			if err != nil {
				t.Fatalf("Trash() error = %v", err)
			}
			if trashed != tc.want {
				t.Errorf("Trash() = %v, want %v", trashed, tc.want)
			}
		})
	}
}

// An unreadable 200 response says neither yes nor no: the mutation may have
// happened. It is an unknown outcome, not a rejection.
func TestDatabaseResourceTrashCallsAnUnreadableResponseAnUnknownOutcome(t *testing.T) {
	tr := transportFunc(func(context.Context, transport.APIRequest) (transport.APIResponse, error) {
		return transport.APIResponse{Status: 200, Body: []byte("not json")}, nil
	})
	trashed, err := NewDatabaseResource(tr, nil).Trash(context.Background(), "db1")
	var unknown *transport.OutcomeUnknownError
	if !errors.As(err, &unknown) {
		t.Fatalf("Trash() error = %v, want an OutcomeUnknownError", err)
	}
	if trashed {
		t.Error("Trash() = true on an unreadable response")
	}
	if !strings.Contains(err.Error(), "  → ") {
		t.Errorf("message = %q, it must carry a corrective action", err.Error())
	}
}

func TestDatabaseResourceTrashReturnsTheAPIError(t *testing.T) {
	wantErr := &transport.APIError{Status: 404, NotionCode: "object_not_found"}
	st := &stubTransport{errs: map[string]error{"PATCH /v1/databases/db1": wantErr}}

	trashed, err := NewDatabaseResource(st, nil).Trash(context.Background(), "db1")
	if !errors.Is(err, wantErr) {
		t.Fatalf("Trash() error = %v, want the API error as is", err)
	}
	if trashed {
		t.Error("Trash() = true while the PATCH failed")
	}
}
