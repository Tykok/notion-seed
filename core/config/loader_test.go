package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeConfig monte une arborescence de config dans un dossier temporaire.
// Les clés sont des chemins relatifs, les valeurs le contenu YAML.
func writeConfig(t *testing.T, files map[string]string) string {
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

const workspaceYAML = `
version: 1
workspace:
  parent_page_id: "44444444-4444-4444-8444-444444444444"
lifecycle:
  prevent_destroy: [projects]
  allow_data_loss: []
`

func TestLoadMergesMultipleFiles(t *testing.T) {
	dir := writeConfig(t, map[string]string{
		"workspace.yaml": workspaceYAML,
		"databases/projects.yaml": `
databases:
  - key: projects
    name: "Projects"
    properties:
      Name:
        type: title
`,
		"databases/tasks.yaml": `
databases:
  - key: tasks
    name: "Tasks"
    properties:
      Name:
        type: title
`,
	})

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Workspace.ParentPageID != "44444444-4444-4444-8444-444444444444" {
		t.Errorf("ParentPageID = %q", cfg.Workspace.ParentPageID)
	}
	if len(cfg.Databases) != 2 {
		t.Fatalf("databases = %d, want 2", len(cfg.Databases))
	}
	// lifecycle vient de workspace.yaml comme le reste de la config globale.
	// Sans cette assertion, supprimer la fusion de lifecycle ne casserait aucun
	// test, alors que prevent_destroy est un garde-fou de sécurité.
	if len(cfg.Lifecycle.PreventDestroy) != 1 || cfg.Lifecycle.PreventDestroy[0] != "projects" {
		t.Errorf("Lifecycle.PreventDestroy = %v, want [projects]", cfg.Lifecycle.PreventDestroy)
	}
	// Ordre déterministe : par key triée. Il n'y a pas de graphe au MVP 0,
	// mais la sortie de plan doit être stable entre deux runs.
	if cfg.Databases[0].Key != "projects" || cfg.Databases[1].Key != "tasks" {
		t.Errorf("ordre = %q, %q ; want projects, tasks",
			cfg.Databases[0].Key, cfg.Databases[1].Key)
	}
}

// Le test central de cette tâche : deux fichiers valides séparément, en
// collision une fois fusionnés.
func TestLoadRejectsDuplicateKeyAcrossFiles(t *testing.T) {
	dir := writeConfig(t, map[string]string{
		"workspace.yaml": workspaceYAML,
		"databases/a.yaml": `
databases:
  - key: projects
    name: "Projects A"
    properties:
      Name:
        type: title
`,
		"databases/b.yaml": `
databases:
  - key: projects
    name: "Projects B"
    properties:
      Name:
        type: title
`,
	})

	_, err := Load(dir)
	var dup *DuplicateKeyError
	if !errors.As(err, &dup) {
		t.Fatalf("error = %v (%T), want *DuplicateKeyError", err, err)
	}
	if dup.Key != "projects" {
		t.Errorf("Key = %q, want %q", dup.Key, "projects")
	}
	msg := err.Error()
	// Le message doit nommer les DEUX fichiers, sinon l'utilisateur doit
	// chercher lui-même le doublon.
	for _, want := range []string{"a.yaml", "b.yaml", "projects"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message = %q, il doit contenir %q", msg, want)
		}
	}
}

func TestLoadReportsWhichFileFailedValidation(t *testing.T) {
	dir := writeConfig(t, map[string]string{
		"workspace.yaml": workspaceYAML,
		"databases/broken.yaml": `
databases:
  - name: "Broken"
    properties:
      Score:
        type: formula
`,
	})

	_, err := Load(dir)
	if err == nil {
		t.Fatal("Load() error = nil, want une erreur de validation")
	}
	if !strings.Contains(err.Error(), "broken.yaml") {
		t.Errorf("message = %q, il doit nommer le fichier fautif", err.Error())
	}
}

func TestLoadIgnoresNonYAMLFiles(t *testing.T) {
	dir := writeConfig(t, map[string]string{
		"workspace.yaml":          workspaceYAML,
		"databases/README.md":     "# notes",
		"databases/.hidden.yaml":  "databases: [not: valid",
		"databases/projects.yaml": "databases:\n  - key: projects\n    name: \"P\"\n    properties:\n      Name:\n        type: title\n",
	})

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.Databases) != 1 {
		t.Errorf("databases = %d, want 1", len(cfg.Databases))
	}
}

