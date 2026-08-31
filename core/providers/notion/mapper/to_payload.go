// Package mapper traduit la configuration en payloads d'API Notion, et les
// réponses d'API en états distants.
package mapper

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/tykok/notion-seed/core/config"
)

// ErrUnsupportedType signale un type de propriété hors périmètre. Le schéma
// le rejette déjà en amont ; ce garde-fou couvre les appels programmatiques.
var ErrUnsupportedType = errors.New("type de propriété non supporté")

// DefaultStatusGroup est le groupe attribué à une option de status sans
// `group` explicite. Il doit être envoyé sur CHAQUE option : l'omettre fait
// retomber toutes les options dans le premier groupe, silencieusement.
const DefaultStatusGroup = "To-do"

// DatabaseCreatePayload construit le corps de POST /v1/databases.
//
// Depuis l'API 2025-09-03, une database est un conteneur : le schéma des
// propriétés vit dans son data source, transmis à la création sous
// initial_data_source.
func DatabaseCreatePayload(db config.Database, parentPageID string) ([]byte, error) {
	props := make(map[string]any, len(db.Properties))
	for name, p := range db.Properties {
		payload, err := PropertyPayload(p)
		if err != nil {
			return nil, fmt.Errorf("database %q, propriété %q: %w", db.Key, name, err)
		}
		props[name] = payload
	}

	body := map[string]any{
		"parent": map[string]any{
			"type":    "page_id",
			"page_id": parentPageID,
		},
		"title": []any{
			map[string]any{"text": map[string]any{"content": db.Name}},
		},
		"initial_data_source": map[string]any{"properties": props},
	}
	if db.Description != "" {
		body["description"] = []any{
			map[string]any{"text": map[string]any{"content": db.Description}},
		}
	}
	if db.Icon != "" {
		body["icon"] = map[string]any{"type": "emoji", "emoji": db.Icon}
	}
	return json.Marshal(body)
}

// PropertyPayload construit la configuration d'une propriété.
func PropertyPayload(p config.Property) (map[string]any, error) {
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
			"options": optionsPayload(p.Options, false),
		}}, nil

	case "status":
		// Pas de champ `groups` : l'API ne l'accepte pas en modification, et le
		// groupe se pilote par le champ `group` de chaque option.
		return map[string]any{"status": map[string]any{
			"options": optionsPayload(p.Options, true),
		}}, nil

	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedType, p.Type)
	}
}

// optionsPayload rend la liste COMPLÈTE des options. L'API remplace la liste
// au lieu de la fusionner : toute option absente du payload est détruite, et
// les lignes qui la portaient perdent leur valeur.
//
// La `key` de configuration n'est jamais transmise : c'est une identité
// interne à notion-seed, l'API ne la connaît pas.
func optionsPayload(options []config.Option, withGroup bool) []map[string]any {
	out := make([]map[string]any, 0, len(options))
	for _, o := range options {
		entry := map[string]any{"name": o.Name}
		if o.Color != "" {
			entry["color"] = o.Color
		}
		if withGroup {
			group := o.Group
			if group == "" {
				group = DefaultStatusGroup
			}
			entry["group"] = group
		}
		out = append(out, entry)
	}
	return out
}
