package transport

import "fmt"

// APIError est une erreur retournée par l'API Notion, extraite du stderr de
// ntn (exit code 5).
type APIError struct {
	Status     int
	NotionCode string
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("notion api %d %s: %s", e.Status, e.NotionCode, e.Message)
}

// Retryable ne vaut true que sur 429 et 5xx. Un 4xx est une erreur de config
// ou de permission : la rejouer ne fait que perdre du temps.
func (e *APIError) Retryable() bool {
	return e.Status == 429 || e.Status >= 500
}

// OutcomeUnknownError signale qu'on ne sait pas si l'appel a abouti côté
// serveur — typiquement un timeout. Ne jamais traiter comme un échec : la
// mutation a peut-être été appliquée.
type OutcomeUnknownError struct {
	Cause error
}

func (e *OutcomeUnknownError) Error() string {
	return fmt.Sprintf("résultat inconnu, l'appel a peut-être abouti côté serveur: %v", e.Cause)
}

func (e *OutcomeUnknownError) Unwrap() error { return e.Cause }

// UsageError signale que notion-seed a construit un appel `ntn` invalide
// (exit code 2). C'est un bug interne, jamais rejoué. La commande construite
// est reportée : sans elle, l'utilisateur n'a aucun moyen de rapporter le bug.
type UsageError struct {
	Command string
	Stderr  string
}

func (e *UsageError) Error() string {
	return fmt.Sprintf(
		"appel ntn invalide (bug interne de notion-seed)\n  commande : %s\n  %s",
		e.Command, e.Stderr)
}
