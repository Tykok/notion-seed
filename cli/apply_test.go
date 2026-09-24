// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/tykok/notion-seed/core/state"
)

// testDatabaseID est l'UUID passé à import. Le faux ntn répond avec l'id
// interne "db-1" : le state retient ce que l'API rend, pas ce qu'on a tapé.
const testDatabaseID = "1b2c3d4e-5f60-4a1b-8c2d-3e4f5a6b7c8d"

const oneDatabase = `
databases:
  - key: projects
    name: "Projects"
    properties:
      Name:
        type: title
`

// tasksWithStatus décrit la database que le scénario authenticated_database
// rend : deux options de status, avec leurs couleurs et leurs groupes.
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

// forceInteractive fait croire à apply qu'il parle à un terminal. Le vrai test
// (stdin est un périphérique caractère) est infaisable dans `go test`, et le
// contourner par un flag de production serait pire : le point d'injection reste
// interne au paquet.
func forceInteractive(t *testing.T) {
	t.Helper()
	previous := isInteractive
	isInteractive = func(*cobra.Command) bool { return true }
	t.Cleanup(func() { isInteractive = previous })
}

// runCmdWithStdin exécute une commande en lui fournissant une entrée standard.
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

// Le chemin heureux : la création part, le state est écrit, le compte rendu la
// nomme.
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
	if !strings.Contains(out, "database.projects créée") {
		t.Errorf("sortie:\n%s", out)
	}
	snap, lerr := state.Load(dir)
	if lerr != nil {
		t.Fatal(lerr)
	}
	if snap.Databases["projects"].ID != "db-new" {
		t.Errorf("state = %+v, want l'id db-new", snap.Databases["projects"])
	}
}

// Un plan bloqué gagne sur --auto-approve : le consentement est déclaratif, et
// la confirmation ne lève rien. Aucune écriture.
func TestApplyRefusesBlockedPlanEvenWithAutoApprove(t *testing.T) {
	withFakeNtn(t, "authenticated_database")
	// La database importée porte l'option "Fait" que le YAML ne déclare plus :
	// retrait d'une option de status, donc réécriture silencieuse.
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
	before := mustReadFile(t, filepath.Join(dir, state.FileName))

	out, err := runCmd(t, "apply", "--dir", dir, "--auto-approve")
	if err == nil {
		t.Fatalf("Execute() error = nil, want un refus\n%s", out)
	}
	if !strings.Contains(out, "Plan bloqué") {
		t.Errorf("sortie:\n%s", out)
	}
	if after := mustReadFile(t, filepath.Join(dir, state.FileName)); after != before {
		t.Error("le state a été modifié malgré un plan bloqué")
	}
}

// Un plan déjà convergé n'écrit rien, ne demande rien, et sort en 0.
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
	if !strings.Contains(out, "Aucun changement") {
		t.Errorf("sortie:\n%s", out)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Error("le state a été réécrit alors que rien ne changeait")
	}
}

// Toute réponse qui n'est pas exactement « apply » refuse.
func TestApplyRefusesOnAnythingButTheWord(t *testing.T) {
	for _, answer := range []string{"", "oui", "APPLY", "y", "apply now"} {
		t.Run("réponse "+answer, func(t *testing.T) {
			withFakeNtn(t, "authenticated_create")
			dir := writeConfigDir(t, map[string]string{
				"workspace.yaml":     workspaceYAML,
				"databases/all.yaml": oneDatabase,
			})
			forceInteractive(t)

			out, err := runCmdWithStdin(t, answer+"\n", "apply", "--dir", dir)
			if err == nil {
				t.Fatalf("Execute() error = nil, want un refus pour %q\n%s", answer, out)
			}
			if _, serr := os.Stat(filepath.Join(dir, state.FileName)); !os.IsNotExist(serr) {
				t.Error("un state a été écrit alors que la confirmation a été refusée")
			}
		})
	}
}

