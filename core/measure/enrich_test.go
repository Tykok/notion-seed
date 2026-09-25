// SPDX-License-Identifier: GPL-3.0-or-later

package measure

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/tykok/notion-seed/core/change"
	"github.com/tykok/notion-seed/core/diff"
	"github.com/tykok/notion-seed/core/providers/notion/resources"
)

type counterFunc func(ctx context.Context, r Request) (Result, error)

func (f counterFunc) Count(ctx context.Context, r Request) (Result, error) { return f(ctx, r) }

func planWithRemoval(propType, option string) *diff.Plan {
	return &diff.Plan{Changes: []diff.Change{{
		Resource: "database.tasks", Key: "tasks",
		Details: []resources.Detail{{
			Op: "-", Target: `option "` + option + `"`,
			Class: change.ClassUnknownImpact, Count: -1,
			Measure: &resources.Measurement{
				Property: "Statut", PropertyType: propType, Option: option,
			},
		}},
	}}}
}

// 0 ligne concernée : le retrait ne coûte rien, et la ligne devient sûre. C'est
// tout l'intérêt de mesurer plutôt que de refuser.
func TestEnrichMakesARemovalSafeWhenNoRowUsesTheOption(t *testing.T) {
	p := planWithRemoval("status", "Annulé")
	c := counterFunc(func(context.Context, Request) (Result, error) { return Result{Count: 0}, nil })

	if fails := Enrich(context.Background(), c, map[string]string{"tasks": "ds-1"}, p); len(fails) != 0 {
		t.Fatalf("échecs = %v, want aucun", fails)
	}
	d := p.Changes[0].Details[0]
	if d.Count != 0 || d.Class != change.ClassSafe {
		t.Errorf("Detail = {Count:%d Class:%v}, want {0 sûr}", d.Count, d.Class)
	}
}

// 47 lignes sur un status : réécriture silencieuse, avec le chiffre.
func TestEnrichClassifiesAStatusRemovalWithRowsAsSilentRewrite(t *testing.T) {
	p := planWithRemoval("status", "Annulé")
	c := counterFunc(func(context.Context, Request) (Result, error) { return Result{Count: 47}, nil })

	Enrich(context.Background(), c, map[string]string{"tasks": "ds-1"}, p)
	d := p.Changes[0].Details[0]
	if d.Count != 47 || d.Class != change.ClassSilentRewrite {
		t.Errorf("Detail = {Count:%d Class:%v}, want {47 réécriture silencieuse}", d.Count, d.Class)
	}
}

// Une mesure en échec ne fait pas échouer le plan : la ligne reste inconnue, la
// cause est rendue, et le reste du plan est rendu quand même.
func TestEnrichKeepsGoingWhenAMeasurementFails(t *testing.T) {
	p := planWithRemoval("status", "Annulé")
	c := counterFunc(func(context.Context, Request) (Result, error) {
		return Result{}, errors.New("403 restricted_resource")
	})

	fails := Enrich(context.Background(), c, map[string]string{"tasks": "ds-1"}, p)
	if len(fails) != 1 || !strings.Contains(fails[0], "403") {
		t.Errorf("échecs = %v, want la cause", fails)
	}
	d := p.Changes[0].Details[0]
	if d.Count != -1 || d.Class != change.ClassUnknownImpact {
		t.Errorf("Detail = {Count:%d Class:%v}, want inconnu", d.Count, d.Class)
	}
}

// Un type non filtrable n'est pas un échec à signaler comme une panne : c'est
// une question qu'on ne sait pas poser. La ligne reste inconnue.
func TestEnrichLeavesUnfilterableTypesUnknown(t *testing.T) {
	p := planWithRemoval("people", "Quelqu'un")
	c := counterFunc(func(context.Context, Request) (Result, error) {
		return Result{}, ErrUnsupportedFilter
	})

	Enrich(context.Background(), c, map[string]string{"tasks": "ds-1"}, p)
	if d := p.Changes[0].Details[0]; d.Class != change.ClassUnknownImpact {
		t.Errorf("Class = %v, want ClassUnknownImpact", d.Class)
	}
}

