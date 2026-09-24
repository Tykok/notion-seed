// SPDX-License-Identifier: GPL-3.0-or-later

package diff

import (
	"bytes"
	"strings"
	"testing"

	"github.com/tykok/notion-seed/core/config"
	"github.com/tykok/notion-seed/core/providers/notion/resources"
	"github.com/tykok/notion-seed/core/state"
)

func TestRenderEmptyPlan(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, &Plan{}); err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "Aucun changement") {
		t.Errorf("sortie = %q, want une mention explicite d'absence de changement", got)
	}
}

// C1 : une database gérée introuvable ou archivée empile une BlockedReason
// sans jamais toucher Changes ni Unmanaged. Avant correction, Render sortait
// ici « Aucun changement. La configuration correspond à l'état réel. » sur
// stdout, en même temps qu'un code d'erreur — stdout affirmait l'inverse de
// stderr.
func TestRenderBlockedPlanNeverSaysNoChange(t *testing.T) {
	p := &Plan{
		Blocked: true,
		BlockedReasons: []string{
			"database.tasks est dans le state mais introuvable (404) dans Notion.\n" +
				"  → restaurez-la dans Notion, ou retirez son entrée de notion-seed.state.json",
		},
	}
	var buf bytes.Buffer
	if err := Render(&buf, p); err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	got := buf.String()
	if strings.Contains(got, "Aucun changement") {
		t.Errorf("un plan bloqué ne doit jamais afficher « Aucun changement »:\n%s", got)
	}
	if !strings.Contains(got, "Plan bloqué") {
		t.Errorf("sortie = %q, elle doit afficher la section de blocage", got)
	}
	if !strings.Contains(got, "introuvable (404)") {
		t.Errorf("sortie = %q, elle doit nommer la raison du blocage", got)
	}
}

// C2 : --skip-preflight ne lit jamais le réel pour une ressource que le state
// ancre. Compute alimente alors NotCompared au lieu de laisser un Plan
// entièrement vide passer pour une conformité constatée.
func TestRenderNotComparedBlocksNoChangeMessage(t *testing.T) {
	p := &Plan{NotCompared: []string{"database.tasks"}}
	var buf bytes.Buffer
	if err := Render(&buf, p); err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	got := buf.String()
	if strings.Contains(got, "correspond à l'état réel") {
		t.Errorf("une ressource non comparée ne doit jamais afficher la conformité:\n%s", got)
	}
	if !strings.Contains(got, "Non comparé") || !strings.Contains(got, "--skip-preflight") {
		t.Errorf("sortie = %q, elle doit nommer le mode qui a empêché la comparaison", got)
	}
	if !strings.Contains(got, "database.tasks") {
		t.Errorf("sortie = %q, elle doit nommer la ressource non comparée", got)
	}
}

// I3 : Note porte déjà ses propres guillemets là où il en faut (un
// renommage de propriété avertit avec `"Ancien" n'est pas renommée...`).
// Avant correction, absorb ré-échappait toute la note avec %q, produisant des
// antislashs illisibles — précisément sur la ligne censée éviter à
// l'utilisateur de croire qu'il a renommé une propriété. Les autres tests de
// rendu fabriquent des Lines déjà formatées : celui-ci seul, en passant par
// Compute puis par Render, exerce la concaténation réelle de Note.
func TestRenderNoteWithQuotesIsNotReEscaped(t *testing.T) {
	cfg := &config.Config{Databases: []config.Database{{
		Key: "tasks", Name: "Tasks",
		Properties: map[string]config.Property{"Charge": {Type: "number"}},
	}}}
	applied := &state.Snapshot{Version: state.Version, Databases: map[string]state.Database{
		"tasks": {ID: "db1", Name: "Tasks", Properties: map[string]state.Property{
			"Estimate": {ID: "p1", Type: "number"},
		}},
	}}
	actual := map[string]Refreshed{"tasks": {Database: state.Database{
		ID: "db1", Name: "Tasks", Properties: map[string]state.Property{
			"Estimate": {ID: "p1", Type: "number"},
		},
	}}}

	p, err := Compute(cfg, applied, actual)
	if err != nil {
		t.Fatalf("Compute() error = %v", err)
	}
	var buf bytes.Buffer
	if err := Render(&buf, p); err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	got := buf.String()
	if strings.Contains(got, `\"`) {
		t.Errorf("la note a été ré-échappée (antislashs) : %q", got)
	}
	if !strings.Contains(got, `"Estimate" n'est pas renommée`) {
		t.Errorf("la note doit rester lisible avec ses guillemets d'origine:\n%s", got)
	}
	if !strings.Contains(got, " — ") {
		t.Errorf("le séparateur doit être « — », pas « : »:\n%s", got)
	}
}

