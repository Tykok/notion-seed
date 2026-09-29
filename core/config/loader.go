// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// WorkspaceFile is the global configuration file, at the root of the config
// directory.
const WorkspaceFile = "workspace.yaml"

// DatabasesDir holds one file per database, the way Terraform loads every .tf
// of a directory. No include system.
const DatabasesDir = "databases"

// DuplicateKeyError reports two resources declaring the same key. It names
// both files: without that, the user has to hunt for the duplicate by hand.
type DuplicateKeyError struct {
	Key        string
	FirstFile  string
	SecondFile string
}

func (e *DuplicateKeyError) Error() string {
	return fmt.Sprintf(
		"key %q declared twice: in %s and in %s\n"+
			"  → a key is an identity, it must be unique across all "+
			"files: rename one of the two declarations, or merge them",
		e.Key, e.FirstFile, e.SecondFile)
}

// globalSections are the sections only workspace.yaml may hold. Two of them
// decide something sensitive: `workspace` picks the write target, `lifecycle`
// holds the acknowledgements the plan shows.
var globalSections = []string{"version", "workspace", "lifecycle"}

// document is the shape of an individual config file. Every field is
// optional: workspace.yaml holds the workspace, the files of databases/ hold
// databases.
type document struct {
	Version   int        `yaml:"version"`
	Workspace *Workspace `yaml:"workspace"`
	Databases []Database `yaml:"databases"`
	Lifecycle *Lifecycle `yaml:"lifecycle"`
}

// Load loads and merges the whole configuration of a directory.
//
// The order of the passes is mandatory: each file is validated individually,
// THEN everything is merged, THEN global key uniqueness is checked. Checking
// uniqueness file by file would let two files declare the same key without
// any validation failing.
func Load(dir string) (*Config, error) {
	wsPath := filepath.Join(dir, WorkspaceFile)
	if _, err := os.Stat(wsPath); err != nil {
		return nil, fmt.Errorf(
			"%s not found in %s\n"+
				"  → create it with `version: 1` and `workspace.parent_page_id: \"<parent page id>\"`, "+
				"or point to the right directory with --dir",
			WorkspaceFile, dir)
	}

	paths := []string{wsPath}
	dbPaths, err := yamlFiles(filepath.Join(dir, DatabasesDir))
	if err != nil {
		return nil, err
	}
	paths = append(paths, dbPaths...)

	cfg := &Config{Version: 1}
	// firstSeen records, for each key, the file that declared it.
	firstSeen := make(map[string]string)

	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf(
				"failed to read %s: %w\n"+
					"  → %s and the files of databases/ must be readable YAML "+
					"files; if this one is a directory, rename or delete it",
				path, err, WorkspaceFile)
		}

		// The sections present are read on a LOOSE shape, not on `document`: a
		// value of the wrong type would produce a yaml decoding error there
		// before anything useful could be said, whereas the schema can name
		// it.
		var top map[string]any
		if err := yaml.Unmarshal(raw, &top); err != nil {
			if isNonMappingRoot(err) {
				return nil, &ValidationError{
					Path:    path,
					Message: "the document root is not a mapping (key: value)",
					Hint: fmt.Sprintf(
						"a config file must be a mapping at its root — a list of "+
							"databases is declared under a `databases:` key, not directly at the "+
							"root (cause: %s)", err),
				}
			}
			return nil, &ValidationError{Path: path, Message: "unreadable YAML: " + err.Error()}
		}

		// A file of databases/ holds ONLY databases. Demonstrated:
		// `databases/z.yaml` declaring `workspace.parent_page_id` hijacked the
		// write target without a warning, and a `lifecycle: {}` there erased
		// the acknowledgements. With workspace.yaml processed first, any file of
		// databases/ won.
		if path != wsPath {
			if err := rejectGlobalSections(path, top); err != nil {
				return nil, err
			}
		}

		// For workspace.yaml, check the version BEFORE schema validation,
		// otherwise the schema rejects a wrong version with its own message
		// before ours, more useful, can be given. Any other document holding
		// `version` has already been rejected above, with its own message: the
		// schema's generic enum message never comes out on this field.
		if path == wsPath {
			if err := checkVersion(path, raw, top); err != nil {
				return nil, err
			}
		}

		if err := ValidateDocument(path, raw); err != nil {
			return nil, err
		}
		var doc document
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			return nil, &ValidationError{Path: path, Message: "unreadable YAML: " + err.Error()}
		}
		if doc.Workspace != nil {
			cfg.Workspace = *doc.Workspace
		}
		if doc.Lifecycle != nil {
			cfg.Lifecycle = *doc.Lifecycle
			cfg.Warnings = append(cfg.Warnings, deprecatedLifecycleWarnings(path, top)...)
		}
		for _, db := range doc.Databases {
			db.SourceFile = path
			if err := checkExactlyOneTitle(db); err != nil {
				return nil, err
			}
			if err := checkUniqueOptions(db); err != nil {
				return nil, err
			}
			cfg.Databases = append(cfg.Databases, db)
		}
	}

	// Uniqueness of explicit keys, across all files.
	for _, db := range cfg.Databases {
		if db.Key == "" {
			continue
		}
		if first, seen := firstSeen[db.Key]; seen {
			return nil, &DuplicateKeyError{
				Key:        db.Key,
				FirstFile:  first,
				SecondFile: db.SourceFile,
			}
		}
		firstSeen[db.Key] = db.SourceFile
	}

	// Missing keys are derived and deduplicated afterwards, once all explicit
	// keys are known.
	cfg.Databases = ResolveKeys(cfg.Databases, parentNameHint(cfg))

	// Deterministic order: the plan output must be stable between two runs.
	sort.Slice(cfg.Databases, func(i, j int) bool {
		return cfg.Databases[i].Key < cfg.Databases[j].Key
	})
	return cfg, nil
}

