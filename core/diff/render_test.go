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
// Avant correction, l'aplatissement ré-échappait toute la note avec %q,
// produisant des antislashs illisibles — précisément sur la ligne censée éviter
// à l'utilisateur de croire qu'il a renommé une propriété. La concaténation vit
// désormais dans Render, et les autres tests de rendu fabriquent des Details
// sans Note : celui-ci seul, en passant par Compute puis par Render, l'exerce
// réellement.
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
			Details: []resources.Detail{
				{Op: "+", Target: `property "Estimate" (number)`, Class: ClassSafe, Count: -1},
				{Op: "+", Target: `property "Name" (title)`, Class: ClassSafe, Count: -1},
			},
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
			Details: []resources.Detail{
				{Op: "-", Target: `option "Shipped" du status "Status"`,
					Class: ClassSilentRewrite, Count: -1},
			},
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
			Resource: "database.tasks",
			Class:    ClassMigration,
			Details: []resources.Detail{{
				Op:     "~",
				Target: `option "Terminé" → "Fait" (propriété "Statut")`,
				Class:  ClassMigration,
				Count:  -1,
			}},
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
		Details: []resources.Detail{
			{Op: "+", Target: `property "Name" (title)`, Class: ClassSafe, Count: -1},
		},
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
			Details: []resources.Detail{
				{Op: "-", Target: `option "Annulé"`, Class: ClassSilentRewrite, Count: -1},
			},
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
// depuis c.Class au lieu de d.Class passerait inaperçue : dans tous les autres
// tests, la classe de la ressource et celle de ses détails portent la même
// valeur.
func TestRenderLineClassIsIndependentOfResourceClass(t *testing.T) {
	p := &Plan{
		ToChange: 1,
		Changes: []Change{{
			Resource: "database.flows",
			Class:    ClassSilentRewrite,
			Details: []resources.Detail{
				{Op: "+", Target: `property "Name" (title)`, Class: ClassSafe, Count: -1},
				{Op: "-", Target: `option "Annulé" (status "Étape")`,
					Class: ClassSilentRewrite, Count: -1},
			},
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
			Details: []resources.Detail{
				{Op: "-", Target: `option "Annulé"`, Class: ClassSilentRewrite, Count: -1},
			},
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

// Chaque ligne mesurée porte son chiffre : c'est ce qui fait décider.
func TestRenderShowsTheMeasuredCount(t *testing.T) {
	p := &Plan{ToChange: 1, Changes: []Change{{
		Resource: "database.tasks", Kind: resources.KindUpdate,
		Class: ClassSilentRewrite,
		Details: []resources.Detail{{
			Op: "-", Target: `option "Annulé" (propriété "Statut")`,
			Class: ClassSilentRewrite, Count: 47,
			Measure: &resources.Measurement{Property: "Statut", PropertyType: "status", Option: "Annulé"},
		}},
	}}}
	var b bytes.Buffer
	if err := Render(&b, p); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	for _, want := range []string{"47 lignes", "réassignées", "sans trace"} {
		if !strings.Contains(got, want) {
			t.Errorf("sortie:\n%s\nil manque %q", got, want)
		}
	}
}

// Mesuré le 2026-09-24 contre l'API : retirer une option de `select` VIDE la
// cellule. La phrase peut donc parler de LEUR valeur, au singulier — la ligne
// n'en portait qu'une.
func TestRenderSaysASelectRemovalEmptiesTheCell(t *testing.T) {
	p := &Plan{ToChange: 1, Changes: []Change{{
		Resource: "database.tasks", Kind: resources.KindUpdate,
		Details: []resources.Detail{{
			Op: "-", Target: `option "Basse" (propriété "Priorité")`,
			Class: ClassDestructive, Count: 3,
			Measure: &resources.Measurement{Property: "Priorité", PropertyType: "select", Option: "Basse"},
		}},
	}}}
	var b bytes.Buffer
	if err := Render(&b, p); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "3 lignes passeront à vide") {
		t.Errorf("sortie:\n%s\nun retrait sur select vide bien la cellule", b.String())
	}
}

// Mesuré le 2026-09-24 contre l'API, sur une page jetable : une ligne portant
// ['Un','Deux'] dont on retire 'Un' garde ['Deux'] ; une ligne ne portant que
// ['Un'] passe à []. Un multi_select perd donc CETTE valeur, et ne se vide que
// s'il n'en portait pas d'autre.
//
// « perdront leur valeur » se lit « la cellule sera vidée » : c'est vrai pour
// select, faux pour multi_select. Et on ne dit PAS combien de lignes se
// videront vraiment — la mesure compte les lignes portant l'option, pas celles
// qui n'en portent qu'elle.
func TestRenderDoesNotClaimAMultiSelectRemovalEmptiesTheCell(t *testing.T) {
	p := &Plan{ToChange: 1, Changes: []Change{{
		Resource: "database.tasks", Kind: resources.KindUpdate,
		Details: []resources.Detail{{
			Op: "-", Target: `option "Un" (propriété "Tags")`,
			Class: ClassDestructive, Count: 3,
			Measure: &resources.Measurement{Property: "Tags", PropertyType: "multi_select", Option: "Un"},
		}},
	}}}
	var b bytes.Buffer
	if err := Render(&b, p); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	if strings.Contains(got, "perdront leur valeur") {
		t.Errorf("sortie:\n%s\n« leur valeur » affirme que la cellule sera vidée, "+
			"ce qui est faux sur multi_select", got)
	}
	for _, want := range []string{"3 lignes perdront cette valeur", "n'en portaient pas d'autre"} {
		if !strings.Contains(got, want) {
			t.Errorf("sortie:\n%s\nil manque %q", got, want)
		}
	}
}

// 0 ligne : la ligne doit le dire, et être sûre. Sans ça, l'utilisateur ne sait
// pas que notion-seed a vérifié.
func TestRenderSaysWhenNoRowIsAffected(t *testing.T) {
	p := &Plan{ToChange: 1, Changes: []Change{{
		Resource: "database.tasks", Kind: resources.KindUpdate,
		Details: []resources.Detail{{
			Op: "-", Target: `option "Legacy" (propriété "Priorité")`,
			Class: ClassSafe, Count: 0,
			Measure: &resources.Measurement{Property: "Priorité", PropertyType: "select", Option: "Legacy"},
		}},
	}}}
	var b bytes.Buffer
	if err := Render(&b, p); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "0 ligne concernée") {
		t.Errorf("sortie:\n%s", b.String())
	}
}

// Un compte plafonné ne doit jamais s'afficher comme un compte exact.
func TestRenderMarksACappedCountAsALowerBound(t *testing.T) {
	p := &Plan{ToChange: 1, Changes: []Change{{
		Resource: "database.tasks", Kind: resources.KindUpdate,
		Details: []resources.Detail{{
			Op: "-", Target: `option "X" (propriété "Statut")`,
			Class: ClassSilentRewrite, Count: 300, Capped: true,
			Measure: &resources.Measurement{Property: "Statut", PropertyType: "status", Option: "X"},
		}},
	}}}
	var b bytes.Buffer
	if err := Render(&b, p); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "plus de 300") {
		t.Errorf("sortie:\n%s\nun compte plafonné doit se lire comme un minorant", b.String())
	}
}

// La ligne Impact n'additionne JAMAIS deux familles différentes, et ne
// revendique jamais plus que ce qui a été mesuré.
func TestImpactSeparatesReassignedFromWeakened(t *testing.T) {
	p := &Plan{Changes: []Change{{
		Resource: "database.tasks",
		Details: []resources.Detail{
			{Op: "-", Class: ClassSilentRewrite, Count: 47,
				Measure: &resources.Measurement{PropertyType: "status", Option: "Annulé"}},
			{Op: "~", Class: ClassSilentRewrite, Count: 230,
				Measure: &resources.Measurement{PropertyType: "multi_select"}},
		},
	}}}
	got := Impact(p)
	if !strings.Contains(got, "47") || !strings.Contains(got, "jusqu'à 230") {
		t.Errorf("Impact = %q : le compte de valeurs non vides est un MAJORANT, "+
			"il ne dit pas combien de lignes perdront vraiment quelque chose", got)
	}
}

// Une ligne de migration retient sa ressource : rien n'est écrit. Son compte
// est le coût du remède — les lignes à déplacer à la main —, jamais une perte.
// La rendre comme un retrait annoncerait une réassignation qui n'aura pas lieu.
func TestRenderStatesAMigrationCountAsTheRemedyCost(t *testing.T) {
	for _, tc := range []struct {
		name, propType string
	}{
		{"renommage de status", "status"},
		{"couleur de select", "select"},
		{"couleur de multi_select", "multi_select"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &Plan{ToChange: 1, Changes: []Change{{
				Resource: "database.tasks", Kind: resources.KindUpdate,
				Class: ClassMigration,
				Details: []resources.Detail{{
					Op: "~", Target: `option "Fait" → "Terminé" (propriété "Statut")`,
					Class: ClassMigration, Count: 2,
					Measure: &resources.Measurement{
						Property: "Statut", PropertyType: tc.propType, Option: "Fait",
					},
				}},
			}}}
			var b bytes.Buffer
			if err := Render(&b, p); err != nil {
				t.Fatal(err)
			}
			got := b.String()
			want := `→ 2 lignes portent "Fait" : à migrer à la main avant d'appliquer.`
			if !strings.Contains(got, want) {
				t.Errorf("sortie:\n%s\nil manque %q", got, want)
			}
			for _, lie := range []string{"réassignées", "passeront à vide", "perdront", "Impact :"} {
				if strings.Contains(got, lie) {
					t.Errorf("sortie:\n%s\n%q annonce une perte qu'une ressource retenue ne cause pas",
						got, lie)
				}
			}
		})
	}
}

