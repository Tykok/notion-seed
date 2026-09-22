// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tykok/notion-seed/core/state"
)

// Review Focus 2 : l'utilisateur a une URL Notion sous la main, pas un UUID.
func TestParseNotionID(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"1b2c3d4e-5f60-4a1b-8c2d-3e4f5a6b7c8d", "1b2c3d4e-5f60-4a1b-8c2d-3e4f5a6b7c8d"},
		{"1b2c3d4e5f604a1b8c2d3e4f5a6b7c8d", "1b2c3d4e-5f60-4a1b-8c2d-3e4f5a6b7c8d"},
		{"https://www.notion.so/space/Tasks-1b2c3d4e5f604a1b8c2d3e4f5a6b7c8d?v=abc",
			"1b2c3d4e-5f60-4a1b-8c2d-3e4f5a6b7c8d"},
		// Le cas qui compte : une vraie URL de database porte un id de VUE en
		// query, lui aussi sur 32 hexadécimaux. Le prendre ferait adopter la vue.
		{"https://www.notion.so/space/Tasks-1b2c3d4e5f604a1b8c2d3e4f5a6b7c8d?v=99887766554433221100ffeeddccbbaa&pvs=4",
			"1b2c3d4e-5f60-4a1b-8c2d-3e4f5a6b7c8d"},
		{"https://www.notion.so/1b2c3d4e5f604a1b8c2d3e4f5a6b7c8d",
			"1b2c3d4e-5f60-4a1b-8c2d-3e4f5a6b7c8d"},
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
	if _, err := parseNotionID("pas-un-id"); err == nil {
		t.Fatal("un identifiant illisible doit être refusé")
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
		t.Fatal("database tasks absente du state")
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
		t.Errorf("la key doit être jointe sur le nom: %+v", todo)
	}
	if fait.Key != "" {
		t.Errorf("sans déclaration, pas de key inventée: %+v", fait)
	}
	if !strings.Contains(out, "1 option") {
		t.Errorf("la sortie doit compter les options sans key:\n%s", out)
	}
}

func TestImportRefusesUnknownKey(t *testing.T) {
	withFakeNtn(t, "authenticated_database")
	dir := writeImportFixture(t)

	out, err := runCmd(t, "import", "database.inconnue",
		"1b2c3d4e-5f60-4a1b-8c2d-3e4f5a6b7c8d", "--dir", dir)
	if err == nil {
		t.Fatalf("une key non déclarée doit être refusée\n%s", out)
	}
	if !strings.Contains(err.Error(), "tasks") {
		t.Errorf("le message doit nommer les keys déclarées: %v", err)
	}
}

func TestImportRefusesAlreadyImportedKey(t *testing.T) {
	withFakeNtn(t, "authenticated_database")
	dir := writeImportFixture(t)
	id := "1b2c3d4e-5f60-4a1b-8c2d-3e4f5a6b7c8d"

	if out, err := runCmd(t, "import", "database.tasks", id, "--dir", dir); err != nil {
		t.Fatalf("premier import: %v\n%s", err, out)
	}
	_, err := runCmd(t, "import", "database.tasks", id, "--dir", dir)
	if err == nil {
		t.Fatal("un ré-import doit être refusé")
	}
	if !strings.Contains(err.Error(), state.FileName) {
		t.Errorf("le message doit dire quoi retirer: %v", err)
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
		t.Fatal("un state d'un autre workspace doit être refusé")
	}
	if !strings.Contains(err.Error(), "44444444") {
		t.Errorf("le message doit nommer le workspace du state: %v", err)
	}
}

func TestImportRefusesNonDatabaseAddress(t *testing.T) {
	dir := writeImportFixture(t)
	_, err := runCmd(t, "import", "page.accueil",
		"1b2c3d4e-5f60-4a1b-8c2d-3e4f5a6b7c8d", "--dir", dir)
	if err == nil {
		t.Fatal("seules les databases sont importables au MVP 0")
	}
	if !strings.Contains(err.Error(), "database.") {
		t.Errorf("le message doit montrer la forme attendue: %v", err)
	}
}
