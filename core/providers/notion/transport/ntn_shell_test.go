package transport

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestExecuteSuccessParsesStatusAndBody(t *testing.T) {
	withFakeNtn(t, "ok")
	tr := NewNtnShell()

	resp, err := tr.Execute(context.Background(), APIRequest{Method: "GET", Path: "/v1/x"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if resp.Status != 200 {
		t.Errorf("Status = %d, want 200", resp.Status)
	}
	if !strings.Contains(string(resp.Body), `"object":"data_source"`) {
		t.Errorf("Body = %q, want the JSON payload", resp.Body)
	}
}

func TestExecuteAPIErrorIsTyped(t *testing.T) {
	withFakeNtn(t, "not_found")
	tr := NewNtnShell()

	_, err := tr.Execute(context.Background(), APIRequest{Method: "GET", Path: "/v1/x"})
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v (%T), want *APIError", err, err)
	}
	if apiErr.Status != 404 || apiErr.NotionCode != "object_not_found" {
		t.Errorf("got %d %q, want 404 object_not_found", apiErr.Status, apiErr.NotionCode)
	}
	if apiErr.Retryable() {
		t.Error("un 404 ne doit pas être retryable")
	}
}

func TestExecuteUsageErrorIsTypedAndNotRetryable(t *testing.T) {
	withFakeNtn(t, "usage_error")
	tr := NewNtnShell()

	_, err := tr.Execute(context.Background(), APIRequest{Method: "GET", Path: "/v1/x"})
	var usageErr *UsageError
	if !errors.As(err, &usageErr) {
		t.Fatalf("error = %v (%T), want *UsageError", err, err)
	}
	// La commande construite doit figurer dans le message : c'est un bug
	// interne, l'utilisateur doit pouvoir le rapporter tel quel.
	if !strings.Contains(usageErr.Command, "--notion-version") {
		t.Errorf("Command = %q, elle doit contenir la commande construite", usageErr.Command)
	}
}

func TestExecuteTimeoutIsOutcomeUnknown(t *testing.T) {
	withFakeNtn(t, "hang")
	tr := NewNtnShell()

	start := time.Now()
	_, err := tr.Execute(context.Background(), APIRequest{
		Method:  "POST",
		Path:    "/v1/databases",
		Body:    []byte(`{"a":1}`),
		Timeout: 200 * time.Millisecond,
	})
	elapsed := time.Since(start)

	var unknown *OutcomeUnknownError
	if !errors.As(err, &unknown) {
		t.Fatalf("error = %v (%T), want *OutcomeUnknownError", err, err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("le timeout n'a pas interrompu le process: %v", elapsed)
	}
}

// Ce test relie le parsing au type : un 429 réel de ntn doit produire une
// APIError retryable ET un Retry-After lisible. C'est ce couple que la
// politique de retry consomme ; le tester sur des chaînes seules ne prouverait
// pas que la chaîne complète fonctionne.
func TestExecuteRateLimitedExposesRetryAfter(t *testing.T) {
	withFakeNtn(t, "rate_limited")
	tr := NewNtnShell()

	resp, err := tr.Execute(context.Background(), APIRequest{
		Method: "POST", Path: "/v1/pages", Body: []byte(`{}`),
	})
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v (%T), want *APIError", err, err)
	}
	if apiErr.Status != 429 || !apiErr.Retryable() {
		t.Errorf("got %d retryable=%v, want 429 retryable", apiErr.Status, apiErr.Retryable())
	}
	d, ok := resp.RetryAfter()
	if !ok {
		t.Fatal("RetryAfter() ok = false, want true — le header n'a pas été parsé")
	}
	if d != time.Second {
		t.Errorf("RetryAfter() = %v, want 1s", d)
	}
}

func TestExecuteServerErrorIsRetryable(t *testing.T) {
	withFakeNtn(t, "server_error")
	tr := NewNtnShell()

	_, err := tr.Execute(context.Background(), APIRequest{Method: "GET", Path: "/v1/x"})
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v (%T), want *APIError", err, err)
	}
	if apiErr.Status != 502 || !apiErr.Retryable() {
		t.Errorf("got %d retryable=%v, want 502 retryable", apiErr.Status, apiErr.Retryable())
	}
}

