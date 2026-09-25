// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"github.com/tykok/notion-seed/schema"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
	"gopkg.in/yaml.v3"
)

// msgPrinter formats the JSON Schema validator's error messages. The library
// accepts a nil *message.Printer in its public interface
// (ErrorKind.LocalizedString), but some internal ErrorKinds (kind.Enum in
// particular) call p.Sprintf without checking p == nil and panic. So a
// concrete printer is always passed.
var msgPrinter = message.NewPrinter(language.English)

// ValidationError holds a validation error with what is needed to fix it.
type ValidationError struct {
	Path    string // file
	Pointer string // JSON pointer in the document
	Message string
	Hint    string // targeted advice, empty if none applies
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
		panic("unreadable embedded schema: " + err.Error())
	}
	c := jsonschema.NewCompiler()
	const id = "notion-seed.schema.json"
	if err := c.AddResource(id, doc); err != nil {
		panic("invalid embedded schema: " + err.Error())
	}
	s, err := c.Compile(id)
	if err != nil {
		panic("embedded schema does not compile: " + err.Error())
	}
	return s
}()

// ValidateDocument parses a YAML document and validates it against the JSON Schema.
func ValidateDocument(path string, yamlBytes []byte) error {
	var raw any
	if err := yaml.Unmarshal(yamlBytes, &raw); err != nil {
		return &ValidationError{Path: path, Message: "unreadable YAML: " + err.Error()}
	}
	// Going through JSON normalizes the types (yaml.v3 returns map[string]any,
	// but integers and dates differ from the JSON model the validator
	// expects).
	jsonBytes, err := json.Marshal(raw)
	if err != nil {
		return &ValidationError{Path: path, Message: "document cannot be converted to JSON: " + err.Error()}
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
		Hint:    hintFor(pointer, leaf.ErrorKind, jsonBytes),
	}
}

func asValidationError(err error, target **jsonschema.ValidationError) bool {
	ve, ok := err.(*jsonschema.ValidationError)
	if ok {
		*target = ve
	}
	return ok
}

// deepestCause goes down to the most specific error: the root only says "the
// document does not validate".
func deepestCause(ve *jsonschema.ValidationError) *jsonschema.ValidationError {
	for len(ve.Causes) > 0 {
		ve = ve.Causes[0]
	}
	return ve
}

