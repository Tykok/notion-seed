// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/tykok/notion-seed/core/apply"
	"github.com/tykok/notion-seed/core/state"
)

// testDatabaseID is the UUID passed to import. The fake ntn answers with the
// internal id "db-1": the state keeps what the API returns, not what was typed.
const testDatabaseID = "1b2c3d4e-5f60-4a1b-8c2d-3e4f5a6b7c8d"

const oneDatabase = `
databases:
  - key: projects
    name: "Projects"
    properties:
      Name:
        type: title
`

// tasksWithStatus describes the database the authenticated_database scenario
// returns: two status options, with their colors and groups.
const tasksWithStatus = `
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
          - key: done
            name: "Fait"
            color: green
            group: "Complete"
`

// forceInteractive makes apply believe it talks to a terminal. The real test
// (stdin is a character device) cannot be done in `go test`, and bypassing it
// with a production flag would be worse: the injection point stays internal to
// the package.
func forceInteractive(t *testing.T) {
	t.Helper()
	previous := isInteractive
	isInteractive = func(*cobra.Command) bool { return true }
	t.Cleanup(func() { isInteractive = previous })
}

// runCmdWithStdin runs a command, feeding it a standard input.
func runCmdWithStdin(t *testing.T, input string, args ...string) (string, error) {
	t.Helper()
	cmd := NewRootCmd()
	var b bytes.Buffer
	cmd.SetOut(&b)
	cmd.SetErr(&b)
	cmd.SetIn(strings.NewReader(input))
	cmd.SetArgs(args)
	err := cmd.Execute()
	return b.String(), err
}

func mustReadFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The happy path: the creation goes out, the state is written, the report
// names it.
func TestApplyCreatesDatabaseAndWritesState(t *testing.T) {
	withFakeNtn(t, "authenticated_create")
	dir := writeConfigDir(t, map[string]string{
		"workspace.yaml":     workspaceYAML,
		"databases/all.yaml": oneDatabase,
	})

	out, err := runCmd(t, "apply", "--dir", dir, "--auto-approve")
	if err != nil {
		t.Fatalf("Execute() error = %v\n%s", err, out)
	}
	if !strings.Contains(out, "database.projects created") {
		t.Errorf("output:\n%s", out)
	}
	snap, lerr := state.Load(dir)
	if lerr != nil {
		t.Fatal(lerr)
	}
	if snap.Databases["projects"].ID != "db-new" {
		t.Errorf("state = %+v, want id db-new", snap.Databases["projects"])
	}
}

// apply is often the FIRST command that writes the state. If it does not
// record the workspace, checkWorkspaceMatch stays disarmed forever: a state
// without workspace_id is treated as "cannot check". The project could then be
// run against any workspace, and a 404 from the wrong workspace would throw
// away a perfectly valid identity.
func TestApplyRecordsTheWorkspaceInTheStateItCreates(t *testing.T) {
	withFakeNtn(t, "authenticated_create")
	dir := writeConfigDir(t, map[string]string{
		"workspace.yaml":     workspaceYAML,
		"databases/all.yaml": oneDatabase,
	})

	if out, err := runCmd(t, "apply", "--dir", dir, "--auto-approve"); err != nil {
		t.Fatalf("Execute() error = %v\n%s", err, out)
	}
	snap, err := state.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	// The id the fake ntn announces in whoami.
	if snap.WorkspaceID != "33333333-3333-4333-8333-333333333333" {
		t.Errorf("WorkspaceID = %q, want the one ntn is authenticated on", snap.WorkspaceID)
	}
}

