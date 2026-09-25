// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"regexp"
	"strings"
	"testing"
)

// fingerprintTasks is the reference configuration of the fingerprint tests:
// one database, a select with two options, one lifecycle entry.
const fingerprintTasks = `
databases:
  - key: tasks
    name: "Tasks"
    properties:
      Name:
        type: title
      Prio:
        type: select
        options:
          - key: high
            name: "High"
            color: red
          - key: low
            name: "Low"
            color: blue
`

func fingerprintOf(t *testing.T, files map[string]string) string {
	t.Helper()
	cfg, err := Load(writeConfig(t, files))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	return Fingerprint(cfg)
}

func TestFingerprintIsAHexSHA256(t *testing.T) {
	got := fingerprintOf(t, map[string]string{
		"workspace.yaml":       workspaceYAML,
		"databases/tasks.yaml": fingerprintTasks,
	})
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(got) {
		t.Errorf("Fingerprint() = %q, want 64 lowercase hex digits", got)
	}
}

// P7: a comment, a reindentation, reordered mapping keys, a database moved to
// another file and a lifecycle list reordered do not change what notion-seed
// would do. A plan must not go stale over them.
func TestFingerprintIgnoresWhatDoesNotChangeTheMeaning(t *testing.T) {
	want := fingerprintOf(t, map[string]string{
		"workspace.yaml":       workspaceYAML,
		"databases/tasks.yaml": fingerprintTasks,
	})

	reworded := map[string]string{
		"workspace.yaml": `
# the parent page of every database
version: 1
lifecycle:
  acknowledge_data_loss: []
  acknowledge_destroy:
    - projects
    - projects
workspace:
    parent_page_id: "44444444-4444-4444-8444-444444444444"
`,
		// Another file name, another indentation, keys in another order, a
		// comment: the same database.
		"databases/all-of-them.yaml": `
databases:
    -   name: "Tasks"   # displayed in Notion
        key: tasks
        properties:
            Prio:
                options:
                    -   name: "High"
                        key: high
                        color: red
                    -   color: blue
                        key: low
                        name: "Low"
                type: select
            Name:
                type: title
`,
	}
	if got := fingerprintOf(t, reworded); got != want {
		t.Errorf("Fingerprint() = %s, want %s: only the bytes changed, not the meaning", got, want)
	}
}

// Each of these edits changes what apply would write, or what the state joins
// on, or what the plan shows: the fingerprint must change.
func TestFingerprintChangesWithTheMeaning(t *testing.T) {
	base := fingerprintOf(t, map[string]string{
		"workspace.yaml":       workspaceYAML,
		"databases/tasks.yaml": fingerprintTasks,
	})

	for _, tc := range []struct {
		name      string
		workspace string
		tasks     string
	}{
		{"option key changed", workspaceYAML,
			strings.Replace(fingerprintTasks, "key: high", "key: urgent", 1)},
		{"option name changed", workspaceYAML,
			strings.Replace(fingerprintTasks, `name: "High"`, `name: "Urgent"`, 1)},
		{"options reordered", workspaceYAML, `
databases:
  - key: tasks
    name: "Tasks"
    properties:
      Name:
        type: title
      Prio:
        type: select
        options:
          - key: low
            name: "Low"
            color: blue
          - key: high
            name: "High"
            color: red
`},
		{"property type changed", workspaceYAML,
			strings.Replace(fingerprintTasks, "type: select", "type: multi_select", 1)},
		{"database renamed", workspaceYAML,
			strings.Replace(fingerprintTasks, `name: "Tasks"`, `name: "Todo"`, 1)},
		{"lifecycle entry added",
			strings.Replace(workspaceYAML, "acknowledge_data_loss: []",
				"acknowledge_data_loss: [database.tasks]", 1), fingerprintTasks},
		{"lifecycle entry removed",
			strings.Replace(workspaceYAML, "acknowledge_destroy: [projects]",
				"acknowledge_destroy: []", 1), fingerprintTasks},
		{"lifecycle key renamed to its deprecated name",
			strings.Replace(workspaceYAML, "acknowledge_destroy:", "prevent_destroy:", 1),
			fingerprintTasks},
		{"parent page changed",
			strings.Replace(workspaceYAML, "44444444-4444-4444-8444-444444444444",
				"55555555-5555-4555-8555-555555555555", 1), fingerprintTasks},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := fingerprintOf(t, map[string]string{
				"workspace.yaml":       tc.workspace,
				"databases/tasks.yaml": tc.tasks,
			})
			if got == base {
				t.Errorf("Fingerprint() unchanged by %q", tc.name)
			}
		})
	}
}
