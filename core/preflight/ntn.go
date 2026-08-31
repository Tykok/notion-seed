// Package preflight vérifie que l'environnement peut faire tourner
// notion-seed : ntn présent, assez récent, et authentifié.
package preflight

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// MinNtnVersion est la seule version sur laquelle le contrat de sortie de ntn
// a été mesuré. À relever quand une plus récente est validée, jamais à
// supposer compatible vers le bas : notion-seed parse le stdout et le stderr
// de ntn, donc un changement de format le casse silencieusement.
const MinNtnVersion = "0.22.11"

var (
	ErrNotInstalled     = errors.New("ntn n'est pas installé")
	ErrNotAuthenticated = errors.New("ntn n'est pas authentifié")
)

// TooOldError signale un ntn présent mais trop ancien.
type TooOldError struct {
	Found   string
	Minimum string
}

func (e *TooOldError) Error() string {
	return fmt.Sprintf(
		"ntn %s est trop ancien, notion-seed exige au moins %s — mettez-le à jour avec `ntn update`",
		e.Found, e.Minimum)
}

// Info décrit l'environnement validé.
type Info struct {
	NtnVersion    string
	WorkspaceID   string
	WorkspaceName string
	BotEmail      string
}

// Check vérifie la présence, la version et l'authentification de ntn.
func Check(ctx context.Context, binary string) (Info, error) {
	if _, err := exec.LookPath(binary); err != nil {
		return Info{}, fmt.Errorf(
			"%w — installez-le avec `npm i -g ntn`, puis lancez `notion-seed init`",
			ErrNotInstalled)
	}

	versionOut, err := run(ctx, binary, "--version")
	if err != nil {
		return Info{}, fmt.Errorf(
			"`%s --version` a échoué: %w\n"+
				"  → %s est présent mais ne répond pas ; réinstallez-le avec "+
				"`npm i -g ntn@%s`, puis relancez `notion-seed init`",
			binary, err, binary, MinNtnVersion)
	}
	version, err := parseVersion(versionOut)
	if err != nil {
		return Info{}, err
	}
	if !versionAtLeast(version, MinNtnVersion) {
		return Info{}, &TooOldError{Found: version, Minimum: MinNtnVersion}
	}

	whoamiOut, err := run(ctx, binary, "whoami")
	if err != nil {
		// La cause réelle est conservée : un échec réseau, un plantage interne de
		// ntn ou une annulation de contexte ne sont pas « pas connecté », et dire
		// à l'utilisateur de relancer init ne corrigerait rien. Le %w sur la
		// sentinelle garde errors.Is utilisable par les appelants.
		return Info{}, fmt.Errorf(
			"%w — lancez `notion-seed init` pour vous connecter (cause: %v)",
			ErrNotAuthenticated, err)
	}
	info, err := parseWhoami(whoamiOut)
	if err != nil {
		return Info{}, err
	}
	info.NtnVersion = version
	return info, nil
}

func run(ctx context.Context, binary string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// cmd.Stdin laissé nil : os/exec donne /dev/null à l'enfant. Ne jamais y
	// mettre os.Stdin, que `ntn` prendrait pour un body sans EOF.
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// parseVersion lit la sortie de `ntn --version`, de la forme "ntn 0.22.11".
func parseVersion(out []byte) (string, error) {
	fields := strings.Fields(string(out))
	if len(fields) < 2 {
		return "", fmt.Errorf(
			"sortie de `ntn --version` illisible: %q\n"+
				"  → notion-seed lit le format de sortie de ntn %s ; épinglez cette "+
				"version avec `npm i -g ntn@%s`",
			strings.TrimSpace(string(out)), MinNtnVersion, MinNtnVersion)
	}
	return fields[len(fields)-1], nil
}

// parseWhoami lit la sortie de `ntn whoami`, une ligne de champs séparés par
// des tabulations : bot_id, bot_name, "bot", email, workspace_id,
// workspace_name, user_id, user_name, "person".
func parseWhoami(out []byte) (Info, error) {
	line := strings.TrimSpace(string(out))
	fields := strings.Split(line, "\t")
	if len(fields) < 6 {
		return Info{}, fmt.Errorf(
			"sortie de `ntn whoami` illisible (%d champs, 6 attendus au minimum): %q\n"+
				"  → notion-seed lit le format de sortie de ntn %s ; épinglez cette "+
				"version avec `npm i -g ntn@%s`, ou relancez `ntn login` si la session a expiré",
			len(fields), line, MinNtnVersion, MinNtnVersion)
	}
	return Info{
		BotEmail:      fields[3],
		WorkspaceID:   fields[4],
		WorkspaceName: fields[5],
	}, nil
}

// versionAtLeast compare deux versions semver-ish champ par champ. Un champ
// non numérique (pré-release) est traité comme 0.
func versionAtLeast(found, minimum string) bool {
	f := splitVersion(found)
	m := splitVersion(minimum)
	for i := 0; i < 3; i++ {
		switch {
		case f[i] > m[i]:
			return true
		case f[i] < m[i]:
			return false
		}
	}
	return true
}

func splitVersion(v string) [3]int {
	var out [3]int
	parts := strings.SplitN(strings.TrimPrefix(v, "v"), ".", 4)
	for i := 0; i < 3 && i < len(parts); i++ {
		numeric := parts[i]
		if idx := strings.IndexFunc(numeric, func(r rune) bool {
			return r < '0' || r > '9'
		}); idx >= 0 {
			numeric = numeric[:idx]
		}
		out[i], _ = strconv.Atoi(numeric)
	}
	return out
}