// Une entrée standard fermée sans réponse n'est pas un oui.
func TestApplyRefusesOnEOF(t *testing.T) {
	withFakeNtn(t, "authenticated_create")
	dir := writeConfigDir(t, map[string]string{
		"workspace.yaml":     workspaceYAML,
		"databases/all.yaml": oneDatabase,
	})
	forceInteractive(t)

	out, err := runCmdWithStdin(t, "", "apply", "--dir", dir)
	if err == nil {
		t.Fatalf("Execute() error = nil, want un refus sur EOF\n%s", out)
	}
	if _, serr := os.Stat(filepath.Join(dir, state.FileName)); !os.IsNotExist(serr) {
		t.Error("un state a été écrit alors qu'aucune confirmation n'a été donnée")
	}
}

// Le mot exact confirme, et la création part.
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
	if !strings.Contains(out, "database.projects créée") {
		t.Errorf("sortie:\n%s", out)
	}
}

// Hors TTY et sans --auto-approve : refus explicite, plutôt qu'une exécution
// parce que personne ne répondait.
func TestApplyRefusesWithoutTTYAndWithoutAutoApprove(t *testing.T) {
	withFakeNtn(t, "authenticated_create")
	dir := writeConfigDir(t, map[string]string{
		"workspace.yaml":     workspaceYAML,
		"databases/all.yaml": oneDatabase,
	})

	out, err := runCmd(t, "apply", "--dir", dir)
	if err == nil {
		t.Fatalf("Execute() error = nil, want un refus\n%s", out)
	}
	if !strings.Contains(err.Error(), "--auto-approve") {
		t.Errorf("message = %q, il doit nommer --auto-approve", err.Error())
	}
	if _, serr := os.Stat(filepath.Join(dir, state.FileName)); !os.IsNotExist(serr) {
		t.Error("un state a été écrit sans confirmation possible")
	}
}

// Écrire hors ligne n'a pas de sens, comme pour import.
func TestApplyRejectsSkipPreflight(t *testing.T) {
	dir := writeConfigDir(t, map[string]string{
		"workspace.yaml":     workspaceYAML,
		"databases/all.yaml": oneDatabase,
	})
	out, err := runCmd(t, "apply", "--dir", dir, "--skip-preflight", "--auto-approve")
	if err == nil {
		t.Fatalf("Execute() error = nil, want un refus\n%s", out)
	}
	if !strings.Contains(err.Error(), "--skip-preflight") {
		t.Errorf("message = %q, il doit nommer --skip-preflight", err.Error())
	}
}

// Un refus de l'API arrête la série sans rien annuler, et le state ne retient
// rien.
func TestApplyStopsWithoutRollbackWhenAPIRefuses(t *testing.T) {
	withFakeNtn(t, "authenticated_create_refused")
	dir := writeConfigDir(t, map[string]string{
		"workspace.yaml":     workspaceYAML,
		"databases/all.yaml": oneDatabase,
	})

	out, err := runCmd(t, "apply", "--dir", dir, "--auto-approve")
	if err == nil {
		t.Fatalf("Execute() error = nil, want l'échec de création\n%s", out)
	}
	if _, serr := os.Stat(filepath.Join(dir, state.FileName)); !os.IsNotExist(serr) {
		t.Error("un state a été écrit alors qu'aucune création n'a abouti")
	}
}

// Ce qu'apply ne sait pas écrire est nommé avant la confirmation, et le code de
// sortie dit que le plan n'a pas convergé.
func TestApplyNamesWhatItCannotWriteAndFails(t *testing.T) {
	withFakeNtn(t, "authenticated_database")
	// La database importée n'a pas la propriété Estimate : c'est un update, que
	// cette version n'écrit pas.
	dir := writeConfigDir(t, map[string]string{
		"workspace.yaml": workspaceYAML,
		"databases/all.yaml": tasksWithStatus + `      Estimate:
        type: number
`,
	})
	if _, err := runCmd(t, "import", "database.tasks", testDatabaseID, "--dir", dir); err != nil {
		t.Fatalf("import: %v", err)
	}

	out, err := runCmd(t, "apply", "--dir", dir, "--auto-approve")
	if err == nil {
		t.Fatalf("Execute() error = nil, want un apply non convergé\n%s", out)
	}
	if !strings.Contains(out, "Non appliqué par cette version") {
		t.Errorf("sortie:\n%s", out)
	}
	if !strings.Contains(out, "database.tasks") {
		t.Errorf("la section ne nomme pas la ressource:\n%s", out)
	}
}
