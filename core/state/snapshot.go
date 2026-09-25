// SPDX-License-Identifier: GPL-3.0-or-later

// Package state holds the last state applied by notion-seed: the Notion
// identities of the managed resources, and a snapshot of their attributes.
//
// It is what makes drift nameable. Without it, only the YAML can be compared
// to the actual state, so there is no telling "the YAML changed" from
// "someone changed Notion by hand".
package state

import "path/filepath"

// Version is the version of the file format. A state of another version is
// rejected rather than read on a best-effort basis: a partial read of an
// unknown format would produce wrong identities, hence writes to the wrong
// ids.
const Version = 1

// FileName is the file's name, next to workspace.yaml. It is meant to be
// versioned in git: it holds no secret.
const FileName = "notion-seed.state.json"

// Path returns the path of the state file for a configuration directory.
func Path(dir string) string { return filepath.Join(dir, FileName) }

// Snapshot is the full applied state.
type Snapshot struct {
	Version int `json:"version"`

	// WorkspaceID is the workspace in which these identities make sense.
	// Running a state against another workspace would plan writes to ids that
	// do not exist there.
	WorkspaceID string `json:"workspace_id,omitempty"`

	// Databases is indexed by the configuration key, not by the Notion id: the
	// key is what links it to the YAML.
	Databases map[string]Database `json:"databases,omitempty"`
}

// Database is the applied state of a database and its data source.
type Database struct {
	ID           string `json:"id,omitempty"`
	DataSourceID string `json:"data_source_id,omitempty"`
	Name         string `json:"name,omitempty"`
	Description  string `json:"description,omitempty"`
	Icon         string `json:"icon,omitempty"`

	// Properties is indexed by display name: it is a property's identity on the
	// configuration side, for lack of a declarable key.
	Properties map[string]Property `json:"properties,omitempty"`
}

// Property is the applied state of a property.
type Property struct {
	ID      string   `json:"id,omitempty"`
	Type    string   `json:"type,omitempty"`
	Format  string   `json:"format,omitempty"` // number only
	Options []Option `json:"options,omitempty"`
}

// Option holds BOTH identities of an option: Notion's (ID) and the
// configuration's (Key). Having them side by side here is what makes it
// possible to say an option was renamed, rather than destroyed then
// re-created.
//
// The order of the options is kept as the API returns it: it is visible in
// Notion, sorting it would make it wrong.
type Option struct {
	ID    string `json:"id,omitempty"`
	Key   string `json:"key,omitempty"`
	Name  string `json:"name,omitempty"`
	Color string `json:"color,omitempty"`
	Group string `json:"group,omitempty"` // status only
}