func TestLoadRequiresVersionInWorkspaceFile(t *testing.T) {
	dir := writeConfig(t, map[string]string{
		"workspace.yaml":          "workspace:\n  parent_page_id: \"abc\"\n",
		"databases/projects.yaml": "databases:\n  - key: projects\n    name: \"P\"\n    properties:\n      Name:\n        type: title\n",
	})

	_, err := Load(dir)
	if err == nil {
		t.Fatal("Load() error = nil, want une erreur sur version manquante")
	}
	for _, want := range []string{"version", "workspace.yaml"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message = %q, il doit contenir %q", err.Error(), want)
		}
	}
	// Ne pas exposer "trouvé 0" : c'est un détail d'implémentation (zéro Go), pas ce qu'a écrit l'utilisateur.
	if strings.Contains(err.Error(), "trouvé 0") {
		t.Errorf("message = %q, ne doit pas rapporter la version absente comme « trouvé 0 »", err.Error())
	}
}

func TestLoadRejectsWrongVersionWithADifferentHint(t *testing.T) {
	dir := writeConfig(t, map[string]string{
		"workspace.yaml": "version: 2\nworkspace:\n  parent_page_id: \"abc\"\n",
	})

	_, err := Load(dir)
	if err == nil {
		t.Fatal("Load() error = nil, want un rejet de version: 2")
	}
	msg := err.Error()
	if !strings.Contains(msg, "remplacez") {
		t.Errorf("message = %q : quand le champ est présent mais faux, le conseil doit dire de le REMPLACER", msg)
	}
	if strings.Contains(msg, "ajoutez") {
		t.Errorf("message = %q : « ajoutez » envoie chercher un champ déjà présent", msg)
	}
}

func TestLoadRequiresWorkspaceFile(t *testing.T) {
	dir := writeConfig(t, map[string]string{
		"databases/projects.yaml": "databases:\n  - key: projects\n    name: \"P\"\n    properties:\n      Name:\n        type: title\n",
	})

	_, err := Load(dir)
	if err == nil {
		t.Fatal("Load() error = nil, want une erreur sur workspace.yaml manquant")
	}
	if !strings.Contains(err.Error(), "workspace.yaml") {
		t.Errorf("message = %q, il doit nommer workspace.yaml", err.Error())
	}
}

func TestLoadDerivesKeysAndResolvesCollisions(t *testing.T) {
	dir := writeConfig(t, map[string]string{
		"workspace.yaml": workspaceYAML,
		"databases/a.yaml": `
databases:
  - name: "Tasks"
    properties:
      Name:
        type: title
  - name: "Tasks"
    properties:
      Name:
        type: title
`,
	})

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	keys := []string{cfg.Databases[0].Key, cfg.Databases[1].Key}
	if keys[0] == keys[1] {
		t.Fatalf("les deux key sont identiques: %q", keys[0])
	}
	for _, k := range keys {
		if k == "" {
			t.Error("une key est restée vide après résolution")
		}
	}
}

func TestLoadRecordsSourceFile(t *testing.T) {
	dir := writeConfig(t, map[string]string{
		"workspace.yaml": workspaceYAML,
		"databases/projects.yaml": `
databases:
  - key: projects
    name: "Projects"
    properties:
      Name:
        type: title
`,
	})

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !strings.HasSuffix(cfg.Databases[0].SourceFile, "databases/projects.yaml") {
		t.Errorf("SourceFile = %q", cfg.Databases[0].SourceFile)
	}
}

