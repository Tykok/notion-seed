// SPDX-License-Identifier: GPL-3.0-or-later

package mapper

import (
	"context"
	"strings"
	"testing"

	"github.com/tykok/notion-seed/core/providers/notion/resources"
	"github.com/tykok/notion-seed/core/providers/notion/transport"
)

const dbBody = `{
  "object": "database",
  "id": "55555555-5555-4555-8555-555555555555",
  "archived": false,
  "in_trash": false,
  "data_sources": [{"id": "66666666-6666-4666-8666-666666666666", "name": "spike-projects"}],
  "parent": {"page_id": "44444444-4444-4444-8444-444444444444", "type": "page_id"}
}`

const dsBody = `{
  "object": "data_source",
  "id": "66666666-6666-4666-8666-666666666666",
  "title": [{"plain_text": "spike-projects"}],
  "description": [{"plain_text": "Suivi des projets internes"}],
  "parent": {"database_id": "55555555-5555-4555-8555-555555555555", "type": "database_id"},
  "properties": {
    "Name": {"id": "title", "name": "Name", "type": "title", "title": {}},
    "Budget": {"id": "Q_bo", "name": "Budget", "type": "number", "number": {"format": "euro"}},
    "Priority": {
      "id": "S%5ENS", "name": "Priority", "type": "select",
      "select": {"options": [
        {"id": "77777777-7777-4777-8777-777777777777", "name": "Low", "color": "gray"},
        {"id": "8fab7efc-0000-0000-0000-000000000000", "name": "High", "color": "red"}
      ]}
    },
    "Status": {
      "id": "f%3Cyc", "name": "Status", "type": "status",
      "status": {
        "options": [
          {"id": "88888888-8888-4888-8888-888888888888", "name": "To-Do", "color": "gray"},
          {"id": "5529b100-0000-0000-0000-000000000000", "name": "Building", "color": "blue"}
        ],
        "groups": [
          {"id": "6ac28dc1-0000-0000-0000-000000000000", "name": "To-do", "color": "gray",
           "option_ids": ["88888888-8888-4888-8888-888888888888"]},
          {"id": "95b7387d-0000-0000-0000-000000000000", "name": "In progress", "color": "blue",
           "option_ids": ["5529b100-0000-0000-0000-000000000000"]}
        ]
      }
    }
  }
}`

func TestRemoteDatabaseFromJSON(t *testing.T) {
	got, err := RemoteDatabaseFromJSON([]byte(dbBody), []byte(dsBody))
	if err != nil {
		t.Fatalf("RemoteDatabaseFromJSON() error = %v", err)
	}
	if !got.Exists() {
		t.Error("Exists() = false, want true")
	}
	if got.ID != "55555555-5555-4555-8555-555555555555" {
		t.Errorf("ID = %q", got.ID)
	}
	if got.DataSourceID != "66666666-6666-4666-8666-666666666666" {
		t.Errorf("DataSourceID = %q", got.DataSourceID)
	}
	if got.Name != "spike-projects" {
		t.Errorf("Name = %q", got.Name)
	}
	if got.Description != "Suivi des projets internes" {
		t.Errorf("Description = %q", got.Description)
	}
	if got.Archived {
		t.Error("Archived = true, want false")
	}
}

// Property ids are essential: they tell a rename from a deletion followed by
// a creation.
func TestRemoteDatabaseFromJSONKeepsPropertyIDs(t *testing.T) {
	got, _ := RemoteDatabaseFromJSON([]byte(dbBody), []byte(dsBody))
	if p := got.Properties["Budget"]; p.ID != "Q_bo" || p.NumberFormat != "euro" {
		t.Errorf("Budget = %+v, want ID Q_bo and format euro", p)
	}
	if p := got.Properties["Status"]; p.ID != "f%3Cyc" {
		t.Errorf("Status.ID = %q, want %q", p.ID, "f%3Cyc")
	}
}

// The group is not held by the option in the response: it has to be rebuilt
// from groups[].option_ids. Without that, a diff on the group would be
// impossible.
func TestRemoteDatabaseFromJSONResolvesOptionGroupsFromOptionIDs(t *testing.T) {
	got, _ := RemoteDatabaseFromJSON([]byte(dbBody), []byte(dsBody))
	status := got.Properties["Status"]
	byName := map[string]string{}
	for _, o := range status.Options {
		byName[o.Name] = o.Group
	}
	if byName["To-Do"] != "To-do" {
		t.Errorf("group of To-Do = %q, want %q", byName["To-Do"], "To-do")
	}
	if byName["Building"] != "In progress" {
		t.Errorf("group of Building = %q, want %q", byName["Building"], "In progress")
	}
}