// L'agrégat ne compte que ce qui sera écrit : une migration retenue n'y entre
// pas, même à côté d'un retrait réel qu'elle ne doit pas gonfler.
func TestImpactExcludesMigrationLines(t *testing.T) {
	p := &Plan{Changes: []Change{{
		Resource: "database.tasks",
		Details: []resources.Detail{
			{Op: "~", Class: ClassMigration, Count: 2,
				Measure: &resources.Measurement{PropertyType: "status", Option: "Fait"}},
			{Op: "~", Class: ClassMigration, Count: 5,
				Measure: &resources.Measurement{PropertyType: "select", Option: "Haute"}},
			{Op: "-", Class: ClassSilentRewrite, Count: 3,
				Measure: &resources.Measurement{PropertyType: "status", Option: "Annulé"}},
		},
	}}}
	if got, want := Impact(p), "Impact : 3 valeurs réassignées sans trace."; got != want {
		t.Errorf("Impact = %q, want %q", got, want)
	}
	only := &Plan{Changes: []Change{{
		Resource: "database.tasks",
		Details:  p.Changes[0].Details[:2],
	}}}
	if got := Impact(only); got != "" {
		t.Errorf("Impact = %q, want vide : une migration retenue ne perd rien", got)
	}
}

func TestImpactIsEmptyWhenNothingIsAtStake(t *testing.T) {
	p := &Plan{Changes: []Change{{
		Resource: "database.tasks",
		Details:  []resources.Detail{{Op: "+", Class: ClassSafe, Count: -1}},
	}}}
	if got := Impact(p); got != "" {
		t.Errorf("Impact = %q, want vide", got)
	}
}

