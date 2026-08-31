package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/tykok/notion-seed/schema"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
	"gopkg.in/yaml.v3"
)

// msgPrinter formate les messages d'erreur du validateur JSON Schema.
// La bibliothèque accepte un *message.Printer nil dans son interface
// publique (ErrorKind.LocalizedString), mais certains ErrorKind internes
// (kind.Enum notamment) appellent p.Sprintf sans vérifier p == nil et
// paniquent. On passe donc toujours un printer concret.
var msgPrinter = message.NewPrinter(language.English)

// ValidationError porte une erreur de validation avec de quoi la corriger.
type ValidationError struct {
	Path    string // fichier
	Pointer string // pointeur JSON dans le document
	Message string
	Hint    string // conseil ciblé, vide si aucun ne s'applique
}

func (e *ValidationError) Error() string {
	var b strings.Builder
	b.WriteString(e.Path)
	if e.Pointer != "" && e.Pointer != "/" {
		b.WriteString(": ")
		b.WriteString(e.Pointer)
	}
	b.WriteString(": ")
	b.WriteString(e.Message)
	if e.Hint != "" {
		b.WriteString("\n  → ")
		b.WriteString(e.Hint)
	}
	return b.String()
}

var compiled = func() *jsonschema.Schema {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schema.Bytes))
	if err != nil {
		panic("schéma embarqué illisible: " + err.Error())
	}
	c := jsonschema.NewCompiler()
	const id = "notion-seed.schema.json"
	if err := c.AddResource(id, doc); err != nil {
		panic("schéma embarqué invalide: " + err.Error())
	}
	s, err := c.Compile(id)
	if err != nil {
		panic("schéma embarqué non compilable: " + err.Error())
	}
	return s
}()

// ValidateDocument parse un document YAML et le valide contre le JSON Schema.
func ValidateDocument(path string, yamlBytes []byte) error {
	var raw any
	if err := yaml.Unmarshal(yamlBytes, &raw); err != nil {
		return &ValidationError{Path: path, Message: "YAML illisible: " + err.Error()}
	}
	// Passer par JSON normalise les types (yaml.v3 rend map[string]any, mais
	// les entiers et les dates diffèrent du modèle JSON attendu par le
	// validateur).
	jsonBytes, err := json.Marshal(raw)
	if err != nil {
		return &ValidationError{Path: path, Message: "document non convertible en JSON: " + err.Error()}
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(jsonBytes))
	if err != nil {
		return &ValidationError{Path: path, Message: err.Error()}
	}

	verr := compiled.Validate(doc)
	if verr == nil {
		return nil
	}
	var ve *jsonschema.ValidationError
	if !asValidationError(verr, &ve) {
		return &ValidationError{Path: path, Message: verr.Error()}
	}
	leaf := deepestCause(ve)
	pointer := "/" + strings.Join(leaf.InstanceLocation, "/")
	return &ValidationError{
		Path:    path,
		Pointer: pointer,
		Message: leaf.ErrorKind.LocalizedString(msgPrinter),
		Hint:    hintFor(pointer, jsonBytes),
	}
}

func asValidationError(err error, target **jsonschema.ValidationError) bool {
	ve, ok := err.(*jsonschema.ValidationError)
	if ok {
		*target = ve
	}
	return ok
}

// deepestCause descend jusqu'à l'erreur la plus spécifique : la racine dit
// seulement « le document ne valide pas ».
func deepestCause(ve *jsonschema.ValidationError) *jsonschema.ValidationError {
	for len(ve.Causes) > 0 {
		ve = ve.Causes[0]
	}
	return ve
}

// hintFor produit un conseil ciblé sur les pièges connus de l'API Notion.
// Le cas central : l'API exige "To-do" avec un trait d'union. Un message
// d'énumération brut ne le rend pas visible.
func hintFor(pointer string, jsonBytes []byte) string {
	if !strings.HasSuffix(pointer, "/group") {
		return ""
	}
	var doc any
	if err := json.Unmarshal(jsonBytes, &doc); err != nil {
		return ""
	}
	got, ok := valueAtPointer(doc, pointer).(string)
	if !ok {
		return fmt.Sprintf("`group` n'accepte que %s.", quotedList(StatusGroups))
	}
	if normalizeGroup(got) == normalizeGroup("To-do") {
		return `l'API Notion exige "To-do" avec un trait d'union. Remplacez ` +
			fmt.Sprintf("%q par \"To-do\".", got)
	}
	return fmt.Sprintf(
		"`group` n'accepte que %s — l'API Notion refuse les groupes nommés librement. Trouvé %q.",
		quotedList(StatusGroups), got)
}

func normalizeGroup(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "-", "")
	return strings.ReplaceAll(s, " ", "")
}

func quotedList(items []string) string {
	q := make([]string, len(items))
	for i, s := range items {
		q[i] = fmt.Sprintf("%q", s)
	}
	return strings.Join(q, ", ")
}

// valueAtPointer résout un pointeur JSON simple (sans échappement) sur un
// document décodé.
func valueAtPointer(doc any, pointer string) any {
	cur := doc
	for _, token := range strings.Split(strings.TrimPrefix(pointer, "/"), "/") {
		if token == "" {
			continue
		}
		switch node := cur.(type) {
		case map[string]any:
			cur = node[token]
		case []any:
			var idx int
			if _, err := fmt.Sscanf(token, "%d", &idx); err != nil || idx >= len(node) {
				return nil
			}
			cur = node[idx]
		default:
			return nil
		}
	}
	return cur
}
