// SPDX-License-Identifier: GPL-3.0-or-later

// Package diff calcule le plan de changements et le rend en texte.
package diff

import "github.com/tykok/notion-seed/core/change"

// La classification vit dans core/change, paquet feuille partagé avec
// resources. Ces alias gardent `diff.ClassSafe` et consorts valides : les
// appelants n'ont pas à savoir où le type a déménagé.
type Class = change.Class

const (
	ClassSafe          = change.ClassSafe
	ClassMigration     = change.ClassMigration
	ClassDestructive   = change.ClassDestructive
	ClassSilentRewrite = change.ClassSilentRewrite
)

// ClassifyOptionRemoval donne la classe du retrait d'une option, selon le type
// de la propriété.
func ClassifyOptionRemoval(propertyType string) Class {
	return change.ClassifyOptionRemoval(propertyType)
}
