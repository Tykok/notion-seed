// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tykok/notion-seed/core/diff"
	"github.com/tykok/notion-seed/core/providers/notion/transport"
	"github.com/tykok/notion-seed/core/state"
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

// runCmd runs a root command and returns its output, for the tests that do not
// pin a golden.
func runCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := NewRootCmd()
	var b bytes.Buffer
	cmd.SetOut(&b)
	cmd.SetErr(&b)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return b.String(), err
}

// testParentPageID is a well-formed UUID: the schema enforces this pattern on
// parent_page_id, so that the config is rejected before the call rather than by
// a 400 from the API.
const testParentPageID = "44444444-4444-4444-8444-444444444444"

// workspaceYAML is the minimal workspace.yaml of the plan tests.
const workspaceYAML = "version: 1\nworkspace:\n  parent_page_id: \"" + testParentPageID + "\"\n"

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

// This test runs ONLINE, without --skip-preflight: the fake ntn is actually
// invoked. Before, the three tests of the plan path put the fake binary on the
// PATH then passed --skip-preflight, so neither the assembly of the transport
// stack, nor the workspace header, nor checkParentPage were executed.
//
// The comparison is against a golden file: every assertion on the product's
// real output was a strings.Contains, so indentation, blank lines, the header
// and the precedence of markers could change with the suite green. The golden
// also pins determinism, a binding constraint.
func TestPlanRendersCreationsForTwoDatabases(t *testing.T) {
	withFakeNtn(t, "authenticated")
	dir := writeConfigDir(t, map[string]string{
		"workspace.yaml":     workspaceYAML,
		"databases/all.yaml": twoDatabases,
	})

	cmd := NewRootCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"plan", "--dir", dir})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v\n%s\n%s", err, out.String(), errOut.String())
	}
	if errOut.Len() != 0 {
		t.Errorf("non-empty stderr on a successful plan: %q", errOut.String())
	}
	assertGolden(t, "plan_two_databases.golden", out.String())
}

// The output must be identical from one run to the next: plan is meant to be
// read in CI and compared.
func TestPlanOutputIsStableAcrossRuns(t *testing.T) {
	withFakeNtn(t, "authenticated")
	dir := writeConfigDir(t, map[string]string{
		"workspace.yaml":     workspaceYAML,
		"databases/all.yaml": twoDatabases,
	})

	run := func() string {
		cmd := NewRootCmd()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetArgs([]string{"plan", "--dir", dir})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		return out.String()
	}
	first := run()
	for i := 0; i < 5; i++ {
		if got := run(); got != first {
			t.Fatalf("run %d differs from the first:\n%s", i, lineDiff(first, got))
		}
	}
}

// The 404 branch of checkParentPage and its message were covered by no test:
// the three tests of the plan path skipped the preflight.
func TestPlanReportsUnreachableParentPage(t *testing.T) {
	withFakeNtn(t, "authenticated_page_404")
	dir := writeConfigDir(t, map[string]string{
		"workspace.yaml":     workspaceYAML,
		"databases/all.yaml": twoDatabases,
	})

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"plan", "--dir", dir})

	err := cmd.Execute()
	if err == nil {
		t.Fatalf("Execute() error = nil, want a failure on the parent page\n%s", out.String())
	}
	for _, want := range []string{
		testParentPageID,
		"not found",
		"parent_page_id",
		// ntn's token sees the whole workspace: a 404 is not a sharing defect,
		// and the message must not send the user down that trail.
		"not a permissions problem",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message = %q, it must contain %q", err.Error(), want)
		}
	}
	// The plan must not be rendered when the parent page is unreadable.
	if strings.Contains(out.String(), "Plan:") {
		t.Errorf("a plan was shown despite an unreadable parent page:\n%s", out.String())
	}
}

// Measured on 2026-09-25: with a parent page in the trash, plan said "No
// changes" and exited 0, while every write under it is refused (400 "archived
// ancestor"). The page reads as 200: only its in_trash field says so.
func TestPlanRefusesATrashedParentPage(t *testing.T) {
	withFakeNtn(t, "authenticated_page_in_trash")
	dir := writeConfigDir(t, map[string]string{
		"workspace.yaml":     workspaceYAML,
		"databases/all.yaml": twoDatabases,
	})

	out, err := runCmd(t, "plan", "--dir", dir)
	if err == nil {
		t.Fatalf("Execute() error = nil, want a refusal on the parent page in the trash\n%s", out)
	}
	for _, want := range []string{testParentPageID, "trash", "restore", "parent_page_id", "  → "} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message = %q, it must contain %q", err.Error(), want)
		}
	}
	if strings.Contains(out, "Plan:") || strings.Contains(out, "No changes") {
		t.Errorf("a plan was rendered despite a parent page in the trash:\n%s", out)
	}
}

