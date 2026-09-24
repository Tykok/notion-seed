// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"bytes"
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

// runCmd exécute une commande racine et rend sa sortie, pour les tests qui
// n'épinglent pas un golden.
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

// testParentPageID est un UUID bien formé : le schéma impose ce motif sur
// parent_page_id, pour que la config soit rejetée avant l'appel plutôt que par
// un 400 de l'API.
const testParentPageID = "44444444-4444-4444-8444-444444444444"

// workspaceYAML est le workspace.yaml minimal des tests de plan.
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

// Ce test tourne EN LIGNE, sans --skip-preflight : le faux ntn est réellement
// invoqué. Avant, les trois tests du chemin plan posaient le faux binaire sur le
// PATH puis passaient --skip-preflight, donc ni l'assemblage de la pile de
// transport, ni l'en-tête de workspace, ni checkParentPage n'étaient exécutés.
//
// La comparaison se fait sur un fichier golden : toutes les assertions sur la
// sortie réelle du produit étaient des strings.Contains, donc l'indentation, les
// lignes vides, l'en-tête et la précédence des marqueurs pouvaient changer avec
// la suite verte. Le golden épingle aussi le déterminisme, contrainte liante.
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
		t.Errorf("stderr non vide sur un plan qui réussit: %q", errOut.String())
	}
	assertGolden(t, "plan_two_databases.golden", out.String())
}

// La sortie doit être identique d'un run à l'autre : plan est fait pour être lu
// en CI et comparé.
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
			t.Fatalf("run %d diffère du premier:\n%s", i, lineDiff(first, got))
		}
	}
}

// La branche 404 de checkParentPage et son message n'étaient couverts par aucun
// test : les trois tests du chemin plan sautaient le preflight.
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
		t.Fatalf("Execute() error = nil, want un échec sur la page parente\n%s", out.String())
	}
	for _, want := range []string{
		testParentPageID,
		"introuvable",
		"parent_page_id",
		// Le jeton de ntn voit tout le workspace : un 404 n'est pas un défaut de
		// partage, et le message ne doit pas envoyer sur cette piste.
		"pas un problème de permissions",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message = %q, il doit contenir %q", err.Error(), want)
		}
	}
	// Le plan ne doit pas être rendu quand la page parente est illisible.
	if strings.Contains(out.String(), "Plan:") {
		t.Errorf("un plan a été affiché malgré une page parente illisible:\n%s", out.String())
	}
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
	// En ligne, sans --skip-preflight : « plan n'écrit rien » doit tenir aussi
	// quand la commande parle réellement à ntn.
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
		"workspace.yaml":     workspaceYAML,
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
		t.Error("plan et diff doivent produire la même sortie au MVP 0")
	}
}

// updateGolden réécrit les fichiers golden au lieu de les comparer :
//
//	go test ./cli/ -run TestPlan -update
var updateGolden = flag.Bool("update", false, "réécrit les fichiers golden de testdata/")

// assertGolden compare une sortie au fichier golden correspondant. En cas
// d'écart, l'échec affiche un diff ligne à ligne : « sortie différente » sans le
// détail obligerait à relancer à la main pour savoir quoi.
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
		t.Logf("golden réécrit: %s", path)
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("golden illisible: %v — relancez avec -update pour le créer", err)
	}
	if got != string(want) {
		t.Errorf("la sortie de plan diffère de %s :\n%s", path, lineDiff(string(want), got))
	}
}

// lineDiff rend un diff ligne à ligne, aligné sur les numéros de ligne : "-"
// pour la ligne attendue, "+" pour celle obtenue.
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

// Démontré avant correction : un 429 épuisé rendait « page parente X illisible:
// notion api 429 rate_limited: Rate limited. » sans aucune action corrective, sur
// l'échec non-404 le plus probable. Et les attentes de retry étaient totalement
// muettes — mesuré, 9,35 s de silence.
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
		t.Fatalf("Execute() error = nil, want un échec après épuisement des tentatives\n%s", out.String())
	}
	for _, want := range []string{"429", "--rate", "réessayez"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message = %q, il doit contenir %q", err.Error(), want)
		}
	}
	// Une attente muette est indistinguable d'un blocage : chaque attente est
	// annoncée, et sur stderr pour ne pas polluer le plan.
	if !strings.Contains(errOut.String(), "en attente") {
		t.Errorf("stderr = %q, chaque attente de retry doit être annoncée", errOut.String())
	}
}

