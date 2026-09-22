// SPDX-License-Identifier: GPL-3.0-or-later

// Package state porte le dernier état appliqué par notion-seed : les identités
// Notion des ressources gérées, et un instantané de leurs attributs.
//
// C'est ce qui rend la dérive nommable. Sans lui, on ne peut comparer que le
// YAML au réel, donc on ne sait pas distinguer « le YAML a changé » de
// « quelqu'un a changé Notion à la main ».
package state

import "path/filepath"

// Version est la version du format de fichier. Un state d'une autre version
// est refusé plutôt que lu au mieux : une lecture partielle d'un format inconnu
// produirait des identités fausses, donc des écritures sur les mauvais ids.
const Version = 1

// FileName est le nom du fichier, à côté de workspace.yaml. Il est fait pour
// être versionné dans git : il ne porte aucun secret.
const FileName = "notion-seed.state.json"

// Path rend le chemin du fichier de state pour un dossier de configuration.
func Path(dir string) string { return filepath.Join(dir, FileName) }

// Snapshot est l'état appliqué complet.
type Snapshot struct {
	Version int `json:"version"`

	// WorkspaceID est le workspace sur lequel ces identités ont un sens. Jouer
	// un state contre un autre workspace planifierait des écritures sur des ids
	// qui n'y existent pas.
	WorkspaceID string `json:"workspace_id,omitempty"`

	// Databases est indexée par la key de configuration, pas par l'id Notion :
	// c'est la key qui fait le lien avec le YAML.
	Databases map[string]Database `json:"databases,omitempty"`
}

// Database est l'état appliqué d'une database et de son data source.
type Database struct {
	ID           string `json:"id,omitempty"`
	DataSourceID string `json:"data_source_id,omitempty"`
	Name         string `json:"name,omitempty"`
	Description  string `json:"description,omitempty"`
	Icon         string `json:"icon,omitempty"`

	// Properties est indexée par nom affiché : c'est l'identité d'une propriété
	// côté configuration, faute d'une key déclarable.
	Properties map[string]Property `json:"properties,omitempty"`
}

// Property est l'état appliqué d'une propriété.
type Property struct {
	ID      string   `json:"id,omitempty"`
	Type    string   `json:"type,omitempty"`
	Format  string   `json:"format,omitempty"` // number uniquement
	Options []Option `json:"options,omitempty"`
}

// Option porte les DEUX identités d'une option : celle de Notion (ID) et celle
// de la configuration (Key). C'est leur cohabitation ici qui permet de dire
// qu'une option a été renommée, plutôt que détruite puis recréée.
//
// L'ordre des options est conservé tel que l'API le rend : il est visible dans
// Notion, le trier le rendrait faux.
type Option struct {
	ID    string `json:"id,omitempty"`
	Key   string `json:"key,omitempty"`
	Name  string `json:"name,omitempty"`
	Color string `json:"color,omitempty"`
	Group string `json:"group,omitempty"` // status uniquement
}
