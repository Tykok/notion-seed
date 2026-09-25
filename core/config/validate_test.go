// SPDX-License-Identifier: GPL-3.0-or-later

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
  acknowledge_destroy: [projects]
  acknowledge_data_loss: []
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
		t.Fatal("ValidateDocument() error = nil, want `formula` rejected")
	}
	if !strings.Contains(err.Error(), "databases/projects.yaml") {
		t.Errorf("message = %q, it must name the file", err.Error())
	}
}

// The central trap: the API accepts only "To-do", not "To do". A generic enum
// message is not enough, the user will not see the hyphen.
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
		t.Fatal("ValidateDocument() error = nil, want `To do` rejected")
	}
	msg := err.Error()
	// Asserting only the presence of "To-do" proved nothing: the generic group
	// hint contains it too, as does the library's enum message. Removing the
	// branch dedicated to the trap left this test green. What is specific to
	// the dedicated branch is NAMING the hyphen and saying to REPLACE the value.
	for _, want := range []string{"hyphen", "Replace", `"To do"`, `"To-do"`} {
		if !strings.Contains(msg, want) {
			t.Errorf("message = %q, it must contain %q", msg, want)
		}
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
	err := ValidateDocument("databases/projects.yaml", doc)
	if err == nil {
		t.Fatal("ValidateDocument() error = nil, want `Backlog` rejected")
	}
	// `err != nil` alone did not say whether the user learns anything. The
	// message must name the rejected value, the three accepted groups, and the
	// fact that the API rejects freely named groups.
	msg := err.Error()
	for _, want := range []string{`"Backlog"`, `"To-do"`, `"In progress"`, `"Complete"`, "freely named"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message = %q, it must contain %q", msg, want)
		}
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
		t.Fatal("ValidateDocument() error = nil, want `options` on a url rejected")
	}
}

// The message of a `not` is "'not' failed" and nothing else: the keyword
// carries no information about what wrongly validated. Without a hint, the
// user does not even know which field to remove.
func TestValidateDocumentNamesTheOffendingFieldOnNotFailures(t *testing.T) {
	tests := []struct {
		name     string
		doc      string
		contains []string
	}{
		{
			"options on a url",
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
			"format on a url",
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
				t.Fatal("ValidateDocument() error = nil, want a rejection")
			}
			msg := err.Error()
			if strings.Contains(msg, "'not' failed") && !strings.Contains(msg, "→") {
				t.Errorf("message = %q: bare \"'not' failed\", without advice", msg)
			}
			for _, want := range tt.contains {
				if !strings.Contains(msg, want) {
					t.Errorf("message = %q, it must contain %q", msg, want)
				}
			}
		})
	}
}

// A hint must never contradict the error it comes with. Measured: filtering on
// the document's shape made it say "remove the options block" while the real
// error was "missing type".
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
		t.Fatal("ValidateDocument() error = nil, want the missing type rejected")
	}
	msg := err.Error()
	if !strings.Contains(msg, "type") {
		t.Errorf("message = %q, it must report the missing type", msg)
	}
	if strings.Contains(msg, "remove the `options` block") {
		t.Errorf("message = %q: the advice contradicts the error — the real fix is to ADD `type`", msg)
	}
}

// An unquoted date-shaped scalar is resolved by yaml.v3 into a time.Time. The
// user is then judged on a value they never typed: the message must tell them
// to add quotes.
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
		t.Fatal("ValidateDocument() error = nil, want the key rejected")
	}
	if !strings.Contains(err.Error(), "quotes") {
		t.Errorf("message = %q, it must advise adding quotes", err.Error())
	}
}

// The schema and SupportedPropertyTypes encode the same list of ten types
// independently. Nothing keeps them in sync, except this test.
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
		t.Fatalf("unreadable embedded schema: %v", err)
	}
	fromSchema := doc.Defs.Property.Properties.Type.Enum
	if len(fromSchema) == 0 {
		t.Fatal("no type read from the schema: the schema structure changed")
	}
	if len(fromSchema) != len(SupportedPropertyTypes) {
		t.Fatalf("schema: %d types, SupportedPropertyTypes: %d — the two lists drifted\n  schema: %v\n  Go: %v",
			len(fromSchema), len(SupportedPropertyTypes), fromSchema, SupportedPropertyTypes)
	}
	inGo := make(map[string]bool, len(SupportedPropertyTypes))
	for _, t := range SupportedPropertyTypes {
		inGo[t] = true
	}
	for _, t2 := range fromSchema {
		if !inGo[t2] {
			t.Errorf("type %q present in the schema but missing from SupportedPropertyTypes", t2)
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
		t.Fatal("ValidateDocument() error = nil, want the key `Mon Projet` rejected")
	}
}