func TestRemoteDatabaseFromJSONKeepsOptionIDs(t *testing.T) {
	got, _ := RemoteDatabaseFromJSON([]byte(dbBody), []byte(dsBody))
	sel := got.Properties["Priority"]
	if len(sel.Options) != 2 {
		t.Fatalf("options = %d, want 2", len(sel.Options))
	}
	if sel.Options[0].ID != "77777777-7777-4777-8777-777777777777" {
		t.Errorf("Options[0].ID = %q", sel.Options[0].ID)
	}
}

// In a well-formed response, each status option appears in exactly one group:
// the API assigns one by default, even if the request provides none. An
// option missing from every option_ids therefore signals a truncated
// response, not an option "without a group" — and must be rejected, not
// decoded with an empty group that would produce a permanent phantom diff.
const dsBodyMissingGroup = `{
  "object": "data_source",
  "id": "66666666-6666-4666-8666-666666666666",
  "title": [{"plain_text": "spike-projects"}],
  "properties": {
    "Status": {
      "id": "f%3Cyc", "name": "Status", "type": "status",
      "status": {
        "options": [
          {"id": "88888888-8888-4888-8888-888888888888", "name": "To-Do", "color": "gray"},
          {"id": "5529b100-0000-0000-0000-000000000000", "name": "Building", "color": "blue"}
        ],
        "groups": [
          {"id": "6ac28dc1-0000-0000-0000-000000000000", "name": "To-do", "color": "gray",
           "option_ids": ["88888888-8888-4888-8888-888888888888"]}
        ]
      }
    }
  }
}`

func TestRemoteDatabaseFromJSONRejectsStatusOptionMissingFromAnyGroup(t *testing.T) {
	_, err := RemoteDatabaseFromJSON([]byte(dbBody), []byte(dsBodyMissingGroup))
	if err == nil {
		t.Fatal("RemoteDatabaseFromJSON() error = nil, want an error: Building appears in no group")
	}
	if !strings.Contains(err.Error(), "Building") {
		t.Errorf("error = %q, want it to name option %q", err.Error(), "Building")
	}
}

// stubTransport returns one response per called path, and records the paths.
type stubTransport struct {
	byPath map[string]string
	calls  []string
}

func (s *stubTransport) Execute(_ context.Context, req transport.APIRequest) (transport.APIResponse, error) {
	s.calls = append(s.calls, req.Path)
	return transport.APIResponse{Status: 200, Body: []byte(s.byPath[req.Path])}, nil
}

// resources.Read's minimal probe and this package's decoder both read
// data_sources[0].id from the SAME body, with two distinct structs.
//
// This test lives here and not in resources: there, it could only inject a
// hard-coded closure, so renaming the `json:"data_sources"` tag of
// from_api.go broke nothing — the compensating check that justified the
// duplication did not exist. Here, the real decoder runs on the fixture the
// probe read, and both must agree on the id.
func TestProbeAndDecoderAgreeOnDataSourceID(t *testing.T) {
	const probeBody = `{"object":"database","id":"db1","data_sources":[{"id":"ds-expected","name":"P"}]}`
	st := &stubTransport{byPath: map[string]string{
		"/v1/databases/db1": probeBody,
		"/v1/data_sources/ds-expected": `{"id":"ds-expected","title":[{"plain_text":"P"}],
			"properties":{"Name":{"id":"title","name":"Name","type":"title"}}}`,
	}}
	r := resources.NewDatabaseResource(st, RemoteDatabaseFromJSON)

	state, err := r.Read(context.Background(), "db1")
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	db, ok := state.(resources.RemoteDatabase)
	if !ok {
		t.Fatalf("Read() returned %T, want resources.RemoteDatabase", state)
	}
	if db.DataSourceID != "ds-expected" {
		t.Errorf("the decoder read DataSourceID = %q, want %q", db.DataSourceID, "ds-expected")
	}
	if len(st.calls) != 2 {
		t.Fatalf("calls = %v, want database then data_source", st.calls)
	}
	if st.calls[1] != "/v1/data_sources/"+db.DataSourceID {
		t.Errorf("the probe called %q, the decoder read id %q: the two shapes drifted",
			st.calls[1], db.DataSourceID)
	}
}