// apply shares plan's preflight: it refuses before the prompt, hence before any
// write.
func TestApplyRefusesATrashedParentPageBeforeWriting(t *testing.T) {
	withFakeNtn(t, "authenticated_page_in_trash")
	dir := writeConfigDir(t, map[string]string{
		"workspace.yaml":     workspaceYAML,
		"databases/all.yaml": twoDatabases,
	})

	out, err := runCmd(t, "apply", "--dir", dir, "--auto-approve")
	if err == nil || !strings.Contains(err.Error(), "trash") {
		t.Fatalf("Execute() error = %v, want the refusal of the parent page in the trash\n%s", err, out)
	}
	if _, serr := os.Stat(filepath.Join(dir, state.FileName)); !os.IsNotExist(serr) {
		t.Error("a state was written under a parent page in the trash")
	}
}

// The shape measured on 2026-09-25 (API 2025-09-03): in_trash alone, without
// archived. in_trash, and it alone, must decide.
func TestCheckParentPageReadsInTrashAlone(t *testing.T) {
	if err := checkParentPage(context.Background(), fixedTransport{body: `{"object":"page","in_trash":true}`},
		testParentPageID, 3); err == nil || !strings.Contains(err.Error(), "trash") {
		t.Errorf(`{"in_trash":true} alone: err = %v, want a refusal`, err)
	}
	if err := checkParentPage(context.Background(), fixedTransport{body: `{"object":"page","in_trash":false}`},
		testParentPageID, 3); err != nil {
		t.Errorf(`{"in_trash":false} alone: err = %v, want nil`, err)
	}
}

// A response that does not say whether the page is in the trash does not mean
// "live page": the API's silence is not a measurement.
func TestCheckParentPageRefusesAnAnswerWithoutTrashFields(t *testing.T) {
	for name, body := range map[string]string{
		"no field":   `{"object":"page","id":"p"}`,
		"unreadable": `not json`,
	} {
		t.Run(name, func(t *testing.T) {
			tr := fixedTransport{body: body}
			err := checkParentPage(context.Background(), tr, testParentPageID, 3)
			if err == nil {
				t.Fatal("checkParentPage() = nil, want a refusal")
			}
			if !strings.Contains(err.Error(), "  → ") {
				t.Errorf("message = %q, it must carry a corrective action", err.Error())
			}
		})
	}
}

// fixedTransport always returns the same body with a 200.
type fixedTransport struct{ body string }

func (f fixedTransport) Execute(context.Context, transport.APIRequest) (transport.APIResponse, error) {
	return transport.APIResponse{Status: 200, Body: []byte(f.body)}, nil
}

func TestPlanFailsOnDuplicateKeyNamingBothFiles(t *testing.T) {
	dir := writeConfigDir(t, map[string]string{
		"workspace.yaml":   workspaceYAML,
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
		t.Fatal("Execute() error = nil, want a duplicate key error")
	}
	for _, want := range []string{"a.yaml", "b.yaml", "projects"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message = %q, it must contain %q", err.Error(), want)
		}
	}
}

// plan writes nothing in MVP 0: neither state nor mutation. The test guards this
// property, which is the command's promise.
func TestPlanWritesNothingToDisk(t *testing.T) {
	// Online, without --skip-preflight: "plan writes nothing" must also hold
	// when the command actually talks to ntn.
	withFakeNtn(t, "authenticated")
	dir := writeConfigDir(t, map[string]string{
		"workspace.yaml":     workspaceYAML,
		"databases/all.yaml": twoDatabases,
	})

	before := snapshot(t, dir)

	cmd := NewRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"plan", "--dir", dir})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	after := snapshot(t, dir)

	for path, sum := range before {
		got, ok := after[path]
		if !ok {
			t.Errorf("plan deleted %s", path)
			continue
		}
		if got != sum {
			t.Errorf("plan modified the content of %s", path)
		}
	}
	for path := range after {
		if _, ok := before[path]; !ok {
			t.Errorf("plan created %s", path)
		}
	}
}

// snapshot walks the tree RECURSIVELY and keeps a fingerprint of each file's
// content. Counting the entries at the root would see neither a write in
// databases/, nor an in-place content mutation, nor a file created then
// deleted — yet "plan writes nothing" is the command's central promise, it
// deserves to be pinned for real.
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
		"workspace.yaml":     workspaceYAML,
		"databases/all.yaml": twoDatabases,
	})
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"zero rate", []string{"--rate", "0"}, "--rate"},
		{"negative rate", []string{"--rate", "-1"}, "--rate"},
		{"zero burst", []string{"--burst", "0"}, "--burst"},
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
				t.Fatalf("Execute() error = nil, want a rejection of %v", tt.args)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("message = %q, it must name %q", err.Error(), tt.want)
			}
		})
	}
}