// L'argv est comparé comme une LISTE, avec égalité exacte. Une assertion par
// sous-chaîne ne pouvait pas échouer : `strings.Contains(argv, "-v")` est
// satisfait par `--notion-version` seul, donc retirer -v de l'argv laissait le
// test vert — alors que la trace -v est la seule source du statut HTTP et du
// header Retry-After.
func TestExecuteAlwaysPassesVerboseAndNotionVersion(t *testing.T) {
	withFakeNtn(t, "echo_argv")
	tr := NewNtnShell()

	resp, err := tr.Execute(context.Background(), APIRequest{Method: "PATCH", Path: "/v1/x"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	want := []string{"-v", "api", "--notion-version", DefaultNotionVersion, "-X", "PATCH", "/v1/x"}
	if got := echoedArgv(resp.Body); !slices.Equal(got, want) {
		t.Errorf("argv = %q, want %q", got, want)
	}
}

// echoedArgv relit la sortie du scénario echo_argv : un argument par ligne.
func echoedArgv(body []byte) []string {
	trimmed := strings.TrimRight(string(body), "\n")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}

func TestExecuteSendsBodyOnStdinWithDashData(t *testing.T) {
	withFakeNtn(t, "echo_stdin")
	tr := NewNtnShell()

	body := []byte(`{"parent":{"type":"page_id","page_id":"abc"}}`)
	resp, err := tr.Execute(context.Background(), APIRequest{
		Method: "POST", Path: "/v1/databases", Body: body,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if string(resp.Body) != string(body) {
		t.Errorf("body reçu par ntn = %q, want %q", resp.Body, body)
	}
}

// Sans body, aucun -d @- ne doit être passé : le passer sans alimenter stdin
// ferait attendre ntn un body qui n'arrive jamais. Égalité exacte, ici aussi :
// chercher l'absence de la sous-chaîne "-d" ne dit rien de ce qui est présent.
func TestExecuteWithoutBodyPassesNoDataFlag(t *testing.T) {
	withFakeNtn(t, "echo_argv")
	tr := NewNtnShell()

	resp, err := tr.Execute(context.Background(), APIRequest{Method: "GET", Path: "/v1/x"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	want := []string{"-v", "api", "--notion-version", DefaultNotionVersion, "-X", "GET", "/v1/x"}
	if got := echoedArgv(resp.Body); !slices.Equal(got, want) {
		t.Errorf("argv = %q, want %q — aucun -d @- sans body", got, want)
	}
}

// Un exit 0 sans ligne de statut est le risque nommé par la spec : ntn change
// son format de sortie. Il doit échouer vers le rouge, jamais rendre un succès
// qu'on n'a pas constaté — sinon un plan déclare la page parente lisible sans
// avoir lu un seul statut, et un apply rapporterait une mutation non vérifiée.
func TestExecuteExitZeroWithoutStatusLineIsAnError(t *testing.T) {
	withFakeNtn(t, "exit0_no_status")
	tr := NewNtnShell()

	resp, err := tr.Execute(context.Background(), APIRequest{Method: "GET", Path: "/v1/x"})
	if err == nil {
		t.Fatalf("Execute() error = nil alors que la trace -v ne porte aucun statut (resp = %+v)", resp)
	}
	for _, want := range []string{"ligne de statut", "0.22.11"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message = %q, il doit contenir %q", err.Error(), want)
		}
	}
}

// Un 4xx rendu avec un exit 0 ne doit pas passer pour un succès : le statut
// n'est vérifié nulle part en aval, donc c'est ici qu'il doit être reclassé.
func TestExecuteStatusAtLeast400IsAnErrorEvenOnExitZero(t *testing.T) {
	withFakeNtn(t, "exit0_status_403")
	tr := NewNtnShell()

	_, err := tr.Execute(context.Background(), APIRequest{Method: "GET", Path: "/v1/x"})
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v (%T), want *APIError", err, err)
	}
	if apiErr.Status != 403 {
		t.Errorf("Status = %d, want 403", apiErr.Status)
	}
	if apiErr.Retryable() {
		t.Error("un 403 ne doit pas être retryable")
	}
}

// Un chemin d'API relatif est refusé avant tout appel : normalisé par la couche
// HTTP, il viserait un autre endpoint que celui prévu.
func TestExecuteRejectsRelativePath(t *testing.T) {
	withFakeNtn(t, "ok")
	tr := NewNtnShell()

	for _, bad := range []string{"v1/pages/abc", "/v1/pages/../../v1/users"} {
		_, err := tr.Execute(context.Background(), APIRequest{Method: "GET", Path: bad})
		var usageErr *UsageError
		if !errors.As(err, &usageErr) {
			t.Errorf("Execute(%q) error = %v (%T), want *UsageError", bad, err, err)
		}
	}
}

// Un binaire introuvable n'a émis AUCUN appel : l'issue est connue, c'est un
// échec franc. La classer en OutcomeUnknownError serait l'erreur symétrique de
// celle du timeout.
func TestExecuteBinaryNotFoundIsAKnownFailure(t *testing.T) {
	tr := NewNtnShell()
	tr.Binary = "notion-seed-nonexistent-binary-xyz"

	start := time.Now()
	_, err := tr.Execute(context.Background(), APIRequest{Method: "GET", Path: "/v1/x"})
	if err == nil {
		t.Fatal("Execute() error = nil, want une erreur")
	}
	var unknown *OutcomeUnknownError
	if errors.As(err, &unknown) {
		t.Error("binaire absent classé en OutcomeUnknownError : aucun appel n'a été émis, l'issue est connue")
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		t.Error("binaire absent classé en APIError : l'API n'a jamais été contactée")
	}
	var usageErr *UsageError
	if errors.As(err, &usageErr) {
		t.Error("binaire absent classé en UsageError : l'appel était valide, c'est le binaire qui manque")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("a mis %v : un binaire absent doit échouer immédiatement", elapsed)
	}
}
