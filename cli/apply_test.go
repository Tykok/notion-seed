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

// apply est souvent la PREMIÈRE commande qui écrit le state. S'il n'y inscrit
// pas le workspace, checkWorkspaceMatch reste désarmé pour toujours : un state
// sans workspace_id est traité comme « on ne peut pas vérifier ». Le projet
// serait alors jouable contre n'importe quel workspace, et un 404 venu du
// mauvais workspace ferait jeter une identité parfaitement valide.
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
	// L'id que le faux ntn annonce dans whoami.
	if snap.WorkspaceID != "33333333-3333-4333-8333-333333333333" {
		t.Errorf("WorkspaceID = %q, want celui sur lequel ntn est authentifié", snap.WorkspaceID)
	}
}

// Le nettoyage d'une entrée de state obsolète n'écrit RIEN dans Notion. La
// confirmation ne doit pas annoncer le contraire : c'est le seul moment où
// l'utilisateur décide, sur la foi de ce qui est écrit à l'écran.
func TestApplyDoesNotAnnounceNotionWritesForStateCleanupOnly(t *testing.T) {
	withFakeNtn(t, "authenticated_database")
	dir := writeConfigDir(t, map[string]string{
		"workspace.yaml":     workspaceYAML,
		"databases/all.yaml": tasksWithStatus,
	})
	if _, err := runCmd(t, "import", "database.tasks", testDatabaseID, "--dir", dir); err != nil {
		t.Fatalf("import: %v", err)
	}
	// La database sort du YAML ET disparaît de Notion : entrée obsolète pure.
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
	if strings.Contains(out, "vont partir dans la page") {
		t.Errorf("la confirmation annonce une écriture Notion pour un nettoyage local:\n%s", out)
	}
	if !strings.Contains(out, "state") {
		t.Errorf("la confirmation ne dit pas ce qui va être touché:\n%s", out)
	}
	snap, lerr := state.Load(dir)
	if lerr != nil {
		t.Fatal(lerr)
	}
	if _, ok := snap.Databases["tasks"]; ok {
		t.Error("l'entrée obsolète n'a pas été retirée")
	}
}

// Un plan bloqué gagne sur --auto-approve : le consentement est déclaratif, et
// la confirmation ne lève rien. Aucune écriture.
//
// notion-seed ne bloque plus sur la foi de la classe d'un changement, ni sur
// lifecycle.prevent_destroy ou allow_data_loss (voir core/diff) : les deux ne
// sont plus que des accusés de lecture. Le seul blocage qui subsiste est une
// ressource que le state ancre et que Notion ne connaît plus — c'est le
// scénario qui exerce encore un vrai refus ici.
func TestApplyRefusesBlockedPlanEvenWithAutoApprove(t *testing.T) {
	withFakeNtn(t, "authenticated_database")
	dir := writeConfigDir(t, map[string]string{
		"workspace.yaml":     workspaceYAML,
		"databases/all.yaml": tasksWithStatus,
	})
	if _, err := runCmd(t, "import", "database.tasks", testDatabaseID, "--dir", dir); err != nil {
		t.Fatalf("import: %v", err)
	}
	// database.tasks reste déclarée dans le YAML — ce n'est pas une orpheline —
	// mais Notion ne la connaît plus : le plan devient incalculable, quel que
	// soit --auto-approve.
	withFakeNtn(t, "authenticated_database_404")
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

// apply partage planOptions avec plan : --fail-on y apparaît donc dans l'aide.
// Un flag affiché puis ignoré serait pire que pas de flag — la CI qui ÉCRIT est
// justement celle qui croit se protéger. apply doit donc s'arrêter sur la
// classe demandée, avant d'écrire quoi que ce soit.
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
		t.Fatalf("apply error = nil, want un échec\n%s", out)
	}
	// Le message doit être celui de --fail-on, pas celui de la non-convergence :
	// sinon rien ne prouve que le flag a servi à quelque chose.
	if !strings.Contains(err.Error(), "--fail-on") {
		t.Errorf("message = %q, il doit dire que --fail-on a déclenché", err.Error())
	}
	if !strings.Contains(err.Error(), "réécriture silencieuse") {
		t.Errorf("message = %q, il doit nommer la classe qui a déclenché", err.Error())
	}
}

// tasksWithRenamedOption reprend tasksWithStatus en changeant le NOM de
// l'option "Fait" sans toucher à sa key : l'API ne sait pas renommer une
// option, donc la ressource est retenue.
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

// tasksWithEstimate ajoute une propriété à la database importée : un update
// que cette version écrit.
const tasksWithEstimate = tasksWithStatus + `      Estimate:
        type: number
`

