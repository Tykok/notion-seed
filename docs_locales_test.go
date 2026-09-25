// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"path/filepath"
	"slices"
	"testing"
)

// The language switcher sends /x to /fr/x. A page without its French twin
// would be a 404 that the dead-link check never sees, since no link in the
// markdown points to it.
func TestEveryDocsPageHasAFrenchCounterpart(t *testing.T) {
	names := func(pattern string) []string {
		paths, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		for i, p := range paths {
			paths[i] = filepath.Base(p)
		}
		slices.Sort(paths)
		return paths
	}

	en, fr := names("docs/*.md"), names("docs/fr/*.md")
	if len(en) == 0 {
		t.Fatal("no page under docs/")
	}
	if !slices.Equal(en, fr) {
		t.Errorf("docs/ has %v, docs/fr/ has %v: every page needs a twin with the same name", en, fr)
	}
}
