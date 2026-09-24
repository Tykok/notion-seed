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

// transportFunc permet à un test de décider par MÉTHODE autant que par chemin,
// et de rendre une erreur — ce que stubTransport ne sait pas faire.
type transportFunc func(ctx context.Context, req transport.APIRequest) (transport.APIResponse, error)

func (f transportFunc) Execute(ctx context.Context, req transport.APIRequest) (transport.APIResponse, error) {
	return f(ctx, req)
}

// decodeCreated est le décodeur des tests de création. Il est local au paquet :
// utiliser mapper.RemoteDatabaseFromJSON créerait un cycle d'import, puisque
// mapper importe resources. C'est la même raison qui impose que Create prenne
// un []byte déjà sérialisé.
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

// Create doit POSTer puis RELIRE. La relecture n'est pas du zèle : elle
// rapporte les ids d'options, sans lesquels le state est aveugle à la dérive,
// et elle confronte le résultat à ce qui avait été annoncé.
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
		t.Errorf("appels = %v, want POST /v1/databases puis les deux lectures", seen)
	}
	if string(sentBody) != `{"parent":{}}` {
		t.Errorf("body = %q, il doit partir tel quel", sentBody)
	}
}

// Si la relecture échoue APRÈS un POST réussi, l'identité doit survivre : la
// perdre coûterait un doublon au prochain apply. L'erreur est rapportée à part,
// pas confondue avec un échec de création.
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
		t.Fatalf("Create() error = %v, want nil : la création a réussi", err)
	}
	if got.ID != "db-new" || got.DataSourceID != "ds-new" {
		t.Errorf("ids = %q/%q : l'identité ne doit jamais être perdue", got.ID, got.DataSourceID)
	}
	if got.ReadErr == nil {
		t.Error("ReadErr = nil, want l'échec de relecture")
	}
}

// Un POST en échec est un échec de création : aucune identité à conserver.
func TestCreateReturnsErrorWhenPostFails(t *testing.T) {
	tr := transportFunc(func(_ context.Context, _ transport.APIRequest) (transport.APIResponse, error) {
		return transport.APIResponse{}, &transport.APIError{Status: 400, NotionCode: "validation_error"}
	})
	r := NewDatabaseResource(tr, decodeCreated)
	got, err := r.Create(context.Background(), []byte(`{}`))
	if err == nil {
		t.Fatal("Create() error = nil, want l'erreur de l'API")
	}
	if got.ID != "" {
		t.Errorf("ID = %q, want vide : rien n'a été créé", got.ID)
	}
}

// Une réponse sans identifiant ne doit pas passer pour un succès : on ne sait
// pas ce qui a été créé, et le message doit envoyer vérifier.
func TestCreateRefusesResponseWithoutID(t *testing.T) {
	tr := transportFunc(func(_ context.Context, _ transport.APIRequest) (transport.APIResponse, error) {
		return transport.APIResponse{Status: 200, Body: []byte(`{"object":"database"}`)}, nil
	})
	r := NewDatabaseResource(tr, decodeCreated)
	_, err := r.Create(context.Background(), []byte(`{}`))
	if err == nil {
		t.Fatal("Create() error = nil, want un refus")
	}
	for _, want := range []string{"identifiant", "  → ", "page parente"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message = %q, il doit contenir %q", err.Error(), want)
		}
	}
}
