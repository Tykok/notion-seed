// SPDX-License-Identifier: GPL-3.0-or-later

// Command fakentn imitates `ntn` for the tests. The scenario is chosen by the
// FAKE_NTN_SCENARIO environment variable. The outputs reproduce real captures
// of ntn 0.22.11.
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

// createdDatabase and createdDataSource are what fakentn returns after a
// creation, and also what it returns on read-back: both must match, otherwise
// apply would report a mismatch between the target and the actual state where
// there is none. The schema matches the `projects` database of the apply
// tests.
const createdDatabase = `{"object":"database","id":"db-new",` +
	`"archived":false,"in_trash":false,` +
	`"data_sources":[{"id":"ds-new","name":"Projects"}]}`

const createdDataSource = `{"object":"data_source","id":"ds-new",` +
	`"title":[{"plain_text":"Projects"}],` +
	`"properties":{"Name":{"id":"title","name":"Name","type":"title"}}}`

// queryTwoRows is the response to every count query
// (`POST /v1/data_sources/<id>/query`), except the unfiltered count of the
// authenticated_database_updatable scenario (see queryThreeRows). Two rows
// hold the "Fait" option: enough to check that a measured removal comes out
// with its number.
//
// ORDER TRAP, valid for every scenario: the path
// `/v1/data_sources/ds-1/query` ALSO carries the `/v1/data_sources/` prefix. A
// scenario that tests this prefix before the `/query` suffix returns a data
// source's schema to a count query — a perfectly valid 200, without any
// `results`. The count would see no rows there, hence "0 rows affected",
// hence "nothing to lose": a false claim, not an error. The `/query` case
// therefore ALWAYS comes before the `/v1/data_sources/` prefix case — it is
// that overlap, and it alone, that must be defused. It does not necessarily
// come first in the switch: `authenticated_database` handles `/v1/databases/`
// before it, harmlessly, since that prefix overlaps no count path.
//
// TestFakeNtnAnswersQueryWithAListInEveryScenario checks the result rather
// than the order: every scenario that serves `/v1/data_sources/` must return a
// list with a `results` to a count query.
//
// A non-zero count is deliberate: if a scenario receives a count query nobody
// planned for, it is better for it to produce a visible number than a zero
// that would read as "safe".
const queryTwoRows = `{"object":"list","results":[` +
	`{"object":"page","id":"p1"},{"object":"page","id":"p2"}],` +
	`"has_more":false}`

// parentPage is the readable, live parent page the scenarios return.
// Measured on 2026-09-25 against API 2025-09-03: a page carries in_trash, and
// NOT archived. The fixture has the same shape, so a regression that stopped
// reading in_trash is not masked by an archived field the API does not send.
const parentPage = `{"object":"page","id":"page1","in_trash":false}`

// subcommand says which ntn subcommand was invoked. The auth scenarios must
// answer --version and whoami differently: dispatching only on the
// environment variable would make whoami answer with the version, and
// preflight.Check would see an unreadable output instead of an auth failure.
func subcommand() string {
	for _, a := range os.Args[1:] {
		switch a {
		case "--version", "-V", "whoami", "api", "login", "logout":
			return a
		}
	}
	return ""
}

