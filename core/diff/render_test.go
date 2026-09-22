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