// rejectGlobalSections rejects global sections in a file other than
// workspace.yaml. The file AND the offending section are named: without that,
// the user does not know which of their files won the merge.
func rejectGlobalSections(path string, top map[string]any) error {
	var found []string
	for _, section := range globalSections {
		if _, ok := top[section]; ok {
			found = append(found, section)
		}
	}
	if len(found) == 0 {
		return nil
	}
	subject := fmt.Sprintf("section %s not allowed here", quotedList(found))
	if len(found) > 1 {
		subject = fmt.Sprintf("sections %s not allowed here", quotedList(found))
	}
	return &ValidationError{
		Path: path,
		Message: fmt.Sprintf(
			"%s: a file of %s/ declares only databases",
			subject, DatabasesDir),
		Hint: globalSectionsHint(found),
	}
}

// globalSectionsHint gives the right action, per section. `version` is
// DELETED: workspace.yaml already declares `version: 1`, moving it would have
// no effect. `workspace` and `lifecycle` are MOVED, with a reminder of what
// they do elsewhere than here. Measured: a single "move" advice said to move
// `version`, which nobody can usefully do — it was the only case where
// following the message led to the wrong action.
func globalSectionsHint(found []string) string {
	var move []string
	removeVersion := false
	for _, section := range found {
		if section == "version" {
			removeVersion = true
			continue
		}
		move = append(move, section)
	}

	var parts []string
	if len(move) > 0 {
		parts = append(parts, fmt.Sprintf(
			"move %s to %s, the only file that holds the global configuration — "+
				"otherwise `workspace.parent_page_id` there hijacks the write target and "+
				"`lifecycle` there silently replaces the acknowledgements of %s",
			quotedList(move), WorkspaceFile, WorkspaceFile))
	}
	if removeVersion {
		parts = append(parts, fmt.Sprintf(
			"delete `version`: %s already declares it, it does not belong here",
			WorkspaceFile))
	}
	return strings.Join(parts, "; ")
}

// renamedLifecycleKeys pairs each deprecated lifecycle key with its current
// name, in the order the warnings come out.
var renamedLifecycleKeys = []struct{ old, current string }{
	{KeyPreventDestroy, KeyAcknowledgeDestroy},
	{KeyAllowDataLoss, KeyAcknowledgeDataLoss},
}

// deprecatedLifecycleWarnings warns about each lifecycle key still written
// under its name from before the rename. Presence is read on the loose shape:
// an empty `prevent_destroy: []` must be renamed just the same.
//
// Old and new name side by side are MERGED, not refused: lifecycle blocks
// nothing any more, and failing a command over a half-done rename would break
// exactly what the deprecation window exists to spare. The advice changes
// there: renaming the old key would give a duplicate YAML key, which the
// parser rejects, so the warning says to move its entries.
func deprecatedLifecycleWarnings(path string, top map[string]any) []string {
	section, _ := top["lifecycle"].(map[string]any)
	var out []string
	for _, k := range renamedLifecycleKeys {
		if _, ok := section[k.old]; !ok {
			continue
		}
		if _, both := section[k.current]; both {
			out = append(out, fmt.Sprintf(
				"%s: `lifecycle.%s` and `lifecycle.%s` are both set, their entries are "+
					"merged — the old name is still read in this version only\n"+
					"  → move the entries of `%s` into `%s` in %s, then delete `%s`",
				path, k.old, k.current, k.old, k.current, path, k.old))
			continue
		}
		out = append(out, fmt.Sprintf(
			"%s: `lifecycle.%s` is deprecated, it is now `lifecycle.%s` — the old "+
				"name is still read in this version only\n"+
				"  → rename `%s` to `%s` in %s",
			path, k.old, k.current, k.old, k.current, path))
	}
	return out
}