// resources.Trash recomputes "archived || in_trash" locally, without calling
// this decoder: a trash PATCH never reads back the data source, which the
// decoder requires on top of the database. This test runs both rules on the
// SAME response body: without it, making one of them drift (for example by
// forgetting in_trash in one) would break nothing.
func TestTrashLocalRuleAgreesWithDecoderRule(t *testing.T) {
	const stubDSBody = `{"id":"ds1","title":[],"properties":{}}`
	for _, tc := range []struct {
		name string
		body string
	}{
		{"neither archived nor in_trash", `{"object":"database","id":"db1","archived":false,"in_trash":false}`},
		{"archived only", `{"object":"database","id":"db1","archived":true}`},
		{"in_trash only", `{"object":"database","id":"db1","in_trash":true}`},
		{"both", `{"object":"database","id":"db1","archived":true,"in_trash":true}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := &stubTransport{byPath: map[string]string{"/v1/databases/db1": tc.body}}
			trashed, err := resources.NewDatabaseResource(st, RemoteDatabaseFromJSON).
				Trash(context.Background(), "db1")
			if err != nil {
				t.Fatalf("Trash() error = %v", err)
			}

			decoded, err := RemoteDatabaseFromJSON([]byte(tc.body), []byte(stubDSBody))
			if err != nil {
				t.Fatalf("RemoteDatabaseFromJSON() error = %v", err)
			}
			if trashed != decoded.Archived {
				t.Errorf("Trash() = %v, decoder.Archived = %v: the two rules drifted",
					trashed, decoded.Archived)
			}
		})
	}
}

func TestRemoteDatabaseFromJSONReadsEmojiIcon(t *testing.T) {
	dbBody := []byte(`{"id":"db-1","icon":{"type":"emoji","emoji":"🔵"},
		"data_sources":[{"id":"ds-1","name":"Tasks"}]}`)
	dsBody := []byte(`{"id":"ds-1","title":[{"plain_text":"Tasks"}],"properties":{}}`)

	got, err := RemoteDatabaseFromJSON(dbBody, dsBody)
	if err != nil {
		t.Fatalf("RemoteDatabaseFromJSON() error = %v", err)
	}
	if got.Icon != "🔵" {
		t.Errorf("Icon = %q, want %q", got.Icon, "🔵")
	}
}

// A file or external icon is NOT expressible in the YAML, which only declares
// an emoji. Decoding it as anything other than "" would produce a difference
// the plan would show on every run without ever being able to resolve it.
func TestRemoteDatabaseFromJSONIgnoresNonEmojiIcon(t *testing.T) {
	for _, body := range []string{
		`{"id":"db-1","icon":{"type":"external","external":{"url":"https://e/i.png"}},
			"data_sources":[{"id":"ds-1","name":"Tasks"}]}`,
		`{"id":"db-1","icon":null,"data_sources":[{"id":"ds-1","name":"Tasks"}]}`,
	} {
		dsBody := []byte(`{"id":"ds-1","title":[{"plain_text":"Tasks"}],"properties":{}}`)
		got, err := RemoteDatabaseFromJSON([]byte(body), dsBody)
		if err != nil {
			t.Fatalf("RemoteDatabaseFromJSON() error = %v", err)
		}
		if got.Icon != "" {
			t.Errorf("Icon = %q, want \"\" for %s", got.Icon, body)
		}
	}
}

// Moving a database to the trash takes ALL its data sources with it, but the
// count queries only one: the decoder keeps their number so the plan can say
// its count is a lower bound.
func TestRemoteDatabaseFromJSONCountsItsDataSources(t *testing.T) {
	const twoSources = `{"object":"database","id":"db-1","archived":false,"in_trash":false,` +
		`"data_sources":[{"id":"ds-1","name":"A"},{"id":"ds-2","name":"B"}]}`
	got, err := RemoteDatabaseFromJSON([]byte(twoSources), []byte(dsBody))
	if err != nil {
		t.Fatal(err)
	}
	if got.DataSourceID != "ds-1" || got.DataSourceCount != 2 {
		t.Errorf("DataSourceID = %q, DataSourceCount = %d, want ds-1 and 2",
			got.DataSourceID, got.DataSourceCount)
	}
}
