// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// updatableDatabase and updatableDataSource are the starting state of the
// authenticated_database_updatable scenario: the same database as
// authenticated_database's, so the import setups are the same.
const updatableDatabase = `{"object":"database","id":"db-1",` +
	`"archived":false,"in_trash":false,` +
	`"data_sources":[{"id":"ds-1","name":"Tasks"}]}`

const updatableDataSource = `{"object":"data_source","id":"ds-1",` +
	`"title":[{"plain_text":"Tasks"}],` +
	`"properties":{` +
	`"Name":{"id":"title","name":"Name","type":"title"},` +
	`"Statut":{"id":"p-statut","name":"Statut","type":"status",` +
	`"status":{"options":[` +
	`{"id":"o-todo","name":"À faire","color":"blue"},` +
	`{"id":"o-done","name":"Fait","color":"green"}],` +
	`"groups":[` +
	`{"id":"g1","name":"To-do","option_ids":["o-todo"]},` +
	`{"id":"g2","name":"Complete","option_ids":["o-done"]}]}}}}`

// updatableState is what the scenario keeps from one call to the next: each
// fakentn invocation is a new process, so the state lives in the file named
// by FAKE_NTN_STATE_FILE.
type updatableState struct {
	Database   map[string]any `json:"database"`
	DataSource map[string]any `json:"data_source"`
}

// apiMethod returns the method passed to `ntn api -X`, GET by default.
func apiMethod() string {
	args := os.Args[1:]
	for i, a := range args {
		if a == "-X" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return "GET"
}

// runUpdatable serves the authenticated_database_updatable scenario: a
// database that REMEMBERS what is written to it.
//
// The other scenarios return fixed responses. After a PATCH, apply's
// read-back would therefore find the previous state there, and apply would
// report a mismatch between the target and the actual state where there is
// none. Here, each PATCH is merged into the persistent state, and the
// following GETs return it — as the API would.
//
// Without FAKE_NTN_STATE_FILE, nothing is persisted: the scenario returns the
// starting state, exactly like authenticated_database.
//
// What is faithful to the 2026-09-24 measurements, and only that: the title
// is shared between database and data source, the icon written on the
// database is read only on the database, options sent with an id keep it, new
// ones get one, and {"in_trash":true} returns archived and in_trash.
//
// With FAKE_NTN_LOG_FILE, each write is also logged: a test can then say
// EXACTLY what went to the API, not only what the merged state kept of it.
func runUpdatable() {
	switch subcommand() {
	case "whoami":
		fmt.Fprint(os.Stdout, whoamiLine)
		return
	case "api":
	default:
		fmt.Fprint(os.Stdout, versionLine)
		return
	}

	body, _ := io.ReadAll(os.Stdin)
	path, method := apiPath(), apiMethod()
	// The count goes out as a POST but writes nothing: it is not logged.
	if method != "GET" && !strings.HasSuffix(path, "/query") {
		if err := logMutation(method, path, body); err != nil {
			fmt.Fprintf(os.Stderr, "fakentn: log not written: %v\n", err)
			os.Exit(70)
		}
	}
	st, err := loadUpdatable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "fakentn: unreadable state: %v\n", err)
		os.Exit(70)
	}

	var out map[string]any
	switch {
	// Always BEFORE the prefix case: see queryTwoRows.
	case strings.HasSuffix(path, "/query"):
		// FAKE_NTN_QUERY_STATUS=403 makes every count fail, the way a token
		// without read access would: a count that fails at apply time, after a
		// plan that counted.
		if os.Getenv("FAKE_NTN_QUERY_STATUS") == "403" {
			fmt.Fprint(os.Stderr, "> POST https://api.notion.com"+path+"\n"+
				"< 403 Forbidden\n"+
				"error: Public API request failed (403 Forbidden restricted_resource): "+
				"Insufficient permissions.\n")
			os.Exit(5)
		}
		fmt.Fprint(os.Stderr, "> POST https://api.notion.com"+path+"\n"+
			"< 200 OK\n< content-type: application/json\n")
		// FAKE_NTN_QUERY_ROWS sets the number of rows EVERY count returns,
		// filtered or not: a test moves the count between plan and apply.
		if rows, set := os.LookupEnv("FAKE_NTN_QUERY_ROWS"); set {
			n, err := strconv.Atoi(rows)
			if err != nil || n < 0 || n > 100 {
				fmt.Fprintf(os.Stderr, "fakentn: FAKE_NTN_QUERY_ROWS=%q, want 0 to 100\n", rows)
				os.Exit(64)
			}
			fmt.Fprint(os.Stdout, queryRows(n))
			return
		}
		// Without a filter, it is the count of ALL the rows — a destruction's.
		// The database holds three, two of which hold "Fait": a count
		// different from queryTwoRows proves the request went out without a
		// filter.
		if isUnfilteredQuery(body) {
			fmt.Fprint(os.Stdout, queryThreeRows)
			return
		}
		fmt.Fprint(os.Stdout, queryTwoRows)
		return
	case strings.HasPrefix(path, "/v1/databases/"):
		if method == "PATCH" {
			patchDatabase(st, body)
		}
		out = st.Database
	case strings.HasPrefix(path, "/v1/data_sources/"):
		if method == "PATCH" {
			patchDataSource(st, body)
		}
		out = st.DataSource
	default:
		fmt.Fprint(os.Stderr, "> GET https://api.notion.com"+path+"\n"+
			"< 200 OK\n< content-type: application/json\n")
		fmt.Fprint(os.Stdout, parentPage)
		return
	}

	if method == "PATCH" {
		if err := saveUpdatable(st); err != nil {
			fmt.Fprintf(os.Stderr, "fakentn: state not written: %v\n", err)
			os.Exit(70)
		}
	}
	fmt.Fprint(os.Stderr, "> "+method+" https://api.notion.com"+path+"\n"+
		"< 200 OK\n< content-type: application/json\n")
	b, _ := json.Marshal(out)
	fmt.Fprint(os.Stdout, string(b))
}

