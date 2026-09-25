// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tykok/notion-seed/core/state"
)

// Review Focus 2: the user has a Notion URL at hand, not a UUID.
func TestParseNotionID(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"1b2c3d4e-5f60-4a1b-8c2d-3e4f5a6b7c8d", "1b2c3d4e-5f60-4a1b-8c2d-3e4f5a6b7c8d"},
		{"1b2c3d4e5f604a1b8c2d3e4f5a6b7c8d", "1b2c3d4e-5f60-4a1b-8c2d-3e4f5a6b7c8d"},
		{"https://www.notion.so/space/Tasks-1b2c3d4e5f604a1b8c2d3e4f5a6b7c8d?v=abc",
			"1b2c3d4e-5f60-4a1b-8c2d-3e4f5a6b7c8d"},
		// The case that matters: a real database URL carries a VIEW id in the
		// query, also 32 hex digits. Taking it would adopt the view.
		{"https://www.notion.so/space/Tasks-1b2c3d4e5f604a1b8c2d3e4f5a6b7c8d?v=99887766554433221100ffeeddccbbaa&pvs=4",
			"1b2c3d4e-5f60-4a1b-8c2d-3e4f5a6b7c8d"},
		{"https://www.notion.so/1b2c3d4e5f604a1b8c2d3e4f5a6b7c8d",
			"1b2c3d4e-5f60-4a1b-8c2d-3e4f5a6b7c8d"},
		// Review round 1: "copy link to block" adds a `#<block id>` fragment,
		// also 32 hex digits, AFTER the database id in the path. Without cutting
		// the fragment, it is what the last hex group picks up — the same class
		// of trap as `?v=`.
		{"https://www.notion.so/space/Tasks-1b2c3d4e5f604a1b8c2d3e4f5a6b7c8d#99887766554433221100ffeeddccbbaa",
			"1b2c3d4e-5f60-4a1b-8c2d-3e4f5a6b7c8d"},
		// An uppercase id, as Notion may return it: the code already handles it
		// (dashed() forces ToLower), but nothing pinned it.
		{"1B2C3D4E-5F60-4A1B-8C2D-3E4F5A6B7C8D", "1b2c3d4e-5f60-4a1b-8c2d-3e4f5a6b7c8d"},
	}
	for _, tc := range cases {
		got, err := parseNotionID(tc.in)
		if err != nil {
			t.Errorf("parseNotionID(%q) error = %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseNotionID(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestParseNotionIDRejectsGarbage(t *testing.T) {
	if _, err := parseNotionID("not-an-id"); err == nil {
		t.Fatal("an unreadable identifier must be rejected")
	}
}

func writeImportFixture(t *testing.T) string {
	t.Helper()
	return writeConfigDir(t, map[string]string{
		"workspace.yaml": `
version: 1
workspace:
  parent_page_id: 33333333-3333-4333-8333-333333333333
`,
		"databases/tasks.yaml": `
databases:
  - key: tasks
    name: Tasks
    properties:
      Name:
        type: title
      Statut:
        type: status
        options:
          - key: todo
            name: À faire
            group: To-do
`,
	})
}

func TestImportWritesStateAndJoinsKeys(t *testing.T) {
	withFakeNtn(t, "authenticated_database")
	dir := writeImportFixture(t)

	out, err := runCmd(t, "import", "database.tasks",
		"1b2c3d4e-5f60-4a1b-8c2d-3e4f5a6b7c8d", "--dir", dir)
	if err != nil {
		t.Fatalf("import error = %v\n%s", err, out)
	}

	snap, err := state.Load(dir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	db, ok := snap.Databases["tasks"]
	if !ok {
		t.Fatal("database tasks missing from the state")
	}
	if db.ID != "db-1" || db.DataSourceID != "ds-1" {
		t.Errorf("ids = %q / %q, want db-1 / ds-1", db.ID, db.DataSourceID)
	}
	if snap.WorkspaceID != "33333333-3333-4333-8333-333333333333" {
		t.Errorf("WorkspaceID = %q", snap.WorkspaceID)
	}

	var todo, fait state.Option
	for _, o := range db.Properties["Statut"].Options {
		switch o.Name {
		case "À faire":
			todo = o
		case "Fait":
			fait = o
		}
	}
	if todo.Key != "todo" {
		t.Errorf("the key must be joined on the name: %+v", todo)
	}
	if fait.Key != "" {
		t.Errorf("without a declaration, no invented key: %+v", fait)
	}
	if !strings.Contains(out, "1 option") {
		t.Errorf("the output must count the options without a key:\n%s", out)
	}
}

func TestImportRefusesUnknownKey(t *testing.T) {
	withFakeNtn(t, "authenticated_database")
	dir := writeImportFixture(t)

	out, err := runCmd(t, "import", "database.inconnue",
		"1b2c3d4e-5f60-4a1b-8c2d-3e4f5a6b7c8d", "--dir", dir)
	if err == nil {
		t.Fatalf("an undeclared key must be rejected\n%s", out)
	}
	if !strings.Contains(err.Error(), "tasks") {
		t.Errorf("the message must name the declared keys: %v", err)
	}
}

func TestImportRefusesAlreadyImportedKey(t *testing.T) {
	withFakeNtn(t, "authenticated_database")
	dir := writeImportFixture(t)
	id := "1b2c3d4e-5f60-4a1b-8c2d-3e4f5a6b7c8d"

	if out, err := runCmd(t, "import", "database.tasks", id, "--dir", dir); err != nil {
		t.Fatalf("first import: %v\n%s", err, out)
	}
	_, err := runCmd(t, "import", "database.tasks", id, "--dir", dir)
	if err == nil {
		t.Fatal("a re-import must be rejected")
	}
	if !strings.Contains(err.Error(), state.FileName) {
		t.Errorf("the message must say what to remove: %v", err)
	}
}

func TestImportRefusesForeignWorkspaceState(t *testing.T) {
	withFakeNtn(t, "authenticated_database")
	dir := writeImportFixture(t)
	if err := os.WriteFile(filepath.Join(dir, state.FileName),
		[]byte(`{"version":1,"workspace_id":"44444444-4444-4444-8444-444444444444","databases":{}}`),
		0o644); err != nil {
		t.Fatal(err)
	}

	_, err := runCmd(t, "import", "database.tasks",
		"1b2c3d4e-5f60-4a1b-8c2d-3e4f5a6b7c8d", "--dir", dir)
	if err == nil {
		t.Fatal("a state from another workspace must be rejected")
	}
	if !strings.Contains(err.Error(), "44444444") {
		t.Errorf("the message must name the state's workspace: %v", err)
	}
}

func TestImportRefusesNonDatabaseAddress(t *testing.T) {
	dir := writeImportFixture(t)
	_, err := runCmd(t, "import", "page.accueil",
		"1b2c3d4e-5f60-4a1b-8c2d-3e4f5a6b7c8d", "--dir", dir)
	if err == nil {
		t.Fatal("only databases can be imported in MVP 0")
	}
	if !strings.Contains(err.Error(), "database.") {
		t.Errorf("the message must show the expected form: %v", err)
	}
}

// Review round 1: the only one of the five import refusals without coverage.
// It protects against recording in the state an identity that points to a
// resource in the trash, in the only command that writes this file.
func TestImportRefusesArchivedDatabase(t *testing.T) {
	withFakeNtn(t, "archived_database")
	dir := writeImportFixture(t)

	out, err := runCmd(t, "import", "database.tasks",
		"1b2c3d4e-5f60-4a1b-8c2d-3e4f5a6b7c8d", "--dir", dir)
	if err == nil {
		t.Fatalf("an archived database must be rejected\n%s", out)
	}
	if !strings.Contains(err.Error(), "archived") {
		t.Errorf("the message must say the database is archived: %v", err)
	}

	snap, err := state.Load(dir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if _, ok := snap.Databases["tasks"]; ok {
		t.Error("an archived database must not be recorded in the state")
	}
}

// Review round 1, minor point: the --skip-preflight refusal had no test, while
// a refactor could regress it to "hidden but accepted", which would suggest an
// offline import that actually called the API.
func TestImportRefusesSkipPreflight(t *testing.T) {
	dir := writeImportFixture(t)

	out, err := runCmd(t, "import", "database.tasks",
		"1b2c3d4e-5f60-4a1b-8c2d-3e4f5a6b7c8d", "--dir", dir, "--skip-preflight")
	if err == nil {
		t.Fatalf("--skip-preflight must be rejected by import\n%s", out)
	}
	if !strings.Contains(err.Error(), "skip-preflight") {
		t.Errorf("the message must name the rejected flag: %v", err)
	}
}

// import shares planOptions with plan, but it computes no plan: it has no class
// to check against --fail-on. The flag must therefore be rejected, for the same
// reason as --skip-preflight — accepting it then ignoring it would make a CI
// believe it is protected on the command that writes the state.
func TestImportRefusesFailOn(t *testing.T) {
	dir := writeImportFixture(t)

	out, err := runCmd(t, "import", "database.tasks",
		"1b2c3d4e-5f60-4a1b-8c2d-3e4f5a6b7c8d", "--dir", dir, "--fail-on=destructive")
	if err == nil {
		t.Fatalf("--fail-on must be rejected by import\n%s", out)
	}
	if !strings.Contains(err.Error(), "fail-on") {
		t.Errorf("the message must name the rejected flag: %v", err)
	}
	// The corrective action must say where the flag actually applies.
	if !strings.Contains(err.Error(), "  → ") {
		t.Errorf("the message must carry a corrective action: %v", err)
	}
}

// import loads the config too: a deprecated lifecycle key warns there as well.
func TestImportWarnsOnDeprecatedLifecycleKeys(t *testing.T) {
	withFakeNtn(t, "authenticated_database")
	dir := writeImportFixture(t)
	ws := filepath.Join(dir, "workspace.yaml")
	raw, err := os.ReadFile(ws)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ws, append(raw, []byte("lifecycle:\n  allow_data_loss: [database.tasks]\n")...), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runCmd(t, "import", "database.tasks",
		"1b2c3d4e-5f60-4a1b-8c2d-3e4f5a6b7c8d", "--dir", dir)
	if err != nil {
		t.Fatalf("import error = %v\n%s", err, out)
	}
	if !strings.Contains(out, "warning: "+ws+": `lifecycle.allow_data_loss` is deprecated") ||
		!strings.Contains(out, "  → rename `allow_data_loss` to `acknowledge_data_loss` in "+ws) {
		t.Errorf("output:\n%s\nwant the deprecation warning", out)
	}
}