// hintFor produces targeted advice where the library's message is not enough.
// Two families of cases:
//
//  1. The Notion API traps, the central one being: it requires "To-do" with a
//     hyphen, and a raw enum message does not make that visible.
//  2. The constraints the schema expresses with a `not`. The library then
//     returns "'not' failed" and nothing else — the `not` keyword only says
//     "this should not have validated", it carries no information about what
//     wrongly validated. So the context must come from here.
//
// The ErrorKind is passed, not guessed from the document's shape: a hint that
// filters on shape also fires when the real failure is elsewhere. Measured: a
// property without `type` but with `options` produced "missing property
// 'type'" followed by "remove the options block", the opposite of the fix. A
// wrong message is worth less than an empty one.
func hintFor(pointer string, errKind any, jsonBytes []byte) string {
	var doc any
	if err := json.Unmarshal(jsonBytes, &doc); err != nil {
		return ""
	}

	// `group` missing on a status option. The raw message says "missing
	// property "group"" without explaining why this field is required here when
	// it is required nowhere else — and that is precisely the question the user
	// is asking.
	if req, isRequired := errKind.(*kind.Required); isRequired {
		for _, missing := range req.Missing {
			switch missing {
			case "group":
				name := ""
				if opt, ok := valueAtPointer(doc, pointer).(map[string]any); ok {
					name, _ = opt["name"].(string)
				}
				return fmt.Sprintf(
					"`group` is required on every status option (here %q) and accepts only %s. "+
						"notion-seed does not pick a group for you: an option sent without a "+
						"group is put by the API in the first group, without an error.",
					name, quotedList(StatusGroups))

			case "options":
				// Applies to status only: it is the only type that requires it.
				if prop, ok := valueAtPointer(doc, pointer).(map[string]any); ok {
					if t, _ := prop["type"].(string); t != "status" {
						continue
					}
				}
				return "a status property must declare its `options`. Created without them, " +
					"it gets filled by the API with its own default options, which the plan will " +
					"not have shown — and removing them later silently reassigns the " +
					"rows, a loss `acknowledge_data_loss` only acknowledges, it does not undo it."
			}
		}
	}

	// `not` case ONLY: the pointer designates the PROPERTY, not the offending
	// field, so its content is inspected to name the extra field.
	if _, isNot := errKind.(*kind.Not); isNot {
		// `group` on a non-status option. The pointer designates the OPTION, so
		// the offending type is read one level up: the property holds the type,
		// not the option. Without this case, the message boils down to "'not'
		// failed", which does not even name the extra field.
		if opt, ok := valueAtPointer(doc, pointer).(map[string]any); ok {
			if _, hasGroup := opt["group"]; hasGroup {
				t := ""
				if prop, ok := valueAtPointer(doc, propertyPointerOf(pointer)).(map[string]any); ok {
					t, _ = prop["type"].(string)
				}
				return fmt.Sprintf(
					"`group` exists only on the options of a status property. "+
						"This property is of type %q — remove the `group` field.", t)
			}
		}
		if prop, ok := valueAtPointer(doc, pointer).(map[string]any); ok {
			t, _ := prop["type"].(string)
			if _, hasOptions := prop["options"]; hasOptions && !acceptsOptions(t) {
				return fmt.Sprintf(
					"`options` exists only on the select, status and multi_select types. The property is of type %q — remove the `options` block.",
					t)
			}
			if _, hasFormat := prop["format"]; hasFormat && t != "number" {
				return fmt.Sprintf(
					"`format` exists only on the number type. The property is of type %q — remove the `format` field.",
					t)
			}
		}
		return ""
	}

	// YAML round-trip trap, on a pattern failure ONLY: an unquoted date-shaped
	// scalar is resolved by yaml.v3 into a time.Time, then rendered as RFC3339.
	// Careful wording: only the decoded value is visible, never the source, so
	// there is no way to know whether the user had already quoted it —
	// claiming they did not would be wrong half the time.
	if _, isPattern := errKind.(*kind.Pattern); isPattern {
		// The parent_page_id UUID pattern is the entry point of every `plan`:
		// the validator's raw message shows the regular expression, which does
		// not say where to find the right value.
		if strings.HasSuffix(pointer, "/parent_page_id") {
			return "the parent page id is a UUID: open the page in Notion and " +
				"copy the 32 hexadecimal characters at the end of its URL (with or without dashes)."
		}
		if got, ok := valueAtPointer(doc, pointer).(string); ok && looksLikeTimestamp(got) {
			return fmt.Sprintf(
				"if this value was not quoted in the YAML, it was interpreted as a date and becomes %q — in that case, add quotes so it stays text.",
				got)
		}
	}

	if !strings.HasSuffix(pointer, "/group") {
		return ""
	}
	got, ok := valueAtPointer(doc, pointer).(string)
	if !ok {
		return fmt.Sprintf("`group` accepts only %s.", quotedList(StatusGroups))
	}
	if normalizeGroup(got) == normalizeGroup("To-do") {
		return `the Notion API requires "To-do" with a hyphen. Replace ` +
			fmt.Sprintf("%q with \"To-do\".", got)
	}
	return fmt.Sprintf(
		"`group` accepts only %s — the Notion API rejects freely named groups. Found %q.",
		quotedList(StatusGroups), got)
}

// propertyPointerOf goes up from an option's pointer to its property's, by
// removing the `/options/<n>` suffix. An option's type is not held by the
// option: it is on the property.
func propertyPointerOf(optionPointer string) string {
	if i := strings.LastIndex(optionPointer, "/options/"); i >= 0 {
		return optionPointer[:i]
	}
	return optionPointer
}

// acceptsOptions says whether a property type accepts an `options` block.
func acceptsOptions(t string) bool {
	return t == "select" || t == "status" || t == "multi_select"
}

// looksLikeTimestamp recognizes the RFC3339 shape json.Marshal produces from
// a time.Time coming out of the YAML round-trip.
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

// valueAtPointer resolves a simple JSON pointer (without escaping) on a
// decoded document.
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
			// Guard on both bounds. This code only runs AFTER something else has
			// already failed: it must never be able to crash the CLI.
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