// CAUTION, temporary truth: this equality holds only in MVP 0, because plan
// does not write the state yet. In MVP 1 plan will diverge from diff by
// construction — the very reason to keep two commands. This test will then
// have to be loosened or replaced, not "fixed".
func TestDiffProducesSameOutputAsPlan(t *testing.T) {
	withFakeNtn(t, "ok")
	dir := writeConfigDir(t, map[string]string{
		"workspace.yaml":     workspaceYAML,
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
		t.Error("plan and diff must produce the same output in MVP 0")
	}
}

// updateGolden rewrites the golden files instead of comparing them:
//
//	go test ./cli/ -run TestPlan -update
var updateGolden = flag.Bool("update", false, "rewrite the golden files in testdata/")

// assertGolden compares an output with the matching golden file. On a
// mismatch, the failure prints a line-by-line diff: "different output" without
// the detail would force a manual rerun to find out what.
func assertGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("golden rewritten: %s", path)
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("golden unreadable: %v — rerun with -update to create it", err)
	}
	if got != string(want) {
		t.Errorf("the plan output differs from %s:\n%s", path, lineDiff(string(want), got))
	}
}

// lineDiff returns a line-by-line diff, aligned on line numbers: "-" for the
// expected line, "+" for the one obtained.
func lineDiff(want, got string) string {
	wantLines := strings.Split(want, "\n")
	gotLines := strings.Split(got, "\n")
	var b strings.Builder
	for i := 0; i < len(wantLines) || i < len(gotLines); i++ {
		w, g := "", ""
		if i < len(wantLines) {
			w = wantLines[i]
		}
		if i < len(gotLines) {
			g = gotLines[i]
		}
		if w == g {
			fmt.Fprintf(&b, "  %3d  %s\n", i+1, w)
			continue
		}
		if i < len(wantLines) {
			fmt.Fprintf(&b, "- %3d  %s\n", i+1, w)
		}
		if i < len(gotLines) {
			fmt.Fprintf(&b, "+ %3d  %s\n", i+1, g)
		}
	}
	return b.String()
}

// Shown before the fix: an exhausted 429 returned "parent page X unreadable:
// notion api 429 rate_limited: Rate limited." without any corrective action, on
// the most likely non-404 failure. And retry waits were completely silent —
// measured, 9.35 s of silence.
func TestPlanReportsExhaustedRateLimitWithAnAction(t *testing.T) {
	withFakeNtn(t, "authenticated_rate_limited")
	dir := writeConfigDir(t, map[string]string{
		"workspace.yaml":     workspaceYAML,
		"databases/all.yaml": twoDatabases,
	})

	cmd := NewRootCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"plan", "--dir", dir})

	err := cmd.Execute()
	if err == nil {
		t.Fatalf("Execute() error = nil, want a failure after the attempts are exhausted\n%s", out.String())
	}
	for _, want := range []string{"429", "--rate", "retry"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message = %q, it must contain %q", err.Error(), want)
		}
	}
	// A silent wait is indistinguishable from a hang: each wait is announced,
	// and on stderr so as not to pollute the plan.
	if !strings.Contains(errOut.String(), "waiting") {
		t.Errorf("stderr = %q, each retry wait must be announced", errOut.String())
	}
}

