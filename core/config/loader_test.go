// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeConfig sets up a config tree in a temporary directory. The keys are
// relative paths, the values the YAML content.
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
  acknowledge_destroy: [projects]
  acknowledge_data_loss: []
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
	// lifecycle comes from workspace.yaml like the rest of the global config.
	// Without this assertion, removing the lifecycle merge would break no
	// test, even though acknowledge_destroy is what the plan annotates.
	if len(cfg.Lifecycle.AcknowledgeDestroy) != 1 || cfg.Lifecycle.AcknowledgeDestroy[0] != "projects" {
		t.Errorf("Lifecycle.AcknowledgeDestroy = %v, want [projects]", cfg.Lifecycle.AcknowledgeDestroy)
	}
	// The current names warn about nothing.
	if len(cfg.Warnings) != 0 {
		t.Errorf("Warnings = %q, want none with the current key names", cfg.Warnings)
	}
	// Deterministic order: by sorted key. There is no graph in MVP 0, but the
	// plan output must be stable between two runs.
	if cfg.Databases[0].Key != "projects" || cfg.Databases[1].Key != "tasks" {
		t.Errorf("order = %q, %q; want projects, tasks",
			cfg.Databases[0].Key, cfg.Databases[1].Key)
	}
}

// The central test of this task: two files valid separately, colliding once
// merged.
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
	// The message must name BOTH files, otherwise the user has to hunt for the
	// duplicate themselves.
	for _, want := range []string{"a.yaml", "b.yaml", "projects"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message = %q, it must contain %q", msg, want)
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
		t.Fatal("Load() error = nil, want a validation error")
	}
	if !strings.Contains(err.Error(), "broken.yaml") {
		t.Errorf("message = %q, it must name the offending file", err.Error())
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
		t.Fatal("Load() error = nil, want an error on missing version")
	}
	for _, want := range []string{"version", "workspace.yaml"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message = %q, it must contain %q", err.Error(), want)
		}
	}
	// Do not expose "found 0": it is an implementation detail (Go zero value), not what the user wrote.
	if strings.Contains(err.Error(), "found 0") {
		t.Errorf("message = %q, must not report the missing version as \"found 0\"", err.Error())
	}
}

func TestLoadRejectsWrongVersionWithADifferentHint(t *testing.T) {
	dir := writeConfig(t, map[string]string{
		"workspace.yaml": "version: 2\nworkspace:\n  parent_page_id: \"abc\"\n",
	})

	_, err := Load(dir)
	if err == nil {
		t.Fatal("Load() error = nil, want version: 2 rejected")
	}
	msg := err.Error()
	if !strings.Contains(msg, "replace") {
		t.Errorf("message = %q: when the field is present but wrong, the advice must say to REPLACE it", msg)
	}
	if strings.Contains(msg, "add `version") {
		t.Errorf("message = %q: \"add\" sends the user looking for a field that is already there", msg)
	}
}

