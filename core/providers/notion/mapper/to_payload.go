// SPDX-License-Identifier: GPL-3.0-or-later

// Package mapper traduit la configuration en payloads d'API Notion, et les
// réponses d'API en états distants.
package mapper

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/tykok/notion-seed/core/state"
)

// ErrUnsupportedType signale un type de propriété hors périmètre. Le schéma
// le rejette déjà en amont ; ce garde-fou couvre les appels programmatiques.
var ErrUnsupportedType = errors.New("type de propriété non supporté")

// DatabaseCreatePayload construit le corps de POST /v1/databases depuis la
// CIBLE RÉSOLUE, jamais depuis la configuration.
//
// C'est l'invariant de sûreté du produit : `target` est l'état exact que le
// plan a affiché, donc tout ce qui part vers l'API a été annoncé. Un paramètre
// de type config.Database rouvrirait un second chemin, capable de diverger du
// plan en silence — c'est ce qui a produit la substitution du groupe "To-do".
// TestMapperDoesNotImportConfig verrouille la propriété.
//
// Depuis l'API 2025-09-03, une database est un conteneur : le schéma des
// propriétés vit dans son data source, transmis à la création sous
// initial_data_source.
func DatabaseCreatePayload(key string, target state.Database, parentPageID string) ([]byte, error) {
	names := make([]string, 0, len(target.Properties))
	for name := range target.Properties {
		names = append(names, name)
	}
	props, err := propertiesPayload(key, target, names)
	if err != nil {
		return nil, err
	}

	body := map[string]any{
		"parent": map[string]any{
			"type":    "page_id",
			"page_id": parentPageID,
		},
		"title":               titlePayload(target.Name),
		"initial_data_source": map[string]any{"properties": props},
	}
	if target.Description != "" {
		body["description"] = richTextPayload(target.Description)
	}
	if target.Icon != "" {
		body["icon"] = iconPayload(target.Icon)
	}
	return json.Marshal(body)
}

// DataSourceUpdatePayload construit le corps de PATCH /v1/data_sources/{id}
// depuis la CIBLE RÉSOLUE, pour les SEULES propriétés nommées.
//
// `props` vient du plan : ce sont les propriétés qui portent au moins une
// ligne. Envoyer davantage écrirait des propriétés que le plan n'a pas
// montrées ; envoyer moins laisserait un plan non appliqué sans le dire.
// Mesuré le 2026-09-24 : une propriété omise du payload n'est pas touchée.
func DataSourceUpdatePayload(key string, target state.Database, props []string) ([]byte, error) {
	out, err := propertiesPayload(key, target, props)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"properties": out})
}

// DatabaseUpdatePayload construit le corps de PATCH /v1/databases/{id} pour
// les SEULS champs nommés.
//
// Mesuré le 2026-09-24 : la description n'est PAS acceptée sur le data
// source (« Use the Update Database API instead »), et l'icône n'est pas
// partagée entre les deux objets — écrire sur la database met les deux à
// jour, écrire sur le data source les fait diverger. Ces trois champs
// passent donc tous par ici.
func DatabaseUpdatePayload(key string, target state.Database, fields []string) ([]byte, error) {
	body := make(map[string]any, len(fields))
	for _, f := range fields {
		switch f {
		case "name":
			body["title"] = titlePayload(target.Name)
		case "description":
			body["description"] = richTextPayload(target.Description)
		case "icon":
			body["icon"] = iconPayload(target.Icon)
		default:
			return nil, fmt.Errorf(
				"database %q : champ %q inconnu du payload de mise à jour\n"+
					"  → c'est un défaut interne de notion-seed : rapportez-le avec la "+
					"sortie de `notion-seed plan`", key, f)
		}
	}
	return json.Marshal(body)
}

// propertiesPayload construit le payload des propriétés NOMMÉES depuis la
// cible résolue. Partagé par la création, qui nomme toutes les propriétés de
// la cible, et par la mise à jour du data source, qui ne nomme que celles du
// plan : les deux chemins doivent rejeter un type non supporté de la même
// façon, en nommant la database et la propriété.
func propertiesPayload(key string, target state.Database, names []string) (map[string]any, error) {
	out := make(map[string]any, len(names))
	for _, name := range names {
		p, ok := target.Properties[name]
		if !ok {
			return nil, fmt.Errorf(
				"database %q : la propriété %q est à écrire mais absente de la cible\n"+
					"  → c'est un défaut interne de notion-seed, pas une erreur de "+
					"configuration : rapportez-le avec la sortie de `notion-seed plan`",
				key, name)
		}
		payload, err := PropertyPayload(p)
		if err != nil {
			return nil, fmt.Errorf("database %q, propriété %q: %w\n"+
				"  → retirez cette propriété du YAML, ou déclarez-la avec un type supporté",
				key, name, err)
		}
		out[name] = payload
	}
	return out, nil
}

// titlePayload rend la forme API d'un titre de database.
func titlePayload(content string) []any {
	return []any{map[string]any{"text": map[string]any{"content": content}}}
}

// richTextPayload rend la forme API d'un champ rich_text (la description de
// la database).
func richTextPayload(content string) []any {
	return []any{map[string]any{"text": map[string]any{"content": content}}}
}

// iconPayload rend la forme API d'un icône emoji.
func iconPayload(emoji string) map[string]any {
	return map[string]any{"type": "emoji", "emoji": emoji}
}

// PropertyPayload construit la configuration d'une propriété.
func PropertyPayload(p state.Property) (map[string]any, error) {
	switch p.Type {
	case "title", "rich_text", "url", "people", "date", "checkbox":
		return map[string]any{p.Type: map[string]any{}}, nil

	case "number":
		num := map[string]any{}
		if p.Format != "" {
			num["format"] = p.Format
		}
		return map[string]any{"number": num}, nil

	case "select", "multi_select":
		return map[string]any{p.Type: map[string]any{
			"options": optionsPayload(p.Options),
		}}, nil

	case "status":
		// Pas de champ `groups` : l'API ne l'accepte pas en modification, et le
		// groupe se pilote par le champ `group` de chaque option.
		return map[string]any{"status": map[string]any{
			"options": optionsPayload(p.Options),
		}}, nil

	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedType, p.Type)
	}
}

// optionsPayload rend la liste COMPLÈTE des options. L'API remplace la liste
// au lieu de la fusionner : toute option absente du payload est détruite, et
// les lignes qui la portaient perdent leur valeur.
//
// Aucune valeur n'est inventée. Un `group` vide n'est pas transmis plutôt que
// remplacé par un défaut : le schéma garantit qu'une option de status en porte
// toujours un, et substituer une valeur ici serait écrire ce que le plan n'a
// pas affiché. Le paramètre `withGroup` a disparu avec le défaut : il n'y a
// plus rien à décider, seulement à transmettre.
//
// La `key` de configuration n'est jamais transmise : c'est une identité interne
// à notion-seed, l'API ne la connaît pas.
func optionsPayload(options []state.Option) []map[string]any {
	out := make([]map[string]any, 0, len(options))
	for _, o := range options {
		entry := map[string]any{"name": o.Name}
		// L'ID est l'identité de l'option côté Notion. L'omettre sur une option
		// existante la fait apparier PAR NOM, donc un nom changé devient un
		// retrait suivi d'un ajout, et les lignes qui la portaient perdent leur
		// valeur. Une option neuve n'en a pas : l'API lui en crée un.
		if o.ID != "" {
			entry["id"] = o.ID
		}
		if o.Color != "" {
			entry["color"] = o.Color
		}
		if o.Group != "" {
			entry["group"] = o.Group
		}
		out = append(out, entry)
	}
	return out
}
