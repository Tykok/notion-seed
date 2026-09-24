// SPDX-License-Identifier: GPL-3.0-or-later

package measure

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/tykok/notion-seed/core/providers/notion/transport"
)

type transportFunc func(ctx context.Context, req transport.APIRequest) (transport.APIResponse, error)

func (f transportFunc) Execute(ctx context.Context, req transport.APIRequest) (transport.APIResponse, error) {
	return f(ctx, req)
}

// pageOf fabrique une réponse de query avec n résultats et un curseur éventuel.
func pageOf(n int, next string) []byte {
	results := make([]map[string]any, n)
	for i := range results {
		results[i] = map[string]any{"object": "page"}
	}
	body := map[string]any{"object": "list", "results": results, "has_more": next != ""}
	if next != "" {
		body["next_cursor"] = next
	}
	b, _ := json.Marshal(body)
	return b
}

// La forme du filtre doit correspondre au type : mesuré le 2026-09-24, une
// forme multi_select sur un select rend 400.
func TestCountBuildsTheFilterMatchingThePropertyType(t *testing.T) {
	tests := []struct {
		propType string
		wantKey  string
		wantOp   string
	}{
		{"select", "select", "equals"},
		{"status", "status", "equals"},
		{"multi_select", "multi_select", "contains"},
	}
	for _, tt := range tests {
		var sent map[string]any
		tr := transportFunc(func(_ context.Context, req transport.APIRequest) (transport.APIResponse, error) {
			_ = json.Unmarshal(req.Body, &sent)
			return transport.APIResponse{Status: 200, Body: pageOf(0, "")}, nil
		})
		_, err := NewCounter(tr).Count(context.Background(), Request{
			DataSourceID: "ds-1", Property: "Statut", PropertyType: tt.propType, Option: "À faire",
		})
		if err != nil {
			t.Fatalf("%s: Count() error = %v", tt.propType, err)
		}
		filter := sent["filter"].(map[string]any)
		if filter["property"] != "Statut" {
			t.Errorf("%s: property = %v", tt.propType, filter["property"])
		}
		cond, ok := filter[tt.wantKey].(map[string]any)
		if !ok {
			t.Fatalf("%s: filtre = %v, want une clé %q", tt.propType, filter, tt.wantKey)
		}
		if cond[tt.wantOp] != "À faire" {
			t.Errorf("%s: condition = %v, want %s", tt.propType, cond, tt.wantOp)
		}
	}
}

// Option vide = compter les valeurs non vides de la colonne, ce dont un
// changement de type a besoin.
func TestCountWithoutOptionCountsNonEmptyValues(t *testing.T) {
	var sent map[string]any
	tr := transportFunc(func(_ context.Context, req transport.APIRequest) (transport.APIResponse, error) {
		_ = json.Unmarshal(req.Body, &sent)
		return transport.APIResponse{Status: 200, Body: pageOf(2, "")}, nil
	})
	got, err := NewCounter(tr).Count(context.Background(), Request{
		DataSourceID: "ds-1", Property: "Tags", PropertyType: "multi_select",
	})
	if err != nil {
		t.Fatalf("Count() error = %v", err)
	}
	if got.Count != 2 || got.Capped {
		t.Errorf("Result = %+v, want 2 non plafonné", got)
	}
	cond := sent["filter"].(map[string]any)["multi_select"].(map[string]any)
	if cond["is_not_empty"] != true {
		t.Errorf("condition = %v, want is_not_empty", cond)
	}
}

// Un type sans filtre connu ne doit pas fabriquer une requête au hasard : elle
// rendrait 400, et un 400 se lit comme un échec alors que c'est une question
// qu'on ne sait pas poser.
func TestCountRefusesAPropertyTypeItCannotFilter(t *testing.T) {
	tr := transportFunc(func(context.Context, transport.APIRequest) (transport.APIResponse, error) {
		t.Error("aucun appel ne devait être émis")
		return transport.APIResponse{}, nil
	})
	_, err := NewCounter(tr).Count(context.Background(), Request{
		DataSourceID: "ds-1", Property: "Gens", PropertyType: "people", Option: "X",
	})
	if !errors.Is(err, ErrUnsupportedFilter) {
		t.Errorf("error = %v, want ErrUnsupportedFilter", err)
	}
}

// La pagination est plafonnée : « plus de 300 » suffit à décider, et une
// database de 40 000 lignes ne doit pas faire 400 appels pour rendre un plan.
func TestCountStopsAtThePageCap(t *testing.T) {
	calls := 0
	tr := transportFunc(func(context.Context, transport.APIRequest) (transport.APIResponse, error) {
		calls++
		return transport.APIResponse{Status: 200, Body: pageOf(CountPageSize, "curseur")}, nil
	})
	got, err := NewCounter(tr).Count(context.Background(), Request{
		DataSourceID: "ds-1", Property: "Statut", PropertyType: "status", Option: "À faire",
	})
	if err != nil {
		t.Fatalf("Count() error = %v", err)
	}
	if calls != MaxCountedPages {
		t.Errorf("appels = %d, want %d", calls, MaxCountedPages)
	}
	if !got.Capped || got.Count != MaxCountedPages*CountPageSize {
		t.Errorf("Result = %+v, want plafonné à %d", got, MaxCountedPages*CountPageSize)
	}
}

