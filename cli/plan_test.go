// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"bytes"
	"crypto/sha256"
	"flag"
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
