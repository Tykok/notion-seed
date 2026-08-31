package transport

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"time"
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

	// stdin est TOUJOURS explicitement défini. Un stdin hérité, ouvert et sans
	// EOF, fait bloquer ntn api indéfiniment : c'est une source de body valide
	// pour lui, et il attend l'EOF qui ne vient jamais.
	if len(req.Body) > 0 {
		cmd.Stdin = bytes.NewReader(req.Body)
	} else {
		devNull, err := os.Open(os.DevNull)
		if err != nil {
			return APIResponse{}, err
		}
		defer devNull.Close()
		cmd.Stdin = devNull
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()

	// Un timeout ne dit pas si la mutation a été appliquée côté serveur.
	if ctx.Err() != nil {
		return APIResponse{}, &OutcomeUnknownError{Cause: ctx.Err()}
	}

	status, headers, hasStatus := ParseStatusAndHeaders(stderr.Bytes())
	resp := APIResponse{Status: status, Headers: headers, Body: stdout.Bytes()}

	if runErr == nil {
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
