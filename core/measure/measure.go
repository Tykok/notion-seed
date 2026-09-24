// SPDX-License-Identifier: GPL-3.0-or-later

// Package measure compte, EN LECTURE, les lignes qu'un changement va toucher.
//
// C'est ce qui permet au produit de cesser de refuser par principe : bloquer
// était un substitut à la connaissance, et l'API sait répondre. notion-seed lit
// les lignes pour dire ce qu'un changement coûte ; il n'en écrit jamais.
package measure

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/tykok/notion-seed/core/providers/notion/transport"
)

// CountPageSize et MaxCountedPages plafonnent la pagination.
//
// Le compte exact est ce qui fait décider — « 47 lignes » n'a rien à voir avec
// « au moins une ». Mais une database de 40 000 lignes ne doit pas coûter 400
// appels pour rendre un plan : au-delà du plafond, « plus de 300 » suffit
// amplement à décider, et le Result le dit.
const (
	CountPageSize   = 100
	MaxCountedPages = 3
)

// ErrUnsupportedFilter signale un type de propriété pour lequel on ne sait pas
// construire de filtre. Mesuré le 2026-09-24 : une forme de filtre qui ne
// correspond pas au type rend 400. Fabriquer une requête au hasard ferait donc
// passer « je ne sais pas poser la question » pour « l'API a refusé ».
var ErrUnsupportedFilter = errors.New("type de propriété non filtrable")

// Request décrit UNE mesure. Option vide signifie « compter les valeurs non
// vides de la colonne », ce dont un changement de type a besoin.
type Request struct {
	DataSourceID string
	Property     string
	PropertyType string
	Option       string
}

// Result porte le compte. Capped dit que le plafond de pagination a été atteint
// et que Count est donc un minorant, pas un compte.
type Result struct {
	Count  int
	Capped bool
}

// Counter est ce dont le plan a besoin. L'interface est déclarée ici, côté
// consommateur, pour que les tests n'aient pas à monter une pile de transport.
type Counter interface {
	Count(ctx context.Context, r Request) (Result, error)
}

// NotionCounter compte via l'API.
type NotionCounter struct {
	tr transport.Transport
}

func NewCounter(tr transport.Transport) *NotionCounter {
	return &NotionCounter{tr: tr}
}

var _ Counter = (*NotionCounter)(nil)

// filterFor construit le filtre correspondant au type de la propriété.
//
// Mesuré le 2026-09-24 : select et status se filtrent par `equals`,
// multi_select par `contains`. La forme DOIT correspondre au type, sinon 400.
func filterFor(r Request) (map[string]any, error) {
	var op string
	switch r.PropertyType {
	case "select", "status":
		op = "equals"
	case "multi_select":
		op = "contains"
	default:
		return nil, fmt.Errorf("%w: %q\n"+
			"  → notion-seed ne sait pas compter les lignes de ce type ; l'impact "+
			"sera annoncé comme inconnu plutôt que deviné",
			ErrUnsupportedFilter, r.PropertyType)
	}

	cond := map[string]any{op: r.Option}
	if r.Option == "" {
		cond = map[string]any{"is_not_empty": true}
	}
	return map[string]any{
		"property":     r.Property,
		r.PropertyType: cond,
	}, nil
}

// Count rend le nombre de lignes concernées, plafonné.
func (c *NotionCounter) Count(ctx context.Context, r Request) (Result, error) {
	filter, err := filterFor(r)
	if err != nil {
		return Result{}, err
	}

	var out Result
	cursor := ""
	for page := 0; page < MaxCountedPages; page++ {
		body := map[string]any{"filter": filter, "page_size": CountPageSize}
		if cursor != "" {
			body["start_cursor"] = cursor
		}
		raw, err := json.Marshal(body)
		if err != nil {
			return Result{}, fmt.Errorf(
				"construction de la requête de comptage impossible: %w\n"+
					"  → c'est un bug de notion-seed, pas une erreur de configuration : "+
					"signalez-le", err)
		}

		resp, err := c.tr.Execute(ctx, transport.APIRequest{
			Method: "POST",
			Path:   "/v1/data_sources/" + r.DataSourceID + "/query",
			Body:   raw,
		})
		if err != nil {
			return Result{}, fmt.Errorf(
				"comptage des lignes de %q impossible: %w\n"+
					"  → l'impact de ce changement sera annoncé comme inconnu ; réessayez "+
					"pour obtenir le compte", r.Property, err)
		}

		var decoded struct {
			Results    []json.RawMessage `json:"results"`
			HasMore    bool              `json:"has_more"`
			NextCursor string            `json:"next_cursor"`
		}
		if err := json.Unmarshal(resp.Body, &decoded); err != nil {
			return Result{}, fmt.Errorf(
				"réponse de comptage illisible: %w\n"+
					"  → réessayez ; si ça persiste, l'impact sera annoncé comme inconnu", err)
		}

		out.Count += len(decoded.Results)
		if !decoded.HasMore {
			return out, nil
		}
		cursor = decoded.NextCursor
	}

	out.Capped = true
	return out, nil
}