// queryThreeRows is the whole database of the scenario: three rows.
const queryThreeRows = `{"object":"list","results":[` +
	`{"object":"page","id":"p1"},{"object":"page","id":"p2"},` +
	`{"object":"page","id":"p3"}],"has_more":false}`

// queryRows is a count response of n rows on a single page. 100 is the page
// size notion-seed asks for: beyond, the response would have to paginate.
func queryRows(n int) string {
	rows := make([]string, n)
	for i := range rows {
		rows[i] = fmt.Sprintf(`{"object":"page","id":"p%d"}`, i+1)
	}
	return `{"object":"list","results":[` + strings.Join(rows, ",") + `],"has_more":false}`
}

// isUnfilteredQuery says whether the body of a count query carries no
// filter.
func isUnfilteredQuery(body []byte) bool {
	var q map[string]any
	if json.Unmarshal(body, &q) != nil {
		return false
	}
	_, filtered := q["filter"]
	return !filtered
}

func loadUpdatable() (*updatableState, error) {
	st := &updatableState{}
	if path := os.Getenv("FAKE_NTN_STATE_FILE"); path != "" {
		b, err := os.ReadFile(path)
		switch {
		case err == nil:
			return st, json.Unmarshal(b, st)
		case !os.IsNotExist(err):
			return nil, err
		}
	}
	if err := json.Unmarshal([]byte(updatableDatabase), &st.Database); err != nil {
		return nil, err
	}
	return st, json.Unmarshal([]byte(updatableDataSource), &st.DataSource)
}

