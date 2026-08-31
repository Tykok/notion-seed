package transport

import (
	"context"
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

// RetryAfter lit le header Retry-After et le rend en durée. ok vaut false si
// le header est absent ou illisible.
func (r APIResponse) RetryAfter() (d time.Duration, ok bool) {
	vals := r.Headers["retry-after"]
	if len(vals) == 0 {
		return 0, false
	}
	secs, err := time.ParseDuration(vals[0] + "s")
	if err != nil {
		return 0, false
	}
	return secs, true
}
