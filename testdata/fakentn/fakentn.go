// SPDX-License-Identifier: GPL-3.0-or-later

// Command fakentn imite `ntn` pour les tests. Le scénario est choisi par la
// variable d'environnement FAKE_NTN_SCENARIO. Les sorties reproduisent des
// captures réelles de ntn 0.22.11.
package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

const (
	versionLine    = "ntn 0.22.11\n"
	oldVersionLine = "ntn 0.19.0\n"
	whoamiLine     = "11111111-1111-4111-8111-111111111111\tNotion CLI\tbot\t" +
		"bot@example.com\t33333333-3333-4333-8333-333333333333\tExample Space\t" +
		"22222222-2222-4222-8222-222222222222\tExample User\tperson\n"
)

// createdDatabase et createdDataSource sont ce que fakentn rend après une
// création, et aussi ce qu'il rend à la relecture : les deux doivent coïncider,
// sinon apply signalerait un écart entre la cible et le réel là où il n'y en a
// pas. Le schéma correspond à la database `projects` des tests d'apply.
const createdDatabase = `{"object":"database","id":"db-new",` +
	`"archived":false,"in_trash":false,` +
	`"data_sources":[{"id":"ds-new","name":"Projects"}]}`

const createdDataSource = `{"object":"data_source","id":"ds-new",` +
	`"title":[{"plain_text":"Projects"}],` +
	`"properties":{"Name":{"id":"title","name":"Name","type":"title"}}}`

// queryTwoRows est la réponse de TOUTE requête de comptage
// (`POST /v1/data_sources/<id>/query`). Deux lignes portent l'option "Fait" :
// de quoi vérifier qu'un retrait mesuré ressort avec son chiffre.
//
// PIÈGE D'ORDRE, valable pour tous les scénarios : le chemin
// `/v1/data_sources/ds-1/query` porte AUSSI le préfixe `/v1/data_sources/`. Un
// scénario qui teste ce préfixe avant le suffixe `/query` rend le schéma d'un
// data source à une requête de comptage — un 200 parfaitement valide, sans
// aucun `results`. Le comptage n'y verrait aucune ligne, donc « 0 ligne
// concernée », donc « rien à perdre » : une affirmation fausse, pas une erreur.
// Le cas `/query` passe donc TOUJOURS avant le cas de préfixe
// `/v1/data_sources/` — c'est ce recouvrement-là, et lui seul, qu'il faut
// désarmer. Il ne passe pas forcément en tête du switch : `authenticated_database`
// traite `/v1/databases/` avant lui, sans conséquence, puisque ce préfixe ne
// recouvre aucun chemin de comptage.
//
// TestFakeNtnAnswersQueryWithAListInEveryScenario vérifie le résultat plutôt
// que l'ordre : chaque scénario qui sert `/v1/data_sources/` doit rendre une
// liste avec un `results` à une requête de comptage.
//
// Un compte non nul est délibéré : si un scénario reçoit une requête de
// comptage qu'on n'avait pas prévue, il vaut mieux qu'elle produise un chiffre
// visible qu'un zéro qui se lirait « sûr ».
const queryTwoRows = `{"object":"list","results":[` +
	`{"object":"page","id":"p1"},{"object":"page","id":"p2"}],` +
	`"has_more":false}`

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

// apiPath rend le chemin passé à `ntn api`, pour que la trace imite celle du
// vrai binaire.
func apiPath() string {
	for _, a := range os.Args[1:] {
		if strings.HasPrefix(a, "/") {
			return a
		}
	}
	return "/"
}