func saveUpdatable(st *updatableState) error {
	path := os.Getenv("FAKE_NTN_STATE_FILE")
	if path == "" {
		return nil
	}
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// logMutation appends "METHOD path body" to the file named by
// FAKE_NTN_LOG_FILE, one line per write. Without the variable, nothing is
// logged.
func logMutation(method, path string, body []byte) error {
	name := os.Getenv("FAKE_NTN_LOG_FILE")
	if name == "" {
		return nil
	}
	f, err := os.OpenFile(name, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "%s %s %s\n", method, path, strings.TrimSpace(string(body)))
	return err
}

// patchDatabase merges a PATCH /v1/databases. The title and the description
// are read on both sides; the icon, on the database only.
func patchDatabase(st *updatableState, body []byte) {
	var patch map[string]any
	if json.Unmarshal(body, &patch) != nil {
		return
	}
	if v, ok := patch["title"]; ok {
		st.Database["title"] = withPlainText(v)
		st.DataSource["title"] = withPlainText(v)
	}
	if v, ok := patch["description"]; ok {
		st.Database["description"] = withPlainText(v)
		st.DataSource["description"] = withPlainText(v)
	}
	if v, ok := patch["icon"]; ok {
		st.Database["icon"] = v
	}
	// Measured on 2026-09-24: {"in_trash":true} returns archived=true and
	// in_trash=true.
	if v, ok := patch["in_trash"].(bool); ok && v {
		st.Database["archived"] = true
		st.Database["in_trash"] = true
	}
}

// withPlainText completes a rich text sent through `text.content` with the
// `plain_text` the API returns on read, and that the decoder reads.
func withPlainText(v any) any {
	items, ok := v.([]any)
	if !ok {
		return v
	}
	for _, it := range items {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		if text, ok := m["text"].(map[string]any); ok {
			if content, ok := text["content"].(string); ok {
				m["plain_text"] = content
			}
		}
	}
	return items
}

// patchDataSource merges a PATCH /v1/data_sources: omitted properties stay
// untouched, a property set to null is removed.
func patchDataSource(st *updatableState, body []byte) {
	var patch struct {
		Properties map[string]map[string]any `json:"properties"`
	}
	if json.Unmarshal(body, &patch) != nil {
		return
	}
	props, _ := st.DataSource["properties"].(map[string]any)
	if props == nil {
		props = map[string]any{}
		st.DataSource["properties"] = props
	}
	for name, payload := range patch.Properties {
		if payload == nil {
			delete(props, name)
			continue
		}
		prior, _ := props[name].(map[string]any)
		props[name] = mergeProperty(name, prior, payload)
	}
}

// mergeProperty builds the property as the API would read it back after the
// PATCH: its id is kept, its type is the payload's only configuration key.
func mergeProperty(name string, prior, payload map[string]any) map[string]any {
	out := map[string]any{"id": "p-" + strings.ToLower(name), "name": name}
	if prior != nil {
		out["id"] = prior["id"]
	}
	for typ, cfg := range payload {
		if typ == "name" || typ == "id" {
			continue
		}
		out["type"] = typ
		conf, _ := cfg.(map[string]any)
		if conf == nil {
			conf = map[string]any{}
		}
		if opts, ok := conf["options"].([]any); ok {
			var priorConf map[string]any
			if prior != nil && prior["type"] == typ {
				priorConf, _ = prior[typ].(map[string]any)
			}
			conf = mergeOptions(typ, conf, opts, priorConf)
		}
		out[typ] = conf
	}
	return out
}

// mergeOptions assigns an id to new options and keeps the existing ones', with
// their color — immutable on the API side. For a status, each option's
// `group` is folded into `groups[].option_ids`, the shape the API returns on
// read.
func mergeOptions(typ string, conf map[string]any, opts []any, priorConf map[string]any) map[string]any {
	priorByID := map[string]map[string]any{}
	priorGroupOf := map[string]string{}
	var priorGroups []any
	if priorConf != nil {
		if po, ok := priorConf["options"].([]any); ok {
			for _, o := range po {
				if m, ok := o.(map[string]any); ok {
					if id, _ := m["id"].(string); id != "" {
						priorByID[id] = m
					}
				}
			}
		}
		priorGroups, _ = priorConf["groups"].([]any)
		for _, g := range priorGroups {
			gm, _ := g.(map[string]any)
			ids, _ := gm["option_ids"].([]any)
			for _, id := range ids {
				if s, ok := id.(string); ok {
					priorGroupOf[s], _ = gm["name"].(string)
				}
			}
		}
	}

	groupOf := map[string]string{}
	var order []string
	for i, o := range opts {
		m, _ := o.(map[string]any)
		if m == nil {
			continue
		}
		id, _ := m["id"].(string)
		if id == "" {
			id = fmt.Sprintf("o-new-%d", i)
			m["id"] = id
		}
		if p, ok := priorByID[id]; ok {
			m["color"] = p["color"]
		} else if _, ok := m["color"]; !ok {
			m["color"] = "default"
		}
		if typ == "status" {
			g, _ := m["group"].(string)
			if g == "" {
				g = priorGroupOf[id]
			}
			if g == "" {
				g = "To-do"
			}
			if _, seen := groupOf[g]; !seen {
				order = append(order, g)
			}
			groupOf[id] = g
			delete(m, "group")
		}
	}
	conf["options"] = opts
	if typ != "status" {
		return conf
	}

	// Existing groups keep their id and their order; an unknown group gets
	// one.
	var groups []any
	named := map[string]bool{}
	build := func(gid, gname string) map[string]any {
		ids := []any{}
		for _, o := range opts {
			m, _ := o.(map[string]any)
			if m == nil {
				continue
			}
			if id, _ := m["id"].(string); groupOf[id] == gname {
				ids = append(ids, id)
			}
		}
		return map[string]any{"id": gid, "name": gname, "option_ids": ids}
	}
	for _, g := range priorGroups {
		gm, _ := g.(map[string]any)
		gname, _ := gm["name"].(string)
		gid, _ := gm["id"].(string)
		named[gname] = true
		groups = append(groups, build(gid, gname))
	}
	for i, gname := range order {
		if !named[gname] {
			groups = append(groups, build(fmt.Sprintf("g-new-%d", i), gname))
		}
	}
	conf["groups"] = groups
	return conf
}
