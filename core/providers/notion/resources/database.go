// SPDX-License-Identifier: GPL-3.0-or-later

package resources

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/tykok/notion-seed/core/change"
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
	// Icon est l'emoji de la database, ou "" pour toute autre forme d'icône.
	// Le YAML ne déclare qu'un emoji : décoder un fichier ou une URL ici
	// produirait une différence que le plan afficherait à chaque run sans
	// jamais pouvoir la résoudre.
	//
	// Mesuré le 2026-09-24 : l'icône n'est PAS partagée entre la database et son
	// data source. PATCH sur la database met les deux à jour, PATCH sur le data
	// source ne touche que lui. notion-seed lit et écrit celle de la database.
	Icon       string
	Properties map[string]RemoteProperty
	Archived   bool
	Found      bool
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
	// Ce parsing minimal duplique un champ que le décodeur relira. C'est
	// structurel, pas accidentel : on a besoin de l'id du data source AVANT de
	// pouvoir faire le second appel, alors que le décodeur a besoin des deux
	// corps — il ne peut donc pas tourner en premier. Exporter un helper depuis
	// mapper recréerait le cycle d'import que l'injection brise, et injecter une
	// seconde fonction pour un seul champ coûterait plus que la duplication.
	// TestProbeAndDecoderAgreeOnDataSourceID, dans le paquet mapper, garde les
	// deux formes synchronisées en faisant tourner le vrai décodeur.
	var probe struct {
		DataSources []struct {
			ID string `json:"id"`
		} `json:"data_sources"`
	}
	if err := json.Unmarshal(dbResp.Body, &probe); err != nil {
		return RemoteDatabase{}, fmt.Errorf(
			"réponse de GET /v1/databases/%s illisible: %w\n"+
				"  → réessayez ; si ça persiste, vérifiez que `ntn` parle bien la version "+
				"d'API 2025-09-03 (`ntn --version`)", id, err)
	}
	if len(probe.DataSources) == 0 {
		return RemoteDatabase{}, fmt.Errorf(
			"database %s n'expose aucun data source, ce qui ne devrait pas arriver\n"+
				"  → vérifiez que l'id désigne bien une database (et non une page) dans "+
				"l'URL Notion ; si c'est le cas, rapportez le cas : notion-seed ne sait "+
				"pas lire cette forme de réponse", id)
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

// CreatedDatabase porte le résultat d'une création.
//
// ID et DataSourceID sont renseignés dès que le POST a répondu, MÊME si la
// relecture échoue ensuite : une identité perdue coûte une database recréée en
// double au prochain apply, alors qu'un instantané incomplet ne coûte qu'une
// dérive non détectable sur les options. ReadErr porte l'échec de relecture,
// qui n'est pas un échec de création — les confondre ferait croire que rien
// n'a été écrit.
type CreatedDatabase struct {
	ID           string
	DataSourceID string
	Remote       RemoteDatabase
	ReadErr      error
}

// Create crée une database, puis relit le résultat.
//
// Le corps arrive déjà sérialisé : `resources` ne peut pas importer
// `core/state`, qui l'importe déjà, donc la cible résolue est traduite en
// payload par `mapper`, en amont.
//
// La relecture n'est pas du zèle. Elle rapporte les ids d'options, sans
// lesquels le state est aveugle à la dérive, et elle permet à l'appelant de
// confronter le réel à ce que le plan avait annoncé — sur notre propre
// écriture.
func (r *DatabaseResource) Create(ctx context.Context, body []byte) (CreatedDatabase, error) {
	resp, err := r.tr.Execute(ctx, transport.APIRequest{
		Method: "POST",
		Path:   "/v1/databases",
		Body:   body,
	})
	if err != nil {
		return CreatedDatabase{}, err
	}

	var probe struct {
		ID          string `json:"id"`
		DataSources []struct {
			ID string `json:"id"`
		} `json:"data_sources"`
	}
	if err := json.Unmarshal(resp.Body, &probe); err != nil {
		return CreatedDatabase{}, fmt.Errorf(
			"réponse de POST /v1/databases illisible: %w\n"+
				"  → la database a peut-être été créée : ouvrez la page parente dans "+
				"Notion pour vérifier avant de relancer", err)
	}
	if probe.ID == "" {
		return CreatedDatabase{}, fmt.Errorf(
			"POST /v1/databases n'a rendu aucun identifiant\n" +
				"  → la database a peut-être été créée : ouvrez la page parente dans " +
				"Notion pour vérifier avant de relancer")
	}

	out := CreatedDatabase{ID: probe.ID}
	if len(probe.DataSources) > 0 {
		out.DataSourceID = probe.DataSources[0].ID
	}

	remote, err := r.Read(ctx, probe.ID)
	if err != nil {
		out.ReadErr = err
		return out, nil
	}
	rd, ok := remote.(RemoteDatabase)
	if !ok {
		out.ReadErr = fmt.Errorf("relecture de %s : type inattendu %T", probe.ID, remote)
		return out, nil
	}
	out.Remote = rd
	if rd.DataSourceID != "" {
		out.DataSourceID = rd.DataSourceID
	}
	return out, nil
}

// Diff compare une database désirée à son état distant. Au MVP 0, l'état
// distant est toujours absent : tout ressort en création.
func (r *DatabaseResource) Diff(desired any, remote RemoteState) (Changeset, error) {
	db, ok := desired.(config.Database)
	if !ok {
		return Changeset{}, fmt.Errorf("desired doit être une config.Database, got %T", desired)
	}
	return DatabaseChangeset(db, remote), nil
}

// DatabaseChangeset est le diff d'une database, sans transport ni décodeur.
//
// C'est une FONCTION de paquet, et la méthode Diff y délègue : l'exiger sur un
// receiver forçait le moteur de diff à construire un
// NewDatabaseResource(nil, nil) juste pour appeler une méthode pure — une mine
// qui n'attendait qu'un appel réseau ajouté dans ce chemin, et un commentaire
// d'invariant à maintenir.
func DatabaseChangeset(db config.Database, remote RemoteState) Changeset {
	cs := Changeset{Resource: "database." + db.Key}

	// Le typed-nil compte : un RemoteState non-nil dont Exists() est false doit
	// mener à une création comme un nil.
	if remote == nil || !remote.Exists() {
		cs.Kind = KindCreate
		names := make([]string, 0, len(db.Properties))
		for name := range db.Properties {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			cs.Details = append(cs.Details, NewDetail("+",
				fmt.Sprintf("property %q (%s)", name, db.Properties[name].Type),
				change.ClassSafe))
		}
		return cs
	}

	// Le diff fin sur une database existante arrive au MVP 2, avec le refresh
	// et la détection de dérive. Sans state, on ne met aucune ressource en
	// correspondance, donc ce chemin est inatteignable au MVP 0.
	cs.Kind = KindNone
	return cs
}
