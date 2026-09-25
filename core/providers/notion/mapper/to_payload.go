// SPDX-License-Identifier: GPL-3.0-or-later

// Package mapper translates the configuration into Notion API payloads, and
// API responses into remote states.
package mapper

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/tykok/notion-seed/core/state"
)

// ErrUnsupportedType reports an out-of-scope property type. The schema
// already rejects it upstream; this safeguard covers programmatic calls.
var ErrUnsupportedType = errors.New("unsupported property type")

// DatabaseCreatePayload builds the body of POST /v1/databases from the
// RESOLVED TARGET, never from the configuration.
//
// It is the product's safety invariant: `target` is the exact state the plan
// showed, so everything that goes to the API was announced. A parameter of
// type config.Database would reopen a second path, able to diverge from the
// plan silently — that is what produced the "To-do" group substitution.
// TestMapperDoesNotImportConfig locks the property.
//
// Since API 2025-09-03, a database is a container: the properties schema
// lives in its data source, passed on creation under initial_data_source.
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

// DataSourceUpdatePayload builds the body of PATCH /v1/data_sources/{id} from
// the RESOLVED TARGET, for the named properties ONLY.
//
// `props` comes from the plan: they are the properties that carry at least
// one line. Sending more would write properties the plan did not show;
// sending fewer would leave a plan unapplied without saying so. Measured on
// 2026-09-24: a property omitted from the payload is not touched.
func DataSourceUpdatePayload(key string, target state.Database, props []string) ([]byte, error) {
	out, err := propertiesPayload(key, target, props)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"properties": out})
}

// DatabaseUpdatePayload builds the body of PATCH /v1/databases/{id} for the
// named fields ONLY.
//
// Measured on 2026-09-24: the description is NOT accepted on the data source
// ("Use the Update Database API instead"), and the icon is not shared between
// the two objects — writing to the database updates both, writing to the
// data source makes them diverge. So these three fields all go through here.
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
				"database %q: field %q unknown to the update payload\n"+
					"  → this is a notion-seed bug: report it with the "+
					"output of `notion-seed plan`", key, f)
		}
	}
	return json.Marshal(body)
}

// propertiesPayload builds the payload of the NAMED properties from the
// resolved target. Shared by the creation, which names every property of the
// target, and by the data source update, which names only the plan's: both
// paths must reject an unsupported type the same way, naming the database and
// the property.
func propertiesPayload(key string, target state.Database, names []string) (map[string]any, error) {
	out := make(map[string]any, len(names))
	for _, name := range names {
		p, ok := target.Properties[name]
		if !ok {
			return nil, fmt.Errorf(
				"database %q: property %q is to be written but missing from the target\n"+
					"  → this is a notion-seed bug, not a "+
					"configuration error: report it with the output of `notion-seed plan`",
				key, name)
		}
		payload, err := PropertyPayload(p)
		if err != nil {
			return nil, fmt.Errorf("database %q, property %q: %w\n"+
				"  → remove this property from the YAML, or declare it with a supported type",
				key, name, err)
		}
		out[name] = payload
	}
	return out, nil
}

// titlePayload returns the API form of a database title.
func titlePayload(content string) []any {
	return []any{map[string]any{"text": map[string]any{"content": content}}}
}

// richTextPayload returns the API form of a rich_text field (the database's
// description).
func richTextPayload(content string) []any {
	return []any{map[string]any{"text": map[string]any{"content": content}}}
}

// iconPayload returns the API form of an emoji icon.
func iconPayload(emoji string) map[string]any {
	return map[string]any{"type": "emoji", "emoji": emoji}
}

// PropertyPayload builds a property's configuration.
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
		// No `groups` field: the API does not accept it on update, and the group
		// is driven by each option's `group` field.
		return map[string]any{"status": map[string]any{
			"options": optionsPayload(p.Options),
		}}, nil

	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedType, p.Type)
	}
}

// optionsPayload returns the FULL list of options. The API replaces the list
// instead of merging it: any option missing from the payload is destroyed,
// and the rows that held it lose their value.
//
// No value is invented. An empty `group` is not sent rather than replaced by
// a default: the schema guarantees a status option always carries one, and
// substituting a value here would write what the plan did not show. The
// `withGroup` parameter went away with the default: there is nothing left to
// decide, only to pass on.
//
// The configuration `key` is never sent: it is an identity internal to
// notion-seed, the API does not know it.
func optionsPayload(options []state.Option) []map[string]any {
	out := make([]map[string]any, 0, len(options))
	for _, o := range options {
		entry := map[string]any{"name": o.Name}
		// The ID is the option's identity on the Notion side. Omitting it on an
		// existing option makes it match BY NAME, so a changed name becomes a
		// removal followed by an addition, and the rows that held it lose their
		// value. A new option has none: the API creates one for it.
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