// importThenDeclare importe la database du scénario à état de fakentn, puis
// remplace le YAML par celui du test, dans le MÊME dossier.
//
// L'import se fait toujours avec tasksWithStatus : import joint les keys
// d'options PAR NOM, donc importer avec un nom d'option changé laisserait
// l'option renommée sans key, et le plan montrerait un retrait suivi d'un ajout
// au lieu d'une migration. C'est le YAML déclaré APRÈS l'import qui porte le
// changement à tester.
//
// Le scénario garde en fichier les PATCH qu'il reçoit et les fusionne dans ses
// lectures suivantes : sans ça, la relecture qui suit une écriture rendrait
// l'état d'avant, et apply signalerait un écart qui n'existe pas.
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

// applyAfterImport monte importThenDeclare puis lance apply sans confirmation.
// Les tests d'update de ce fichier partagent ce montage : ce qui les distingue
// est le YAML, pas la plomberie.
func applyAfterImport(t *testing.T, databasesYAML string) (string, error) {
	t.Helper()
	dir := importThenDeclare(t, databasesYAML)
	return runCmd(t, "apply", "--dir", dir, "--auto-approve")
}

func TestApplyWithholdsARenamedOptionAndSaysWhy(t *testing.T) {
	out, err := applyAfterImport(t, tasksWithRenamedOption)
	if err == nil {
		t.Fatalf("Execute() error = nil, want un apply non convergé\n%s", out)
	}
	if !strings.Contains(out, "Retenu — migration requise") {
		t.Errorf("sortie:\n%s", out)
	}
	if !strings.Contains(out, "database.tasks") {
		t.Errorf("la section ne nomme pas la ressource:\n%s", out)
	}
	// La raison, pas seulement le fait : une ressource sautée sans motif renvoie
	// l'utilisateur deviner.
	if !strings.Contains(out, "migrée à la main") {
		t.Errorf("la section ne dit pas quoi faire:\n%s", out)
	}
	// Le bilan doit compter la ressource retenue alors qu'apply échoue
	// à cause de cette ressource.
	if !strings.Contains(out, "Retenu : 1 ressource(s)") {
		t.Errorf("le bilan ne compte pas la ressource retenue:\n%s", out)
	}
	// Rien n'est écrit : le compte est le coût du remède, pas une perte.
	if !strings.Contains(out, `2 lignes portent "Fait" : à migrer à la main`) {
		t.Errorf("la ligne ne chiffre pas la migration à faire:\n%s", out)
	}
	if strings.Contains(out, "réassignées") {
		t.Errorf("une ressource retenue est annoncée comme réassignant des lignes:\n%s", out)
	}
}

// Une ressource retenue ne part pas : aucun PATCH, state intact.
func TestApplyWritesNothingForAWithheldResource(t *testing.T) {
	dir := importThenDeclare(t, tasksWithRenamedOption)
	before := mustReadFile(t, filepath.Join(dir, state.FileName))

	if out, err := runCmd(t, "apply", "--dir", dir, "--auto-approve"); err == nil {
		t.Fatalf("Execute() error = nil, want un apply non convergé\n%s", out)
	}
	if after := mustReadFile(t, filepath.Join(dir, state.FileName)); after != before {
		t.Error("le state a été réécrit pour une ressource retenue")
	}
	if _, err := os.Stat(os.Getenv("FAKE_NTN_STATE_FILE")); !os.IsNotExist(err) {
		t.Error("un PATCH est parti pour une ressource retenue")
	}
}

// L'annonce des modifications précède la confirmation : c'est le seul moment où
// l'utilisateur décide, sur la foi de ce qui est à l'écran.
func TestApplyAnnouncesUpdatesBeforeConfirmation(t *testing.T) {
	dir := importThenDeclare(t, tasksWithEstimate)
	forceInteractive(t)

	out, err := runCmdWithStdin(t, "apply\n", "apply", "--dir", dir)
	if err != nil {
		t.Fatalf("Execute() error = %v\n%s", err, out)
	}
	announce := strings.Index(out, "vont être modifiées")
	prompt := strings.Index(out, "Confirmez")
	if announce < 0 || prompt < 0 || announce > prompt {
		t.Errorf("sortie:\n%s\nwant l'annonce des modifications avant la confirmation", out)
	}
}

