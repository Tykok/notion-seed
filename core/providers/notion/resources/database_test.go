// SPDX-License-Identifier: GPL-3.0-or-later

package resources

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/tykok/notion-seed/core/config"
	"github.com/tykok/notion-seed/core/providers/notion/transport"
)

// stubTransport rend une réponse par chemin appelé.
type stubTransport struct {
	byPath map[string]string
	calls  []string
}

func (s *stubTransport) Execute(_ context.Context, req transport.APIRequest) (transport.APIResponse, error) {
	s.calls = append(s.calls, req.Path)
	return transport.APIResponse{Status: 200, Body: []byte(s.byPath[req.Path])}, nil
}

func TestDatabaseResourceReadFetchesDatabaseThenDataSource(t *testing.T) {
	st := &stubTransport{byPath: map[string]string{
		"/v1/databases/db1": `{"id":"db1","data_sources":[{"id":"ds1","name":"Projects"}]}`,
		"/v1/data_sources/ds1": `{"id":"ds1","title":[{"plain_text":"Projects"}],
			"properties":{"Name":{"id":"title","name":"Name","type":"title"}}}`,
	}}
	// Le stub CAPTURE ce qu'il reçoit. Sans ça, un appel decode(dsBody, dbBody)
	// avec les arguments inversés passerait le test sans être vu.
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
		st.calls[0] != "/v1/databases/db1" || st.calls[1] != "/v1/data_sources/ds1" {
		t.Errorf("appels = %v, want database puis data_source", st.calls)
	}
	if !strings.Contains(string(gotDB), `"object":"database"`) &&
		!strings.Contains(string(gotDB), `"data_sources"`) {
		t.Errorf("decode a reçu %q comme dbBody : arguments probablement inversés", gotDB)
	}
	if !strings.Contains(string(gotDS), `"properties"`) {
		t.Errorf("decode a reçu %q comme dsBody : arguments probablement inversés", gotDS)
	}
}

// Le test de dérive entre la sonde de Read et le décodeur vit dans le paquet
// mapper (TestProbeAndDecoderAgreeOnDataSourceID) : ici, il ne pouvait
// qu'injecter une closure codée en dur, donc ne rien garder. Le paquet mapper,
// lui, peut importer resources et faire tourner le VRAI décodeur.

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
	// Ordre déterministe : tri alphabétique des propriétés.
	if cs.Details[0].Target != `property "Budget" (number)` {
		t.Errorf("Details[0].Target = %q", cs.Details[0].Target)
	}
}

// Piège du typed-nil : un RemoteState non-nil dont Exists() est false doit
// mener à une création comme un nil. Sans ce test, simplifier la condition en
// `remote == nil` ne casserait rien.
func TestDatabaseResourceDiffOnNonNilButAbsentRemoteIsAlsoCreate(t *testing.T) {
	r := NewDatabaseResource(nil, nil)
	db := config.Database{Key: "projects", Name: "Projects",
		Properties: map[string]config.Property{"Name": {Type: "title"}}}

	var remote RemoteState = RemoteDatabase{} // non-nil, Found == false
	if remote == nil {
		t.Fatal("le montage du test est faux : remote doit être non-nil")
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
	if _, err := r.Diff("pas une database", nil); err == nil {
		t.Fatal("Diff() error = nil, want une erreur de type")
	}
}

// La méthode Diff délègue à la fonction de paquet, qui n'a besoin d'aucune
// ressource. C'est ce qui permet au moteur de diff de ne plus construire un
// NewDatabaseResource(nil, nil) pour appeler un calcul pur.
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
		t.Errorf("méthode = %+v, fonction = %+v : la délégation a divergé", viaMethod, viaFunc)
	}
}
