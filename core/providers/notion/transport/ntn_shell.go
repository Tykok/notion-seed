package transport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path"
	"strings"
	"time"

	"github.com/tykok/notion-seed/core/preflight"
)

// DefaultNotionVersion est passée explicitement sur chaque appel. ntn a son
// propre défaut, qui bouge avec ses versions ; on ne l'hérite jamais.
const DefaultNotionVersion = "2025-09-03"

// DefaultTimeout borne chaque appel. ntn api n'a aucun timeout interne et a
// été observé bloquant plusieurs minutes sans produire un octet.
const DefaultTimeout = 30 * time.Second

// NtnShell est la seule implémentation de Transport du MVP 0 : un shell-out
// vers `ntn api`. ntn sert à la fois de transport et d'authentification.
type NtnShell struct {
	Binary         string
	NotionVersion  string
	DefaultTimeout time.Duration
}

func NewNtnShell() *NtnShell {
	return &NtnShell{
		Binary:         "ntn",
		NotionVersion:  DefaultNotionVersion,
		DefaultTimeout: DefaultTimeout,
	}
}

func (t *NtnShell) Execute(ctx context.Context, req APIRequest) (APIResponse, error) {
	if err := checkPath(req.Path); err != nil {
		return APIResponse{}, &UsageError{
			Command: t.Binary + " api " + req.Path,
			Stderr:  err.Error(),
		}
	}

	timeout := req.Timeout
	if timeout <= 0 {
		timeout = t.DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args := []string{"-v", "api", "--notion-version", t.NotionVersion}
	if req.Method != "" {
		args = append(args, "-X", req.Method)
	}
	args = append(args, req.Path)
	if len(req.Body) > 0 {
		args = append(args, "-d", "@-")
	}

	cmd := exec.CommandContext(ctx, t.Binary, args...)

	// Avec un body, il part sur stdin (-d @-) et son EOF vient de la fin de
	// lecture. Sans body, on laisse cmd.Stdin nil : os/exec donne alors
	// /dev/null à l'enfant, ce qui est exactement ce qu'on veut.
	//
	// Ne JAMAIS assigner os.Stdin ici. `ntn api` traite stdin comme une source
	// de body valide, donc un stdin sans EOF le fait attendre indéfiniment —
	// mesuré : 2 ms avec Stdin nil, blocage jusqu'au kill avec os.Stdin.
	if len(req.Body) > 0 {
		cmd.Stdin = bytes.NewReader(req.Body)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()

	// Un timeout ne dit pas si la mutation a été appliquée côté serveur.
	if ctx.Err() != nil {
		return APIResponse{}, &OutcomeUnknownError{Cause: ctx.Err()}
	}

	status, headers, hasStatus, parseErr := ParseStatusAndHeaders(stderr.Bytes())
	resp := APIResponse{Status: status, Headers: headers, Body: stdout.Bytes()}

	if runErr == nil {
		// Un exit 0 ne suffit pas : le statut ne vient QUE de la trace -v. Sans
		// ligne de statut, on ne sait pas si l'API a été atteinte, ni avec quel
		// code — rendre (resp, nil) rapporterait un succès qu'on n'a pas
		// constaté. C'est le risque nommé par la spec, « ntn change son format
		// de sortie », et il doit échouer vers le rouge.
		if parseErr != nil {
			return resp, fmt.Errorf("%w\n  → %s", parseErr, preflight.PinNtnHint())
		}
		if !hasStatus {
			return resp, fmt.Errorf(
				"ntn est sorti en 0 mais sa trace -v ne contient aucune ligne de statut : "+
					"notion-seed ne peut pas vérifier que l'appel a abouti\n  → %s",
				preflight.PinNtnHint())
		}
		// Rien en aval ne regarde resp.Status : si ntn rendait un 4xx/5xx en
		// sortant en 0, l'erreur passerait pour un succès. On la reclasse ici,
		// au seul endroit qui voit le statut.
		if status >= 400 {
			if apiErr, ok := ParseAPIError(stderr.Bytes()); ok {
				return resp, apiErr
			}
			return resp, &APIError{
				Status:     status,
				NotionCode: "unparsed",
				Message:    strings.TrimSpace(stderr.String()),
			}
		}
		return resp, nil
	}

	var exitErr *exec.ExitError
	if !errors.As(runErr, &exitErr) {
		// ntn introuvable, non exécutable : aucun appel n'a été émis, donc
		// l'issue est connue — c'est un échec.
		return APIResponse{}, runErr
	}

	switch exitErr.ExitCode() {
	case 2:
		return resp, &UsageError{
			Command: t.Binary + " " + strings.Join(args, " "),
			Stderr:  strings.TrimSpace(stderr.String()),
		}
	case 5:
		if apiErr, ok := ParseAPIError(stderr.Bytes()); ok {
			return resp, apiErr
		}
	}

	if hasStatus {
		return resp, &APIError{
			Status:     status,
			NotionCode: "unparsed",
			Message:    strings.TrimSpace(stderr.String()),
		}
	}
	return resp, &OutcomeUnknownError{Cause: runErr}
}

// checkPath refuse un chemin que notion-seed n'aurait pas dû construire. Il n'y
// a pas d'injection possible aujourd'hui — argv part en éléments séparés, aucun
// shell n'est impliqué — mais un id de page valant "../../v1/users" produit un
// chemin que la couche HTTP peut normaliser vers un autre endpoint que celui
// visé. La garde est ici, au plus près de l'appel, en plus du `pattern` UUID que
// le schéma impose sur parent_page_id.
func checkPath(p string) error {
	if !strings.HasPrefix(p, "/") {
		return fmt.Errorf("chemin d'API %q : il doit commencer par \"/\"", p)
	}
	if p != path.Clean(p) {
		return fmt.Errorf(
			"chemin d'API %q : il contient un segment relatif, ce qui peut viser un autre endpoint que celui prévu", p)
	}
	return nil
}
