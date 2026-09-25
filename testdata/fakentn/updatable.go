// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// updatableDatabase et updatableDataSource sont l'état de départ du scénario
// authenticated_database_updatable : la même database que celle
// d'authenticated_database, pour que les montages d'import soient les mêmes.
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

// updatableState est ce que le scénario garde d'un appel à l'autre : chaque
// invocation de fakentn est un processus neuf, donc l'état vit dans le fichier
// que nomme FAKE_NTN_STATE_FILE.
type updatableState struct {
	Database   map[string]any `json:"database"`
	DataSource map[string]any `json:"data_source"`
}

// apiMethod rend la méthode passée à `ntn api -X`, GET par défaut.
func apiMethod() string {
	args := os.Args[1:]
	for i, a := range args {
		if a == "-X" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return "GET"
}

// runUpdatable sert le scénario authenticated_database_updatable : une
// database qui RETIENT ce qu'on lui écrit.
//
// Les autres scénarios rendent des réponses fixes. Après un PATCH, la
// relecture d'apply y retrouverait donc l'état d'avant, et apply signalerait un
// écart entre la cible et le réel là où il n'y en a pas. Ici, chaque PATCH est
// fusionné dans l'état persistant, et les GET suivants le rendent — comme le
// ferait l'API.
//
// Sans FAKE_NTN_STATE_FILE, rien n'est persisté : le scénario rend l'état de
// départ, exactement comme authenticated_database.
//
// Ce qui est fidèle aux mesures du 2026-09-24, et seulement cela : le titre est
// partagé entre database et data source, l'icône écrite sur la database n'est
// lue que sur la database, les options transmises avec un id le gardent, les
// neuves en reçoivent un, et {"in_trash":true} rend archived et in_trash.
//
// Avec FAKE_NTN_LOG_FILE, chaque écriture est aussi journalisée : un test peut
// alors dire EXACTEMENT ce qui est parti vers l'API, pas seulement ce que l'état
// fusionné en a gardé.
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
	// Le comptage part en POST mais n'écrit rien : il n'est pas journalisé.
	if method != "GET" && !strings.HasSuffix(path, "/query") {
		if err := logMutation(method, path, body); err != nil {
			fmt.Fprintf(os.Stderr, "fakentn: journal non écrit: %v\n", err)
			os.Exit(70)
		}
	}
	st, err := loadUpdatable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "fakentn: état illisible: %v\n", err)
		os.Exit(70)
	}

	var out map[string]any
	switch {
	// Toujours AVANT le cas de préfixe : voir queryTwoRows.
	case strings.HasSuffix(path, "/query"):
		fmt.Fprint(os.Stderr, "> POST https://api.notion.com"+path+"\n"+
			"< 200 OK\n< content-type: application/json\n")
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
		fmt.Fprint(os.Stdout, `{"object":"page","id":"page1"}`)
		return
	}

	if method == "PATCH" {
		if err := saveUpdatable(st); err != nil {
			fmt.Fprintf(os.Stderr, "fakentn: état non écrit: %v\n", err)
			os.Exit(70)
		}
	}
	fmt.Fprint(os.Stderr, "> "+method+" https://api.notion.com"+path+"\n"+
		"< 200 OK\n< content-type: application/json\n")
	b, _ := json.Marshal(out)
	fmt.Fprint(os.Stdout, string(b))
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

// logMutation ajoute « MÉTHODE chemin corps » au fichier que nomme
// FAKE_NTN_LOG_FILE, une ligne par écriture. Sans la variable, rien n'est
// journalisé.
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

// patchDatabase fusionne un PATCH /v1/databases. Le titre et la description se
// lisent des deux côtés ; l'icône, sur la database seule.
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
	// Mesuré le 2026-09-24 : {"in_trash":true} rend archived=true et
	// in_trash=true.
	if v, ok := patch["in_trash"].(bool); ok && v {
		st.Database["archived"] = true
		st.Database["in_trash"] = true
	}
}

// withPlainText complète un rich text envoyé par `text.content` du
// `plain_text` que l'API rend en lecture, et que le décodeur lit.
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

// patchDataSource fusionne un PATCH /v1/data_sources : les propriétés omises
// restent intouchées, une propriété à null est retirée.
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

// mergeProperty construit la propriété telle que l'API la relirait après le
// PATCH : son id est conservé, son type est la seule clé de configuration du
// payload.
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

// mergeOptions attribue un id aux options neuves et garde celui des
// existantes, avec leur couleur — immuable côté API. Pour un status, le
// `group` de chaque option est replié dans `groups[].option_ids`, la forme que
// l'API rend en lecture.
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

	// Les groupes existants gardent leur id et leur ordre ; un groupe inconnu
	// en reçoit un.
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