// ... et il ne produit AUCUNE ligne d'échec : rien n'est en panne. Les verser
// dans la même liste que les 403 et les timeouts ferait apparaître « comptage
// impossible » à chaque plan portant un type non filtrable, et apprendrait à
// ignorer une ligne qui, elle, signale de vrais incidents.
func TestEnrichReportsNoFailureForAnUnfilterableType(t *testing.T) {
	p := planWithRemoval("people", "Quelqu'un")
	c := counterFunc(func(context.Context, Request) (Result, error) {
		// Enveloppée, comme filterFor l'enveloppe réellement : c'est errors.Is
		// qui doit trancher, pas une comparaison d'égalité.
		return Result{}, fmt.Errorf("%w: %q", ErrUnsupportedFilter, "people")
	})

	if fails := Enrich(context.Background(), c, map[string]string{"tasks": "ds-1"}, p); len(fails) != 0 {
		t.Errorf("échecs = %v, want aucun : ne pas savoir poser la question n'est pas une panne", fails)
	}
	// Le comportement de la ligne, lui, ne change pas : on ne sait toujours pas.
	if d := p.Changes[0].Details[0]; d.Class != change.ClassUnknownImpact || d.Count != -1 {
		t.Errorf("Detail = {Count:%d Class:%v}, want inconnu", d.Count, d.Class)
	}
}

// C'est la passe de mesure qui SAIT quels types elle peut filtrer, donc c'est
// elle qui marque la ligne. Le rendu ne peut pas le déduire : la liste des
// types filtrables vit ici, et core/measure importe core/diff — l'inverse
// créerait un cycle, et recopier la table la ferait diverger.
//
// Sans cette marque, le rendu promet « relancez en ligne pour l'obtenir » sur
// une ligne qui ne se comptera jamais.
func TestEnrichMarksAnUnfilterableTypeAsUnmeasurable(t *testing.T) {
	p := planWithRemoval("people", "Quelqu'un")
	c := counterFunc(func(context.Context, Request) (Result, error) {
		return Result{}, fmt.Errorf("%w: %q", ErrUnsupportedFilter, "people")
	})

	Enrich(context.Background(), c, map[string]string{"tasks": "ds-1"}, p)
	if d := p.Changes[0].Details[0]; !d.Unmeasurable {
		t.Error("Unmeasurable = false : un type non filtrable ne le deviendra pas au prochain run")
	}
}

// Une panne, elle, N'EST PAS une impossibilité : un 403 se répare, et la ligne
// doit rester simplement non mesurée pour que le rendu garde son remède.
// Confondre les deux ferait disparaître « relancez » là où relancer marche.
func TestEnrichDoesNotMarkAFailedMeasurementAsUnmeasurable(t *testing.T) {
	p := planWithRemoval("status", "Annulé")
	c := counterFunc(func(context.Context, Request) (Result, error) {
		return Result{}, errors.New("403 restricted_resource")
	})

	Enrich(context.Background(), c, map[string]string{"tasks": "ds-1"}, p)
	if d := p.Changes[0].Details[0]; d.Unmeasurable {
		t.Error("Unmeasurable = true sur une panne : un 403 se répare en relançant")
	}
}

// Aucune demande de mesure : aucun appel. Ne pas payer d'appels pour rien est
// une propriété, pas une optimisation.
func TestEnrichEmitsNoCallWhenNothingNeedsMeasuring(t *testing.T) {
	p := &diff.Plan{Changes: []diff.Change{{
		Resource: "database.tasks", Key: "tasks",
		Details: []resources.Detail{{Op: "+", Target: `property "X"`, Count: -1}},
	}}}
	c := counterFunc(func(context.Context, Request) (Result, error) {
		t.Error("aucune mesure ne devait être demandée")
		return Result{}, nil
	})
	Enrich(context.Background(), c, map[string]string{"tasks": "ds-1"}, p)
}

// Sans data source id (ressource jamais importée), aucune mesure n'est
// possible : la ligne reste inconnue plutôt que de déclencher un appel sur un
// id vide.
func TestEnrichSkipsResourcesWithoutADataSourceID(t *testing.T) {
	p := planWithRemoval("status", "Annulé")
	c := counterFunc(func(context.Context, Request) (Result, error) {
		t.Error("aucun appel ne devait être émis sans data source id")
		return Result{}, nil
	})
	Enrich(context.Background(), c, map[string]string{}, p)
	if d := p.Changes[0].Details[0]; d.Class != change.ClassUnknownImpact {
		t.Errorf("Class = %v, want ClassUnknownImpact", d.Class)
	}
}

// planWithOptionMigration fabrique un renommage ou une couleur d'option : la
// classe est ClassMigration AVANT toute mesure, et la ligne porte tout de
// même une demande de mesure — celle du coût du remède (retirer l'ancienne
// option).
func planWithOptionMigration() *diff.Plan {
	return &diff.Plan{Changes: []diff.Change{{
		Resource: "database.tasks", Key: "tasks",
		Class: change.ClassMigration,
		Details: []resources.Detail{{
			Op: "~", Target: `option "Fait" → "Terminé" (propriété "Statut")`,
			Class: change.ClassMigration, Count: -1,
			Measure: &resources.Measurement{
				Property: "Statut", PropertyType: "status", Option: "Fait",
			},
		}},
	}}}
}