// apiPath returns the path passed to `ntn api`, so the trace imitates the
// real binary's.
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
		// ntn present, up to date, authenticated: the happy path of preflight
		// AND of plan's online preflight, which reads the parent page.
		switch subcommand() {
		case "whoami":
			fmt.Fprint(os.Stdout, whoamiLine)
		case "api":
			io.Copy(io.Discard, os.Stdin)
			fmt.Fprint(os.Stderr, "> GET https://api.notion.com"+apiPath()+"\n"+
				"< 200 OK\n< content-type: application/json\n")
			fmt.Fprint(os.Stdout, parentPage)
		default:
			fmt.Fprint(os.Stdout, versionLine)
		}
	case "authenticated_page_404":
		// ntn authenticated, but the parent page does not exist: covers the 404
		// branch of checkParentPage and its message.
		switch subcommand() {
		case "whoami":
			fmt.Fprint(os.Stdout, whoamiLine)
		case "api":
			io.Copy(io.Discard, os.Stdin)
			fmt.Fprint(os.Stderr, "> GET https://api.notion.com"+apiPath()+"\n"+
				"< 404 Not Found\n"+
				"error: Public API request failed (404 Not Found object_not_found): "+
				"Could not find page with ID: page-missing.\n")
			os.Exit(5)
		default:
			fmt.Fprint(os.Stdout, versionLine)
		}
	case "authenticated_page_in_trash":
		// ntn authenticated, parent page readable but in the trash: covers
		// checkParentPage's rejection. Only the page is served — plan stops
		// before any other read, and an unexpected request must fail loudly
		// rather than return a plausible response.
		switch subcommand() {
		case "whoami":
			fmt.Fprint(os.Stdout, whoamiLine)
		case "api":
			io.Copy(io.Discard, os.Stdin)
			path := apiPath()
			if !strings.HasPrefix(path, "/v1/pages/") {
				fmt.Fprintf(os.Stderr, "fakentn: %s not served by this scenario\n", path)
				os.Exit(64)
			}
			fmt.Fprint(os.Stderr, "> GET https://api.notion.com"+path+"\n"+
				"< 200 OK\n< content-type: application/json\n")
			// The shape measured on 2026-09-25: 200, in_trash set to true, and no
			// archived field.
			fmt.Fprint(os.Stdout, `{"object":"page","id":"page1","in_trash":true}`)
		default:
			fmt.Fprint(os.Stdout, versionLine)
		}
	case "authenticated_rate_limited":
		// ntn authenticated, but the API rate-limits every attempt: covers the
		// exhaustion of retries, its message and the announcement of waits.
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
		// ntn authenticated, with a readable database: covers import, the
		// refresh and the three-way diff end to end.
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
			// Always BEFORE the prefix case: see queryTwoRows.
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
				fmt.Fprint(os.Stdout, parentPage)
			}
		default:
			fmt.Fprint(os.Stdout, versionLine)
		}
	case "authenticated_database_select":
		// authenticated_database's database, with a select property instead of
		// the status: covers end to end the select → multi_select type change,
		// whose options that are not redeclared lose their rows (measured on
		// 2026-09-25).
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
			// Always BEFORE the prefix case: see queryTwoRows.
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
				fmt.Fprint(os.Stdout, parentPage)
			}
		default:
			fmt.Fprint(os.Stdout, versionLine)
		}
	case "authenticated_database_updatable":
		// authenticated_database's database, which remembers what is written to
		// it: covers apply's update end to end, read-back included. See
		// runUpdatable.
		runUpdatable()
	case "authenticated_database_query_403":
		// Everything is readable EXCEPT the count, rejected with 403: covers the
		// fact that a failed count prevents neither the plan nor its rendering,
		// and that its cause goes to stderr.
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
				fmt.Fprint(os.Stdout, parentPage)
			}
		default:
			fmt.Fprint(os.Stdout, versionLine)
		}
	case "authenticated_database_404":
		// ntn authenticated, parent page readable, but the database anchored by
		// the state is gone (404): covers the end-to-end block on a managed
		// resource that is not found — the only defect left without an
		// end-to-end test before this fix.
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
			fmt.Fprint(os.Stdout, parentPage)
		default:
			fmt.Fprint(os.Stdout, versionLine)
		}
	case "archived_database":
		// ntn authenticated, but the database is archived/in the trash: covers
		// the import rejection of a resource in the trash, the only defect that
		// had no test — a copy of authenticated_database with
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
			// Always BEFORE the prefix case: see queryTwoRows.
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
				fmt.Fprint(os.Stdout, parentPage)
			}
		default:
			fmt.Fprint(os.Stdout, versionLine)
		}
	case "authenticated_create":
		// ntn authenticated, parent page readable, creation accepted: apply's
		// happy path end to end. POST /v1/databases differs from
		// GET /v1/databases/<id> by the path alone, no need for the method.
		switch subcommand() {
		case "whoami":
			fmt.Fprint(os.Stdout, whoamiLine)
		case "api":
			io.Copy(io.Discard, os.Stdin)
			path := apiPath()
			fmt.Fprint(os.Stderr, "> https://api.notion.com"+path+"\n"+
				"< 200 OK\n< content-type: application/json\n")
			switch {
			// Always BEFORE the prefix case: see queryTwoRows.
			case strings.HasSuffix(path, "/query"):
				fmt.Fprint(os.Stdout, queryTwoRows)
			case strings.HasPrefix(path, "/v1/databases"):
				fmt.Fprint(os.Stdout, createdDatabase)
			case strings.HasPrefix(path, "/v1/data_sources/"):
				fmt.Fprint(os.Stdout, createdDataSource)
			default:
				fmt.Fprint(os.Stdout, parentPage)
			}
		default:
			fmt.Fprint(os.Stdout, versionLine)
		}
	case "authenticated_create_refused":
		// The creation is rejected by the API (400). Covers the stop without
		// rollback, and the fact that the state keeps nothing.
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
			fmt.Fprint(os.Stdout, parentPage)
		default:
			fmt.Fprint(os.Stdout, versionLine)
		}
	case "version_no_auth":
		// ntn present and up to date, but not authenticated.
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
		// Consumes stdin to reproduce `-d @-`: without it, a test that writes a
		// body would see a broken pipe.
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
		// Makes it possible to check the arguments built by notion-seed. The
		// status line is essential: without it, Execute rejects the success.
		fmt.Fprint(os.Stderr, "< 200 OK\n")
		for _, a := range os.Args[1:] {
			fmt.Fprintln(os.Stdout, a)
		}
	case "exit0_status_403":
		// ntn exits with 0 while the API answered 403: the status is the only
		// way to know.
		io.Copy(io.Discard, os.Stdin)
		fmt.Fprint(os.Stderr, "> GET https://api.notion.com/v1/x\n< 403 Forbidden\n")
		fmt.Fprint(os.Stdout, `{"object":"error","status":403}`)
	case "exit0_no_status":
		// ntn exits with 0 but its -v trace carries no "< NNN" line: it is the
		// shape an ntn output format change would take.
		io.Copy(io.Discard, os.Stdin)
		fmt.Fprint(os.Stderr, "> GET https://api.notion.com/v1/x\n"+
			"HTTP/2 200 (new format)\n")
		fmt.Fprint(os.Stdout, `{"object":"data_source","id":"abc"}`)
	case "exit1_unreadable_trace":
		// ntn exits with a failure (1) with a truncated -v trace: a line of more
		// than 1 MiB without a newline, beyond what the scanner accepts. Covers
		// the path where parseErr was lost on a non-zero exit, degrading into a
		// generic OutcomeUnknownError without saying the trace itself was at
		// fault.
		io.Copy(io.Discard, os.Stdin)
		fmt.Fprint(os.Stderr, strings.Repeat("a", 2*1024*1024))
		os.Exit(1)
	case "echo_stdin":
		fmt.Fprint(os.Stderr, "< 200 OK\n")
		io.Copy(os.Stdout, os.Stdin)
	default:
		fmt.Fprintln(os.Stderr, "fakentn: unknown scenario")
		os.Exit(64)
	}
}