// Cleaning up a stale state entry writes NOTHING to Notion. The confirmation
// must not announce otherwise: it is the only moment the user decides, based
// on what is written on screen.
func TestApplyDoesNotAnnounceNotionWritesForStateCleanupOnly(t *testing.T) {
	withFakeNtn(t, "authenticated_database")
	dir := writeConfigDir(t, map[string]string{
		"workspace.yaml":     workspaceYAML,
		"databases/all.yaml": tasksWithStatus,
	})
	if _, err := runCmd(t, "import", "database.tasks", testDatabaseID, "--dir", dir); err != nil {
		t.Fatalf("import: %v", err)
	}
	// The database leaves the YAML AND vanishes from Notion: a pure stale entry.
	if err := os.WriteFile(filepath.Join(dir, "databases", "all.yaml"),
		[]byte("databases: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	withFakeNtn(t, "authenticated_database_404")
	forceInteractive(t)

	out, err := runCmdWithStdin(t, "apply\n", "apply", "--dir", dir)
	if err != nil {
		t.Fatalf("Execute() error = %v\n%s", err, out)
	}
	for _, notionWrite := range []string{
		"will be created under page",
		"will be updated in Notion",
		"will be moved to the trash in Notion",
	} {
		if strings.Contains(out, notionWrite) {
			t.Errorf("the confirmation announces a Notion write (%q) for a local cleanup:\n%s", notionWrite, out)
		}
	}
	if !strings.Contains(out, "1 stale entry(ies) will be removed from the state") {
		t.Errorf("the confirmation does not say what will be touched:\n%s", out)
	}
	snap, lerr := state.Load(dir)
	if lerr != nil {
		t.Fatal(lerr)
	}
	if _, ok := snap.Databases["tasks"]; ok {
		t.Error("the stale entry was not removed")
	}
}

// A blocked plan wins over --auto-approve: consent is declarative, and the
// confirmation clears nothing. No write.
//
// notion-seed no longer blocks based on a change's class, nor on
// lifecycle.acknowledge_destroy or acknowledge_data_loss (see core/diff): both
// are only acknowledgements. The only remaining block is a resource the state
// anchors and Notion no longer knows — that is the scenario that still
// exercises a real refusal here.
func TestApplyRefusesBlockedPlanEvenWithAutoApprove(t *testing.T) {
	withFakeNtn(t, "authenticated_database")
	dir := writeConfigDir(t, map[string]string{
		"workspace.yaml":     workspaceYAML,
		"databases/all.yaml": tasksWithStatus,
	})
	if _, err := runCmd(t, "import", "database.tasks", testDatabaseID, "--dir", dir); err != nil {
		t.Fatalf("import: %v", err)
	}
	// database.tasks stays declared in the YAML — it is not an orphan — but
	// Notion no longer knows it: the plan cannot be computed, whatever
	// --auto-approve says.
	withFakeNtn(t, "authenticated_database_404")
	before := mustReadFile(t, filepath.Join(dir, state.FileName))

	out, err := runCmd(t, "apply", "--dir", dir, "--auto-approve")
	if err == nil {
		t.Fatalf("Execute() error = nil, want a refusal\n%s", out)
	}
	if !strings.Contains(out, "Plan blocked") {
		t.Errorf("output:\n%s", out)
	}
	if after := mustReadFile(t, filepath.Join(dir, state.FileName)); after != before {
		t.Error("the state was modified despite a blocked plan")
	}
}

// An already converged plan writes nothing, asks nothing, and exits 0.
func TestApplyOnConvergedPlanWritesNothing(t *testing.T) {
	withFakeNtn(t, "authenticated_database")
	dir := writeConfigDir(t, map[string]string{
		"workspace.yaml":     workspaceYAML,
		"databases/all.yaml": tasksWithStatus,
	})
	if _, err := runCmd(t, "import", "database.tasks", testDatabaseID, "--dir", dir); err != nil {
		t.Fatalf("import: %v", err)
	}
	path := filepath.Join(dir, state.FileName)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	out, aerr := runCmd(t, "apply", "--dir", dir, "--auto-approve")
	if aerr != nil {
		t.Fatalf("Execute() error = %v\n%s", aerr, out)
	}
	if !strings.Contains(out, "No changes") {
		t.Errorf("output:\n%s", out)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Error("the state was rewritten while nothing changed")
	}
}

// Any answer that is not exactly "apply" declines.
func TestApplyRefusesOnAnythingButTheWord(t *testing.T) {
	for _, answer := range []string{"", "oui", "APPLY", "y", "apply now"} {
		t.Run("answer "+answer, func(t *testing.T) {
			withFakeNtn(t, "authenticated_create")
			dir := writeConfigDir(t, map[string]string{
				"workspace.yaml":     workspaceYAML,
				"databases/all.yaml": oneDatabase,
			})
			forceInteractive(t)

			out, err := runCmdWithStdin(t, answer+"\n", "apply", "--dir", dir)
			if err == nil {
				t.Fatalf("Execute() error = nil, want a refusal for %q\n%s", answer, out)
			}
			if !strings.Contains(err.Error(), `"apply"`) {
				t.Errorf("message = %q, it must quote \"apply\" like the prompt", err.Error())
			}
			if _, serr := os.Stat(filepath.Join(dir, state.FileName)); !os.IsNotExist(serr) {
				t.Error("a state was written although the confirmation was declined")
			}
		})
	}
}

// A standard input closed without an answer is not a yes.
func TestApplyRefusesOnEOF(t *testing.T) {
	withFakeNtn(t, "authenticated_create")
	dir := writeConfigDir(t, map[string]string{
		"workspace.yaml":     workspaceYAML,
		"databases/all.yaml": oneDatabase,
	})
	forceInteractive(t)

	out, err := runCmdWithStdin(t, "", "apply", "--dir", dir)
	if err == nil {
		t.Fatalf("Execute() error = nil, want a refusal on EOF\n%s", out)
	}
	if _, serr := os.Stat(filepath.Join(dir, state.FileName)); !os.IsNotExist(serr) {
		t.Error("a state was written although no confirmation was given")
	}
}

// Ctrl-D on a real terminal: the input closes without an answer. Measured on
// 2026-09-25, the message said "standard input is not a terminal", which is
// wrong — it was one. End of input has its own message.
func TestApplyNamesEndOfInputOnATerminal(t *testing.T) {
	withFakeNtn(t, "authenticated_create")
	dir := writeConfigDir(t, map[string]string{
		"workspace.yaml":     workspaceYAML,
		"databases/all.yaml": oneDatabase,
	})
	forceInteractive(t)

	out, err := runCmdWithStdin(t, "", "apply", "--dir", dir)
	if err == nil {
		t.Fatalf("Execute() error = nil, want a refusal on end of input\n%s", out)
	}
	if strings.Contains(err.Error(), "not a terminal") {
		t.Errorf("message = %q, it blames the terminal for an end of input", err.Error())
	}
	// "rerun the command", not `notion-seed apply`: the user's flags would be
	// lost. And "apply" in double quotes, as at the prompt.
	for _, want := range []string{"end of input", "nothing was applied", "  → ",
		"rerun the command", `"apply"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message = %q, it must contain %q", err.Error(), want)
		}
	}
}

// /dev/null is a character device, like a terminal: without this test, an
// input wired to it would pass for a terminal, and its immediate end of input
// for a Ctrl-D.
func TestIsInteractiveRejectsTheNullDevice(t *testing.T) {
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Skipf("%s unavailable: %v", os.DevNull, err)
	}
	defer f.Close()
	cmd := NewRootCmd()
	cmd.SetIn(f)
	if isInteractive(cmd) {
		t.Errorf("isInteractive(%s) = true, want false", os.DevNull)
	}
}

// The exact word confirms, and the creation goes out.
func TestApplyProceedsOnExactConfirmation(t *testing.T) {
	withFakeNtn(t, "authenticated_create")
	dir := writeConfigDir(t, map[string]string{
		"workspace.yaml":     workspaceYAML,
		"databases/all.yaml": oneDatabase,
	})
	forceInteractive(t)

	out, err := runCmdWithStdin(t, "apply\n", "apply", "--dir", dir)
	if err != nil {
		t.Fatalf("Execute() error = %v\n%s", err, out)
	}
	if !strings.Contains(out, "database.projects created") {
		t.Errorf("output:\n%s", out)
	}
}

// Outside a TTY and without --auto-approve: an explicit refusal, rather than a
// run because nobody answered.
func TestApplyRefusesWithoutTTYAndWithoutAutoApprove(t *testing.T) {
	withFakeNtn(t, "authenticated_create")
	dir := writeConfigDir(t, map[string]string{
		"workspace.yaml":     workspaceYAML,
		"databases/all.yaml": oneDatabase,
	})

	out, err := runCmd(t, "apply", "--dir", dir)
	if err == nil {
		t.Fatalf("Execute() error = nil, want a refusal\n%s", out)
	}
	if !strings.Contains(err.Error(), "--auto-approve") {
		t.Errorf("message = %q, it must name --auto-approve", err.Error())
	}
	// The word to type is written as the prompt shows it: "apply".
	if !strings.Contains(err.Error(), `"apply"`) {
		t.Errorf("message = %q, it must quote \"apply\" in double quotes", err.Error())
	}
	if _, serr := os.Stat(filepath.Join(dir, state.FileName)); !os.IsNotExist(serr) {
		t.Error("a state was written without any possible confirmation")
	}
}

// Writing offline makes no sense, as for import.
func TestApplyRejectsSkipPreflight(t *testing.T) {
	dir := writeConfigDir(t, map[string]string{
		"workspace.yaml":     workspaceYAML,
		"databases/all.yaml": oneDatabase,
	})
	out, err := runCmd(t, "apply", "--dir", dir, "--skip-preflight", "--auto-approve")
	if err == nil {
		t.Fatalf("Execute() error = nil, want a refusal\n%s", out)
	}
	if !strings.Contains(err.Error(), "--skip-preflight") {
		t.Errorf("message = %q, it must name --skip-preflight", err.Error())
	}
}

// An API refusal stops the series without rolling anything back, and the state
// keeps nothing.
func TestApplyStopsWithoutRollbackWhenAPIRefuses(t *testing.T) {
	withFakeNtn(t, "authenticated_create_refused")
	dir := writeConfigDir(t, map[string]string{
		"workspace.yaml":     workspaceYAML,
		"databases/all.yaml": oneDatabase,
	})

	out, err := runCmd(t, "apply", "--dir", dir, "--auto-approve")
	if err == nil {
		t.Fatalf("Execute() error = nil, want the creation failure\n%s", out)
	}
	if _, serr := os.Stat(filepath.Join(dir, state.FileName)); !os.IsNotExist(serr) {
		t.Error("a state was written although no creation succeeded")
	}
}

// apply shares planOptions with plan: --fail-on therefore shows in its help. A
// flag shown then ignored would be worse than no flag — the CI that WRITES is
// precisely the one that believes it is protected. apply must therefore stop
// on the requested class, before writing anything.
func TestApplyHonoursFailOnBeforeWriting(t *testing.T) {
	withFakeNtn(t, "authenticated_database")
	dir := writeConfigDir(t, map[string]string{
		"workspace.yaml":     workspaceYAML,
		"databases/all.yaml": statusWithoutFait,
	})
	if _, err := runCmd(t, "import", "database.tasks", testDatabaseID, "--dir", dir); err != nil {
		t.Fatalf("import: %v", err)
	}

	out, err := runCmd(t, "apply", "--dir", dir, "--auto-approve",
		"--fail-on=silent-rewrite")
	if err == nil {
		t.Fatalf("apply error = nil, want a failure\n%s", out)
	}
	// The message must be the --fail-on one, not the non-convergence one:
	// otherwise nothing proves the flag did anything.
	if !strings.Contains(err.Error(), "--fail-on") {
		t.Errorf("message = %q, it must say --fail-on triggered", err.Error())
	}
	if !strings.Contains(err.Error(), "silent rewrite") {
		t.Errorf("message = %q, it must name the class that triggered", err.Error())
	}
}

// tasksWithRenamedOption takes tasksWithStatus and changes the NAME of the
// "Fait" option without touching its key: the API cannot rename an option, so
// the resource is withheld.
const tasksWithRenamedOption = `
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
          - key: done
            name: "Terminé"
            color: green
            group: "Complete"
`

// tasksWithEstimate adds a property to the imported database: an update this
// version writes.
const tasksWithEstimate = tasksWithStatus + `      Estimate:
        type: number
`

// importThenDeclare imports the database of fakentn's stateful scenario, then
// replaces the YAML with the test's, in the SAME directory.
//
// The import always uses tasksWithStatus: import joins option keys BY NAME, so
// importing with a changed option name would leave the renamed option without
// a key, and the plan would show a removal followed by an addition instead of
// a migration. The YAML declared AFTER the import carries the change to test.
//
// The scenario keeps the PATCHes it receives in a file and merges them into
// its next reads: without that, the read-back following a write would return
// the previous state, and apply would report a mismatch that does not exist.
func importThenDeclare(t *testing.T, databasesYAML string) string {
	t.Helper()
	withFakeNtn(t, "authenticated_database_updatable")
	t.Setenv("FAKE_NTN_STATE_FILE", filepath.Join(t.TempDir(), "fakentn-state.json"))
	dir := writeConfigDir(t, map[string]string{
		"workspace.yaml":     workspaceYAML,
		"databases/all.yaml": tasksWithStatus,
	})
	if out, err := runCmd(t, "import", "database.tasks", testDatabaseID, "--dir", dir); err != nil {
		t.Fatalf("import: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(dir, "databases", "all.yaml"),
		[]byte(databasesYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// applyAfterImport sets up importThenDeclare then runs apply without
// confirmation. The update tests of this file share this setup: what sets them
// apart is the YAML, not the plumbing.
func applyAfterImport(t *testing.T, databasesYAML string) (string, error) {
	t.Helper()
	dir := importThenDeclare(t, databasesYAML)
	return runCmd(t, "apply", "--dir", dir, "--auto-approve")
}

func TestApplyWithholdsARenamedOptionAndSaysWhy(t *testing.T) {
	out, err := applyAfterImport(t, tasksWithRenamedOption)
	if err == nil {
		t.Fatalf("Execute() error = nil, want an apply that did not converge\n%s", out)
	}
	if !strings.Contains(out, "Withheld — migration required") {
		t.Errorf("output:\n%s", out)
	}
	if !strings.Contains(out, "database.tasks") {
		t.Errorf("the section does not name the resource:\n%s", out)
	}
	// The reason, not just the fact: a resource skipped without a reason leaves
	// the user guessing.
	if !strings.Contains(out, "migrated by hand") {
		t.Errorf("the section does not say what to do:\n%s", out)
	}
	// The summary must count the withheld resource while apply fails
	// because of this resource.
	if !strings.Contains(out, "Withheld: 1 resource(s)") {
		t.Errorf("the summary does not count the withheld resource:\n%s", out)
	}
	// Nothing is written: the count is the cost of the remedy, not a loss.
	if !strings.Contains(out, `2 rows hold "Fait": migrate them by hand`) {
		t.Errorf("the line does not quantify the migration to do:\n%s", out)
	}
	if strings.Contains(out, "reassigned") {
		t.Errorf("a withheld resource is announced as reassigning rows:\n%s", out)
	}
}

// A withheld resource does not go out: no PATCH, state intact.
func TestApplyWritesNothingForAWithheldResource(t *testing.T) {
	dir := importThenDeclare(t, tasksWithRenamedOption)
	before := mustReadFile(t, filepath.Join(dir, state.FileName))

	if out, err := runCmd(t, "apply", "--dir", dir, "--auto-approve"); err == nil {
		t.Fatalf("Execute() error = nil, want an apply that did not converge\n%s", out)
	}
	if after := mustReadFile(t, filepath.Join(dir, state.FileName)); after != before {
		t.Error("the state was rewritten for a withheld resource")
	}
	if _, err := os.Stat(os.Getenv("FAKE_NTN_STATE_FILE")); !os.IsNotExist(err) {
		t.Error("a PATCH went out for a withheld resource")
	}
}

// The update announcement precedes the confirmation: it is the only moment the
// user decides, based on what is on screen.
func TestApplyAnnouncesUpdatesBeforeConfirmation(t *testing.T) {
	dir := importThenDeclare(t, tasksWithEstimate)
	forceInteractive(t)

	out, err := runCmdWithStdin(t, "apply\n", "apply", "--dir", dir)
	if err != nil {
		t.Fatalf("Execute() error = %v\n%s", err, out)
	}
	announce := strings.Index(out, "will be updated")
	prompt := strings.Index(out, "to confirm:")
	if announce < 0 || prompt < 0 || announce > prompt {
		t.Errorf("sortie:\n%s\nwant the update announcement before the confirmation", out)
	}
}

// The update happy path, end to end: the property goes out, the read-back
// reports it, the state records it, the report names it, and the next plan is
// empty.
func TestApplyWritesAnUpdateAndConverges(t *testing.T) {
	dir := importThenDeclare(t, tasksWithEstimate)
	out, err := runCmd(t, "apply", "--dir", dir, "--auto-approve")
	if err != nil {
		t.Fatalf("Execute() error = %v\n%s", err, out)
	}
	if !strings.Contains(out, "~ database.tasks updated") {
		t.Errorf("the report does not name the update:\n%s", out)
	}
	if !strings.Contains(out, "1 updated") {
		t.Errorf("the summary does not count the update:\n%s", out)
	}

	snap, lerr := state.Load(dir)
	if lerr != nil {
		t.Fatal(lerr)
	}
	if _, ok := snap.Databases["tasks"].Properties["Estimate"]; !ok {
		t.Errorf("state = %+v, want the Estimate property read back", snap.Databases["tasks"])
	}

	planOut, perr := runCmd(t, "plan", "--dir", dir)
	if perr != nil {
		t.Fatalf("plan: %v\n%s", perr, planOut)
	}
	if !strings.Contains(planOut, "No changes") {
		t.Errorf("the plan following a successful apply is not empty:\n%s", planOut)
	}
}

// orphanYAML removes every database from the YAML: the imported database
// becomes an orphan, and Notion still holds it.
const orphanYAML = "databases: []\n"

// trashLine is the ONLY write a destruction must produce.
const trashLine = `PATCH /v1/databases/db-1 {"in_trash":true}` + "\n"

// withMutationLog asks the stateful scenario to log every write it receives,
// and returns the log path.
func withMutationLog(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fakentn.log")
	t.Setenv("FAKE_NTN_LOG_FILE", path)
	return path
}

// readMutationLog returns the log, "" if it was never created: no write went
// out.
func readMutationLog(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The destruction happy path, end to end:
//   - the announcement has its own line, before the confirmation, distinct from the cleanup;
//   - a single PATCH goes out, and it carries only the trashing;
//   - the entry leaves the state;
//   - the next plan is empty.
func TestApplyTrashesAnOrphanAndConverges(t *testing.T) {
	logPath := withMutationLog(t)
	dir := importThenDeclare(t, orphanYAML)
	forceInteractive(t)

	out, err := runCmdWithStdin(t, "apply\n", "apply", "--dir", dir)
	if err != nil {
		t.Fatalf("Execute() error = %v\n%s", err, out)
	}
	announce := strings.Index(out, "1 database(s) will be moved to the trash")
	prompt := strings.Index(out, "to confirm:")
	if announce < 0 || prompt < 0 || announce > prompt {
		t.Errorf("sortie:\n%s\nwant the trash announcement before the confirmation", out)
	}
	// A destruction is not a cleanup: it writes to Notion.
	if strings.Contains(out, "stale entry(ies)") {
		t.Errorf("the destruction is announced as a local cleanup:\n%s", out)
	}
	if !strings.Contains(out, "- database.tasks moved to the trash") {
		t.Errorf("the report does not name the destruction:\n%s", out)
	}
	if !strings.Contains(out, "1 trashed") {
		t.Errorf("the summary does not count the destruction:\n%s", out)
	}
	if got := readMutationLog(t, logPath); got != trashLine {
		t.Errorf("writes = %q, want exactly %q", got, trashLine)
	}

	snap, lerr := state.Load(dir)
	if lerr != nil {
		t.Fatal(lerr)
	}
	if _, ok := snap.Databases["tasks"]; ok {
		t.Error("the entry of the trashed database stayed in the state")
	}
	planOut, perr := runCmd(t, "plan", "--dir", dir)
	if perr != nil {
		t.Fatalf("plan: %v\n%s", perr, planOut)
	}
	if !strings.Contains(planOut, "No changes") {
		t.Errorf("the plan following a destruction is not empty:\n%s", planOut)
	}
}

// Measured on 2026-09-25: the destruction was announced without saying how
// many rows the database takes with it. The fake ntn holds 3 — and only 2 hold
// "Fait", so a 2 would betray a filtered query instead of the count of all
// rows.
func TestPlanCountsTheRowsADestroyTakesWithIt(t *testing.T) {
	dir := importThenDeclare(t, orphanYAML)

	out, err := runCmd(t, "plan", "--dir", dir)
	if err != nil {
		t.Fatalf("plan: %v\n%s", err, out)
	}
	for _, want := range []string{
		"  - database.tasks  [destructive]",
		"          → 3 row(s) go to the trash with it.",
		"Impact: 1 database(s) in the trash with 3 row(s).",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output:\n%s\nwant %q", out, want)
		}
	}
}

// Review Focus #4: acknowledge_destroy is an acknowledgement of reading. The
// destruction goes out, and the mention stays displayed — under the key as the
// user wrote it, so the line matches their YAML even under the deprecated name.
func TestApplyTrashesAnAcknowledgedDatabase(t *testing.T) {
	for _, key := range []string{"acknowledge_destroy", "prevent_destroy"} {
		t.Run(key, func(t *testing.T) {
			logPath := withMutationLog(t)
			dir := importThenDeclare(t, orphanYAML)
			if err := os.WriteFile(filepath.Join(dir, "workspace.yaml"),
				[]byte(workspaceYAML+"lifecycle:\n  "+key+":\n    - database.tasks\n"), 0o644); err != nil {
				t.Fatal(err)
			}

			out, err := runCmd(t, "apply", "--dir", dir, "--auto-approve")
			if err != nil {
				t.Fatalf("Execute() error = %v\n%s", err, out)
			}
			if !strings.Contains(out, "→ declared in lifecycle."+key+".") {
				t.Errorf("the %s mention disappeared:\n%s", key, out)
			}
			if got := readMutationLog(t, logPath); got != trashLine {
				t.Errorf("writes = %q, want exactly %q", got, trashLine)
			}
		})
	}
}

// Review Focus #5: a CI that asked for --fail-on=destructive NEVER moves a
// database to the trash. Zero writes, state intact.
func TestApplyFailOnDestructiveTrashesNothing(t *testing.T) {
	logPath := withMutationLog(t)
	dir := importThenDeclare(t, orphanYAML)
	before := mustReadFile(t, filepath.Join(dir, state.FileName))

	out, err := runCmd(t, "apply", "--dir", dir, "--auto-approve", "--fail-on=destructive")
	if err == nil {
		t.Fatalf("Execute() error = nil, want the --fail-on refusal\n%s", out)
	}
	if !strings.Contains(err.Error(), "--fail-on") {
		t.Errorf("message = %q, it must say --fail-on triggered", err.Error())
	}
	if got := readMutationLog(t, logPath); got != "" {
		t.Errorf("writes = %q, want none", got)
	}
	if after := mustReadFile(t, filepath.Join(dir, state.FileName)); after != before {
		t.Error("the state was rewritten despite --fail-on")
	}
}

// Spec §5, common configuration: without a withheld resource, apply announces
// exactly the plan's aggregate — destruction included, now that it writes it.
func TestApplyShowsTheSameImpactAsPlanWhenNothingIsWithheld(t *testing.T) {
	dir := importThenDeclare(t, orphanYAML)
	const want = "Impact: 1 database(s) in the trash with 3 row(s)."

	planOut, perr := runCmd(t, "plan", "--dir", dir)
	if perr != nil {
		t.Fatalf("plan: %v\n%s", perr, planOut)
	}
	if !strings.Contains(planOut, want) {
		t.Fatalf("broken test setup: the plan must carry %q\n%s", want, planOut)
	}
	applyOut, aerr := runCmd(t, "apply", "--dir", dir, "--auto-approve")
	if aerr != nil {
		t.Fatalf("apply: %v\n%s", aerr, applyOut)
	}
	if !strings.Contains(applyOut, want) {
		t.Errorf("apply does not announce the plan's aggregate:\n%s", applyOut)
	}
}

// tasksRenamedWithoutTodo renames "Fait" AND removes "À faire". The rename
// withholds the resource; the removal, which apply will therefore not cause,
// carries a measured impact that plan aggregates.
const tasksRenamedWithoutTodo = `
databases:
  - key: tasks
    name: "Tasks"
    properties:
      Name:
        type: title
      Statut:
        type: status
        options:
          - key: done
            name: "Terminé"
            color: green
            group: "Complete"
`

// Spec §5, second configuration: a withheld resource keeps an impact apply
// will not cause. Its aggregate is therefore strictly lower than plan's — here,
// zero.
func TestApplyLeavesAWithheldResourceOutOfItsImpact(t *testing.T) {
	dir := importThenDeclare(t, tasksRenamedWithoutTodo)

	planOut, perr := runCmd(t, "plan", "--dir", dir)
	if perr != nil {
		t.Fatalf("plan: %v\n%s", perr, planOut)
	}
	if !strings.Contains(planOut, "Impact: 2 values reassigned without a trace.") {
		t.Fatalf("broken test setup: the plan must aggregate the removal\n%s", planOut)
	}
	applyOut, aerr := runCmd(t, "apply", "--dir", dir, "--auto-approve")
	if aerr == nil {
		t.Fatalf("apply error = nil, want an apply that did not converge\n%s", applyOut)
	}
	if !strings.Contains(applyOut, "Withheld — migration required") {
		t.Fatalf("broken test setup: the resource must be withheld\n%s", applyOut)
	}
	if strings.Contains(applyOut, "Impact:") {
		t.Errorf("apply aggregates the impact of a resource it does not write:\n%s", applyOut)
	}
	// Leftover from batch A: the summary no longer follows the "Withheld"
	// section by two blank lines.
	if strings.Contains(applyOut, "\n\n\nApplied") {
		t.Errorf("double blank line before the summary:\n%s", applyOut)
	}
}

// An apply whose only write is an unconfirmed trashing fails: its summary
// must still say nothing was done, and follow the mismatches by a single blank
// line.
func TestApplyReportKeepsItsSummaryWhenOnlyAMismatchRemains(t *testing.T) {
	var b bytes.Buffer
	renderReport(&b, apply.Report{Mismatches: []string{
		"database.tasks — the API answered without moving the database to the trash",
	}}, 0)
	out := b.String()
	if !strings.Contains(out, "Applied: 0 created, 0 updated, 0 trashed") {
		t.Errorf("summary missing:\n%s", out)
	}
	if strings.Contains(out, "\n\n\nApplied") {
		t.Errorf("double blank line before the summary:\n%s", out)
	}
}