func TestLoadRequiresWorkspaceFile(t *testing.T) {
	dir := writeConfig(t, map[string]string{
		"databases/projects.yaml": "databases:\n  - key: projects\n    name: \"P\"\n    properties:\n      Name:\n        type: title\n",
	})

	_, err := Load(dir)
	if err == nil {
		t.Fatal("Load() error = nil, want an error on missing workspace.yaml")
	}
	if !strings.Contains(err.Error(), "workspace.yaml") {
		t.Errorf("message = %q, it must name workspace.yaml", err.Error())
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
		t.Fatalf("both keys are identical: %q", keys[0])
	}
	for _, k := range keys {
		if k == "" {
			t.Error("a key stayed empty after resolution")
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

// Demonstrated before the fix: `databases/z.yaml` declaring
// `workspace.parent_page_id: "HIJACKED"` produced a GET /v1/pages/HIJACKED
// while workspace.yaml said something else, without a warning. The databases/
// file always wins, since workspace.yaml is merged first.
func TestLoadRejectsGlobalSectionsInDatabaseFiles(t *testing.T) {
	tests := []struct {
		name         string
		content      string
		section      string
		wantInMsg    []string
		wantNotInMsg []string
	}{
		{
			name: "workspace hijacks the write target",
			content: "workspace:\n  parent_page_id: \"00000000-0000-0000-0000-000000000000\"\n" +
				"databases:\n  - key: z\n    name: \"Z\"\n    properties:\n      Name:\n        type: title\n",
			section:   "workspace",
			wantInMsg: []string{"move", "hijacks the write target"},
			// Nothing to delete here: the section moves, it does not disappear.
			wantNotInMsg: []string{"delete"},
		},
		{
			name: "lifecycle replaces the acknowledgements",
			content: "lifecycle:\n  acknowledge_destroy: []\n" +
				"databases:\n  - key: z\n    name: \"Z\"\n    properties:\n      Name:\n        type: title\n",
			section:      "lifecycle",
			wantInMsg:    []string{"move", "silently replaces the acknowledgements"},
			wantNotInMsg: []string{"delete"},
		},
		{
			// Re-review leftover: a user following the "move" advice for a lone
			// `version` offense would copy a line already present in
			// workspace.yaml — the right action is to delete it, and the
			// justification about parent_page_id/lifecycle does not belong
			// there, since neither is involved here.
			name: "version outside workspace.yaml",
			content: "version: 2\n" +
				"databases:\n  - key: z\n    name: \"Z\"\n    properties:\n      Name:\n        type: title\n",
			section:   "version",
			wantInMsg: []string{"delete"},
			wantNotInMsg: []string{
				"move",
				"hijacks the write target",
				"silently replaces the acknowledgements",
			},
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
				t.Fatalf("Load() error = nil, want section %q rejected", tt.section)
			}
			// The file AND the offending section must be named, otherwise the
			// user does not know which of their files won the merge.
			for _, want := range []string{"z.yaml", tt.section, WorkspaceFile} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("message = %q, it must contain %q", err.Error(), want)
				}
			}
			for _, want := range tt.wantInMsg {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("message = %q, it must contain %q", err.Error(), want)
				}
			}
			for _, notWant := range tt.wantNotInMsg {
				if strings.Contains(err.Error(), notWant) {
					t.Errorf("message = %q, it must not contain %q", err.Error(), notWant)
				}
			}
		})
	}
}

// The rule must not turn against workspace.yaml itself, which
// legitimately holds all three sections.
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

// Demonstrated before the fix: a database with two `title` properties and one
// with zero both produced `+ create` and exit 0. The API accepts exactly one
// title property per data source, so plan announced something that cannot
// happen.
func TestLoadRejectsDatabasesWithoutExactlyOneTitle(t *testing.T) {
	tests := []struct {
		name       string
		properties string
		wantInMsg  []string
	}{
		{
			"two titles",
			"      Name:\n        type: title\n      Autre:\n        type: title\n",
			[]string{"z.yaml", `"Autre"`, `"Name"`, "2"},
		},
		{
			"zero titles",
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
				t.Fatal("Load() error = nil, want a rejection: the API requires exactly one title property")
			}
			for _, want := range tt.wantInMsg {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("message = %q, it must contain %q", err.Error(), want)
				}
			}
		})
	}
}

// Two options with the same key would designate the same remote option, whose
// id would go out twice; two options with the same name would send the second
// one out as a new option, rejected by the API AFTER the database is written.
// Both stop at load time, with the database, the property and the duplicate
// named.
func TestLoadRejectsDuplicateOptionsInAProperty(t *testing.T) {
	tests := []struct {
		name      string
		options   string
		wantInMsg []string
	}{
		{
			"same key",
			"          - key: haute\n            name: \"Haute\"\n" +
				"          - key: haute\n            name: \"Très haute\"\n",
			[]string{"z.yaml", `"Z"`, `"Prio"`, `key "haute"`, "  → "},
		},
		{
			"same name",
			"          - key: haute\n            name: \"Haute\"\n" +
				"          - key: autre\n            name: \"Haute\"\n",
			[]string{"z.yaml", `"Z"`, `"Prio"`, `name "Haute"`, "  → "},
		},
		{
			"same name, no key",
			"          - name: \"Haute\"\n          - name: \"Haute\"\n",
			[]string{`"Prio"`, `name "Haute"`, "  → "},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := writeConfig(t, map[string]string{
				"workspace.yaml": workspaceYAML,
				"databases/z.yaml": "databases:\n  - key: z\n    name: \"Z\"\n    properties:\n" +
					"      Name:\n        type: title\n" +
					"      Prio:\n        type: select\n        options:\n" + tt.options,
			})
			_, err := Load(dir)
			if err == nil {
				t.Fatal("Load() error = nil, want the duplicate option rejected")
			}
			for _, want := range tt.wantInMsg {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("message = %q, it must contain %q", err.Error(), want)
				}
			}
		})
	}
}

