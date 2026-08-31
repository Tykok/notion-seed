// Command fakentn imite `ntn` pour les tests. Le scénario est choisi par la
// variable d'environnement FAKE_NTN_SCENARIO. Les sorties reproduisent des
// captures réelles de ntn 0.22.11.
package main

import (
	"fmt"
	"io"
	"os"
	"time"
)

const (
	versionLine    = "ntn 0.22.11\n"
	oldVersionLine = "ntn 0.19.0\n"
	whoamiLine     = "11111111-1111-4111-8111-111111111111\tNotion CLI\tbot\t" +
		"bot@example.com\t33333333-3333-4333-8333-333333333333\tExample Space\t" +
		"22222222-2222-4222-8222-222222222222\tExample User\tperson\n"
)

// subcommand dit quelle sous-commande ntn a été invoquée. Les scénarios d'auth
// doivent répondre différemment à --version et à whoami : dispatcher uniquement
// sur la variable d'environnement ferait répondre la version à whoami, et
// preflight.Check verrait une sortie illisible au lieu d'un défaut d'auth.
func subcommand() string {
	for _, a := range os.Args[1:] {
		switch a {
		case "--version", "-V", "whoami", "api", "login", "logout":
			return a
		}
	}
	return ""
}

func main() {
	switch os.Getenv("FAKE_NTN_SCENARIO") {
	case "authenticated":
		// ntn présent, à jour, authentifié : le chemin heureux de preflight.
		switch subcommand() {
		case "whoami":
			fmt.Fprint(os.Stdout, whoamiLine)
		default:
			fmt.Fprint(os.Stdout, versionLine)
		}
	case "version_no_auth":
		// ntn présent et à jour, mais pas authentifié.
		switch subcommand() {
		case "whoami":
			fmt.Fprintln(os.Stderr, "error: not logged in")
			os.Exit(1)
		default:
			fmt.Fprint(os.Stdout, versionLine)
		}
	case "version_old":
		fmt.Fprint(os.Stdout, oldVersionLine)
	case "ok":
		// Consomme stdin pour reproduire `-d @-` : sans ça, un test qui écrit
		// un body verrait un tuyau cassé.
		io.Copy(io.Discard, os.Stdin)
		fmt.Fprint(os.Stderr, "> GET https://api.notion.com/v1/x\n< 200 OK\n"+
			"< content-type: application/json\n")
		fmt.Fprint(os.Stdout, `{"object":"data_source","id":"abc"}`)
	case "not_found":
		io.Copy(io.Discard, os.Stdin)
		fmt.Fprint(os.Stderr, "> GET https://api.notion.com/v1/x\n< 404 Not Found\n"+
			"error: Public API request failed (404 Not Found object_not_found): "+
			"Could not find page with ID: abc.\n")
		os.Exit(5)
	case "rate_limited":
		io.Copy(io.Discard, os.Stdin)
		fmt.Fprint(os.Stderr, "> POST https://api.notion.com/v1/pages\n"+
			"< 429 Too Many Requests\n< retry-after: 1\n"+
			"error: Public API request failed (429 Too Many Requests rate_limited): "+
			"Rate limited.\n")
		os.Exit(5)
	case "server_error":
		io.Copy(io.Discard, os.Stdin)
		fmt.Fprint(os.Stderr, "< 502 Bad Gateway\n"+
			"error: Public API request failed (502 Bad Gateway internal_server_error): "+
			"Bad gateway.\n")
		os.Exit(5)
	case "usage_error":
		fmt.Fprint(os.Stderr, "error: unexpected argument '--nope' found\n")
		os.Exit(2)
	case "hang":
		time.Sleep(10 * time.Minute)
	case "echo_argv":
		// Permet de vérifier les arguments construits par notion-seed.
		for _, a := range os.Args[1:] {
			fmt.Fprintln(os.Stdout, a)
		}
	case "echo_stdin":
		fmt.Fprint(os.Stderr, "< 200 OK\n")
		io.Copy(os.Stdout, os.Stdin)
	default:
		fmt.Fprintln(os.Stderr, "fakentn: scénario inconnu")
		os.Exit(64)
	}
}
