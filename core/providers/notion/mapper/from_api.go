// SPDX-License-Identifier: GPL-3.0-or-later

package mapper

import (
	"encoding/json"
	"fmt"

	"github.com/tykok/notion-seed/core/preflight"
	"github.com/tykok/notion-seed/core/providers/notion/resources"
)

// Raw shapes of the API responses, reduced to the fields notion-seed needs.
// The ignored fields are not an omission: what is not declared in the config
// is not touched.
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

// RemoteDatabaseFromJSON assembles the remote state from the response of
// GET /v1/databases/{id} and that of GET /v1/data_sources/{id}.
//
// Both calls are needed: since 2025-09-03, the database holds the identity and
// the archiving, the data source holds the title and the schema.
func RemoteDatabaseFromJSON(dbBody, dsBody []byte) (resources.RemoteDatabase, error) {
	var db rawDatabase
	if err := json.Unmarshal(dbBody, &db); err != nil {
		return resources.RemoteDatabase{}, fmt.Errorf(
			"unreadable response from GET /v1/databases: %w\n  → retry; if it persists, %s",
			err, preflight.PinNtnHint())
	}
	var ds rawDataSource
	if err := json.Unmarshal(dsBody, &ds); err != nil {
		return resources.RemoteDatabase{}, fmt.Errorf(
			"unreadable response from GET /v1/data_sources: %w\n  → retry; if it persists, %s",
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
	out.DataSourceCount = len(db.DataSources)
	if len(db.DataSources) > 0 {
		out.DataSourceID = db.DataSources[0].ID
		if out.Name == "" {
			out.Name = db.DataSources[0].Name
		}
	}
	// Only the emoji is expressible in the YAML. The other forms stay "", which
	// means "nothing comparable", not "no icon".
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
			// The group is not held by the option in the response: it has to be
			// rebuilt from groups[].option_ids.
			groupByOption := make(map[string]string)
			for _, g := range p.Status.Groups {
				for _, id := range g.OptionIDs {
					groupByOption[id] = g.Name
				}
			}
			rp.Options = convertOptions(p.Status.Options, groupByOption)
			// In a well-formed response, a status option always belongs to
			// exactly one group: the API assigns one by default, even when the
			// request provides none. An empty group therefore does not mean
			// "no group", it means "truncated response". Letting it through
			// would produce a phantom difference on every diff run, since the
			// desired value is never "".
			for _, o := range rp.Options {
				if o.Group == "" {
					return resources.RemoteDatabase{}, fmt.Errorf(
						"property %q: option %q (id %s) appears in no group — incomplete API response",
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
