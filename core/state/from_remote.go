// SPDX-License-Identifier: GPL-3.0-or-later

package state

import "github.com/tykok/notion-seed/core/providers/notion/resources"

// FromRemote projette l'état lu dans l'API dans le type pivot.
//
// Le résultat ne porte aucune key : l'API ne connaît pas cette notion. C'est la
// voie « actual » du diff à trois voies.
//
// Icon reste vide : le décodeur ne lit pas l'icône de la réponse. Tant que
// c'est le cas, l'icône ne doit être comparée NULLE PART — la config la porte,
// le réel jamais, donc toute comparaison produirait un changement fantôme à
// chaque run. Le jour où le décodeur la lira, elle deviendra comparable sans
// autre changement ici.
func FromRemote(rd resources.RemoteDatabase) Database {
	out := Database{
		ID:           rd.ID,
		DataSourceID: rd.DataSourceID,
		Name:         rd.Name,
		Description:  rd.Description,
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

// JoinOptionKeys attache aux options de `actual` la key que `desired` leur
// donne, en joignant sur le NOM.
//
// C'est une opération d'amorçage, valable au seul instant de l'import : le réel
// ne connaît que des ids et des noms, et il faut bien accrocher la key quelque
// part une première fois. Ensuite, l'id porte l'identité et le nom peut bouger
// librement — c'est exactement ce qui rend un renommage détectable.
//
// Une option sans correspondance de nom reste sans key, ce qui est l'exacte
// vérité : on ne sait pas laquelle c'est. Le second retour les compte, pour que
// l'import puisse dire tout de suite ce que le YAML ne couvre pas.
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