// lifecycle ne bloque plus mais doit s'entendre.
func TestRenderShowsLifecycleAcknowledgements(t *testing.T) {
	p := &Plan{ToDestroy: 1, Changes: []Change{{
		Resource: "database.archive", Kind: resources.KindDestroy,
		Class: ClassDestructive, Acknowledged: []string{"prevent_destroy"},
		Details: []resources.Detail{{Op: "-", Target: "database.archive", Class: ClassDestructive, Count: -1}},
	}}}
	var b bytes.Buffer
	if err := Render(&b, p); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "prevent_destroy") {
		t.Errorf("sortie:\n%s", b.String())
	}
}

// Une ligne MESURABLE dont la mesure n'a pas eu lieu ne doit pas se taire.
//
// Le cas est atteignable sans panne ni --skip-preflight : rich_text → number
// est classé « réécriture silencieuse » par la table mesurée, donc le détail
// porte une demande de mesure ; mais core/measure ne sait pas construire de
// filtre pour rich_text, écarte la demande sans la compter pour un incident, et
// le compte reste à -1. Sans conséquence affichée, la ligne se lit « rien à
// signaler » — juste à côté d'une voisine qui porte son chiffre, et sur une
// réécriture silencieuse.
func TestRenderSaysWhenAMeasurableLineWasNotMeasured(t *testing.T) {
	p := &Plan{ToChange: 1, Changes: []Change{{
		Resource: "database.tasks", Kind: resources.KindUpdate,
		Class: ClassSilentRewrite,
		Details: []resources.Detail{{
			Op: "~", Target: `property "Notes"`, Note: "rich_text → number",
			Class: ClassSilentRewrite, Count: -1,
			Measure: &resources.Measurement{Property: "Notes", PropertyType: "rich_text"},
		}},
	}}}
	var b bytes.Buffer
	if err := Render(&b, p); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "impact non mesuré") {
		t.Errorf("sortie:\n%s\nune ligne mesurable non mesurée doit le DIRE, pas se taire", b.String())
	}
}

