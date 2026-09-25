// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// TestIntegrationPlanAgainstRealWorkspace runs against the real ntn and the
// real workspace. Never in the default loop: it requires authentication and a
// real parent page.
//
//	NOTION_SEED_IT_PARENT_PAGE_ID=<id> go test ./cli/ -run TestIntegration -v
func TestIntegrationPlanAgainstRealWorkspace(t *testing.T) {
	pageID := os.Getenv("NOTION_SEED_IT_PARENT_PAGE_ID")
	if pageID == "" {
		t.Skip("NOTION_SEED_IT_PARENT_PAGE_ID not set")
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
		t.Errorf("unexpected output:\n%s", got)
	}
	// The output must name the workspace: it is the safeguard against
	// operating on the wrong one.
	if !strings.Contains(got, "workspace ") {
		t.Errorf("the output does not name the workspace:\n%s", got)
	}
}
