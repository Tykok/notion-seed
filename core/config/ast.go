// SPDX-License-Identifier: GPL-3.0-or-later

// Package config loads and validates notion-seed's YAML configuration.
package config

// StatusGroups lists the only status groups the Notion API accepts, on
// creation as on update. Freely named groups are not supported: the API
// answers 400 validation_error.
var StatusGroups = []string{"To-do", "In progress", "Complete"}

// SupportedPropertyTypes lists the types handled in MVP 0. What is not
// declared is not touched; what is declared with another type is rejected.
var SupportedPropertyTypes = []string{
	"title", "rich_text", "number", "url", "select",
	"status", "multi_select", "date", "checkbox", "people",
}

// Config is the full configuration, after merging all the files.
type Config struct {
	Version   int        `yaml:"version"`
	Workspace Workspace  `yaml:"workspace"`
	Databases []Database `yaml:"databases"`
	Lifecycle Lifecycle  `yaml:"lifecycle"`

	// Warnings are what Load read without refusing it but that the user must
	// fix — a deprecated key name. Each one names its file and carries its
	// action on a line introduced by "  → ". The commands print them on stderr.
	Warnings []string `yaml:"-"`
}

type Workspace struct {
	ParentPageID string `yaml:"parent_page_id"`
}

type Database struct {
	Key         string              `yaml:"key"`
	Name        string              `yaml:"name"`
	Description string              `yaml:"description"`
	Icon        string              `yaml:"icon"`
	Properties  map[string]Property `yaml:"properties"`

	// SourceFile is the file this database comes from. Used by the key
	// uniqueness error messages, which must name both files involved.
	SourceFile string `yaml:"-"`
}

type Property struct {
	Type    string   `yaml:"type"`
	Format  string   `yaml:"format"`
	Options []Option `yaml:"options"`
}

type Option struct {
	Key   string `yaml:"key"`
	Name  string `yaml:"name"`
	Color string `yaml:"color"`
	Group string `yaml:"group"`
}

// Lifecycle holds acknowledgements of reading. Listing a resource here
// prevents nothing and allows nothing: notion-seed never refuses a change on
// its class, it measures and warns. The plan only notes, on the resource's
// line, that the risk was read.
type Lifecycle struct {
	AcknowledgeDestroy  []string `yaml:"acknowledge_destroy"`
	AcknowledgeDataLoss []string `yaml:"acknowledge_data_loss"`

	// The names before the rename, still read for one version with a warning.
	// They are kept apart rather than merged at load time: the plan names the
	// key as the user wrote it, so that the line matches their YAML.
	DeprecatedPreventDestroy []string `yaml:"prevent_destroy"`
	DeprecatedAllowDataLoss  []string `yaml:"allow_data_loss"`
}

// Lifecycle key names, current and deprecated.
const (
	KeyAcknowledgeDestroy  = "acknowledge_destroy"
	KeyAcknowledgeDataLoss = "acknowledge_data_loss"
	KeyPreventDestroy      = "prevent_destroy"
	KeyAllowDataLoss       = "allow_data_loss"
)

// Acknowledged names the lifecycle keys that cover resource, as written in
// the configuration: destruction first, then data loss — the order is part of
// the rendering's contract. A resource listed under both the current and the
// deprecated name of one key is named once, by the current name. A resource
// listed twice under the same key counts once.
func (l Lifecycle) Acknowledged(resource string) []string {
	var out []string
	if name := coveredBy(resource, l.AcknowledgeDestroy, l.DeprecatedPreventDestroy,
		KeyAcknowledgeDestroy, KeyPreventDestroy); name != "" {
		out = append(out, name)
	}
	if name := coveredBy(resource, l.AcknowledgeDataLoss, l.DeprecatedAllowDataLoss,
		KeyAcknowledgeDataLoss, KeyAllowDataLoss); name != "" {
		out = append(out, name)
	}
	return out
}

func coveredBy(resource string, current, deprecated []string, currentName, deprecatedName string) string {
	for _, r := range current {
		if r == resource {
			return currentName
		}
	}
	for _, r := range deprecated {
		if r == resource {
			return deprecatedName
		}
	}
	return ""
}
