// SPDX-License-Identifier: GPL-3.0-or-later

package resources

import (
	"context"
	"encoding/json"
	"errors"
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

// DatabaseExists sonde l'EXISTENCE d'une database, sans rien lire d'autre.
//
// Contrairement à Read, elle n'atteint PAS le data source : Task 8 s'en sert
// pour diagnostiquer un ancêtre archivé après l'échec du PATCH du data source
// avec un 404. Sous un ancêtre à la corbeille, GET database répond 200 alors
// que le data source, lui, est inatteignable — un Read complet échouerait ici
// et cacherait le diagnostic derrière l'échec de la seconde requête.
func (r *DatabaseResource) DatabaseExists(ctx context.Context, id string) (bool, error) {
	_, err := r.tr.Execute(ctx, transport.APIRequest{
		Method: "GET",
		Path:   "/v1/databases/" + id,
	})
	if err == nil {
		return true, nil
	}
	var apiErr *transport.APIError
	if errors.As(err, &apiErr) && apiErr.Status == 404 {
		return false, nil
	}
	return false, err
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
	rd, err := asRemoteDatabase(probe.ID, remote)
	if err != nil {
		out.ReadErr = err
		return out, nil
	}
	out.Remote = rd
	if rd.DataSourceID != "" {
		out.DataSourceID = rd.DataSourceID
	}
	return out, nil
}

// UpdatedDatabase porte le résultat d'une mise à jour.
//
// DatabaseWritten dit si le PATCH de la database est passé. Sans lui, un échec
// du PATCH du data source ne saurait pas dire que le nom, lui, est écrit — et
// l'utilisateur ne saurait pas ce qui reste à faire.
type UpdatedDatabase struct {
	DatabaseWritten bool
	Remote          RemoteDatabase
	ReadErr         error
}

// Update écrit une database existante, puis relit le résultat.
//
// ORDRE : la database d'abord, le data source ensuite. Un échec du second laisse
// alors un nom et une icône à jour et AUCUNE donnée touchée — l'échec le moins
// coûteux. L'ordre inverse écrirait le schéma, donc les lignes, avant de rater
// le cosmétique.
//
// Mesuré le 2026-09-24, et c'est une seconde raison de cet ordre : sur une
// database dont la page ancêtre est à la corbeille, le PATCH de la database rend
// un 400 qui NOMME la cause (« archived ancestor »), là où le PATCH du data
// source rend un 404 qui accuse le partage avec l'intégration. L'ordre fait
// donc tomber l'utilisateur sur le message juste.
//
// Un corps vide saute son endpoint : une mise à jour qui ne touche que des
// propriétés n'a rien à écrire sur la database.
//
// La relecture rapporte les ids des options neuves, sans lesquels le state est
// aveugle à la dérive, et permet à l'appelant de confronter le réel à ce que le
// plan avait annoncé.
func (r *DatabaseResource) Update(
	ctx context.Context, id, dsID string, dbBody, dsBody []byte,
) (UpdatedDatabase, error) {
	var out UpdatedDatabase

	if len(dbBody) > 0 {
		if _, err := r.tr.Execute(ctx, transport.APIRequest{
			Method: "PATCH",
			Path:   "/v1/databases/" + id,
			Body:   dbBody,
		}); err != nil {
			return out, err
		}
		out.DatabaseWritten = true
	}

	if len(dsBody) > 0 {
		if _, err := r.tr.Execute(ctx, transport.APIRequest{
			Method: "PATCH",
			Path:   "/v1/data_sources/" + dsID,
			Body:   dsBody,
		}); err != nil {
			return out, err
		}
	}

	remote, err := r.Read(ctx, id)
	if err != nil {
		out.ReadErr = err
		return out, nil
	}
	rd, err := asRemoteDatabase(id, remote)
	if err != nil {
		out.ReadErr = err
		return out, nil
	}
	out.Remote = rd
	return out, nil
}

// trashBody est le corps qui met une database à la corbeille. Mesuré le
// 2026-09-24 : il suffit, et la réponse porte archived=true, in_trash=true.
const trashBody = `{"in_trash":true}`

// Trash met une database à la corbeille : un seul PATCH, sur la database.
//
// Le booléen dit si la RÉPONSE confirme la corbeille, selon une règle locale
// (archived || in_trash) qui DOIT rester la même que celle du décodeur
// (mapper.RemoteDatabaseFromJSON) — celle-là même par laquelle le plan suivant
// classerait la database. La dupliquer ici, plutôt que d'appeler le décodeur,
// est structurel : le décodeur veut aussi le corps du data source, qu'un PATCH
// corbeille ne relit jamais. TestTrashLocalRuleAgreesWithDecoderRule, dans le
// paquet mapper, garde les deux règles synchronisées en faisant tourner le
// vrai décodeur. Un 200 qui ne confirme pas la corbeille n'est pas une
// destruction : l'appelant garde alors l'identité, car l'abandonner rendrait
// invisible une database qui existe peut-être encore.
//
// Une réponse illisible rend une issue inconnue : l'appel a répondu 200, donc la
// mutation a peut-être eu lieu, et rien ne permet de le dire.
func (r *DatabaseResource) Trash(ctx context.Context, id string) (bool, error) {
	resp, err := r.tr.Execute(ctx, transport.APIRequest{
		Method: "PATCH",
		Path:   "/v1/databases/" + id,
		Body:   []byte(trashBody),
	})
	if err != nil {
		return false, err
	}
	var probe struct {
		Archived bool `json:"archived"`
		InTrash  bool `json:"in_trash"`
	}
	if err := json.Unmarshal(resp.Body, &probe); err != nil {
		return false, &transport.OutcomeUnknownError{Cause: fmt.Errorf(
			"réponse de PATCH /v1/databases/%s illisible: %w\n"+
				"  → réessayez ; si ça persiste, vérifiez que `ntn` parle bien la version "+
				"d'API 2025-09-03 (`ntn --version`)", id, err)}
	}
	return probe.Archived || probe.InTrash, nil
}

// asRemoteDatabase ramène la relecture d'une database à son type concret. Read
// ne rend jamais autre chose : un autre type ne peut venir que d'un défaut de
// notion-seed, et le message le dit plutôt que de laisser chercher du côté de
// Notion.
func asRemoteDatabase(id string, remote RemoteState) (RemoteDatabase, error) {
	rd, ok := remote.(RemoteDatabase)
	if !ok {
		return RemoteDatabase{}, fmt.Errorf(
			"relecture de %s : type inattendu %T\n"+
				"  → c'est un défaut interne de notion-seed : signalez-le avec la "+
				"sortie de `notion-seed plan`", id, remote)
	}
	return rd, nil
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