func main() {
	switch os.Getenv("FAKE_NTN_SCENARIO") {
	case "authenticated":
		// ntn présent, à jour, authentifié : le chemin heureux de preflight ET
		// du preflight en ligne de plan, qui lit la page parente.
		switch subcommand() {
		case "whoami":
			fmt.Fprint(os.Stdout, whoamiLine)
		case "api":
			io.Copy(io.Discard, os.Stdin)
			fmt.Fprint(os.Stderr, "> GET https://api.notion.com"+apiPath()+"\n"+
				"< 200 OK\n< content-type: application/json\n")
			fmt.Fprint(os.Stdout, `{"object":"page","id":"page1"}`)
		default:
			fmt.Fprint(os.Stdout, versionLine)
		}
	case "authenticated_page_404":
		// ntn authentifié, mais la page parente n'existe pas : couvre la branche
		// 404 de checkParentPage et son message.
		switch subcommand() {
		case "whoami":
			fmt.Fprint(os.Stdout, whoamiLine)
		case "api":
			io.Copy(io.Discard, os.Stdin)
			fmt.Fprint(os.Stderr, "> GET https://api.notion.com"+apiPath()+"\n"+
				"< 404 Not Found\n"+
				"error: Public API request failed (404 Not Found object_not_found): "+
				"Could not find page with ID: page-absente.\n")
			os.Exit(5)
		default:
			fmt.Fprint(os.Stdout, versionLine)
		}
	case "authenticated_rate_limited":
		// ntn authentifié, mais l'API limite le débit à chaque tentative : couvre
		// l'épuisement des retries, son message et l'annonce des attentes.
		switch subcommand() {
		case "whoami":
			fmt.Fprint(os.Stdout, whoamiLine)
		case "api":
			io.Copy(io.Discard, os.Stdin)
			fmt.Fprint(os.Stderr, "> GET https://api.notion.com"+apiPath()+"\n"+
				"< 429 Too Many Requests\n< retry-after: 1\n"+
				"error: Public API request failed (429 Too Many Requests rate_limited): "+
				"Rate limited.\n")
			os.Exit(5)
		default:
			fmt.Fprint(os.Stdout, versionLine)
		}
	case "authenticated_database":
		// ntn authentifié, avec une database lisible : couvre import, le refresh
		// et le diff à trois voies de bout en bout.
		switch subcommand() {
		case "whoami":
			fmt.Fprint(os.Stdout, whoamiLine)
		case "api":
			io.Copy(io.Discard, os.Stdin)
			path := apiPath()
			fmt.Fprint(os.Stderr, "> GET https://api.notion.com"+path+"\n"+
				"< 200 OK\n< content-type: application/json\n")
			switch {
			case strings.HasPrefix(path, "/v1/databases/"):
				fmt.Fprint(os.Stdout, `{"object":"database","id":"db-1",`+
					`"archived":false,"in_trash":false,`+
					`"data_sources":[{"id":"ds-1","name":"Tasks"}]}`)
			// Toujours AVANT le cas de préfixe : voir queryTwoRows.
			case strings.HasSuffix(path, "/query"):
				fmt.Fprint(os.Stdout, queryTwoRows)
			case strings.HasPrefix(path, "/v1/data_sources/"):
				fmt.Fprint(os.Stdout, `{"object":"data_source","id":"ds-1",`+
					`"title":[{"plain_text":"Tasks"}],`+
					`"properties":{`+
					`"Name":{"id":"title","name":"Name","type":"title"},`+
					`"Statut":{"id":"p-statut","name":"Statut","type":"status",`+
					`"status":{"options":[`+
					`{"id":"o-todo","name":"À faire","color":"blue"},`+
					`{"id":"o-done","name":"Fait","color":"green"}],`+
					`"groups":[`+
					`{"id":"g1","name":"To-do","option_ids":["o-todo"]},`+
					`{"id":"g2","name":"Complete","option_ids":["o-done"]}]}}}}`)
			default:
				fmt.Fprint(os.Stdout, `{"object":"page","id":"page1"}`)
			}
		default:
			fmt.Fprint(os.Stdout, versionLine)
		}
	case "authenticated_database_select":
		// La database d'authenticated_database, avec une propriété select au
		// lieu du status : couvre de bout en bout le changement de type
		// select → multi_select, dont les options non redéclarées perdent leurs
		// lignes (mesuré le 2026-09-25).
		switch subcommand() {
		case "whoami":
			fmt.Fprint(os.Stdout, whoamiLine)
		case "api":
			io.Copy(io.Discard, os.Stdin)
			path := apiPath()
			fmt.Fprint(os.Stderr, "> GET https://api.notion.com"+path+"\n"+
				"< 200 OK\n< content-type: application/json\n")
			switch {
			case strings.HasPrefix(path, "/v1/databases/"):
				fmt.Fprint(os.Stdout, `{"object":"database","id":"db-1",`+
					`"archived":false,"in_trash":false,`+
					`"data_sources":[{"id":"ds-1","name":"Tasks"}]}`)
			// Toujours AVANT le cas de préfixe : voir queryTwoRows.
			case strings.HasSuffix(path, "/query"):
				fmt.Fprint(os.Stdout, queryTwoRows)
			case strings.HasPrefix(path, "/v1/data_sources/"):
				fmt.Fprint(os.Stdout, `{"object":"data_source","id":"ds-1",`+
					`"title":[{"plain_text":"Tasks"}],`+
					`"properties":{`+
					`"Name":{"id":"title","name":"Name","type":"title"},`+
					`"Prio":{"id":"p-prio","name":"Prio","type":"select",`+
					`"select":{"options":[`+
					`{"id":"o-haute","name":"Haute","color":"red"},`+
					`{"id":"o-basse","name":"Basse","color":"blue"}]}}}}`)
			default:
				fmt.Fprint(os.Stdout, `{"object":"page","id":"page1"}`)
			}
		default:
			fmt.Fprint(os.Stdout, versionLine)
		}
	case "authenticated_database_updatable":
		// La database d'authenticated_database, qui retient ce qu'on lui écrit :
		// couvre l'update d'apply de bout en bout, relecture comprise. Voir
		// runUpdatable.
		runUpdatable()
	case "authenticated_database_query_403":
		// Tout est lisible SAUF le comptage, refusé en 403 : couvre le fait
		// qu'un comptage en échec n'empêche ni le plan ni son rendu, et que sa
		// cause part sur stderr.
		switch subcommand() {
		case "whoami":
			fmt.Fprint(os.Stdout, whoamiLine)
		case "api":
			io.Copy(io.Discard, os.Stdin)
			path := apiPath()
			if strings.HasSuffix(path, "/query") {
				fmt.Fprint(os.Stderr, "> POST https://api.notion.com"+path+"\n"+
					"< 403 Forbidden\n"+
					"error: Public API request failed (403 Forbidden restricted_resource): "+
					"Insufficient permissions.\n")
				os.Exit(5)
			}
			fmt.Fprint(os.Stderr, "> GET https://api.notion.com"+path+"\n"+
				"< 200 OK\n< content-type: application/json\n")
			switch {
			case strings.HasPrefix(path, "/v1/databases/"):
				fmt.Fprint(os.Stdout, `{"object":"database","id":"db-1",`+
					`"archived":false,"in_trash":false,`+
					`"data_sources":[{"id":"ds-1","name":"Tasks"}]}`)
			case strings.HasPrefix(path, "/v1/data_sources/"):
				fmt.Fprint(os.Stdout, `{"object":"data_source","id":"ds-1",`+
					`"title":[{"plain_text":"Tasks"}],`+
					`"properties":{`+
					`"Name":{"id":"title","name":"Name","type":"title"},`+
					`"Statut":{"id":"p-statut","name":"Statut","type":"status",`+
					`"status":{"options":[`+
					`{"id":"o-todo","name":"À faire","color":"blue"},`+
					`{"id":"o-done","name":"Fait","color":"green"}],`+
					`"groups":[`+
					`{"id":"g1","name":"To-do","option_ids":["o-todo"]},`+
					`{"id":"g2","name":"Complete","option_ids":["o-done"]}]}}}}`)
			default:
				fmt.Fprint(os.Stdout, `{"object":"page","id":"page1"}`)
			}
		default:
			fmt.Fprint(os.Stdout, versionLine)
		}
	case "authenticated_database_404":
		// ntn authentifié, page parente lisible, mais la database ancrée par le
		// state a disparu (404) : couvre le blocage de bout en bout sur une
		// ressource gérée introuvable — seul défaut resté sans test de bout en
		// bout avant cette correction.
		switch subcommand() {
		case "whoami":
			fmt.Fprint(os.Stdout, whoamiLine)
		case "api":
			io.Copy(io.Discard, os.Stdin)
			path := apiPath()
			if strings.HasPrefix(path, "/v1/databases/") {
				fmt.Fprint(os.Stderr, "> GET https://api.notion.com"+path+"\n"+
					"< 404 Not Found\n"+
					"error: Public API request failed (404 Not Found object_not_found): "+
					"Could not find database with ID: db-1.\n")
				os.Exit(5)
			}
			fmt.Fprint(os.Stderr, "> GET https://api.notion.com"+path+"\n"+
				"< 200 OK\n< content-type: application/json\n")
			fmt.Fprint(os.Stdout, `{"object":"page","id":"page1"}`)
		default:
			fmt.Fprint(os.Stdout, versionLine)
		}
	case "archived_database":
		// ntn authentifié, mais la database est archivée/en corbeille : couvre
		// le refus d'import d'une ressource en corbeille, seul défaut à
		// n'avoir eu aucun test — copie de authenticated_database avec
		// "archived":true.
		switch subcommand() {
		case "whoami":
			fmt.Fprint(os.Stdout, whoamiLine)
		case "api":
			io.Copy(io.Discard, os.Stdin)
			path := apiPath()
			fmt.Fprint(os.Stderr, "> GET https://api.notion.com"+path+"\n"+
				"< 200 OK\n< content-type: application/json\n")
			switch {
			case strings.HasPrefix(path, "/v1/databases/"):
				fmt.Fprint(os.Stdout, `{"object":"database","id":"db-1",`+
					`"archived":true,"in_trash":true,`+
					`"data_sources":[{"id":"ds-1","name":"Tasks"}]}`)
			// Toujours AVANT le cas de préfixe : voir queryTwoRows.
			case strings.HasSuffix(path, "/query"):
				fmt.Fprint(os.Stdout, queryTwoRows)
			case strings.HasPrefix(path, "/v1/data_sources/"):
				fmt.Fprint(os.Stdout, `{"object":"data_source","id":"ds-1",`+
					`"title":[{"plain_text":"Tasks"}],`+
					`"properties":{`+
					`"Name":{"id":"title","name":"Name","type":"title"},`+
					`"Statut":{"id":"p-statut","name":"Statut","type":"status",`+
					`"status":{"options":[`+
					`{"id":"o-todo","name":"À faire","color":"blue"},`+
					`{"id":"o-done","name":"Fait","color":"green"}],`+
					`"groups":[`+
					`{"id":"g1","name":"To-do","option_ids":["o-todo"]},`+
					`{"id":"g2","name":"Complete","option_ids":["o-done"]}]}}}}`)
			default:
				fmt.Fprint(os.Stdout, `{"object":"page","id":"page1"}`)
			}
		default:
			fmt.Fprint(os.Stdout, versionLine)
		}
	case "authenticated_create":
		// ntn authentifié, page parente lisible, création acceptée : le chemin
		// heureux d'apply de bout en bout. POST /v1/databases se distingue de
		// GET /v1/databases/<id> par le chemin seul, pas besoin de la méthode.
		switch subcommand() {
		case "whoami":
			fmt.Fprint(os.Stdout, whoamiLine)
		case "api":
			io.Copy(io.Discard, os.Stdin)
			path := apiPath()
			fmt.Fprint(os.Stderr, "> https://api.notion.com"+path+"\n"+
				"< 200 OK\n< content-type: application/json\n")
			switch {
			// Toujours AVANT le cas de préfixe : voir queryTwoRows.
			case strings.HasSuffix(path, "/query"):
				fmt.Fprint(os.Stdout, queryTwoRows)
			case strings.HasPrefix(path, "/v1/databases"):
				fmt.Fprint(os.Stdout, createdDatabase)
			case strings.HasPrefix(path, "/v1/data_sources/"):
				fmt.Fprint(os.Stdout, createdDataSource)
			default:
				fmt.Fprint(os.Stdout, `{"object":"page","id":"page1"}`)
			}
		default:
			fmt.Fprint(os.Stdout, versionLine)
		}
	case "authenticated_create_refused":
		// La création est refusée par l'API (400). Couvre l'arrêt sans rollback,
		// et le fait que le state ne retient rien.
		switch subcommand() {
		case "whoami":
			fmt.Fprint(os.Stdout, whoamiLine)
		case "api":
			io.Copy(io.Discard, os.Stdin)
			path := apiPath()
			if path == "/v1/databases" {
				fmt.Fprint(os.Stderr, "> POST https://api.notion.com"+path+"\n"+
					"< 400 Bad Request\n"+
					"error: Public API request failed (400 Bad Request validation_error): "+
					"Invalid property.\n")
				os.Exit(5)
			}
			fmt.Fprint(os.Stderr, "> GET https://api.notion.com"+path+"\n"+
				"< 200 OK\n< content-type: application/json\n")
			fmt.Fprint(os.Stdout, `{"object":"page","id":"page1"}`)
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
		// Permet de vérifier les arguments construits par notion-seed. La ligne
		// de statut est indispensable : sans elle, Execute refuse le succès.
		fmt.Fprint(os.Stderr, "< 200 OK\n")
		for _, a := range os.Args[1:] {
			fmt.Fprintln(os.Stdout, a)
		}
	case "exit0_status_403":
		// ntn sort en 0 alors que l'API a répondu 403 : le statut est la seule
		// façon de le savoir.
		io.Copy(io.Discard, os.Stdin)
		fmt.Fprint(os.Stderr, "> GET https://api.notion.com/v1/x\n< 403 Forbidden\n")
		fmt.Fprint(os.Stdout, `{"object":"error","status":403}`)
	case "exit0_no_status":
		// ntn sort en 0 mais sa trace -v ne porte aucune ligne "< NNN" : c'est la
		// forme que prendrait un changement de format de sortie de ntn.
		io.Copy(io.Discard, os.Stdin)
		fmt.Fprint(os.Stderr, "> GET https://api.notion.com/v1/x\n"+
			"HTTP/2 200 (nouveau format)\n")
		fmt.Fprint(os.Stdout, `{"object":"data_source","id":"abc"}`)
	case "exit1_unreadable_trace":
		// ntn sort en échec (1) avec une trace -v tronquée : une ligne de plus de
		// 1 MiB sans retour à la ligne, au-delà de ce que le scanner accepte.
		// Couvre le chemin où parseErr était perdu sur un exit non-nul, dégradant
		// vers un OutcomeUnknownError générique sans dire que la trace elle-même
		// était en cause.
		io.Copy(io.Discard, os.Stdin)
		fmt.Fprint(os.Stderr, strings.Repeat("a", 2*1024*1024))
		os.Exit(1)
	case "echo_stdin":
		fmt.Fprint(os.Stderr, "< 200 OK\n")
		io.Copy(os.Stdout, os.Stdin)
	default:
		fmt.Fprintln(os.Stderr, "fakentn: scénario inconnu")
		os.Exit(64)
	}
}
