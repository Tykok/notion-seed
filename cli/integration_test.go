// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// TestIntegrationPlanAgainstRealWorkspace tourne contre le vrai ntn et le vrai
// workspace. Jamais dans la boucle par défaut : il exige une authentification
// et une page parente réelle.
//
//	NOTION_SEED_IT_PARENT_PAGE_ID=<id> go test ./cli/ -run TestIntegration -v
func TestIntegrationPlanAgainstRealWorkspace(t *testing.T) {
	pageID := os.Getenv("NOTION_SEED_IT_PARENT_PAGE_ID")
	if pageID == "" {
		t.Skip("NOTION_SEED_IT_PARENT_PAGE_ID non défini")
	}

	dir := writeConfigDir(t, map[string]string{
		"workspace.yaml":     "version: 1\nworkspace:\n  parent_page_id: \"" + pageID + "\"\n",
		"databases/all.yaml": twoDatabases,
	})

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"plan", "--dir", dir})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v\n%s", err, out.String())
	}
	got := out.String()
	if !strings.Contains(got, "Plan: 2 to add") {
		t.Errorf("sortie inattendue:\n%s", got)
	}
	// La sortie doit nommer le workspace : c'est le garde-fou qui évite
	// d'opérer sur le mauvais.
	if !strings.Contains(got, "workspace ") {
		t.Errorf("la sortie ne nomme pas le workspace:\n%s", got)
	}
}
