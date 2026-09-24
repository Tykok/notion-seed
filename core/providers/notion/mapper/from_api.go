// SPDX-License-Identifier: GPL-3.0-or-later

package mapper

import (
	"encoding/json"
	"fmt"

	"github.com/tykok/notion-seed/core/preflight"
	"github.com/tykok/notion-seed/core/providers/notion/resources"
)

// Formes brutes des réponses de l'API, réduites aux champs dont notion-seed a
// besoin. Les champs ignorés ne sont pas une omission : ce qui n'est pas
// déclaré dans la config n'est pas touché.
type rawDatabase struct {
	ID       string `json:"id"`
	Archived bool   `json:"archived"`
	InTrash  bool   `json:"in_trash"`
	Icon     struct {
		Type  string `json:"type"`
		Emoji string `json:"emoji"`
	} `json:"icon"`
	DataSources []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"data_sources"`
}

type rawRichText struct {
	PlainText string `json:"plain_text"`
}

type rawOption struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

type rawGroup struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	OptionIDs []string `json:"option_ids"`
}

type rawProperty struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Type   string `json:"type"`
	Number struct {
		Format string `json:"format"`
	} `json:"number"`
	Select struct {
		Options []rawOption `json:"options"`
	} `json:"select"`
	MultiSelect struct {
		Options []rawOption `json:"options"`
	} `json:"multi_select"`
	Status struct {
		Options []rawOption `json:"options"`
		Groups  []rawGroup  `json:"groups"`
	} `json:"status"`
}

type rawDataSource struct {
	ID          string                 `json:"id"`
	Title       []rawRichText          `json:"title"`
	Description []rawRichText          `json:"description"`
	Properties  map[string]rawProperty `json:"properties"`
}

// RemoteDatabaseFromJSON assemble l'état distant depuis la réponse de
// GET /v1/databases/{id} et celle de GET /v1/data_sources/{id}.
//
// Les deux appels sont nécessaires : depuis 2025-09-03, la database porte
// l'identité et l'archivage, le data source porte le titre et le schéma.
func RemoteDatabaseFromJSON(dbBody, dsBody []byte) (resources.RemoteDatabase, error) {
	var db rawDatabase
	if err := json.Unmarshal(dbBody, &db); err != nil {
		return resources.RemoteDatabase{}, fmt.Errorf(
			"réponse de GET /v1/databases illisible: %w\n  → réessayez ; si ça persiste, %s",
			err, preflight.PinNtnHint())
	}
	var ds rawDataSource
	if err := json.Unmarshal(dsBody, &ds); err != nil {
		return resources.RemoteDatabase{}, fmt.Errorf(
			"réponse de GET /v1/data_sources illisible: %w\n  → réessayez ; si ça persiste, %s",
			err, preflight.PinNtnHint())
	}

	out := resources.RemoteDatabase{
		ID:          db.ID,
		Archived:    db.Archived || db.InTrash,
		Found:       db.ID != "",
		Description: firstPlainText(ds.Description),
		Name:        firstPlainText(ds.Title),
		Properties:  make(map[string]resources.RemoteProperty, len(ds.Properties)),
	}
	if len(db.DataSources) > 0 {
		out.DataSourceID = db.DataSources[0].ID
		if out.Name == "" {
			out.Name = db.DataSources[0].Name
		}
	}
	// Seul l'emoji est exprimable dans le YAML. Les autres formes restent "",
	// ce qui vaut « rien de comparable », pas « pas d'icône ».
	if db.Icon.Type == "emoji" {
		out.Icon = db.Icon.Emoji
	}

	for name, p := range ds.Properties {
		rp := resources.RemoteProperty{ID: p.ID, Type: p.Type}
		switch p.Type {
		case "number":
			rp.NumberFormat = p.Number.Format
		case "select":
			rp.Options = convertOptions(p.Select.Options, nil)
		case "multi_select":
			rp.Options = convertOptions(p.MultiSelect.Options, nil)
		case "status":
			// Le group n'est pas porté par l'option dans la réponse : il faut le
			// reconstruire depuis groups[].option_ids.
			groupByOption := make(map[string]string)
			for _, g := range p.Status.Groups {
				for _, id := range g.OptionIDs {
					groupByOption[id] = g.Name
				}
			}
			rp.Options = convertOptions(p.Status.Options, groupByOption)
			// Dans une réponse bien formée, une option de status appartient
			// toujours à exactement un groupe : l'API en assigne un d'office,
			// même quand la requête n'en fournit aucun. Un group vide ne veut
			// donc pas dire « sans groupe », il veut dire « réponse tronquée ».
			// Le laisser passer produirait une différence fantôme à chaque run
			// du diff, puisque le désiré ne vaut jamais "".
			for _, o := range rp.Options {
				if o.Group == "" {
					return resources.RemoteDatabase{}, fmt.Errorf(
						"propriété %q : l'option %q (id %s) n'apparaît dans aucun groupe — réponse de l'API incomplète",
						name, o.Name, o.ID)
				}
			}
		}
		out.Properties[name] = rp
	}
	return out, nil
}

func convertOptions(in []rawOption, groupByOption map[string]string) []resources.RemoteOption {
	out := make([]resources.RemoteOption, 0, len(in))
	for _, o := range in {
		out = append(out, resources.RemoteOption{
			ID:    o.ID,
			Name:  o.Name,
			Color: o.Color,
			Group: groupByOption[o.ID],
		})
	}
	return out
}

func firstPlainText(rt []rawRichText) string {
	if len(rt) == 0 {
		return ""
	}
	return rt[0].PlainText
}
