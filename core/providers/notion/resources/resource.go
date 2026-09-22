// SPDX-License-Identifier: GPL-3.0-or-later

// Package resources porte les types de ressources Notion gérés par
// notion-seed. Le moteur de diff est écrit contre l'interface Resource :
// ajouter un type ne doit pas le modifier.
package resources

import (
	"context"

	"github.com/tykok/notion-seed/core/change"
)

// RemoteState est l'état réel d'une ressource, lu depuis l'API.
type RemoteState interface {
	Exists() bool
}

// ChangeKind classe un changeset au niveau de la ressource.
type ChangeKind int

const (
	KindNone ChangeKind = iota
	KindCreate
	KindUpdate
	// KindDestroy : la ressource est dans le state mais plus dans la config.
	// Son identité n'a plus d'ancre déclarée.
	KindDestroy
)

// Detail décrit un changement élémentaire à l'intérieur d'une ressource.
type Detail struct {
	Op     string // "+", "~", "-"
	Target string // `property "Estimate" (number)`
	Note   string // précision optionnelle
	Class  change.Class
}

// Changeset regroupe les changements d'une ressource.
type Changeset struct {
	Resource string // "database.projects"
	Kind     ChangeKind
	Details  []Detail
}

// Resource est le contrat que remplit chaque type de ressource Notion.
//
// Apply et Destroy sont absentes au MVP 0 : le read-modify-write imposé par
// l'API contraint leur signature, et on ne la connaîtra qu'en écrivant
// réellement. ID(state) est absente aussi : elle dépend du modèle de state,
// hors périmètre MVP 0.
type Resource interface {
	Type() string
	Read(ctx context.Context, id string) (RemoteState, error)
	Diff(desired any, remote RemoteState) (Changeset, error)
}
