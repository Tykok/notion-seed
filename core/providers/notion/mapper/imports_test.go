// SPDX-License-Identifier: GPL-3.0-or-later

package mapper

import (
	"go/parser"
	"go/token"
	"io/fs"
	"strings"
	"testing"
)

// TestMapperDoesNotImportConfig locks the central invariant of writing: the
// payload sent to the API is built ONLY from the pivot type, hence from the
// target the plan showed. An import of core/config would reopen a second path
// from the configuration to the API, able to diverge from the plan without
// any test noticing — that is exactly what produced the silent "To-do" group
// substitution.
//
// The test covers production files only: a test is allowed to build a config
// to check something else.
func TestMapperDoesNotImportConfig(t *testing.T) {
	const forbidden = "github.com/tykok/notion-seed/core/config"

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("failed to read the package: %v", err)
	}
	if len(pkgs) == 0 {
		t.Fatal("no production file read: the test would check nothing")
	}
	for _, pkg := range pkgs {
		for path, file := range pkg.Files {
			for _, imp := range file.Imports {
				if strings.Trim(imp.Path.Value, `"`) == forbidden {
					t.Errorf("%s imports %s: the payload must be built from "+
						"state.Database, the target the plan showed", path, forbidden)
				}
			}
		}
	}
}