// The state anchors database.tasks on db-1: the refresh reads it, the
// comparator finds it identical to the desired one, so no change comes out —
// whereas without a state, the same config would produce a creation.
func TestPlanWithStateReportsNoChange(t *testing.T) {
	withFakeNtn(t, "authenticated_database")
	dir := writeConfigDir(t, map[string]string{
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
          - key: done
            name: Fait
            group: Complete
`,
		state.FileName: `{
  "version": 1,
  "workspace_id": "33333333-3333-4333-8333-333333333333",
  "databases": {
    "tasks": {
      "id": "db-1",
      "data_source_id": "ds-1",
      "name": "Tasks",
      "properties": {
        "Name": {"id": "title", "type": "title"},
        "Statut": {"id": "p-statut", "type": "status", "options": [
          {"id": "o-todo", "key": "todo", "name": "À faire", "color": "blue", "group": "To-do"},
          {"id": "o-done", "key": "done", "name": "Fait", "color": "green", "group": "Complete"}
        ]}
      }
    }
  }
}
`,
	})

	out, err := runCmd(t, "plan", "--dir", dir)
	if err != nil {
		t.Fatalf("plan error = %v\n%s", err, out)
	}
	if !strings.Contains(out, "No changes") {
		t.Errorf("the state must make the database recognized:\n%s", out)
	}
}

// tasksWorkspaceYAML, tasksConfigYAML and tasksStateJSON describe a
// configuration with a single database "tasks", whose state anchors the id
// "db-1" — the one served by the fakentn scenario "authenticated_database".
// Shared by the tests below, which check plan's behavior with a state that
// actually reads a remote resource.
const tasksWorkspaceYAML = `
version: 1
workspace:
  parent_page_id: 33333333-3333-4333-8333-333333333333
`

const tasksConfigYAML = `
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
          - key: done
            name: Fait
            group: Complete
`

const tasksStateJSON = `{
  "version": 1,
  "workspace_id": "33333333-3333-4333-8333-333333333333",
  "databases": {
    "tasks": {
      "id": "db-1",
      "data_source_id": "ds-1",
      "name": "Tasks",
      "properties": {
        "Name": {"id": "title", "type": "title"},
        "Statut": {"id": "p-statut", "type": "status", "options": [
          {"id": "o-todo", "key": "todo", "name": "À faire", "color": "blue", "group": "To-do"},
          {"id": "o-done", "key": "done", "name": "Fait", "color": "green", "group": "Complete"}
        ]}
      }
    }
  }
}
`

// Review round 1: TestPlanWritesNothingToDisk locks the "plan writes nothing"
// invariant ONLY on the path without a state — with no entry in
// snap.Databases, refreshManaged returns at its first guard and its read loop
// is never exercised. This test replays the same lock with a populated state
// that actually reads a database (id "db-1", served by the fakentn scenario
// "authenticated_database"): the refresh must make its GET calls without ever
// writing to disk.
func TestPlanWithPopulatedStateWritesNothingToDisk(t *testing.T) {
	withFakeNtn(t, "authenticated_database")
	dir := writeConfigDir(t, map[string]string{
		"workspace.yaml":       tasksWorkspaceYAML,
		"databases/tasks.yaml": tasksConfigYAML,
		state.FileName:         tasksStateJSON,
	})

	before := snapshot(t, dir)

	out, err := runCmd(t, "plan", "--dir", dir)
	if err != nil {
		t.Fatalf("plan error = %v\n%s", err, out)
	}
	// The proof that the refresh actually ran, and did not just pass the
	// "empty state" guard: the three-way comparison can return "No changes"
	// only if `actual` was successfully read from the API.
	if !strings.Contains(out, "No changes") {
		t.Fatalf("the refresh did not produce the expected comparison:\n%s", out)
	}

	after := snapshot(t, dir)
	for path, sum := range before {
		got, ok := after[path]
		if !ok {
			t.Errorf("plan deleted %s", path)
			continue
		}
		if got != sum {
			t.Errorf("plan modified the content of %s", path)
		}
	}
	for path := range after {
		if _, ok := before[path]; !ok {
			t.Errorf("plan created %s", path)
		}
	}
}

// Review round 1: a state entry without an id (state written or merged by
// hand) must not reach the API. Without this guard, refreshManaged would call
// GET /v1/databases/ (empty path), which would return either a false
// "vanished", or a message about deletion while the real problem is a damaged
// state.
func TestPlanRejectsStateEntryWithoutID(t *testing.T) {
	withFakeNtn(t, "authenticated_database")
	dir := writeConfigDir(t, map[string]string{
		"workspace.yaml":       tasksWorkspaceYAML,
		"databases/tasks.yaml": tasksConfigYAML,
		state.FileName: `{
  "version": 1,
  "workspace_id": "33333333-3333-4333-8333-333333333333",
  "databases": {
    "tasks": {
      "data_source_id": "ds-1",
      "name": "Tasks"
    }
  }
}
`,
	})

	out, err := runCmd(t, "plan", "--dir", dir)
	if err == nil {
		t.Fatalf("Execute() error = nil, want a refusal of the entry without an id\n%s", out)
	}
	for _, want := range []string{"database.tasks", "has no identifier", state.FileName} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message = %q, it must contain %q", err.Error(), want)
		}
	}
}

// C1: a vanished managed database (404) must block the plan AND say so on
// stdout. Before the fix, Render printed "No changes. The configuration
// matches the actual state." even though the command returned an error code —
// stdout asserted the opposite of stderr.
func TestPlanBlocksAndSaysSoWhenStateDatabaseIs404(t *testing.T) {
	withFakeNtn(t, "authenticated_database_404")
	dir := writeConfigDir(t, map[string]string{
		"workspace.yaml":       tasksWorkspaceYAML,
		"databases/tasks.yaml": tasksConfigYAML,
		state.FileName:         tasksStateJSON,
	})

	out, err := runCmd(t, "plan", "--dir", dir)
	if err == nil {
		t.Fatalf("Execute() error = nil, want a block on a database that is not found\n%s", out)
	}
	if strings.Contains(out, "No changes") {
		t.Errorf("stdout asserts conformity while the database vanished:\n%s", out)
	}
	for _, want := range []string{"Plan blocked", "not found", "database.tasks"} {
		if !strings.Contains(out, want) {
			t.Errorf("output = %q, it must contain %q", out, want)
		}
	}
}

// C1: same defect, on the archiving side — until now the only branch of
// refreshManaged without any end-to-end test.
func TestPlanBlocksAndSaysSoWhenStateDatabaseIsArchived(t *testing.T) {
	withFakeNtn(t, "archived_database")
	dir := writeConfigDir(t, map[string]string{
		"workspace.yaml":       tasksWorkspaceYAML,
		"databases/tasks.yaml": tasksConfigYAML,
		state.FileName:         tasksStateJSON,
	})

	out, err := runCmd(t, "plan", "--dir", dir)
	if err == nil {
		t.Fatalf("Execute() error = nil, want a block on an archived database\n%s", out)
	}
	if strings.Contains(out, "No changes") {
		t.Errorf("stdout asserts conformity while the database is archived:\n%s", out)
	}
	for _, want := range []string{"Plan blocked", "archiv", "database.tasks"} {
		if !strings.Contains(out, want) {
			t.Errorf("output = %q, it must contain %q", out, want)
		}
	}
}

// C2: after import, plan --skip-preflight made no network call — it therefore
// cannot assert that the configuration matches the actual state. A CI relying
// on this mode would go green on a silent rewrite without this guarantee.
func TestPlanSkipPreflightNamesResourceItDidNotCompare(t *testing.T) {
	withFakeNtn(t, "authenticated_database")
	dir := writeImportFixture(t)

	if out, err := runCmd(t, "import", "database.tasks",
		"1b2c3d4e-5f60-4a1b-8c2d-3e4f5a6b7c8d", "--dir", dir); err != nil {
		t.Fatalf("import error = %v\n%s", err, out)
	}

	out, err := runCmd(t, "plan", "--dir", dir, "--skip-preflight")
	if err != nil {
		t.Fatalf("plan error = %v\n%s", err, out)
	}
	if strings.Contains(out, "matches the actual state") {
		t.Errorf("--skip-preflight must never assert conformity:\n%s", out)
	}
	if !strings.Contains(out, "Not compared") || !strings.Contains(out, "database.tasks") {
		t.Errorf("output = %q, it must name the resource not compared", out)
	}
}

// Review Focus 5: a state without workspace_id cannot be compared. It must not
// count as "different workspace".
func TestCheckWorkspaceMatchAcceptsEmptyWorkspaceID(t *testing.T) {
	snap := &state.Snapshot{Version: state.Version}
	if err := checkWorkspaceMatch(snap, "33333333-3333-4333-8333-333333333333"); err != nil {
		t.Errorf("an empty workspace_id is unknown, not different: %v", err)
	}
}

func TestCheckWorkspaceMatchRejectsForeignWorkspace(t *testing.T) {
	snap := &state.Snapshot{Version: state.Version, WorkspaceID: "44444444-4444-4444-8444-444444444444"}
	err := checkWorkspaceMatch(snap, "33333333-3333-4333-8333-333333333333")
	if err == nil {
		t.Fatal("a state from another workspace must be rejected")
	}
	if !strings.Contains(err.Error(), "44444444") || !strings.Contains(err.Error(), "33333333") {
		t.Errorf("the message must name both workspaces: %v", err)
	}
}

// End to end: the plan actually measures the rows against the API and
// reclassifies the affected line with what it counted, instead of leaving it
// as "unknown impact". It no longer blocks.
//
// The fake ntn returns two rows holding the "Fait" option on a status
// property: measured, this removal is a silent rewrite. Unmeasured, it would
// stay "unknown impact" — that is exactly the gap this pass closes. Since the
// render carries the number, this test asserts it: it is the only end-to-end
// check that the number SHOWN is the one that was counted, and not a count of
// another database or a leftover of classification.
func TestPlanMeasuresRowsAndDoesNotBlock(t *testing.T) {
	withFakeNtn(t, "authenticated_database")
	dir := writeConfigDir(t, map[string]string{
		"workspace.yaml": workspaceYAML,
		// The YAML no longer declares the "Fait" option the real database holds:
		// its removal is measured at 2 rows.
		"databases/all.yaml": `
databases:
  - key: tasks
    name: "Tasks"
    properties:
      Name:
        type: title
      Statut:
        type: status
        options:
          - key: todo
            name: "À faire"
            color: blue
            group: "To-do"
`,
	})
	if _, err := runCmd(t, "import", "database.tasks", testDatabaseID, "--dir", dir); err != nil {
		t.Fatalf("import: %v", err)
	}

	out, err := runCmd(t, "plan", "--dir", dir)
	if err != nil {
		t.Fatalf("plan must no longer fail: %v\n%s", err, out)
	}
	if !strings.Contains(out, "silent rewrite") {
		t.Errorf("the removal was not reclassified by the measurement:\n%s", out)
	}
	if strings.Contains(out, "unknown impact") {
		t.Errorf("the line stayed unmeasured:\n%s", out)
	}
	if strings.Contains(out, "Plan blocked") {
		t.Errorf("the plan still blocks:\n%s", out)
	}
	// The number itself, not just the class: it is the product.
	//
	// The WHOLE sentence, not "2 rows": that fragment is already satisfied by
	// the aggregate line alone, so it would not prove the detail line carries
	// its count — which is the whole point of this pass.
	if !strings.Contains(out, "2 rows will be reassigned to another option, without a trace") {
		t.Errorf("the detail line does not carry its measured count:\n%s", out)
	}
	if !strings.Contains(out, "Impact: 2 values reassigned without a trace.") {
		t.Errorf("the aggregate line is missing or does not say what was measured:\n%s", out)
	}
}

// The fake ntn's ordering trap, disarmed for ALL scenarios.
//
// `/v1/data_sources/ds-1/query` carries the `/v1/data_sources/` prefix: a
// scenario that tests the prefix before the `/query` suffix returns a data
// source's schema to a count query. It is not a visible error — the response
// is a valid 200 — but it carries no `results`, and a count that got 0 from it
// would announce "nothing to lose".
//
// This test queries the fake binary directly, scenario by scenario: it catches
// the trap in an existing scenario as well as in a future one.
func TestFakeNtnAnswersQueryWithAListInEveryScenario(t *testing.T) {
	// Every scenario that serves /v1/data_sources/ and answers 200.
	scenarios := []string{
		"authenticated_database",
		"authenticated_database_select",
		"authenticated_database_updatable",
		"archived_database",
		"authenticated_create",
	}
	for _, scenario := range scenarios {
		t.Run(scenario, func(t *testing.T) {
			withFakeNtn(t, scenario)
			out, err := exec.Command("ntn", "api", "/v1/data_sources/ds-1/query").Output()
			if err != nil {
				t.Fatalf("ntn api: %v", err)
			}
			if !strings.Contains(string(out), `"object":"list"`) {
				t.Errorf("a count query must return a list, not %s", out)
			}
			if !strings.Contains(string(out), `"results"`) {
				t.Errorf("the list must carry a results field, not %s", out)
			}
		})
	}
}

// The count must query the data source the plan just READ, not the one the
// state remembered. The plan is computed against `refreshed`; if the
// measurement queries another object, it counts the rows of another database.
// A stale id still alive then belongs to someone else, answers 200, and the 0
// coming out of it becomes "nothing to lose".
func TestMeasuredDataSourceIDsPreferTheRefreshedOne(t *testing.T) {
	snap := &state.Snapshot{
		Version: state.Version,
		Databases: map[string]state.Database{
			"tasks":   {ID: "db-1", DataSourceID: "ds-périmé"},
			"notes":   {ID: "db-2", DataSourceID: "ds-du-state"},
			"orphans": {ID: "db-3", DataSourceID: "ds-jamais-relue"},
		},
	}
	refreshed := map[string]diff.Refreshed{
		"tasks": {Database: state.Database{ID: "db-1", DataSourceID: "ds-frais"}},
		// Read back, but without a data source id: the state stays the best
		// available answer, and beats no measurement at all.
		"notes": {Database: state.Database{ID: "db-2"}},
	}

	got := measuredDataSourceIDs(snap, refreshed)
	want := map[string]string{
		"tasks":   "ds-frais",
		"notes":   "ds-du-state",
		"orphans": "ds-jamais-relue",
	}
	for key, w := range want {
		if got[key] != w {
			t.Errorf("dataSourceIDs[%q] = %q, want %q", key, got[key], w)
		}
	}
}

// A resource the state does not anchor but the actual state returned (an
// orphan read by Compute) must stay measurable: its id then comes from the only
// place that has it.
func TestMeasuredDataSourceIDsIncludesResourcesAbsentFromTheState(t *testing.T) {
	got := measuredDataSourceIDs(&state.Snapshot{Version: state.Version},
		map[string]diff.Refreshed{
			"tasks": {Database: state.Database{ID: "db-1", DataSourceID: "ds-frais"}},
		})
	if got["tasks"] != "ds-frais" {
		t.Errorf("dataSourceIDs[\"tasks\"] = %q, want %q", got["tasks"], "ds-frais")
	}
}

// Review Focus 2: a count refused by the API does not fail the command. The
// line stays "unknown impact", the cause goes to stderr, and the rest of the
// plan is rendered anyway — depriving the user of their plan because a count
// failed would be the real defect.
func TestPlanReportsAFailedCountAndStillRendersThePlan(t *testing.T) {
	withFakeNtn(t, "authenticated_database_query_403")
	dir := writeConfigDir(t, map[string]string{
		"workspace.yaml": workspaceYAML,
		"databases/all.yaml": `
databases:
  - key: tasks
    name: "Tasks"
    properties:
      Name:
        type: title
      Statut:
        type: status
        options:
          - key: todo
            name: "À faire"
            color: blue
            group: "To-do"
`,
	})
	if _, err := runCmd(t, "import", "database.tasks", testDatabaseID, "--dir", dir); err != nil {
		t.Fatalf("import: %v", err)
	}

	cmd := NewRootCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"plan", "--dir", dir})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("a failed count must not fail plan: %v\n%s", err, out.String())
	}

	if !strings.Contains(out.String(), `option "Fait"`) {
		t.Errorf("the rest of the plan is not rendered:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "unknown impact") {
		t.Errorf("the unmeasured line must stay unknown:\n%s", out.String())
	}
	// Incidents do not pollute stdout: the plan must stay usable in a pipe.
	if strings.Contains(out.String(), "count failed") {
		t.Errorf("the incident is on stdout:\n%s", out.String())
	}
	for _, want := range []string{"count failed", "database.tasks", "403"} {
		if !strings.Contains(errOut.String(), want) {
			t.Errorf("stderr = %q, it must contain %q", errOut.String(), want)
		}
	}
}

// statusWithoutFait declares the `tasks` database WITHOUT the "Fait" option the
// fake ntn returns: its removal is therefore measured, and classified as a
// silent rewrite because rows hold it.
const statusWithoutFait = `
databases:
  - key: tasks
    name: "Tasks"
    properties:
      Name:
        type: title
      Statut:
        type: status
        options:
          - key: todo
            name: "À faire"
            color: blue
            group: "To-do"
`

// Without --fail-on, a plan that reassigns rows stays successful: nothing
// blocks anymore, the user is responsible for their database.
func TestPlanSucceedsOnSilentRewriteWithoutFailOn(t *testing.T) {
	withFakeNtn(t, "authenticated_database")
	dir := writeConfigDir(t, map[string]string{
		"workspace.yaml":     workspaceYAML,
		"databases/all.yaml": statusWithoutFait,
	})
	if _, err := runCmd(t, "import", "database.tasks",
		"1b2c3d4e-5f60-4a1b-8c2d-3e4f5a6b7c8d", "--dir", dir); err != nil {
		t.Fatalf("import: %v", err)
	}
	if out, err := runCmd(t, "plan", "--dir", dir); err != nil {
		t.Fatalf("plan error = %v, want nil\n%s", err, out)
	}
}

// With --fail-on, the CI catches the change. It is the mechanism that replaces
// the block, and it is chosen in the workflow.
func TestPlanFailsOnSilentRewriteWhenAsked(t *testing.T) {
	withFakeNtn(t, "authenticated_database")
	dir := writeConfigDir(t, map[string]string{
		"workspace.yaml":     workspaceYAML,
		"databases/all.yaml": statusWithoutFait,
	})
	if _, err := runCmd(t, "import", "database.tasks",
		"1b2c3d4e-5f60-4a1b-8c2d-3e4f5a6b7c8d", "--dir", dir); err != nil {
		t.Fatalf("import: %v", err)
	}
	out, err := runCmd(t, "plan", "--dir", dir, "--fail-on=silent-rewrite")
	if err == nil {
		t.Fatalf("plan error = nil, want a failure\n%s", out)
	}
	if !strings.Contains(err.Error(), "silent rewrite") {
		t.Errorf("message = %q, it must name the class that triggered", err.Error())
	}
}

// A class that does not exist must be rejected BEFORE any network call:
// otherwise a misconfigured CI would go green believing it is protected.
func TestPlanRejectsAnUnknownFailOnValue(t *testing.T) {
	dir := writeConfigDir(t, map[string]string{
		"workspace.yaml":     workspaceYAML,
		"databases/all.yaml": twoDatabases,
	})
	_, err := runCmd(t, "plan", "--dir", dir, "--fail-on=dangereux")
	if err == nil {
		t.Fatal("plan error = nil, want a rejection of the value")
	}
	for _, want := range []string{"dangereux", "destructive", "silent-rewrite", "unknown", "  → "} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message = %q, it must contain %q", err.Error(), want)
		}
	}
}

// diff shares planOptions with plan, so --fail-on is available there with
// nothing more. It is diff, not plan, that the documentation recommends in CI:
// the flag would be useless there if it only worked on plan, and nobody would
// notice before the CI let a rewrite through.
func TestDiffFailsOnSilentRewriteWhenAsked(t *testing.T) {
	withFakeNtn(t, "authenticated_database")
	dir := writeConfigDir(t, map[string]string{
		"workspace.yaml":     workspaceYAML,
		"databases/all.yaml": statusWithoutFait,
	})
	if _, err := runCmd(t, "import", "database.tasks", testDatabaseID, "--dir", dir); err != nil {
		t.Fatalf("import: %v", err)
	}
	out, err := runCmd(t, "diff", "--dir", dir, "--fail-on=silent-rewrite")
	if err == nil {
		t.Fatalf("diff error = nil, want a failure\n%s", out)
	}
	if !strings.Contains(err.Error(), "silent rewrite") {
		t.Errorf("message = %q, it must name the class that triggered", err.Error())
	}
	// The plan is rendered anyway: --fail-on changes the exit code, it does not
	// deprive the user of what explains the failure.
	if !strings.Contains(out, `option "Fait"`) {
		t.Errorf("the plan was not rendered before the failure:\n%s", out)
	}
}

// --fail-on must not trigger on a class it was not given. The list is
// enumerated, not a threshold: asking for `destructive` does not ask for
// "everything at least as serious", and failing on a silent rewrite or an
// unknown impact that was not asked for would invent an ordering the product
// refuses.
func TestPlanFailOnIsEnumeratedNotAThreshold(t *testing.T) {
	withFakeNtn(t, "authenticated_database")
	dir := writeConfigDir(t, map[string]string{
		"workspace.yaml":     workspaceYAML,
		"databases/all.yaml": statusWithoutFait,
	})
	if _, err := runCmd(t, "import", "database.tasks", testDatabaseID, "--dir", dir); err != nil {
		t.Fatalf("import: %v", err)
	}
	// The plan carries a measured silent rewrite. Asking for `destructive` and
	// `migration` must catch nothing.
	if out, err := runCmd(t, "plan", "--dir", dir, "--fail-on=destructive,migration"); err != nil {
		t.Fatalf("plan error = %v, want nil: no requested class is in the plan\n%s", err, out)
	}
}

// select → multi_select is classified safe by the pair table, and it is for
// the redeclared options. Those the YAML omits vanish, and their rows are
// emptied (measured on 2026-09-25). Before, the plan said so nowhere and
// --fail-on=destructive let it through: it is the loss this product exists to
// announce.
func TestPlanFailsOnDestructiveWhenATypeChangeDropsAnOption(t *testing.T) {
	withFakeNtn(t, "authenticated_database_select")
	dir := writeConfigDir(t, map[string]string{
		"workspace.yaml": workspaceYAML,
		"databases/all.yaml": `
databases:
  - key: tasks
    name: "Tasks"
    properties:
      Name:
        type: title
      Prio:
        type: multi_select
        options:
          - key: haute
            name: "Haute"
            color: red
`,
	})
	if _, err := runCmd(t, "import", "database.tasks", testDatabaseID, "--dir", dir); err != nil {
		t.Fatalf("import: %v", err)
	}
	out, err := runCmd(t, "plan", "--dir", dir, "--fail-on=destructive")
	if err == nil {
		t.Fatalf("plan error = nil, want a failure\n%s", out)
	}
	if !strings.Contains(err.Error(), "destructi") {
		t.Errorf("message = %q, it must name the class that triggered", err.Error())
	}
	for _, want := range []string{
		`- option "Basse" (property "Prio")`,
		"2 rows will be emptied",
		"Impact: 2 values lost.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("%q is missing:\n%s", want, out)
		}
	}
	if strings.Contains(out, `- option "Haute"`) {
		t.Errorf("an option redeclared under the same name keeps its rows:\n%s", out)
	}
}

// routedTransport returns with a 200 the body mapped to the prefix of the requested path.
type routedTransport map[string]string

func (r routedTransport) Execute(_ context.Context, req transport.APIRequest) (transport.APIResponse, error) {
	for prefix, body := range r {
		if strings.HasPrefix(req.Path, prefix) {
			return transport.APIResponse{Status: 200, Body: []byte(body)}, nil
		}
	}
	return transport.APIResponse{}, fmt.Errorf("path not served: %s", req.Path)
}

// The refresh keeps the data source count of the database read back: it is
// what tells the plan that a destruction row count covers only one of them.
func TestRefreshManagedKeepsTheDataSourceCount(t *testing.T) {
	tr := routedTransport{
		"/v1/databases/": `{"object":"database","id":"db-1","archived":false,"in_trash":false,` +
			`"data_sources":[{"id":"ds-1","name":"A"},{"id":"ds-2","name":"B"}]}`,
		"/v1/data_sources/": `{"object":"data_source","id":"ds-1","title":[{"plain_text":"A"}],` +
			`"properties":{"Name":{"id":"title","name":"Name","type":"title"}}}`,
	}
	snap := &state.Snapshot{Version: state.Version,
		Databases: map[string]state.Database{"a": {ID: "db-1", DataSourceID: "ds-1"}}}
	got, err := refreshManaged(context.Background(), tr, snap)
	if err != nil {
		t.Fatal(err)
	}
	if got["a"].DataSources != 2 {
		t.Errorf("DataSources = %d, want 2", got["a"].DataSources)
	}
}

// A deprecated lifecycle key is still read, and every command that loads the
// config says so on stderr — stdout carries only the plan, which must stay
// usable in a pipe. The current name warns about nothing.
func TestPlanWarnsOnDeprecatedLifecycleKeys(t *testing.T) {
	for _, tc := range []struct {
		name      string
		lifecycle string
		wantWarn  bool
	}{
		{"deprecated name", "lifecycle:\n  prevent_destroy: [database.projects]\n", true},
		{"current name", "lifecycle:\n  acknowledge_destroy: [database.projects]\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeConfigDir(t, map[string]string{
				"workspace.yaml":     workspaceYAML + tc.lifecycle,
				"databases/all.yaml": twoDatabases,
			})
			for _, command := range []string{"plan", "diff"} {
				cmd := NewRootCmd()
				var out, errOut bytes.Buffer
				cmd.SetOut(&out)
				cmd.SetErr(&errOut)
				cmd.SetArgs([]string{command, "--dir", dir, "--skip-preflight"})
				if err := cmd.Execute(); err != nil {
					t.Fatalf("%s: Execute() error = %v\n%s", command, err, errOut.String())
				}
				ws := filepath.Join(dir, "workspace.yaml")
				want := "warning: " + ws + ": `lifecycle.prevent_destroy` is deprecated, it is now " +
					"`lifecycle.acknowledge_destroy` — the old name is still read in this version only\n" +
					"  → rename `prevent_destroy` to `acknowledge_destroy` in " + ws + "\n"
				if tc.wantWarn && !strings.HasPrefix(errOut.String(), want) {
					t.Errorf("%s: stderr =\n%s\nwant it to start with\n%s", command, errOut.String(), want)
				}
				if !tc.wantWarn && strings.Contains(errOut.String(), "warning:") {
					t.Errorf("%s: stderr = %q, want no warning", command, errOut.String())
				}
				if strings.Contains(out.String(), "warning:") {
					t.Errorf("%s: the warning leaked onto stdout:\n%s", command, out.String())
				}
			}
		})
	}
}