// isNonMappingRoot recognizes the specific YAML decoding failure where the
// document root is not a mapping (a sequence or a bare scalar):
// gopkg.in/yaml.v3 then returns a *yaml.TypeError naming the rejected type. A
// real syntax error (indentation, missing ':'...) returns a different error
// type and is not caught here: it keeps the raw "unreadable YAML" message, for
// lack of anything better to say.
func isNonMappingRoot(err error) bool {
	te, ok := err.(*yaml.TypeError)
	if !ok || len(te.Errors) != 1 {
		return false
	}
	return strings.Contains(te.Errors[0], "into map[string]interface {}")
}

// checkVersion requires `version: 1`. The field's presence is read on the
// loose shape, its value on `document`: telling "missing" from "present but
// wrong" changes the advice, and `version: 0` is present, not missing.
func checkVersion(path string, raw []byte, top map[string]any) error {
	var doc document
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return &ValidationError{Path: path, Message: "unreadable YAML: " + err.Error()}
	}
	value, present := top["version"]
	if present && doc.Version == 1 {
		return nil
	}
	hint := "add `version: 1` at the top of the file"
	what := "missing"
	if present {
		hint = fmt.Sprintf("replace `version: %v` with `version: 1`", value)
		what = fmt.Sprintf("found %v", value)
	}
	return &ValidationError{
		Path:    path,
		Message: fmt.Sprintf("`version: 1` is required in %s (%s)", WorkspaceFile, what),
		Hint:    hint,
	}
}

// checkExactlyOneTitle rejects a database that does not declare exactly one
// title property. The Notion API accepts only one per data source, neither
// zero nor two; JSON Schema cannot count over `additionalProperties`, so the
// check lives here. Without it, `plan` announces a creation the API will
// certainly reject — a defect in the tool's central promise, not a missing
// feature.
func checkExactlyOneTitle(db Database) error {
	var titles []string
	for name, p := range db.Properties {
		if p.Type == "title" {
			titles = append(titles, name)
		}
	}
	sort.Strings(titles)
	switch {
	case len(titles) == 0:
		return &ValidationError{
			Path: db.SourceFile,
			Message: fmt.Sprintf(
				"database %q declares no title property", db.Name),
			Hint: "the Notion API requires exactly one per database — switch one of its " +
				"properties to `type: title`, or add `Name: {type: title}`",
		}
	case len(titles) > 1:
		return &ValidationError{
			Path: db.SourceFile,
			Message: fmt.Sprintf(
				"database %q declares %d title properties: %s",
				db.Name, len(titles), quotedList(titles)),
			Hint: "the Notion API accepts only one per database — keep a single one and " +
				"give the others another type (`rich_text` for free text)",
		}
	}
	return nil
}

// checkUniqueOptions rejects two options with the same key, or the same name,
// in one property. JSON Schema cannot express the uniqueness of a field in a
// list of objects, so the check lives here.
//
// Both duplicates break the matching with remote options, each in its own
// way:
//   - two identical keys designate the SAME remote option, whose id would go
//     out twice in a single PATCH;
//   - two identical names: the second no longer finds a free position and goes
//     out as a new option carrying a name that already exists — a 400 from the
//     API, AFTER the database PATCH went through.
//
// Rejecting at load time stops both before any call.
func checkUniqueOptions(db Database) error {
	names := make([]string, 0, len(db.Properties))
	for name := range db.Properties {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, prop := range names {
		keys := map[string]bool{}
		optNames := map[string]bool{}
		for _, o := range db.Properties[prop].Options {
			if o.Key != "" {
				if keys[o.Key] {
					return &ValidationError{
						Path: db.SourceFile,
						Message: fmt.Sprintf(
							"database %q, property %q: two options have the key %q",
							db.Name, prop, o.Key),
						Hint: "an option's key is its identity: give each option " +
							"of the property a distinct key",
					}
				}
				keys[o.Key] = true
			}
			if optNames[o.Name] {
				return &ValidationError{
					Path: db.SourceFile,
					Message: fmt.Sprintf(
						"database %q, property %q: two options have the name %q",
						db.Name, prop, o.Name),
					Hint: "two options with the same name are indistinguishable, for Notion as " +
						"for notion-seed: rename one of them, or remove the duplicate",
				}
			}
			optNames[o.Name] = true
		}
	}
	return nil
}

// parentNameHint provides the key disambiguation prefix. In MVP 0 pages are
// not resources, so their name is not known: it falls back to the parent
// page's id, truncated. When pages arrive (post-MVP), replace it with the
// page's real name.
func parentNameHint(cfg *Config) string {
	id := cfg.Workspace.ParentPageID
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// yamlFiles lists the *.yaml of a directory, excluding hidden files. A missing
// directory is not an error: a config may have no database.
func yamlFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || strings.HasPrefix(name, ".") {
			continue
		}
		if ext := filepath.Ext(name); ext != ".yaml" && ext != ".yml" {
			continue
		}
		out = append(out, filepath.Join(dir, name))
	}
	sort.Strings(out)
	return out, nil
}
