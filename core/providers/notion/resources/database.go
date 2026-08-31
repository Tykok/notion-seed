package resources

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/tykok/notion-seed/core/config"
	"github.com/tykok/notion-seed/core/providers/notion/transport"
)

// RemoteOption est une option de select/status/multi_select telle qu'elle
// existe dans Notion. L'ID est la seule ancre d'identité : ré-ajouter une
// option par son nom après l'avoir détruite en crée une nouvelle, avec un
// nouvel ID.
type RemoteOption struct {
	ID    string
	Name  string
	Color string
	Group string
}

// RemoteProperty est une propriété telle qu'elle existe dans Notion.
type RemoteProperty struct {
	ID           string
	Type         string
	NumberFormat string
	Options      []RemoteOption
}

// RemoteDatabase est l'état distant d'une database et de son data source par
// défaut. Les deux sont modélisés ensemble au MVP 0, qui ne gère qu'un data
// source par database, mais les identités restent distinctes.
type RemoteDatabase struct {
	ID           string
	DataSourceID string
	Name         string
	Description  string
	Properties   map[string]RemoteProperty
	Archived     bool
	Found        bool
}

func (d RemoteDatabase) Exists() bool { return d.Found }

// DatabaseResource lit et compare des databases Notion.
type DatabaseResource struct {
	tr     transport.Transport
	decode func(dbBody, dsBody []byte) (RemoteDatabase, error)
}

var _ Resource = (*DatabaseResource)(nil)

// NewDatabaseResource construit la ressource. Le décodeur est injecté pour
// éviter un cycle d'import entre resources et mapper.
func NewDatabaseResource(
	tr transport.Transport,
	decode func(dbBody, dsBody []byte) (RemoteDatabase, error),
) *DatabaseResource {
	return &DatabaseResource{tr: tr, decode: decode}
}

func (r *DatabaseResource) Type() string { return "database" }

// Read lit une database et son data source par défaut. Deux appels sont
// nécessaires : la database porte l'identité et l'archivage, le data source
// porte le titre et le schéma.
//
// Non appelée par `plan` au MVP 0 (aucune ressource n'est mise en
// correspondance sans state) ; c'est le seam du refresh MVP 1 et d'`import`.
func (r *DatabaseResource) Read(ctx context.Context, id string) (RemoteState, error) {
	dbResp, err := r.tr.Execute(ctx, transport.APIRequest{
		Method: "GET",
		Path:   "/v1/databases/" + id,
	})
	if err != nil {
		return RemoteDatabase{}, err
	}
	var probe struct {
		DataSources []struct {
			ID string `json:"id"`
		} `json:"data_sources"`
	}
	if err := jsonUnmarshal(dbResp.Body, &probe); err != nil {
		return RemoteDatabase{}, err
	}
	if len(probe.DataSources) == 0 {
		return RemoteDatabase{}, fmt.Errorf(
			"database %s n'expose aucun data source, ce qui ne devrait pas arriver", id)
	}

	dsResp, err := r.tr.Execute(ctx, transport.APIRequest{
		Method: "GET",
		Path:   "/v1/data_sources/" + probe.DataSources[0].ID,
	})
	if err != nil {
		return RemoteDatabase{}, err
	}
	return r.decode(dbResp.Body, dsResp.Body)
}

// Diff compare une database désirée à son état distant. Au MVP 0, l'état
// distant est toujours absent : tout ressort en création.
func (r *DatabaseResource) Diff(desired any, remote RemoteState) (Changeset, error) {
	db, ok := desired.(config.Database)
	if !ok {
		return Changeset{}, fmt.Errorf("desired doit être une config.Database, got %T", desired)
	}
	cs := Changeset{Resource: "database." + db.Key}

	if remote == nil || !remote.Exists() {
		cs.Kind = KindCreate
		names := make([]string, 0, len(db.Properties))
		for name := range db.Properties {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			cs.Details = append(cs.Details, Detail{
				Op:     "+",
				Target: fmt.Sprintf("property %q (%s)", name, db.Properties[name].Type),
			})
		}
		return cs, nil
	}

	// Le diff fin sur une database existante arrive au MVP 2, avec le refresh
	// et la détection de dérive. Sans state, on ne met aucune ressource en
	// correspondance, donc ce chemin est inatteignable au MVP 0.
	cs.Kind = KindNone
	return cs, nil
}

// jsonUnmarshal isole l'import encoding/json du reste du fichier.
func jsonUnmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}
