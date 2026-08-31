package config

import (
	"strings"
	"testing"
)

func TestValidateDocumentAcceptsValidConfig(t *testing.T) {
	doc := []byte(`
version: 1
workspace:
  parent_page_id: "44444444-4444-4444-8444-444444444444"
databases:
  - key: projects
    name: "Projects"
    description: "Suivi des projets internes"
    icon: "🚀"
    properties:
      Name:
        type: title
      Status:
        type: status
        options:
          - key: todo
            name: "To-Do"
            color: gray
            group: "To-do"
          - key: shipped
            name: "Shipped"
            color: green
            group: "Complete"
      Budget:
        type: number
        format: euro
      Tags:
        type: multi_select
        options:
          - key: backend
            name: "backend"
lifecycle:
  prevent_destroy: [projects]
  allow_data_loss: []
`)
	if err := ValidateDocument("databases/projects.yaml", doc); err != nil {
		t.Fatalf("ValidateDocument() error = %v", err)
	}
}

func TestValidateDocumentRejectsUnknownPropertyType(t *testing.T) {
	doc := []byte(`
version: 1
databases:
  - name: "Projects"
    properties:
      Score:
        type: formula
`)
	err := ValidateDocument("databases/projects.yaml", doc)
	if err == nil {
		t.Fatal("ValidateDocument() error = nil, want un rejet de `formula`")
	}
	if !strings.Contains(err.Error(), "databases/projects.yaml") {
		t.Errorf("message = %q, il doit nommer le fichier", err.Error())
	}
}

// Le piège central : l'API n'accepte que "To-do", pas "To do". Un message
// génarique d'énumération ne suffit pas, l'utilisateur ne verra pas le trait
// d'union.
func TestValidateDocumentGivesDedicatedHintForToDoWithoutHyphen(t *testing.T) {
	doc := []byte(`
version: 1
databases:
  - name: "Projects"
    properties:
      Status:
        type: status
        options:
          - name: "Todo"
            group: "To do"
`)
	err := ValidateDocument("databases/projects.yaml", doc)
	if err == nil {
		t.Fatal("ValidateDocument() error = nil, want un rejet de `To do`")
	}
	msg := err.Error()
	if !strings.Contains(msg, `"To-do"`) {
		t.Errorf("message = %q, il doit proposer la forme exacte \"To-do\"", msg)
	}
}

func TestValidateDocumentRejectsCustomStatusGroup(t *testing.T) {
	doc := []byte(`
version: 1
databases:
  - name: "Projects"
    properties:
      Status:
        type: status
        options:
          - name: "Idea"
            group: "Backlog"
`)
	if err := ValidateDocument("databases/projects.yaml", doc); err == nil {
		t.Fatal("ValidateDocument() error = nil, want un rejet de `Backlog`")
	}
}

func TestValidateDocumentRejectsOptionsOnNonSelectProperty(t *testing.T) {
	doc := []byte(`
version: 1
databases:
  - name: "Projects"
    properties:
      Repository:
        type: url
        options:
          - name: "nope"
`)
	if err := ValidateDocument("databases/projects.yaml", doc); err == nil {
		t.Fatal("ValidateDocument() error = nil, want un rejet de `options` sur un url")
	}
}

func TestValidateDocumentRejectsInvalidKeyPattern(t *testing.T) {
	doc := []byte(`
version: 1
databases:
  - key: "Mon Projet"
    name: "Projects"
    properties:
      Name:
        type: title
`)
	if err := ValidateDocument("databases/projects.yaml", doc); err == nil {
		t.Fatal("ValidateDocument() error = nil, want un rejet de la key `Mon Projet`")
	}
}

func TestValidateDocumentRejectsMalformedYAML(t *testing.T) {
	doc := []byte("version: 1\ndatabases:\n  - name: [unclosed\n")
	err := ValidateDocument("databases/projects.yaml", doc)
	if err == nil {
		t.Fatal("ValidateDocument() error = nil, want une erreur de parsing YAML")
	}
	if !strings.Contains(err.Error(), "databases/projects.yaml") {
		t.Errorf("message = %q, il doit nommer le fichier", err.Error())
	}
}