// Le state ancre database.tasks sur db-1 : le refresh la lit, le comparateur
// la retrouve identique au désiré, donc aucun changement ne ressort — alors
// que sans state, la même config produirait une création.
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
	if !strings.Contains(out, "Aucun changement") {
		t.Errorf("le state doit faire reconnaître la database:\n%s", out)
	}
}

// tasksWorkspaceYAML, tasksConfigYAML et tasksStateJSON décrivent une
// configuration à une seule database "tasks", dont le state ancre l'id
// "db-1" — celui que sert le scénario fakentn "authenticated_database".
// Partagés par les tests ci-dessous, qui vérifient le comportement de plan
// avec un state qui fait réellement lire une ressource distante.
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

// Ronde de correction 1 : TestPlanWritesNothingToDisk ne verrouille
// l'invariant « plan n'écrit rien » QUE sur le chemin sans state — sans
// entrée dans snap.Databases, refreshManaged sort à son premier garde et sa
// boucle de lecture n'est jamais exercée. Ce test rejoue le même verrou avec
// un state peuplé qui fait réellement lire une database (id "db-1", servie
// par le scénario fakentn "authenticated_database") : le refresh doit
// accomplir ses appels GET sans jamais écrire sur disque.
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
	// La preuve que le refresh a réellement tourné, et pas seulement traversé
	// le garde « state vide » : la comparaison à trois voies ne peut rendre
	// « Aucun changement » que si `actual` a été lu avec succès depuis l'API.
	if !strings.Contains(out, "Aucun changement") {
		t.Fatalf("le refresh n'a pas produit la comparaison attendue:\n%s", out)
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

// Ronde de correction 1 : une entrée de state sans id (state écrit ou
// fusionné à la main) ne doit pas atteindre l'API. Sans ce garde,
// refreshManaged appellerait GET /v1/databases/ (chemin vide), qui rendrait
// soit un faux « disparue », soit un message qui parle de suppression alors
// que le vrai problème est un state abîmé.
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
		t.Fatalf("Execute() error = nil, want un refus d'entrée sans id\n%s", out)
	}
	for _, want := range []string{"database.tasks", "n'a pas d'identifiant", state.FileName} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message = %q, il doit contenir %q", err.Error(), want)
		}
	}
}

// C1 : une database gérée disparue (404) doit bloquer le plan ET le dire sur
// stdout. Avant correction, Render sortait « Aucun changement. La
// configuration correspond à l'état réel. » alors même que la commande
// rendait un code d'erreur — stdout affirmait l'inverse de stderr.
func TestPlanBlocksAndSaysSoWhenStateDatabaseIs404(t *testing.T) {
	withFakeNtn(t, "authenticated_database_404")
	dir := writeConfigDir(t, map[string]string{
		"workspace.yaml":       tasksWorkspaceYAML,
		"databases/tasks.yaml": tasksConfigYAML,
		state.FileName:         tasksStateJSON,
	})

	out, err := runCmd(t, "plan", "--dir", dir)
	if err == nil {
		t.Fatalf("Execute() error = nil, want un blocage sur une database introuvable\n%s", out)
	}
	if strings.Contains(out, "Aucun changement") {
		t.Errorf("stdout affirme la conformité alors que la database a disparu:\n%s", out)
	}
	for _, want := range []string{"Plan bloqué", "introuvable", "database.tasks"} {
		if !strings.Contains(out, want) {
			t.Errorf("sortie = %q, elle doit contenir %q", out, want)
		}
	}
}

