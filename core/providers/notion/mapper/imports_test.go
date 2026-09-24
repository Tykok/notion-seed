// SPDX-License-Identifier: GPL-3.0-or-later

package mapper

import (
	"go/parser"
	"go/token"
	"io/fs"
	"strings"
	"testing"
)

// TestMapperDoesNotImportConfig verrouille l'invariant central de l'écriture :
// le payload envoyé à l'API ne se construit QUE depuis le type pivot, donc
// depuis la cible que le plan a affichée. Un import de core/config rouvrirait
// un second chemin de la configuration vers l'API, capable de diverger du plan
// sans qu'aucun test ne s'en aperçoive — c'est exactement ce qui a produit la
// substitution silencieuse du groupe "To-do".
//
// Le test porte sur les fichiers de production seuls : un test a le droit de
// construire une config pour vérifier autre chose.
func TestMapperDoesNotImportConfig(t *testing.T) {
	const forbidden = "github.com/tykok/notion-seed/core/config"

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("lecture du paquet: %v", err)
	}
	if len(pkgs) == 0 {
		t.Fatal("aucun fichier de production lu : le test ne vérifierait rien")
	}
	for _, pkg := range pkgs {
		for path, file := range pkg.Files {
			for _, imp := range file.Imports {
				if strings.Trim(imp.Path.Value, `"`) == forbidden {
					t.Errorf("%s importe %s : le payload doit se construire depuis "+
						"state.Database, la cible que le plan a affichée", path, forbidden)
				}
			}
		}
	}
}
