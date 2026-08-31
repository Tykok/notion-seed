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

// Les ids de propriété sont indispensables : ils distinguent un renommage
// d'une suppression suivie d'une création.
func TestRemoteDatabaseFromJSONKeepsPropertyIDs(t *testing.T) {
	got, _ := RemoteDatabaseFromJSON([]byte(dbBody), []byte(dsBody))
	if p := got.Properties["Budget"]; p.ID != "Q_bo" || p.NumberFormat != "euro" {
		t.Errorf("Budget = %+v, want ID Q_bo et format euro", p)
	}
	if p := got.Properties["Status"]; p.ID != "f%3Cyc" {
		t.Errorf("Status.ID = %q, want %q", p.ID, "f%3Cyc")
	}
}

// Le group n'est pas porté par l'option dans la réponse : il faut le
// reconstruire depuis groups[].option_ids. Sans ça, un diff sur le groupe
// serait impossible.
func TestRemoteDatabaseFromJSONResolvesOptionGroupsFromOptionIDs(t *testing.T) {
	got, _ := RemoteDatabaseFromJSON([]byte(dbBody), []byte(dsBody))
	status := got.Properties["Status"]
	byName := map[string]string{}
	for _, o := range status.Options {
		byName[o.Name] = o.Group
	}
	if byName["To-Do"] != "To-do" {
		t.Errorf("group de To-Do = %q, want %q", byName["To-Do"], "To-do")
	}
	if byName["Building"] != "In progress" {
		t.Errorf("group de Building = %q, want %q", byName["Building"], "In progress")
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

// Dans une réponse bien formée, chaque option de status apparaît dans
// exactement un groupe : l'API en assigne un d'office, même si la requête
// n'en fournit aucun. Une option absente de tous les option_ids signale donc
// une réponse tronquée, pas une option "sans groupe" — et doit être rejetée,
// pas décodée avec un group vide qui produirait un diff fantôme permanent.
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
		t.Fatal("RemoteDatabaseFromJSON() error = nil, want une erreur : Building n'apparaît dans aucun groupe")
	}
	if !strings.Contains(err.Error(), "Building") {
		t.Errorf("erreur = %q, want qu'elle nomme l'option %q", err.Error(), "Building")
	}
}

// stubTransport rend une réponse par chemin appelé, et retient les chemins.
type stubTransport struct {
	byPath map[string]string
	calls  []string
}

func (s *stubTransport) Execute(_ context.Context, req transport.APIRequest) (transport.APIResponse, error) {
	s.calls = append(s.calls, req.Path)
	return transport.APIResponse{Status: 200, Body: []byte(s.byPath[req.Path])}, nil
}

// La sonde minimale de resources.Read et le décodeur de ce paquet lisent tous
// deux data_sources[0].id depuis le MÊME corps, avec deux structs distincts.
//
// Ce test vit ici et non dans resources : là-bas, il ne pouvait qu'injecter une
// closure codée en dur, donc renommer le tag `json:"data_sources"` de
// from_api.go ne cassait rien — le contrôle compensatoire qui justifiait la
// duplication n'existait pas. Ici, le vrai décodeur tourne sur la fixture que
// la sonde a lue, et les deux doivent s'accorder sur l'id.
func TestProbeAndDecoderAgreeOnDataSourceID(t *testing.T) {
	const probeBody = `{"object":"database","id":"db1","data_sources":[{"id":"ds-attendu","name":"P"}]}`
	st := &stubTransport{byPath: map[string]string{
		"/v1/databases/db1": probeBody,
		"/v1/data_sources/ds-attendu": `{"id":"ds-attendu","title":[{"plain_text":"P"}],
			"properties":{"Name":{"id":"title","name":"Name","type":"title"}}}`,
	}}
	r := resources.NewDatabaseResource(st, RemoteDatabaseFromJSON)

	state, err := r.Read(context.Background(), "db1")
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	db, ok := state.(resources.RemoteDatabase)
	if !ok {
		t.Fatalf("Read() a rendu %T, want resources.RemoteDatabase", state)
	}
	if db.DataSourceID != "ds-attendu" {
		t.Errorf("le décodeur a lu DataSourceID = %q, want %q", db.DataSourceID, "ds-attendu")
	}
	if len(st.calls) != 2 {
		t.Fatalf("appels = %v, want database puis data_source", st.calls)
	}
	if st.calls[1] != "/v1/data_sources/"+db.DataSourceID {
		t.Errorf("la sonde a appelé %q, le décodeur a lu l'id %q : les deux formes ont dérivé",
			st.calls[1], db.DataSourceID)
	}
}
