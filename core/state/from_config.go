// SPDX-License-Identifier: GPL-3.0-or-later

package state

import "github.com/tykok/notion-seed/core/config"

// FromConfig projects a declared database into the pivot type.
//
// The result carries no id: the configuration only knows keys. It is the
// "desired" side of the three-way diff.
func FromConfig(db config.Database) Database {
	out := Database{
		Name:        db.Name,
		Description: db.Description,
		Icon:        db.Icon,
		Properties:  make(map[string]Property, len(db.Properties)),
	}
	for name, p := range db.Properties {
		prop := Property{Type: p.Type, Format: p.Format}
		for _, o := range p.Options {
			prop.Options = append(prop.Options, Option{
				Key:   o.Key,
				Name:  o.Name,
				Color: o.Color,
				Group: o.Group,
			})
		}
		out.Properties[name] = prop
	}
	return out
}
