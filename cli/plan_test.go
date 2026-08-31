package cli

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfigDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const twoDatabases = `
databases:
  - key: projects
    name: "Projects"
    properties:
      Name:
        type: title
  - key: tasks
    name: "Tasks"
    properties:
      Name:
        type: title
      Estimate:
        type: number
`

func TestPlanRendersCreationsForTwoDatabases(t *testing.T) {
	withFakeNtn(t, "ok")
	dir := writeConfigDir(t, map[string]string{
		"workspace.yaml":     "version: 1\nworkspace:\n  parent_page_id: \"page1\"\n",
		"databases/all.yaml": twoDatabases,
	})

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"plan", "--dir", dir, "--skip-preflight"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v\n%s", err, out.String())
	}
	got := out.String()
	for _, want := range []string{
		"Plan: 2 to add",
		"+ database.projects (new)",
		"+ database.tasks (new)",
		`property "Estimate" (number)`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("sortie ne contient pas %q\n--- sortie ---\n%s", want, got)
		}
	}
}

func TestPlanFailsOnDuplicateKeyNamingBothFiles(t *testing.T) {
	dir := writeConfigDir(t, map[string]string{
		"workspace.yaml":   "version: 1\nworkspace:\n  parent_page_id: \"page1\"\n",
		"databases/a.yaml": "databases:\n  - key: projects\n    name: \"A\"\n    properties:\n      Name:\n        type: title\n",
		"databases/b.yaml": "databases:\n  - key: projects\n    name: \"B\"\n    properties:\n      Name:\n        type: title\n",
	})

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"plan", "--dir", dir, "--skip-preflight"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil, want une erreur de key dupliquée")
	}
	for _, want := range []string{"a.yaml", "b.yaml", "projects"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message = %q, il doit contenir %q", err.Error(), want)
		}
	}
}

// plan n'écrit rien au MVP 0 : ni state, ni mutation. Le test garde cette
// propriété, qui est la promesse de la commande.
func TestPlanWritesNothingToDisk(t *testing.T) {
	withFakeNtn(t, "ok")
	dir := writeConfigDir(t, map[string]string{
		"workspace.yaml":     "version: 1\nworkspace:\n  parent_page_id: \"page1\"\n",
		"databases/all.yaml": twoDatabases,
	})

	before := snapshot(t, dir)

	cmd := NewRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"plan", "--dir", dir, "--skip-preflight"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	after := snapshot(t, dir)

	for path, sum := range before {
		got, ok := after[path]
		if !ok {
			t.Errorf("plan a supprimé %s", path)
			continue
		}
		if got != sum {
			t.Errorf("plan a modifié le contenu de %s", path)
		}
	}
	for path := range after {
		if _, ok := before[path]; !ok {
			t.Errorf("plan a créé %s", path)
		}
	}
}

// snapshot parcourt l'arborescence RÉCURSIVEMENT et retient une empreinte du
// contenu de chaque fichier. Compter les entrées à la racine ne verrait ni une
// écriture dans databases/, ni une mutation de contenu en place, ni un fichier
// créé puis supprimé — or « plan n'écrit rien » est la promesse centrale de la
// commande, elle mérite d'être épinglée pour de vrai.
func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if d.IsDir() {
			out[rel+"/"] = ""
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[rel] = fmt.Sprintf("%x", sha256.Sum256(b))
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot(%s): %v", root, err)
	}
	return out
}

func TestPlanRejectsNonPositiveRateAndBurst(t *testing.T) {
	dir := writeConfigDir(t, map[string]string{
		"workspace.yaml":     "version: 1\nworkspace:\n  parent_page_id: \"page1\"\n",
		"databases/all.yaml": twoDatabases,
	})
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"rate nul", []string{"--rate", "0"}, "--rate"},
		{"rate négatif", []string{"--rate", "-1"}, "--rate"},
		{"burst nul", []string{"--burst", "0"}, "--burst"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := NewRootCmd()
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&out)
			cmd.SetArgs(append([]string{"plan", "--dir", dir, "--skip-preflight"}, tt.args...))

			err := cmd.Execute()
			if err == nil {
				t.Fatalf("Execute() error = nil, want un rejet de %v", tt.args)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("message = %q, il doit nommer %q", err.Error(), tt.want)
			}
		})
	}
}

// ATTENTION, vérité temporaire : cette égalité ne tient qu'au MVP 0, parce que
// plan n'écrit pas encore de state. Au MVP 1 plan divergera de diff par
// construction — c'est la raison même de garder deux commandes. Ce test devra
// alors être desserré ou remplacé, pas « réparé ».
func TestDiffProducesSameOutputAsPlan(t *testing.T) {
	withFakeNtn(t, "ok")
	dir := writeConfigDir(t, map[string]string{
		"workspace.yaml":     "version: 1\nworkspace:\n  parent_page_id: \"page1\"\n",
		"databases/all.yaml": twoDatabases,
	})

	run := func(sub string) string {
		cmd := NewRootCmd()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetArgs([]string{sub, "--dir", dir, "--skip-preflight"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("%s: Execute() error = %v", sub, err)
		}
		return out.String()
	}
	if run("plan") != run("diff") {
		t.Error("plan et diff doivent produire la même sortie au MVP 0")
	}
}