// The same option key in TWO properties is allowed: an option's identity is
// local to its property.
func TestLoadAcceptsTheSameOptionKeyInTwoProperties(t *testing.T) {
	dir := writeConfig(t, map[string]string{
		"workspace.yaml": workspaceYAML,
		"databases/z.yaml": "databases:\n  - key: z\n    name: \"Z\"\n    properties:\n" +
			"      Name:\n        type: title\n" +
			"      Prio:\n        type: select\n        options:\n" +
			"          - key: haute\n            name: \"Haute\"\n" +
			"      Tags:\n        type: multi_select\n        options:\n" +
			"          - key: haute\n            name: \"Haute\"\n",
	})
	if _, err := Load(dir); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
}

// parent_page_id carries a UUID pattern: an id shaped like a relative path
// would produce a URL targeting another endpoint, and the API's error on a
// malformed id is less clear than this one.
func TestLoadRejectsMalformedParentPageID(t *testing.T) {
	for _, bad := range []string{"page1", "../../v1/users", "3cdf830d"} {
		dir := writeConfig(t, map[string]string{
			"workspace.yaml": "version: 1\nworkspace:\n  parent_page_id: \"" + bad + "\"\n",
		})

		_, err := Load(dir)
		if err == nil {
			t.Fatalf("Load() error = nil for parent_page_id = %q", bad)
		}
		if !strings.Contains(err.Error(), "UUID") {
			t.Errorf("message = %q for %q, it must say where to find the UUID", err.Error(), bad)
		}
	}
}

// A parent_page_id without dashes is accepted: the Notion API tolerates it,
// and Notion URLs show it in that form.
func TestLoadAcceptsParentPageIDWithoutHyphens(t *testing.T) {
	dir := writeConfig(t, map[string]string{
		"workspace.yaml": "version: 1\nworkspace:\n  parent_page_id: \"3cdf830dbf9f81618d11c08cacf79fa2\"\n",
	})

	if _, err := Load(dir); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
}

// Re-review leftover: the loose `map[string]any` pass now runs BEFORE
// ValidateDocument for every file. A databases/ file written as a YAML
// sequence at the root — the plausible mistake of forgetting the
// `databases:` key — must therefore not degenerate into the decoder's raw
// message; the schema never sees this file early enough to name the error
// itself.
func TestLoadReportsNonMappingRootInProjectVoice(t *testing.T) {
	dir := writeConfig(t, map[string]string{
		"workspace.yaml": workspaceYAML,
		"databases/z.yaml": "- key: z\n  name: \"Z\"\n  properties:\n" +
			"    Name:\n      type: title\n",
	})

	_, err := Load(dir)
	if err == nil {
		t.Fatal("Load() error = nil, want the non-mapping root rejected")
	}
	msg := err.Error()
	if !strings.Contains(msg, "z.yaml") {
		t.Errorf("message = %q, it must name the offending file", msg)
	}
	if !strings.Contains(msg, "mapping") {
		t.Errorf("message = %q, it must say in its own voice that the root is not a mapping", msg)
	}
	if !strings.Contains(msg, "databases:") {
		t.Errorf("message = %q, it must name the plausible cause: the forgotten `databases:` key", msg)
	}
	// The decoder's raw text stays available as the cause, like the other
	// errors of this file — but it must not be the ONLY message.
	if !strings.Contains(msg, "cannot unmarshal") {
		t.Errorf("message = %q, it must keep the decoder's raw text as the cause", msg)
	}
}

