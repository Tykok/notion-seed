// SPDX-License-Identifier: GPL-3.0-or-later

// Package config charge et valide la configuration YAML de notion-seed.
package config

// StatusGroups énumère les seuls groupes de status acceptés par l'API Notion,
// à la création comme à la modification. Les groupes nommés librement ne sont
// pas supportés : l'API répond 400 validation_error.
var StatusGroups = []string{"To-do", "In progress", "Complete"}

// SupportedPropertyTypes liste les types gérés au MVP 0. Ce qui n'est pas
// déclaré n'est pas touché ; ce qui est déclaré avec un autre type est rejeté.
var SupportedPropertyTypes = []string{
	"title", "rich_text", "number", "url", "select",
	"status", "multi_select", "date", "checkbox", "people",
}

// Config est la configuration complète, après fusion de tous les fichiers.
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

	// SourceFile est le fichier d'où vient cette database. Sert aux messages
	// d'erreur d'unicité de key, qui doivent nommer les deux fichiers en cause.
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