// Le chemin heureux de l'update, de bout en bout : la propriété part, la
// relecture la rapporte, le state l'inscrit, le compte rendu la nomme, et le
// plan suivant est vide.
func TestApplyWritesAnUpdateAndConverges(t *testing.T) {
	dir := importThenDeclare(t, tasksWithEstimate)
	out, err := runCmd(t, "apply", "--dir", dir, "--auto-approve")
	if err != nil {
		t.Fatalf("Execute() error = %v\n%s", err, out)
	}
	if !strings.Contains(out, "~ database.tasks modifiée") {
		t.Errorf("le compte rendu ne nomme pas la modification:\n%s", out)
	}
	if !strings.Contains(out, "1 modification(s)") {
		t.Errorf("le bilan ne compte pas la modification:\n%s", out)
	}

	snap, lerr := state.Load(dir)
	if lerr != nil {
		t.Fatal(lerr)
	}
	if _, ok := snap.Databases["tasks"].Properties["Estimate"]; !ok {
		t.Errorf("state = %+v, want la propriété Estimate relue", snap.Databases["tasks"])
	}

	planOut, perr := runCmd(t, "plan", "--dir", dir)
	if perr != nil {
		t.Fatalf("plan: %v\n%s", perr, planOut)
	}
	if !strings.Contains(planOut, "Aucun changement") {
		t.Errorf("le plan qui suit un apply réussi n'est pas vide:\n%s", planOut)
	}
}

// orphanYAML retire toute database du YAML : la database importée devient
// orpheline, et Notion la porte toujours.
const orphanYAML = "databases: []\n"

// trashLine est la SEULE écriture qu'une destruction doit produire.
const trashLine = `PATCH /v1/databases/db-1 {"in_trash":true}` + "\n"

// withMutationLog demande au scénario à état de journaliser chaque écriture
// qu'il reçoit, et rend le chemin du journal.
func withMutationLog(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fakentn.log")
	t.Setenv("FAKE_NTN_LOG_FILE", path)
	return path
}

// readMutationLog rend le journal, "" s'il n'a jamais été créé : aucune
// écriture n'est partie.
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

// Le chemin heureux de la destruction, de bout en bout :
//   - l'annonce a sa propre ligne, avant la confirmation, distincte du nettoyage ;
//   - un seul PATCH part, et il ne porte que la corbeille ;
//   - l'entrée quitte le state ;
//   - le plan suivant est vide.
func TestApplyTrashesAnOrphanAndConverges(t *testing.T) {
	logPath := withMutationLog(t)
	dir := importThenDeclare(t, orphanYAML)
	forceInteractive(t)

	out, err := runCmdWithStdin(t, "apply\n", "apply", "--dir", dir)
	if err != nil {
		t.Fatalf("Execute() error = %v\n%s", err, out)
	}
	announce := strings.Index(out, "1 database(s) vont être mises à la corbeille")
	prompt := strings.Index(out, "Confirmez")
	if announce < 0 || prompt < 0 || announce > prompt {
		t.Errorf("sortie:\n%s\nwant l'annonce de la corbeille avant la confirmation", out)
	}
	// Une destruction n'est pas un nettoyage : elle écrit dans Notion.
	if strings.Contains(out, "entrée(s) obsolètes") {
		t.Errorf("la destruction est annoncée comme un nettoyage local:\n%s", out)
	}
	if !strings.Contains(out, "- database.tasks mise à la corbeille") {
		t.Errorf("le compte rendu ne nomme pas la destruction:\n%s", out)
	}
	if !strings.Contains(out, "1 mise(s) à la corbeille") {
		t.Errorf("le bilan ne compte pas la destruction:\n%s", out)
	}
	if got := readMutationLog(t, logPath); got != trashLine {
		t.Errorf("écritures = %q, want exactement %q", got, trashLine)
	}

	snap, lerr := state.Load(dir)
	if lerr != nil {
		t.Fatal(lerr)
	}
	if _, ok := snap.Databases["tasks"]; ok {
		t.Error("l'entrée de la database mise à la corbeille est restée dans le state")
	}
	planOut, perr := runCmd(t, "plan", "--dir", dir)
	if perr != nil {
		t.Fatalf("plan: %v\n%s", perr, planOut)
	}
	if !strings.Contains(planOut, "Aucun changement") {
		t.Errorf("le plan qui suit une destruction n'est pas vide:\n%s", planOut)
	}
}