// Un renommage ou une couleur d'option est déjà inexprimable AVANT toute
// mesure : ClassifyOptionRemoval ne doit donc jamais reclasser une ligne
// ClassMigration, quel que soit le compte — seul le COÛT du remède doit être
// rempli. Sans cette garde, un renommage sur une propriété portant des lignes
// (compte non nul) retombait en ClassDestructive/ClassSilentRewrite, et un
// renommage sur une colonne vide (compte nul) retombait en ClassSafe : dans
// les deux cas, `--fail-on=migration` cessait de se déclencher.
func TestEnrichKeepsMigrationClassAndFillsCount(t *testing.T) {
	p := planWithOptionMigration()
	c := counterFunc(func(context.Context, Request) (Result, error) { return Result{Count: 12}, nil })

	if fails := Enrich(context.Background(), c, map[string]string{"tasks": "ds-1"}, p); len(fails) != 0 {
		t.Fatalf("échecs = %v, want aucun", fails)
	}
	d := p.Changes[0].Details[0]
	if d.Class != change.ClassMigration {
		t.Errorf("Class = %v, want ClassMigration : la mesure ne reclasse pas une ligne déjà inexprimable", d.Class)
	}
	if d.Count != 12 {
		t.Errorf("Count = %d, want 12 : le coût du remède doit être rempli", d.Count)
	}
	if got := p.Changes[0].Class; got != change.ClassMigration {
		t.Errorf("Class d'en-tête = %v, want ClassMigration", got)
	}
}

// planWithTypeChange fabrique un changement de type : la mesure porte la
// colonne entière (Option vide), pas une option.
func planWithTypeChange() *diff.Plan {
	return &diff.Plan{Changes: []diff.Change{{
		Resource: "database.tasks", Key: "tasks",
		Class: change.ClassSilentRewrite,
		Details: []resources.Detail{{
			Op: "~", Target: `property "Tags" — multi_select → select`,
			Class: change.ClassSilentRewrite, Count: -1,
			Measure: &resources.Measurement{Property: "Tags", PropertyType: "multi_select"},
		}},
	}}}
}

// Une colonne VIDE qui change de type ne coûte rien, exactement comme une
// option que personne ne porte. Sans ce déclassement, la ligne se contredit
// elle-même — « réécriture silencieuse » suivi de « 0 ligne concernée » — et
// `plan --fail-on=silent-rewrite` sort en code non nul sur une colonne sans
// aucune donnée à perdre.
func TestEnrichMakesATypeChangeSafeWhenTheColumnIsEmpty(t *testing.T) {
	p := planWithTypeChange()
	c := counterFunc(func(context.Context, Request) (Result, error) { return Result{Count: 0}, nil })

	Enrich(context.Background(), c, map[string]string{"tasks": "ds-1"}, p)
	if d := p.Changes[0].Details[0]; d.Count != 0 || d.Class != change.ClassSafe {
		t.Errorf("Detail = {Count:%d Class:%v}, want {0 sûr}", d.Count, d.Class)
	}
	if got := p.Changes[0].Class; got != change.ClassSafe {
		t.Errorf("Class d'en-tête = %v, want sûr", got)
	}
}

// Un compte NON nul, lui, laisse la classe du changement de type intacte : le
// compte dit l'ampleur, la table mesurée dit la nature. Rien dans « 12 lignes
// non vides » ne rend une réécriture silencieuse moins silencieuse.
func TestEnrichKeepsTheTableClassWhenATypeChangeHasRows(t *testing.T) {
	p := planWithTypeChange()
	c := counterFunc(func(context.Context, Request) (Result, error) { return Result{Count: 12}, nil })

	Enrich(context.Background(), c, map[string]string{"tasks": "ds-1"}, p)
	if d := p.Changes[0].Details[0]; d.Count != 12 || d.Class != change.ClassSilentRewrite {
		t.Errorf("Detail = {Count:%d Class:%v}, want {12 réécriture silencieuse}", d.Count, d.Class)
	}
}

// Sous un changement de type, une option de status non redéclarée disparaît et
// ses lignes perdent leur valeur : c'est une perte, pas une réassignation. La
// mesure la reclasse destructive, quel que soit l'ancien type.
func TestEnrichClassifiesARetypedStatusOptionAsDestructive(t *testing.T) {
	p := planWithRemoval("status", "Fait")
	p.Changes[0].Details[0].Measure.Retyped = true
	c := counterFunc(func(context.Context, Request) (Result, error) { return Result{Count: 2}, nil })

	Enrich(context.Background(), c, map[string]string{"tasks": "ds-1"}, p)
	d := p.Changes[0].Details[0]
	if d.Count != 2 || d.Class != change.ClassDestructive {
		t.Errorf("Detail = {Count:%d Class:%v}, want {2 destructif}", d.Count, d.Class)
	}
}
