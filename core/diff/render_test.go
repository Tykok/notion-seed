// SPDX-License-Identifier: GPL-3.0-or-later

package diff

import (
	"bytes"
	"strings"
	"testing"
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

func TestRenderCreatePlan(t *testing.T) {
	p := &Plan{
		ToAdd: 1,
		Changes: []Change{{
			Class:    ClassSafe,
			Resource: "database.tasks",
			Detail:   "(new)",
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
}