// C1 : même défaut, côté archivage — jusqu'ici la seule branche de
// refreshManaged sans aucun test de bout en bout.
func TestPlanBlocksAndSaysSoWhenStateDatabaseIsArchived(t *testing.T) {
	withFakeNtn(t, "archived_database")
	dir := writeConfigDir(t, map[string]string{
		"workspace.yaml":       tasksWorkspaceYAML,
		"databases/tasks.yaml": tasksConfigYAML,
		state.FileName:         tasksStateJSON,
	})

	out, err := runCmd(t, "plan", "--dir", dir)
	if err == nil {
		t.Fatalf("Execute() error = nil, want un blocage sur une database archivée\n%s", out)
	}
	if strings.Contains(out, "Aucun changement") {
		t.Errorf("stdout affirme la conformité alors que la database est archivée:\n%s", out)
	}
	for _, want := range []string{"Plan bloqué", "archiv", "database.tasks"} {
		if !strings.Contains(out, want) {
			t.Errorf("sortie = %q, elle doit contenir %q", out, want)
		}
	}
}

// C2 : après import, plan --skip-preflight n'a fait aucun appel réseau — il
// ne peut donc pas affirmer que la configuration correspond à l'état réel.
// Une CI qui s'appuierait sur ce mode passerait au vert sur une réécriture
// silencieuse sans cette garantie.
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
	if strings.Contains(out, "correspond à l'état réel") {
		t.Errorf("--skip-preflight ne doit jamais affirmer la conformité:\n%s", out)
	}
	if !strings.Contains(out, "Non comparé") || !strings.Contains(out, "database.tasks") {
		t.Errorf("sortie = %q, elle doit nommer la ressource non comparée", out)
	}
}

// Review Focus 5 : un state sans workspace_id ne peut pas être comparé. Ça ne
// doit pas valoir « workspace différent ».
func TestCheckWorkspaceMatchAcceptsEmptyWorkspaceID(t *testing.T) {
	snap := &state.Snapshot{Version: state.Version}
	if err := checkWorkspaceMatch(snap, "33333333-3333-4333-8333-333333333333"); err != nil {
		t.Errorf("un workspace_id vide est inconnu, pas différent: %v", err)
	}
}

func TestCheckWorkspaceMatchRejectsForeignWorkspace(t *testing.T) {
	snap := &state.Snapshot{Version: state.Version, WorkspaceID: "44444444-4444-4444-8444-444444444444"}
	err := checkWorkspaceMatch(snap, "33333333-3333-4333-8333-333333333333")
	if err == nil {
		t.Fatal("un state d'un autre workspace doit être refusé")
	}
	if !strings.Contains(err.Error(), "44444444") || !strings.Contains(err.Error(), "33333333") {
		t.Errorf("le message doit nommer les deux workspaces: %v", err)
	}
}

