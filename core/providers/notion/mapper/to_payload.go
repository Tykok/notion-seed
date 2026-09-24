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
	props := make(map[string]any, len(target.Properties))
	for name, p := range target.Properties {
		payload, err := PropertyPayload(p)
		if err != nil {
			return nil, fmt.Errorf("database %q, propriété %q: %w\n"+
				"  → retirez cette propriété du YAML, ou déclarez-la avec un type supporté",
				key, name, err)
		}
		props[name] = payload
	}

	body := map[string]any{
		"parent": map[string]any{
			"type":    "page_id",
			"page_id": parentPageID,
		},
		"title": []any{
			map[string]any{"text": map[string]any{"content": target.Name}},
		},
		"initial_data_source": map[string]any{"properties": props},
	}
	if target.Description != "" {
		body["description"] = []any{
			map[string]any{"text": map[string]any{"content": target.Description}},
		}
	}
	if target.Icon != "" {
		body["icon"] = map[string]any{"type": "emoji", "emoji": target.Icon}
	}
	return json.Marshal(body)
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
