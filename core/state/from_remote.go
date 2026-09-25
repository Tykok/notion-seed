// SPDX-License-Identifier: GPL-3.0-or-later

package state

import "github.com/tykok/notion-seed/core/providers/notion/resources"

// FromRemote projects the state read from the API into the pivot type.
//
// The result carries no key: the API does not know this notion. It is the
// "actual" side of the three-way diff.
//
// Icon holds the database's emoji, or "" for any other form of icon: the YAML
// only declares an emoji.
func FromRemote(rd resources.RemoteDatabase) Database {
	out := Database{
		ID:           rd.ID,
		DataSourceID: rd.DataSourceID,
		Name:         rd.Name,
		Description:  rd.Description,
		Icon:         rd.Icon,
		Properties:   make(map[string]Property, len(rd.Properties)),
	}
	for name, p := range rd.Properties {
		prop := Property{ID: p.ID, Type: p.Type, Format: p.NumberFormat}
		for _, o := range p.Options {
			prop.Options = append(prop.Options, Option{
				ID:    o.ID,
				Name:  o.Name,
				Color: o.Color,
				Group: o.Group,
			})
		}
		out.Properties[name] = prop
	}
	return out
}

// JoinOptionKeys attaches to the options of `actual` the key `desired` gives
// them, joining on the NAME.
//
// It is a bootstrap operation, valid only at the moment of import: the actual
// state only knows ids and names, and the key has to be hooked somewhere a
// first time. After that, the id holds the identity and the name can move
// freely — that is exactly what makes a rename detectable.
//
// An option with no name match stays without a key, which is the exact truth:
// notion-seed does not know which one it is. The second return value counts
// them, so import can say right away what the YAML does not cover.
func JoinOptionKeys(actual, desired Database) (Database, int) {
	out := Database{
		ID:           actual.ID,
		DataSourceID: actual.DataSourceID,
		Name:         actual.Name,
		Description:  actual.Description,
		Icon:         actual.Icon,
		Properties:   make(map[string]Property, len(actual.Properties)),
	}

	orphans := 0
	for name, p := range actual.Properties {
		keyByName := make(map[string]string)
		for _, o := range desired.Properties[name].Options {
			if o.Key != "" {
				keyByName[o.Name] = o.Key
			}
		}

		prop := Property{ID: p.ID, Type: p.Type, Format: p.Format}
		for _, o := range p.Options {
			joined := o
			joined.Key = keyByName[o.Name]
			if joined.Key == "" {
				orphans++
			}
			prop.Options = append(prop.Options, joined)
		}
		out.Properties[name] = prop
	}
	return out, orphans
}