// Le curseur de la page précédente doit être transmis, sinon on recompte la
// première page indéfiniment.
func TestCountPassesTheCursorToTheNextPage(t *testing.T) {
	var cursors []string
	tr := transportFunc(func(_ context.Context, req transport.APIRequest) (transport.APIResponse, error) {
		var body map[string]any
		_ = json.Unmarshal(req.Body, &body)
		c, _ := body["start_cursor"].(string)
		cursors = append(cursors, c)
		if len(cursors) == 1 {
			return transport.APIResponse{Status: 200, Body: pageOf(CountPageSize, "c2")}, nil
		}
		return transport.APIResponse{Status: 200, Body: pageOf(5, "")}, nil
	})
	got, err := NewCounter(tr).Count(context.Background(), Request{
		DataSourceID: "ds-1", Property: "Statut", PropertyType: "status", Option: "X",
	})
	if err != nil {
		t.Fatalf("Count() error = %v", err)
	}
	if len(cursors) != 2 || cursors[0] != "" || cursors[1] != "c2" {
		t.Errorf("curseurs = %v, want [\"\", \"c2\"]", cursors)
	}
	if got.Count != CountPageSize+5 || got.Capped {
		t.Errorf("Result = %+v, want %d non plafonné", got, CountPageSize+5)
	}
}

func TestCountPropagatesTransportErrors(t *testing.T) {
	tr := transportFunc(func(context.Context, transport.APIRequest) (transport.APIResponse, error) {
		return transport.APIResponse{}, &transport.APIError{Status: 403, NotionCode: "restricted_resource"}
	})
	_, err := NewCounter(tr).Count(context.Background(), Request{
		DataSourceID: "ds-1", Property: "Statut", PropertyType: "status", Option: "X",
	})
	if err == nil {
		t.Fatal("Count() error = nil, want l'erreur du transport")
	}
	if !strings.Contains(fmt.Sprint(err), "403") {
		t.Errorf("error = %v, elle doit porter le statut", err)
	}
}

// Une réponse 200 qui n'est PAS une liste doit être refusée.
//
// Sans ce refus, n'importe quel objet JSON se décode sans erreur avec un
// `results` absent, donc Count = 0, donc ClassifyOptionRemoval rend ClassSafe :
// notion-seed affirmerait « 0 ligne concernée, rien à perdre » sur une réponse
// dont il n'a rien compris. C'est exactement l'affirmation invérifiée que ce
// produit existe pour rendre impossible.
func TestCountRefusesAResponseThatIsNotAList(t *testing.T) {
	// Le schéma d'un data source : ce que rendrait un ordre de routes erroné,
	// côté serveur comme côté faux binaire de test.
	body := []byte(`{"object":"data_source","id":"ds-1",` +
		`"properties":{"Statut":{"id":"p-statut","type":"status"}}}`)
	tr := transportFunc(func(context.Context, transport.APIRequest) (transport.APIResponse, error) {
		return transport.APIResponse{Status: 200, Body: body}, nil
	})

	res, err := NewCounter(tr).Count(context.Background(), Request{
		DataSourceID: "ds-1", Property: "Statut", PropertyType: "status", Option: "Fait",
	})
	if err == nil {
		t.Fatalf("Count() error = nil (Count = %d), want le refus d'une réponse non comprise",
			res.Count)
	}
	if !errors.Is(err, ErrUnreadableCount) {
		t.Errorf("error = %v, want une erreur %v", err, ErrUnreadableCount)
	}
	if !strings.Contains(fmt.Sprint(err), "  → ") {
		t.Errorf("error = %v, elle doit porter une action corrective", err)
	}
}

// Une réponse de type liste SANS champ `results` n'est pas une liste vide :
// c'est une réponse qu'on n'a pas comprise. Les distinguer impose un pointeur
// de tranche — une tranche nue confond « absent » et « vide ».
func TestCountRefusesAListWithoutResults(t *testing.T) {
	tr := transportFunc(func(context.Context, transport.APIRequest) (transport.APIResponse, error) {
		return transport.APIResponse{Status: 200, Body: []byte(`{"object":"list","has_more":false}`)}, nil
	})

	res, err := NewCounter(tr).Count(context.Background(), Request{
		DataSourceID: "ds-1", Property: "Statut", PropertyType: "status", Option: "Fait",
	})
	if err == nil {
		t.Fatalf("Count() error = nil (Count = %d), want le refus d'une liste sans results",
			res.Count)
	}
	if !errors.Is(err, ErrUnreadableCount) {
		t.Errorf("error = %v, want une erreur %v", err, ErrUnreadableCount)
	}
}

// Une liste VIDE, elle, est une réponse parfaitement légitime : personne
// n'utilise l'option, et c'est le cas qui rend un retrait sûr. La confondre
// avec une réponse incomprise ferait perdre tout l'intérêt de mesurer.
func TestCountAcceptsAnEmptyListAsZero(t *testing.T) {
	tr := transportFunc(func(context.Context, transport.APIRequest) (transport.APIResponse, error) {
		return transport.APIResponse{
			Status: 200,
			Body:   []byte(`{"object":"list","results":[],"has_more":false}`),
		}, nil
	})

	res, err := NewCounter(tr).Count(context.Background(), Request{
		DataSourceID: "ds-1", Property: "Statut", PropertyType: "status", Option: "Fait",
	})
	if err != nil {
		t.Fatalf("Count() error = %v, want une liste vide acceptée", err)
	}
	if res.Count != 0 {
		t.Errorf("Count = %d, want 0", res.Count)
	}
}