// Démontré avant correction : `databases/z.yaml` déclarant
// `workspace.parent_page_id: "HIJACKED"` produisait un GET /v1/pages/HIJACKED
// alors que workspace.yaml disait autre chose, sans un avertissement. Le fichier
// de databases/ gagne toujours, puisque workspace.yaml est fusionné en premier.
func TestLoadRejectsGlobalSectionsInDatabaseFiles(t *testing.T) {
	tests := []struct {
		name    string
		content string
		section string
	}{
		{
			"workspace détourne la cible d'écriture",
			"workspace:\n  parent_page_id: \"00000000-0000-0000-0000-000000000000\"\n" +
				"databases:\n  - key: z\n    name: \"Z\"\n    properties:\n      Name:\n        type: title\n",
			"workspace",
		},
		{
			"lifecycle efface le garde-fou",
			"lifecycle:\n  prevent_destroy: []\n" +
				"databases:\n  - key: z\n    name: \"Z\"\n    properties:\n      Name:\n        type: title\n",
			"lifecycle",
		},
		{
			"version hors de workspace.yaml",
			"version: 2\n" +
				"databases:\n  - key: z\n    name: \"Z\"\n    properties:\n      Name:\n        type: title\n",
			"version",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := writeConfig(t, map[string]string{
				"workspace.yaml":   workspaceYAML,
				"databases/z.yaml": tt.content,
			})

			_, err := Load(dir)
			if err == nil {
				t.Fatalf("Load() error = nil, want un rejet de la section %q", tt.section)
			}
			// Le fichier ET la section fautive doivent être nommés, sinon
			// l'utilisateur ne sait pas lequel de ses fichiers a gagné la fusion.
			for _, want := range []string{"z.yaml", tt.section, WorkspaceFile} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("message = %q, il doit contenir %q", err.Error(), want)
				}
			}
		})
	}
}

// Le garde-fou ne doit pas se retourner contre workspace.yaml lui-même, qui
// porte légitimement les trois sections.
func TestLoadStillAcceptsGlobalSectionsInWorkspaceFile(t *testing.T) {
	dir := writeConfig(t, map[string]string{
		"workspace.yaml": workspaceYAML,
		"databases/z.yaml": "databases:\n  - key: z\n    name: \"Z\"\n" +
			"    properties:\n      Name:\n        type: title\n",
	})

	if _, err := Load(dir); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
}

// Démontré avant correction : une database avec deux propriétés `title` et une
// avec zéro produisaient toutes deux `+ create` et un exit 0. L'API n'accepte
// qu'exactement une propriété title par data source, donc plan annonçait quelque
// chose qui ne peut pas se produire.
func TestLoadRejectsDatabasesWithoutExactlyOneTitle(t *testing.T) {
	tests := []struct {
		name       string
		properties string
		wantInMsg  []string
	}{
		{
			"deux title",
			"      Name:\n        type: title\n      Autre:\n        type: title\n",
			[]string{"z.yaml", `"Autre"`, `"Name"`, "2"},
		},
		{
			"zéro title",
			"      Estimate:\n        type: number\n",
			[]string{"z.yaml", "title"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := writeConfig(t, map[string]string{
				"workspace.yaml": workspaceYAML,
				"databases/z.yaml": "databases:\n  - key: z\n    name: \"Z\"\n    properties:\n" +
					tt.properties,
			})

			_, err := Load(dir)
			if err == nil {
				t.Fatal("Load() error = nil, want un rejet : l'API exige exactement une propriété title")
			}
			for _, want := range tt.wantInMsg {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("message = %q, il doit contenir %q", err.Error(), want)
				}
			}
		})
	}
}

// parent_page_id porte un motif UUID : un id en forme de chemin relatif
// produirait une URL qui vise un autre endpoint, et l'erreur de l'API sur un id
// mal formé est moins claire que celle-ci.
func TestLoadRejectsMalformedParentPageID(t *testing.T) {
	for _, bad := range []string{"page1", "../../v1/users", "3cdf830d"} {
		dir := writeConfig(t, map[string]string{
			"workspace.yaml": "version: 1\nworkspace:\n  parent_page_id: \"" + bad + "\"\n",
		})

		_, err := Load(dir)
		if err == nil {
			t.Fatalf("Load() error = nil pour parent_page_id = %q", bad)
		}
		if !strings.Contains(err.Error(), "UUID") {
			t.Errorf("message = %q pour %q, il doit dire où trouver l'UUID", err.Error(), bad)
		}
	}
}

// Un parent_page_id sans tirets est accepté : l'API Notion le tolère, et les
// URL de Notion le rendent sous cette forme.
func TestLoadAcceptsParentPageIDWithoutHyphens(t *testing.T) {
	dir := writeConfig(t, map[string]string{
		"workspace.yaml": "version: 1\nworkspace:\n  parent_page_id: \"3cdf830dbf9f81618d11c08cacf79fa2\"\n",
	})

	if _, err := Load(dir); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
}
