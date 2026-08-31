package config

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/tykok/notion-seed/schema"
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

// Le message d'un `not` est « 'not' failed » et rien d'autre : le mot-clé ne
// porte aucune information sur ce qui a validé à tort. Sans hint, l'utilisateur
// ne sait même pas quel champ retirer.
func TestValidateDocumentNamesTheOffendingFieldOnNotFailures(t *testing.T) {
	tests := []struct {
		name     string
		doc      string
		contains []string
	}{
		{
			"options sur un url",
			`
version: 1
databases:
  - name: "P"
    properties:
      Repository:
        type: url
        options:
          - name: "nope"
`,
			[]string{"options", "url"},
		},
		{
			"format sur un url",
			`
version: 1
databases:
  - name: "P"
    properties:
      Repository:
        type: url
        format: euro
`,
			[]string{"format", "number"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateDocument("databases/projects.yaml", []byte(tt.doc))
			if err == nil {
				t.Fatal("ValidateDocument() error = nil, want un rejet")
			}
			msg := err.Error()
			if strings.Contains(msg, "'not' failed") && !strings.Contains(msg, "→") {
				t.Errorf("message = %q : « 'not' failed » nu, sans conseil", msg)
			}
			for _, want := range tt.contains {
				if !strings.Contains(msg, want) {
					t.Errorf("message = %q, il doit contenir %q", msg, want)
				}
			}
		})
	}
}

// Un hint ne doit jamais contredire l'erreur qu'il accompagne. Mesuré : filtrer
// sur la forme du document faisait dire « retirez le bloc options » alors que
// l'erreur réelle était « type manquant ».
func TestValidateDocumentHintNeverContradictsTheError(t *testing.T) {
	doc := []byte(`
version: 1
databases:
  - name: "P"
    properties:
      Repository:
        options:
          - name: "x"
`)
	err := ValidateDocument("databases/projects.yaml", doc)
	if err == nil {
		t.Fatal("ValidateDocument() error = nil, want un rejet du type manquant")
	}
	msg := err.Error()
	if !strings.Contains(msg, "type") {
		t.Errorf("message = %q, il doit signaler le type manquant", msg)
	}
	if strings.Contains(msg, "retirez le bloc `options`") {
		t.Errorf("message = %q : le conseil contredit l'erreur — le vrai correctif est d'AJOUTER `type`", msg)
	}
}

// Un scalaire non quoté en forme de date est résolu par yaml.v3 en time.Time.
// L'utilisateur est alors jugé sur une valeur qu'il n'a jamais tapée : le
// message doit lui dire de mettre des guillemets.
func TestValidateDocumentExplainsYAMLDateCoercion(t *testing.T) {
	doc := []byte(`
version: 1
databases:
  - key: 2024-01-01
    name: "P"
    properties:
      Name:
        type: title
`)
	err := ValidateDocument("databases/projects.yaml", doc)
	if err == nil {
		t.Fatal("ValidateDocument() error = nil, want un rejet de la key")
	}
	if !strings.Contains(err.Error(), "guillemets") {
		t.Errorf("message = %q, il doit conseiller de mettre des guillemets", err.Error())
	}
}

// Le schéma et SupportedPropertyTypes encodent la même liste de dix types de
// façon indépendante. Rien ne les garde synchronisés, sauf ce test.
func TestSchemaAndSupportedPropertyTypesAgree(t *testing.T) {
	var doc struct {
		Defs struct {
			Property struct {
				Properties struct {
					Type struct {
						Enum []string `json:"enum"`
					} `json:"type"`
				} `json:"properties"`
			} `json:"property"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(schema.Bytes, &doc); err != nil {
		t.Fatalf("schéma embarqué illisible: %v", err)
	}
	fromSchema := doc.Defs.Property.Properties.Type.Enum
	if len(fromSchema) == 0 {
		t.Fatal("aucun type lu depuis le schéma : la structure du schéma a changé")
	}
	if len(fromSchema) != len(SupportedPropertyTypes) {
		t.Fatalf("schéma: %d types, SupportedPropertyTypes: %d — les deux listes ont dérivé\n  schéma: %v\n  Go: %v",
			len(fromSchema), len(SupportedPropertyTypes), fromSchema, SupportedPropertyTypes)
	}
	inGo := make(map[string]bool, len(SupportedPropertyTypes))
	for _, t := range SupportedPropertyTypes {
		inGo[t] = true
	}
	for _, t2 := range fromSchema {
		if !inGo[t2] {
			t.Errorf("type %q présent dans le schéma mais absent de SupportedPropertyTypes", t2)
		}
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