// « Non mesurable » et « non mesuré » ne se réparent pas pareil, donc ne se
// disent pas pareil. Un type que notion-seed ne sait pas filtrer ne se comptera
// pas davantage au dixième run : promettre « relancez » serait annoncer une
// action corrective qui n'arrivera jamais — exactement la faute que ce produit
// existe pour supprimer.
func TestRenderNeverPromisesARetryOnAnUnmeasurableLine(t *testing.T) {
	p := &Plan{ToChange: 1, Changes: []Change{{
		Resource: "database.tasks", Kind: resources.KindUpdate,
		Class: ClassSilentRewrite,
		Details: []resources.Detail{{
			Op: "~", Target: `property "Notes"`, Note: "rich_text → number",
			Class: ClassSilentRewrite, Count: -1, Unmeasurable: true,
			Measure: &resources.Measurement{Property: "Notes", PropertyType: "rich_text"},
		}},
	}}}
	var b bytes.Buffer
	if err := Render(&b, p); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	if strings.Contains(got, "relancez") {
		t.Errorf("sortie:\n%s\nune ligne non mesurable ne se répare pas en relançant", got)
	}
	// Elle doit malgré tout parler, et nommer ce qui bloque.
	if !strings.Contains(got, "rich_text") {
		t.Errorf("sortie:\n%s\nla phrase doit nommer le type qu'on ne sait pas compter", got)
	}
	if !strings.Contains(got, "inconnu") {
		t.Errorf("sortie:\n%s\nl'impact réel reste inconnu et doit se dire", got)
	}
}

// Le pendant exact : une mesure simplement PAS FAITE (--skip-preflight, 403,
// 429) se répare bien en relançant, et doit garder sa promesse. Sans ce test,
// la correction ci-dessus pourrait supprimer le remède partout.
func TestRenderStillPromisesARetryOnAMerelyUnmeasuredLine(t *testing.T) {
	p := &Plan{ToChange: 1, Changes: []Change{{
		Resource: "database.tasks", Kind: resources.KindUpdate,
		Class: ClassSilentRewrite,
		Details: []resources.Detail{{
			Op: "-", Target: `option "Annulé" (propriété "Statut")`,
			Class: ClassSilentRewrite, Count: -1,
			Measure: &resources.Measurement{
				Property: "Statut", PropertyType: "status", Option: "Annulé"},
		}},
	}}}
	var b bytes.Buffer
	if err := Render(&b, p); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "relancez en ligne pour l'obtenir") {
		t.Errorf("sortie:\n%s\nune mesure non faite se répare en relançant : dites-le", b.String())
	}
}

// Le pendant du test précédent : une ligne sans demande de mesure ne coûte
// rien, et n'a donc aucune conséquence à annoncer. Sans cette borne, la
// correction ci-dessus ferait déborder « impact non mesuré » sur chaque ajout
// de propriété d'une création.
func TestRenderStaysSilentOnALineThatCostsNothing(t *testing.T) {
	p := &Plan{ToAdd: 1, Changes: []Change{{
		Resource: "database.tasks", Kind: resources.KindCreate, Detail: "(new)",
		Details: []resources.Detail{
			{Op: "+", Target: `property "Name" (title)`, Class: ClassSafe, Count: -1},
		},
	}}}
	var b bytes.Buffer
	if err := Render(&b, p); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), "→") {
		t.Errorf("sortie:\n%s\nune ligne qui ne coûte rien n'annonce rien", b.String())
	}
}