// De bout en bout : le plan mesure réellement les lignes contre l'API et
// reclasse la ligne concernée avec ce qu'il a compté, au lieu de la laisser en
// « impact inconnu ». Il ne bloque plus.
//
// Le faux ntn rend deux lignes portant l'option "Fait" sur une propriété de
// type status : mesuré, ce retrait est une réécriture silencieuse. Non mesuré,
// il resterait « impact inconnu » — c'est exactement l'écart que cette passe
// ferme. Depuis que le rendu porte le chiffre, ce test l'assère : c'est la
// seule vérification de bout en bout que le nombre AFFICHÉ est celui qui a été
// compté, et pas un compte d'une autre database ou un reste de classification.
func TestPlanMeasuresRowsAndDoesNotBlock(t *testing.T) {
	withFakeNtn(t, "authenticated_database")
	dir := writeConfigDir(t, map[string]string{
		"workspace.yaml": workspaceYAML,
		// Le YAML ne déclare plus l'option "Fait" que porte la database réelle :
		// son retrait est mesuré à 2 lignes.
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
		t.Fatalf("plan ne doit plus échouer : %v\n%s", err, out)
	}
	if !strings.Contains(out, "réécriture silencieuse") {
		t.Errorf("le retrait n'a pas été reclassé par la mesure:\n%s", out)
	}
	if strings.Contains(out, "impact inconnu") {
		t.Errorf("la ligne est restée non mesurée:\n%s", out)
	}
	if strings.Contains(out, "Plan bloqué") {
		t.Errorf("le plan bloque encore:\n%s", out)
	}
	// Le chiffre lui-même, pas seulement la classe : c'est lui le produit.
	if !strings.Contains(out, "2 lignes") {
		t.Errorf("le compte mesuré n'est pas affiché:\n%s", out)
	}
	if !strings.Contains(out, "Impact : 2 lignes réassignées sans trace.") {
		t.Errorf("la ligne d'agrégat manque ou ne dit pas ce qui a été mesuré:\n%s", out)
	}
}

// Le piège d'ordre du faux ntn, désarmé pour TOUS les scénarios.
//
// `/v1/data_sources/ds-1/query` porte le préfixe `/v1/data_sources/` : un
// scénario qui teste le préfixe avant le suffixe `/query` rend le schéma d'un
// data source à une requête de comptage. Ce n'est pas une erreur visible — la
// réponse est un 200 valide — mais elle ne porte aucun `results`, et un
// comptage qui en tirerait 0 ferait annoncer « rien à perdre ».
//
// Ce test interroge le faux binaire directement, scénario par scénario : il
// rattrape le piège dans un scénario existant comme dans un scénario futur.
func TestFakeNtnAnswersQueryWithAListInEveryScenario(t *testing.T) {
	// Tous les scénarios qui servent /v1/data_sources/ et répondent en 200.
	scenarios := []string{
		"authenticated_database",
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
				t.Errorf("une requête de comptage doit rendre une liste, pas %s", out)
			}
			if !strings.Contains(string(out), `"results"`) {
				t.Errorf("la liste doit porter un champ results, pas %s", out)
			}
		})
	}
}

// Le comptage doit interroger le data source que le plan vient de LIRE, pas
// celui que le state a mémorisé. Le plan est calculé contre `refreshed` ; si la
// mesure interroge un autre objet, elle compte les lignes d'une autre
// database. Un id périmé encore vivant appartient alors à quelqu'un d'autre,
// répond 200, et le 0 qui en sort devient « rien à perdre ».
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
		// Relue, mais sans data source id : le state reste la meilleure réponse
		// disponible, et vaut mieux que pas de mesure du tout.
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

// Une ressource que le state n'ancre pas mais que le réel a rendue (une
// orpheline lue par Compute) doit rester mesurable : son id vient alors du seul
// endroit qui l'ait.
func TestMeasuredDataSourceIDsIncludesResourcesAbsentFromTheState(t *testing.T) {
	got := measuredDataSourceIDs(&state.Snapshot{Version: state.Version},
		map[string]diff.Refreshed{
			"tasks": {Database: state.Database{ID: "db-1", DataSourceID: "ds-frais"}},
		})
	if got["tasks"] != "ds-frais" {
		t.Errorf("dataSourceIDs[\"tasks\"] = %q, want %q", got["tasks"], "ds-frais")
	}
}

// Review Focus 2 : un comptage refusé par l'API ne fait pas échouer la
// commande. La ligne reste en « impact inconnu », la cause part sur stderr, et
// le reste du plan est rendu quand même — priver l'utilisateur de son plan
// parce qu'un comptage a échoué serait le vrai défaut.
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
		t.Fatalf("un comptage en échec ne doit pas faire échouer plan : %v\n%s", err, out.String())
	}

	if !strings.Contains(out.String(), `option "Fait"`) {
		t.Errorf("le reste du plan n'est pas rendu:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "impact inconnu") {
		t.Errorf("la ligne non mesurée doit rester inconnue:\n%s", out.String())
	}
	// Les incidents ne polluent pas stdout : le plan doit rester exploitable
	// dans un pipe.
	if strings.Contains(out.String(), "comptage impossible") {
		t.Errorf("l'incident est sur stdout:\n%s", out.String())
	}
	for _, want := range []string{"comptage impossible", "database.tasks", "403"} {
		if !strings.Contains(errOut.String(), want) {
			t.Errorf("stderr = %q, il doit contenir %q", errOut.String(), want)
		}
	}
}
