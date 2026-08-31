package transport

import (
	"context"
	"strconv"
	"strings"
	"time"
)

// APIRequest décrit un appel à l'API publique Notion.
type APIRequest struct {
	Method  string
	Path    string
	Body    []byte        // JSON. Passé à ntn via -d @- sur stdin.
	Timeout time.Duration // Obligatoire : ntn api n'a aucun timeout interne.
}

// APIResponse porte le résultat d'un appel. Status et Headers viennent du
// stderr verbeux de ntn ; Body vient de son stdout.
type APIResponse struct {
	Status  int
	Headers map[string][]string
	Body    []byte
}

// Transport exécute un appel à l'API Notion. La seule implémentation du
// MVP 0 est NtnShell, un shell-out vers `ntn api`.
type Transport interface {
	Execute(ctx context.Context, req APIRequest) (APIResponse, error)
}

// MaxPlausibleRetryAfter borne la valeur qu'on accepte de lire dans le header.
// Au-delà, on la traite comme illisible plutôt que comme une consigne : la
// convertir en durée pourrait déborder int64, et aucune valeur de cet ordre n'a
// de sens pour un appel unitaire. Le backoff calculé prend alors le relais.
const MaxPlausibleRetryAfter = 24 * time.Hour

// RetryAfter lit le header Retry-After et le rend en durée. ok vaut false si le
// header est absent, illisible, ou porte une valeur non exploitable.
//
// Seule la forme « entier de secondes » est acceptée, la seule que l'API Notion
// émette. On ne concatène plus "s" à la valeur brute : "5m" devenait 5
// millisecondes au lieu d'être rejeté, et "-5" se parsait en durée négative,
// donc en trois rejeux immédiats en rafale. La forme HTTP-date du header n'est
// pas gérée ; elle sort en ok=false, ce qui rend la main au backoff calculé —
// une dégradation sûre, jamais une attente fausse.
func (r APIResponse) RetryAfter() (d time.Duration, ok bool) {
	vals := r.Headers["retry-after"]
	if len(vals) == 0 {
		return 0, false
	}
	secs, err := strconv.Atoi(strings.TrimSpace(vals[0]))
	if err != nil || secs <= 0 {
		return 0, false
	}
	if secs > int(MaxPlausibleRetryAfter/time.Second) {
		return 0, false
	}
	return time.Duration(secs) * time.Second, true
}