// Impact somme des comptes pris sur des mesures DIFFÉRENTES, qui peuvent
// porter sur les mêmes lignes : deux options retirées d'une même propriété
// multi_select se filtrent par `contains`, donc une ligne portant les deux est
// comptée deux fois. 10 + 8 ne fait pas 18 lignes distinctes. Le total qu'on
// détient est un nombre de valeurs — de couples (ligne, option) — et c'est
// ainsi qu'il doit se lire.
func TestImpactDoesNotPresentValuesAsDistinctRows(t *testing.T) {
	p := &Plan{Changes: []Change{{
		Resource: "database.tasks",
		Details: []resources.Detail{
			{Op: "-", Class: ClassDestructive, Count: 10,
				Measure: &resources.Measurement{
					Property: "Tags", PropertyType: "multi_select", Option: "A"}},
			{Op: "-", Class: ClassDestructive, Count: 8,
				Measure: &resources.Measurement{
					Property: "Tags", PropertyType: "multi_select", Option: "B"}},
		},
	}}}
	got := Impact(p)
	if strings.Contains(got, "18 lignes") {
		t.Errorf("Impact = %q : 18 est un nombre de valeurs, pas de lignes distinctes "+
			"(les mêmes lignes peuvent porter les deux options)", got)
	}
	if !strings.Contains(got, "18 valeurs") {
		t.Errorf("Impact = %q : le total mesuré doit rester visible, nommé pour ce qu'il est", got)
	}
}

// Un changement de type PLAFONNÉ ne se borne pas par le haut. Le compte est
// celui des lignes non vides, donc un majorant de ce qui sera perdu — d'où
// « jusqu'à » — mais plafonné il devient un MINORANT de ce majorant. Les deux
// à la fois donneraient « jusqu'à plus de 300 », qui ne veut rien dire et
// laisse croire à une borne supérieure qui n'existe pas.
func TestRenderNeverBoundsACappedTypeChangeFromAbove(t *testing.T) {
	p := &Plan{ToChange: 1, Changes: []Change{{
		Resource: "database.tasks", Kind: resources.KindUpdate,
		Class: ClassSilentRewrite,
		Details: []resources.Detail{{
			Op: "~", Target: `property "Tags" (multi_select → select)`,
			Class: ClassSilentRewrite, Count: 300, Capped: true,
			Measure: &resources.Measurement{Property: "Tags", PropertyType: "multi_select"},
		}},
	}}}
	var b bytes.Buffer
	if err := Render(&b, p); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	if strings.Contains(got, "jusqu'à") {
		t.Errorf("sortie:\n%s\nun compte plafonné ne borne rien par le haut", got)
	}
	if !strings.Contains(got, "plus de 300") {
		t.Errorf("sortie:\n%s\nle minorant mesuré doit rester visible", got)
	}
}

// La ligne Impact somme des comptes ; si l'un d'eux est plafonné, la somme est
// un minorant et doit se lire comme tel. L'afficher en compte ferme serait
// exactement l'affirmation invérifiée que notion-seed existe pour empêcher.
func TestImpactKeepsACappedSumALowerBound(t *testing.T) {
	p := &Plan{Changes: []Change{{
		Resource: "database.tasks",
		Details: []resources.Detail{
			{Op: "-", Class: ClassSilentRewrite, Count: 300, Capped: true,
				Measure: &resources.Measurement{PropertyType: "status", Option: "Annulé"}},
			{Op: "-", Class: ClassDestructive, Count: 12,
				Measure: &resources.Measurement{PropertyType: "select", Option: "Legacy"}},
		},
	}}}
	got := Impact(p)
	if !strings.Contains(got, "plus de 300 valeurs réassignées") {
		t.Errorf("Impact = %q : une somme qui contient un compte plafonné est un minorant", got)
	}
	// La famille non plafonnée garde son compte ferme : le plafond de l'une ne
	// doit pas rendre l'autre plus floue qu'elle ne l'est.
	if !strings.Contains(got, "12 valeurs perdues") {
		t.Errorf("Impact = %q : la famille non plafonnée garde son compte exact", got)
	}
}