func TestRenderCreatePlan(t *testing.T) {
	p := &Plan{
		ToAdd: 1,
		Changes: []Change{{
			Class:    ClassSafe,
			Resource: "database.tasks",
			Detail:   "(new)",
			Kind:     resources.KindCreate,
			Lines:    []string{`+ property "Estimate" (number)`, `+ property "Name" (title)`},
		}},
	}
	var buf bytes.Buffer
	if err := Render(&buf, p); err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	got := buf.String()
	for _, want := range []string{
		"Plan: 1 to add, 0 to change, 0 to destroy",
		"+ database.tasks (new)",
		`property "Estimate" (number)`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("sortie ne contient pas %q\n--- sortie ---\n%s", want, got)
		}
	}
}

// Depuis que notion-seed ne bloque plus sur la foi d'une Class (voir
// core/change), le marqueur d'en-tête suit le Kind de la ressource — ce que
// l'opération FAIT, pas ce qu'elle coûte. Sans ce test, permuter les deux
// `case` du switch de Render, ou mapper KindDestroy sur "+" par erreur de
// copier-coller, resterait invisible : go test ./... passerait quand même,
// puisque seul le cas création était couvert par TestRenderCreatePlan.
func TestRenderDestroyMarksResourceWithMinus(t *testing.T) {
	p := &Plan{
		ToDestroy: 1,
		Changes: []Change{{
			Resource: "database.tasks",
			Kind:     resources.KindDestroy,
		}},
	}
	var buf bytes.Buffer
	if err := Render(&buf, p); err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	header := findLineContaining(t, buf.String(), "database.tasks")
	if !strings.HasPrefix(header, "  - ") {
		t.Errorf("en-tête = %q, want un marqueur « - » (destruction)", header)
	}
}

// Symétrique du test ci-dessus, côté modification : sans lui, un Kind autre
// que création ou destruction pourrait glisser sur n'importe quel marqueur
// sans qu'aucun test ne le remarque.
func TestRenderUpdateMarksResourceWithTilde(t *testing.T) {
	p := &Plan{
		ToChange: 1,
		Changes: []Change{{
			Resource: "database.tasks",
			Kind:     resources.KindUpdate,
		}},
	}
	var buf bytes.Buffer
	if err := Render(&buf, p); err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	header := findLineContaining(t, buf.String(), "database.tasks")
	if !strings.HasPrefix(header, "  ~ ") {
		t.Errorf("en-tête = %q, want un marqueur « ~ » (modification)", header)
	}
}

// findLineContaining rend la première ligne de out qui contient sub, pour
// isoler l'en-tête d'une ressource du reste du rendu sans dépendre de son
// contenu exact au-delà du marqueur.
func findLineContaining(t *testing.T, out, sub string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, sub) {
			return line
		}
	}
	t.Fatalf("aucune ligne ne contient %q dans:\n%s", sub, out)
	return ""
}

func TestRenderMarksBlockingChanges(t *testing.T) {
	p := &Plan{
		ToChange: 1,
		Blocked:  true,
		Changes: []Change{{
			Class:    ClassSilentRewrite,
			Resource: "database.tasks",
			Lines:    []string{`- option "Shipped" du status "Status"`},
		}},
	}
	var buf bytes.Buffer
	if err := Render(&buf, p); err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "réécriture silencieuse") {
		t.Errorf("sortie = %q, elle doit nommer la classe du changement", got)
	}
	if !strings.Contains(got, "bloqué") {
		t.Errorf("sortie = %q, elle doit dire que le plan est bloqué", got)
	}
}

// Le rendu part sur stdout en texte brut : pas de couleur inconditionnelle,
// pas de séquence d'échappement, la sortie doit rester utilisable en CI et
// dans un pipe.
func TestRenderIsPlainText(t *testing.T) {
	p := &Plan{ToAdd: 1, Changes: []Change{{
		Class: ClassSafe, Resource: "database.tasks", Detail: "(new)",
	}}}
	var buf bytes.Buffer
	if err := Render(&buf, p); err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if strings.Contains(buf.String(), "\x1b[") {
		t.Error("la sortie contient une séquence d'échappement ANSI")
	}
}

func TestRenderShowsDriftBeforePlan(t *testing.T) {
	p := &Plan{
		ToChange: 1,
		Drifts: []Drift{{Resource: "database.tasks", Lines: []string{
			`~ option "Fait" de la propriété "Statut" renommée en "Terminé" hors de notion-seed`,
		}}},
		Changes: []Change{{
			Resource:    "database.tasks",
			Class:       ClassMigration,
			Lines:       []string{`~ option "Terminé" → "Fait" (propriété "Statut")`},
			LineClasses: []Class{ClassMigration},
		}},
	}
	var b strings.Builder
	if err := Render(&b, p); err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	out := b.String()

	iDrift := strings.Index(out, "Dérive détectée hors de notion-seed")
	iPlan := strings.Index(out, "Plan:")
	if iDrift < 0 || iPlan < 0 || iDrift > iPlan {
		t.Errorf("la dérive doit précéder le plan:\n%s", out)
	}
	if !strings.Contains(out, "[migration requise]") {
		t.Errorf("la classe de la ligne doit apparaître:\n%s", out)
	}
}

