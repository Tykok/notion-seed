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

// Measurement décrit ce qu'il faut compter pour savoir ce qu'un détail coûte.
//
// Le comparateur l'ÉMET sans l'exécuter : il reste pur, sans réseau ni horloge,
// et c'est ce qui permet de le couvrir en table sur des triplets. Une passe
// séparée exécute les demandes et reclasse.
//
// Option vide signifie « compter les valeurs non vides de la colonne », ce dont
// un changement de type a besoin.
type Measurement struct {
	Property     string
	PropertyType string
	Option       string
}

// Detail décrit un changement élémentaire à l'intérieur d'une ressource.
type Detail struct {
	Op     string // "+", "~", "-"
	Target string // `property "Estimate" (number)`
	Note   string // précision optionnelle
	Class  change.Class

	// Measure est la demande de mesure, nil quand le détail ne coûte rien.
	Measure *Measurement
	// Count est le nombre de lignes concernées. -1 tant que rien n'a été
	// mesuré : ni 0 ni un compte, mais « on ne sait pas ».
	Count int
	// Capped dit que le plafond de pagination a été atteint et que Count est
	// donc un minorant.
	Capped bool
}

// NewDetail construit un détail non mesuré. À utiliser SYSTÉMATIQUEMENT : un
// Detail composé à la main porte Count = 0, donc « aucune ligne concernée »,
// donc « sûr » — une affirmation que personne n'a vérifiée.
//
// Exportée parce que le comparateur, dans le paquet diff, produit l'essentiel
// des détails du dépôt : une fonction non exportée l'aurait laissé sans
// garde-fou, seul endroit où il en faut vraiment un.
//
// Elle ne prend PAS Note ni les trois champs de mesure : un constructeur à six
// arguments serait moins lisible que le littéral qu'il remplace. Les détails
// qui portent une mesure restent donc des littéraux, avec Count: -1 écrit
// explicitement. Le filet qui rattrape un oubli n'est pas ce constructeur mais
// TestCompareDatabaseNeverEmitsAnUnmeasuredZeroCount, dans core/diff.
func NewDetail(op, target string, class change.Class) Detail {
	return Detail{Op: op, Target: target, Class: class, Count: -1}
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
