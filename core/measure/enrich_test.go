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