// The keys before the rename are still read for one version: nobody breaks at
// once. Each one warns, naming the file, the old key and the new one, with the
// action on a line of its own.
func TestLoadReadsDeprecatedLifecycleKeysWithAWarning(t *testing.T) {
	dir := writeConfig(t, map[string]string{
		"workspace.yaml": `
version: 1
workspace:
  parent_page_id: "44444444-4444-4444-8444-444444444444"
lifecycle:
  prevent_destroy: [database.projects]
  allow_data_loss: [database.tasks]
`,
	})

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	ws := filepath.Join(dir, WorkspaceFile)
	if got := cfg.Lifecycle.Acknowledged("database.projects"); len(got) != 1 || got[0] != "prevent_destroy" {
		t.Errorf("Acknowledged(projects) = %v, want [prevent_destroy]", got)
	}
	if got := cfg.Lifecycle.Acknowledged("database.tasks"); len(got) != 1 || got[0] != "allow_data_loss" {
		t.Errorf("Acknowledged(tasks) = %v, want [allow_data_loss]", got)
	}
	want := []string{
		ws + ": `lifecycle.prevent_destroy` is deprecated, it is now " +
			"`lifecycle.acknowledge_destroy` — the old name is still read in this version only\n" +
			"  → rename `prevent_destroy` to `acknowledge_destroy` in " + ws,
		ws + ": `lifecycle.allow_data_loss` is deprecated, it is now " +
			"`lifecycle.acknowledge_data_loss` — the old name is still read in this version only\n" +
			"  → rename `allow_data_loss` to `acknowledge_data_loss` in " + ws,
	}
	if len(cfg.Warnings) != len(want) {
		t.Fatalf("Warnings = %q, want %q", cfg.Warnings, want)
	}
	for i := range want {
		if cfg.Warnings[i] != want[i] {
			t.Errorf("Warnings[%d] =\n%s\nwant\n%s", i, cfg.Warnings[i], want[i])
		}
	}
}

// An empty old key still warns: its presence is what must be renamed, the
// content does not matter.
func TestLoadWarnsOnAnEmptyDeprecatedLifecycleKey(t *testing.T) {
	dir := writeConfig(t, map[string]string{
		"workspace.yaml": "version: 1\nworkspace:\n  parent_page_id: \"44444444-4444-4444-8444-444444444444\"\n" +
			"lifecycle:\n  prevent_destroy: []\n",
	})
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.Warnings) != 1 || !strings.Contains(cfg.Warnings[0], "rename `prevent_destroy`") {
		t.Errorf("Warnings = %q, want one rename warning", cfg.Warnings)
	}
}

// Old and new name side by side, mid-migration: the entries are merged, never
// refused — notion-seed refuses nothing on lifecycle any more, and failing a
// CI over a half-done rename would break exactly what the deprecation window
// exists to spare. The advice changes: a bare rename would give a duplicate
// YAML key, so the warning says to move the entries.
func TestLoadMergesOldAndNewLifecycleKeysWithAWarning(t *testing.T) {
	dir := writeConfig(t, map[string]string{
		"workspace.yaml": `
version: 1
workspace:
  parent_page_id: "44444444-4444-4444-8444-444444444444"
lifecycle:
  acknowledge_destroy: [database.projects, database.both]
  prevent_destroy: [database.tasks, database.both]
`,
	})

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	ws := filepath.Join(dir, WorkspaceFile)
	for resource, want := range map[string]string{
		"database.projects": "acknowledge_destroy",
		"database.tasks":    "prevent_destroy",
		// Listed under both: named once, by the current name.
		"database.both": "acknowledge_destroy",
	} {
		if got := cfg.Lifecycle.Acknowledged(resource); len(got) != 1 || got[0] != want {
			t.Errorf("Acknowledged(%s) = %v, want [%s]", resource, got, want)
		}
	}
	want := ws + ": `lifecycle.prevent_destroy` and `lifecycle.acknowledge_destroy` are both " +
		"set, their entries are merged — the old name is still read in this version only\n" +
		"  → move the entries of `prevent_destroy` into `acknowledge_destroy` in " + ws +
		", then delete `prevent_destroy`"
	if len(cfg.Warnings) != 1 || cfg.Warnings[0] != want {
		t.Errorf("Warnings = %q, want [%q]", cfg.Warnings, want)
	}
}

// The order of the acknowledgements is part of the rendering's contract:
// destruction first, then data loss.
func TestLifecycleAcknowledgedOrder(t *testing.T) {
	l := Lifecycle{
		AcknowledgeDataLoss: []string{"database.tasks"},
		AcknowledgeDestroy:  []string{"database.tasks"},
	}
	got := l.Acknowledged("database.tasks")
	if len(got) != 2 || got[0] != "acknowledge_destroy" || got[1] != "acknowledge_data_loss" {
		t.Errorf("Acknowledged = %v, want [acknowledge_destroy acknowledge_data_loss]", got)
	}
	if got := l.Acknowledged("database.other"); len(got) != 0 {
		t.Errorf("Acknowledged(other) = %v, want none", got)
	}
}
