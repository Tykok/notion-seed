package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"time"

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

// hintFor produit un conseil ciblé là où le message de la bibliothèque ne suffit
// pas. Deux familles de cas :
//
//  1. Les pièges de l'API Notion, dont le central : elle exige "To-do" avec un
//     trait d'union, et un message d'énumération brut ne le rend pas visible.
//  2. Les contraintes que le schéma exprime par un `not`. La bibliothèque rend
//     alors « 'not' failed » et rien d'autre — le mot-clé `not` dit seulement
//     « ceci n'aurait pas dû valider », il ne porte aucune information sur ce
//     qui a validé à tort. Le contexte doit donc venir d'ici.
func hintFor(pointer string, jsonBytes []byte) string {
	var doc any
	if err := json.Unmarshal(jsonBytes, &doc); err != nil {
		return ""
	}

	// Cas `not` : le pointeur désigne la PROPRIÉTÉ, pas le champ fautif, donc
	// on regarde ce qu'elle contient pour nommer le champ en trop.
	if prop, ok := valueAtPointer(doc, pointer).(map[string]any); ok {
		t, _ := prop["type"].(string)
		if _, hasOptions := prop["options"]; hasOptions && !acceptsOptions(t) {
			return fmt.Sprintf(
				"`options` n'existe que sur les types select, status et multi_select. La propriété est de type %q — retirez le bloc `options`.",
				t)
		}
		if _, hasFormat := prop["format"]; hasFormat && t != "number" {
			return fmt.Sprintf(
				"`format` n'existe que sur le type number. La propriété est de type %q — retirez le champ `format`.",
				t)
		}
	}

	// Piège du round-trip YAML : un scalaire non quoté en forme de date est
	// résolu par yaml.v3 en time.Time, puis rendu en RFC3339. L'utilisateur est
	// alors jugé sur une valeur qu'il n'a jamais écrite.
	if got, ok := valueAtPointer(doc, pointer).(string); ok && looksLikeTimestamp(got) {
		return fmt.Sprintf(
			"la valeur a été interprétée comme une date par YAML et devient %q. Entourez-la de guillemets pour qu'elle reste du texte.",
			got)
	}

	if !strings.HasSuffix(pointer, "/group") {
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

// acceptsOptions dit si un type de propriété accepte un bloc `options`.
func acceptsOptions(t string) bool {
	return t == "select" || t == "status" || t == "multi_select"
}

// looksLikeTimestamp reconnaît la forme RFC3339 que json.Marshal produit à
// partir d'un time.Time issu du round-trip YAML.
func looksLikeTimestamp(v string) bool {
	_, err := time.Parse(time.RFC3339, v)
	return err == nil
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
			// Garde sur les deux bornes. Ce code ne s'exécute qu'APRÈS qu'autre
			// chose a déjà échoué : il ne doit jamais pouvoir faire planter la CLI.
			var idx int
			if _, err := fmt.Sscanf(token, "%d", &idx); err != nil || idx < 0 || idx >= len(node) {
				return nil
			}
			cur = node[idx]
		default:
			return nil
		}
	}
	return cur
}