// Review Focus #4 : prevent_destroy est un accusé de lecture. La destruction
// part, et la mention reste affichée.
func TestApplyTrashesADatabaseDeclaredInPreventDestroy(t *testing.T) {
	logPath := withMutationLog(t)
	dir := importThenDeclare(t, orphanYAML)
	if err := os.WriteFile(filepath.Join(dir, "workspace.yaml"),
		[]byte(workspaceYAML+"lifecycle:\n  prevent_destroy:\n    - database.tasks\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runCmd(t, "apply", "--dir", dir, "--auto-approve")
	if err != nil {
		t.Fatalf("Execute() error = %v\n%s", err, out)
	}
	if !strings.Contains(out, "→ déclarée dans lifecycle.prevent_destroy.") {
		t.Errorf("la mention prevent_destroy a disparu:\n%s", out)
	}
	if got := readMutationLog(t, logPath); got != trashLine {
		t.Errorf("écritures = %q, want exactement %q", got, trashLine)
	}
}

// Review Focus #5 : une CI qui a demandé --fail-on=destructive ne met JAMAIS une
// database à la corbeille. Zéro écriture, state intact.
func TestApplyFailOnDestructiveTrashesNothing(t *testing.T) {
	logPath := withMutationLog(t)
	dir := importThenDeclare(t, orphanYAML)
	before := mustReadFile(t, filepath.Join(dir, state.FileName))

	out, err := runCmd(t, "apply", "--dir", dir, "--auto-approve", "--fail-on=destructive")
	if err == nil {
		t.Fatalf("Execute() error = nil, want le refus de --fail-on\n%s", out)
	}
	if !strings.Contains(err.Error(), "--fail-on") {
		t.Errorf("message = %q, il doit dire que --fail-on a déclenché", err.Error())
	}
	if got := readMutationLog(t, logPath); got != "" {
		t.Errorf("écritures = %q, want aucune", got)
	}
	if after := mustReadFile(t, filepath.Join(dir, state.FileName)); after != before {
		t.Error("le state a été réécrit malgré --fail-on")
	}
}

// Spec §5, configuration courante : sans ressource retenue, apply annonce
// exactement l'agrégat de plan — destruction comprise, maintenant qu'il l'écrit.
func TestApplyShowsTheSameImpactAsPlanWhenNothingIsWithheld(t *testing.T) {
	dir := importThenDeclare(t, orphanYAML)
	const want = "Impact : 1 database(s) à la corbeille."

	planOut, perr := runCmd(t, "plan", "--dir", dir)
	if perr != nil {
		t.Fatalf("plan: %v\n%s", perr, planOut)
	}
	if !strings.Contains(planOut, want) {
		t.Fatalf("montage du test faux : le plan doit porter %q\n%s", want, planOut)
	}
	applyOut, aerr := runCmd(t, "apply", "--dir", dir, "--auto-approve")
	if aerr != nil {
		t.Fatalf("apply: %v\n%s", aerr, applyOut)
	}
	if !strings.Contains(applyOut, want) {
		t.Errorf("apply n'annonce pas l'agrégat de plan:\n%s", applyOut)
	}
}

// tasksRenamedWithoutTodo renomme « Fait » ET retire « À faire ». Le renommage
// retient la ressource ; le retrait, qu'apply ne causera donc pas, porte un
// impact mesuré que plan agrège.
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

// Spec §5, seconde configuration : une ressource retenue garde un impact
// qu'apply ne causera pas. Son agrégat est donc strictement inférieur à celui
// de plan — ici, nul.
func TestApplyLeavesAWithheldResourceOutOfItsImpact(t *testing.T) {
	dir := importThenDeclare(t, tasksRenamedWithoutTodo)

	planOut, perr := runCmd(t, "plan", "--dir", dir)
	if perr != nil {
		t.Fatalf("plan: %v\n%s", perr, planOut)
	}
	if !strings.Contains(planOut, "Impact : 2 valeurs réassignées sans trace.") {
		t.Fatalf("montage du test faux : le plan doit agréger le retrait\n%s", planOut)
	}
	applyOut, aerr := runCmd(t, "apply", "--dir", dir, "--auto-approve")
	if aerr == nil {
		t.Fatalf("apply error = nil, want un apply non convergé\n%s", applyOut)
	}
	if !strings.Contains(applyOut, "Retenu — migration requise") {
		t.Fatalf("montage du test faux : la ressource doit être retenue\n%s", applyOut)
	}
	if strings.Contains(applyOut, "Impact :") {
		t.Errorf("apply agrège l'impact d'une ressource qu'il n'écrit pas:\n%s", applyOut)
	}
	// Reliquat du lot A : le bilan ne suit plus la section « Retenu » de deux
	// lignes vides.
	if strings.Contains(applyOut, "\n\n\nAppliqué") {
		t.Errorf("double ligne vide avant le bilan:\n%s", applyOut)
	}
}

// Un apply dont la seule écriture est une corbeille non confirmée échoue : son
// bilan doit quand même dire que rien n'a été acquis, et suivre les écarts
// d'une seule ligne vide.
func TestApplyReportKeepsItsSummaryWhenOnlyAMismatchRemains(t *testing.T) {
	var b bytes.Buffer
	renderReport(&b, apply.Report{Mismatches: []string{
		"database.tasks — l'API a répondu sans mettre la database à la corbeille",
	}}, 0)
	out := b.String()
	if !strings.Contains(out, "Appliqué : 0 création(s), 0 modification(s), 0 mise(s) à la corbeille") {
		t.Errorf("bilan absent:\n%s", out)
	}
	if strings.Contains(out, "\n\n\nAppliqué") {
		t.Errorf("double ligne vide avant le bilan:\n%s", out)
	}
}