func TestRenderOmitsEmptySections(t *testing.T) {
	p := &Plan{ToAdd: 1, Changes: []Change{{
		Resource: "database.tasks", Detail: "(new)",
		Lines: []string{`+ property "Name" (title)`}, LineClasses: []Class{ClassSafe},
	}}}
	var b strings.Builder
	if err := Render(&b, p); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if strings.Contains(out, "Dérive") || strings.Contains(out, "Hors config") {
		t.Errorf("sans state, la sortie doit être celle d'avant:\n%s", out)
	}
}

func TestRenderBlockedMessageNamesReasonAndRemedy(t *testing.T) {
	p := &Plan{
		Blocked: true,
		BlockedReasons: []string{
			"database.flows : réécriture silencieuse (option \"Annulé\").\n  → migrez les lignes",
		},
		ToChange: 1,
		Changes: []Change{{
			Resource: "database.flows", Class: ClassSilentRewrite,
			Lines: []string{`- option "Annulé"`}, LineClasses: []Class{ClassSilentRewrite},
		}},
	}
	var b strings.Builder
	if err := Render(&b, p); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if !strings.Contains(out, "Plan bloqué") || !strings.Contains(out, "→ migrez les lignes") {
		t.Errorf("le blocage doit nommer sa raison et son issue:\n%s", out)
	}
}

func TestRenderListsUnmanaged(t *testing.T) {
	p := &Plan{Unmanaged: []Unmanaged{{
		Resource: "database.tasks", Lines: []string{`property "Créé le" (created_time)`},
	}}}
	var b strings.Builder
	if err := Render(&b, p); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if !strings.Contains(out, "Hors config") || !strings.Contains(out, "Créé le") {
		t.Errorf("hors config manquant:\n%s", out)
	}
	// Du hors config sans aucun changement n'est pas une absence de changement :
	// il y a bien quelque chose à montrer. Verrouille ce choix, sinon un futur
	// correctif pourrait faire réapparaître le message par effet de bord.
	if strings.Contains(out, "Aucun changement") {
		t.Errorf("le hors config est un changement à montrer, pas une absence de changement:\n%s", out)
	}
}

// La classe d'une ligne est indépendante de la classe de la ressource : une
// database qui reçoit un ajout sûr en même temps qu'un retrait d'option de
// status ne doit pas faire porter l'étiquette dangereuse à la ligne sûre.
// Sans ce test, une régression qui dériverait le suffixe de chaque ligne
// depuis c.Class au lieu de c.LineClasses[i] passerait inaperçue : dans tous
// les autres tests, Class et LineClasses portent la même valeur.
func TestRenderLineClassIsIndependentOfResourceClass(t *testing.T) {
	p := &Plan{
		ToChange: 1,
		Changes: []Change{{
			Resource: "database.flows",
			Class:    ClassSilentRewrite,
			Lines: []string{
				`+ property "Name" (title)`,
				`- option "Annulé" (status "Étape")`,
			},
			LineClasses: []Class{ClassSafe, ClassSilentRewrite},
		}},
	}
	var b strings.Builder
	if err := Render(&b, p); err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	out := b.String()

	var safeLine, rewriteLine string
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.Contains(line, `property "Name"`):
			safeLine = line
		case strings.Contains(line, `option "Annulé"`):
			rewriteLine = line
		}
	}
	if safeLine == "" || rewriteLine == "" {
		t.Fatalf("lignes attendues absentes de la sortie:\n%s", out)
	}
	if strings.Contains(safeLine, "[") {
		t.Errorf("une ligne sûre ne doit pas hériter de l'étiquette de la ressource: %q", safeLine)
	}
	if !strings.Contains(rewriteLine, "[réécriture silencieuse]") {
		t.Errorf("la ligne dangereuse doit porter sa classe: %q", rewriteLine)
	}
}

// L'ordre des sections est délibéré (voir le commentaire de Render) : le hors
// config précède le blocage, sur le même modèle que TestRenderShowsDriftBeforePlan
// pour dérive/plan.
func TestRenderOrdersUnmanagedBeforeBlocked(t *testing.T) {
	p := &Plan{
		Blocked: true,
		BlockedReasons: []string{
			"database.flows : réécriture silencieuse (option \"Annulé\").\n  → migrez les lignes",
		},
		Unmanaged: []Unmanaged{{
			Resource: "database.tasks", Lines: []string{`property "Créé le" (created_time)`},
		}},
		ToChange: 1,
		Changes: []Change{{
			Resource: "database.flows", Class: ClassSilentRewrite,
			Lines: []string{`- option "Annulé"`}, LineClasses: []Class{ClassSilentRewrite},
		}},
	}
	var b strings.Builder
	if err := Render(&b, p); err != nil {
		t.Fatal(err)
	}
	out := b.String()

	iUnmanaged := strings.Index(out, "Hors config")
	iBlocked := strings.Index(out, "Plan bloqué")
	if iUnmanaged < 0 || iBlocked < 0 || iUnmanaged > iBlocked {
		t.Errorf("le hors config doit précéder le blocage:\n%s", out)
	}
}