// Même raisonnement sur la famille « appauvries » : plafonnée, elle perd sa
// borne supérieure, donc son « jusqu'à ».
func TestImpactNeverBoundsACappedTypeChangeFromAbove(t *testing.T) {
	p := &Plan{Changes: []Change{{
		Resource: "database.tasks",
		Details: []resources.Detail{{
			Op: "~", Class: ClassSilentRewrite, Count: 300, Capped: true,
			Measure: &resources.Measurement{PropertyType: "multi_select"},
		}},
	}}}
	got := Impact(p)
	if strings.Contains(got, "jusqu'à") {
		t.Errorf("Impact = %q : un compte plafonné ne borne rien par le haut", got)
	}
	if got == "" {
		t.Error("Impact ne doit pas se taire : des lignes seront appauvries")
	}
}

// apply n'écrit pas les destructions : lui faire afficher la ligne d'agrégat du
// plan complet lui ferait annoncer des destructions qu'il ne fera pas — démenties quatre lignes plus bas par sa propre section « Non
// appliqué ». La ligne la plus lue du produit ne peut pas mentir sur la
// commande qui écrit.
func TestRenderWithoutImpactOmitsTheAggregateLine(t *testing.T) {
	p := &Plan{ToDestroy: 1, Changes: []Change{{
		Resource: "database.archive", Kind: resources.KindDestroy,
		Class: ClassDestructive,
		Details: []resources.Detail{{
			Op: "-", Target: "database.archive", Class: ClassDestructive, Count: -1,
		}},
	}}}

	var withImpact bytes.Buffer
	if err := Render(&withImpact, p); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(withImpact.String(), "Impact :") {
		t.Fatalf("montage du test faux : Render doit porter la ligne Impact\n%s", withImpact.String())
	}

	var without bytes.Buffer
	if err := RenderWithoutImpact(&without, p); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(without.String(), "Impact :") {
		t.Errorf("sortie:\n%s\nelle ne doit pas porter la ligne d'agrégat", without.String())
	}
	// Tout le reste du plan doit être rendu à l'identique : seule la ligne
	// d'agrégat disparaît, pas le détail de ce qui est en jeu.
	if !strings.Contains(without.String(), "database.archive") {
		t.Errorf("sortie:\n%s\nle plan lui-même doit rester rendu", without.String())
	}
}

// Une option de status qui disparaît dans un CHANGEMENT DE TYPE n'est pas un
// retrait d'option de status : rien ne reste où réassigner la ligne. Mesuré sur
// select → multi_select le 2026-09-25, la ligne perd la valeur dont le nom ne
// revient pas ; l'annoncer « réassignée » affirmerait un sort qui n'est pas le
// sien, et la rangerait dans le mauvais total.
func TestRenderSaysARetypedStatusOptionEmptiesTheCell(t *testing.T) {
	p := &Plan{ToChange: 1, Changes: []Change{{
		Resource: "database.tasks", Kind: resources.KindUpdate,
		Details: []resources.Detail{{
			Op: "-", Target: `option "Fait" (propriété "Statut")`,
			Class: ClassDestructive, Count: 2,
			Measure: &resources.Measurement{
				Property: "Statut", PropertyType: "status", Option: "Fait", Retyped: true,
			},
		}},
	}}}
	var b bytes.Buffer
	if err := Render(&b, p); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	if !strings.Contains(got, "2 lignes passeront à vide") {
		t.Errorf("sortie:\n%s\nil manque « 2 lignes passeront à vide »", got)
	}
	if strings.Contains(got, "réassignées") {
		t.Errorf("sortie:\n%s\nune option qui disparaît avec son type ne réassigne rien", got)
	}
	if !strings.Contains(got, "Impact : 2 valeurs perdues.") {
		t.Errorf("sortie:\n%s\nle total doit compter une perte, pas une réassignation", got)
	}
}
