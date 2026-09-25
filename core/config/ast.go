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

type Lifecycle struct {
	PreventDestroy []string `yaml:"prevent_destroy"`
	AllowDataLoss  []string `yaml:"allow_data_loss"`
}