func TestValidateDocumentRejectsMalformedYAML(t *testing.T) {
	doc := []byte("version: 1\ndatabases:\n  - name: [unclosed\n")
	err := ValidateDocument("databases/projects.yaml", doc)
	if err == nil {
		t.Fatal("ValidateDocument() error = nil, want a YAML parsing error")
	}
	if !strings.Contains(err.Error(), "databases/projects.yaml") {
		t.Errorf("message = %q, it must name the file", err.Error())
	}
}

// A status option without a group must be rejected at load time. It is the
// counterpart of removing the "To-do" default in the mapper: without a
// default, an option without a group would produce a payload the API would
// silently put in the first group.
func TestValidateRejectsStatusOptionWithoutGroup(t *testing.T) {
	doc := []byte(`
databases:
  - key: tasks
    name: "Tasks"
    properties:
      Statut:
        type: status
        options:
          - key: todo
            name: "À faire"
`)
	err := ValidateDocument("databases/tasks.yaml", doc)
	if err == nil {
		t.Fatal("ValidateDocument() = nil, want the option without a group rejected")
	}
	for _, want := range []string{"group", "À faire", "To-do", "  → "} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message = %q, it must contain %q", err.Error(), want)
		}
	}
}

// A status property without an `options` block would go to the API with an
// empty list. Notion would then fill it with its own default options, which
// the plan never showed — and whose later removal is classified as a silent
// rewrite, which nothing unblocks. Declaring a status without saying what it
// holds is the same abdication as not declaring its group.
func TestValidateRejectsStatusPropertyWithoutOptions(t *testing.T) {
	doc := []byte(`
databases:
  - key: tasks
    name: "Tasks"
    properties:
      Statut:
        type: status
`)
	err := ValidateDocument("databases/tasks.yaml", doc)
	if err == nil {
		t.Fatal("ValidateDocument() = nil, want the status without options rejected")
	}
	for _, want := range []string{"options", "  → "} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message = %q, it must contain %q", err.Error(), want)
		}
	}
}

// An empty options list is no better than a missing one.
func TestValidateRejectsStatusPropertyWithEmptyOptions(t *testing.T) {
	doc := []byte(`
databases:
  - key: tasks
    name: "Tasks"
    properties:
      Statut:
        type: status
        options: []
`)
	if err := ValidateDocument("databases/tasks.yaml", doc); err == nil {
		t.Fatal("ValidateDocument() = nil, want the empty list rejected")
	}
}

// select and multi_select keep their freedom: their options can be managed by
// hand in Notion without risk of a silent rewrite.
func TestValidateAcceptsSelectWithoutOptions(t *testing.T) {
	doc := []byte(`
databases:
  - key: tasks
    name: "Tasks"
    properties:
      Priorité:
        type: select
`)
	if err := ValidateDocument("databases/tasks.yaml", doc); err != nil {
		t.Fatalf("ValidateDocument() = %v, want nil", err)
	}
}

// Symmetric with the handling of `format`: `group` only makes sense on status.
func TestValidateRejectsGroupOnSelectOption(t *testing.T) {
	doc := []byte(`
databases:
  - key: tasks
    name: "Tasks"
    properties:
      Priorité:
        type: select
        options:
          - name: "Haute"
            group: "To-do"
`)
	err := ValidateDocument("databases/tasks.yaml", doc)
	if err == nil {
		t.Fatal("ValidateDocument() = nil, want group on a select rejected")
	}
	if !strings.Contains(err.Error(), "group") {
		t.Errorf("message = %q, it must name `group`", err.Error())
	}
}

// The happy path must not regress.
func TestValidateAcceptsStatusOptionWithGroup(t *testing.T) {
	doc := []byte(`
databases:
  - key: tasks
    name: "Tasks"
    properties:
      Statut:
        type: status
        options:
          - key: todo
            name: "À faire"
            group: "To-do"
`)
	if err := ValidateDocument("databases/tasks.yaml", doc); err != nil {
		t.Fatalf("ValidateDocument() = %v, want nil", err)
	}
}

// The schema accepts the names before the rename for one version, alone or
// next to the current ones: the loader warns, it does not refuse.
func TestValidateDocumentAcceptsDeprecatedLifecycleKeys(t *testing.T) {
	for _, doc := range []string{
		"lifecycle:\n  prevent_destroy: [database.a]\n  allow_data_loss: [database.b]\n",
		"lifecycle:\n  prevent_destroy: [database.a]\n  acknowledge_destroy: [database.b]\n" +
			"  allow_data_loss: []\n  acknowledge_data_loss: [database.c]\n",
	} {
		if err := ValidateDocument("workspace.yaml", []byte(doc)); err != nil {
			t.Errorf("ValidateDocument(%q) error = %v", doc, err)
		}
	}
}
