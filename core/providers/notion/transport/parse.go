package transport

import (
	"bufio"
	"bytes"
	"regexp"
	"strconv"
	"strings"
)

// statusLine matche la ligne de statut du stderr verbeux de ntn : "< 200 OK".
var statusLine = regexp.MustCompile(`^< (\d{3}) `)

// apiErrorLine matche le message d'erreur de ntn :
// error: Public API request failed (404 Not Found object_not_found): message
var apiErrorLine = regexp.MustCompile(
	`^error: Public API request failed \((\d{3}) [^)]*? ([a-z_]+)\): (.*)$`)

// ParseStatusAndHeaders extrait le statut HTTP et les headers de réponse du
// stderr verbeux. Les noms de headers sont normalisés en minuscules. ok vaut
// false si aucune ligne de statut n'est présente (ntn n'a pas atteint l'API).
func ParseStatusAndHeaders(stderr []byte) (int, map[string][]string, bool) {
	var status int
	headers := make(map[string][]string)
	inResponse := false

	sc := bufio.NewScanner(bytes.NewReader(stderr))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if m := statusLine.FindStringSubmatch(line); m != nil {
			status, _ = strconv.Atoi(m[1])
			inResponse = true
			continue
		}
		if !inResponse || !strings.HasPrefix(line, "< ") {
			continue
		}
		name, value, found := strings.Cut(strings.TrimPrefix(line, "< "), ":")
		if !found {
			continue
		}
		name = strings.ToLower(strings.TrimSpace(name))
		headers[name] = append(headers[name], strings.TrimSpace(value))
	}
	if status == 0 {
		return 0, headers, false
	}
	return status, headers, true
}

// ParseAPIError extrait l'erreur API du stderr. Le message peut s'étendre sur
// plusieurs lignes (cas des validation_error) : tout ce qui suit la ligne
// d'erreur et ne commence pas par "> " ou "< " en fait partie.
func ParseAPIError(stderr []byte) (*APIError, bool) {
	lines := strings.Split(string(stderr), "\n")
	for i, line := range lines {
		m := apiErrorLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		status, _ := strconv.Atoi(m[1])
		msg := []string{m[3]}
		for _, next := range lines[i+1:] {
			if strings.HasPrefix(next, "> ") || strings.HasPrefix(next, "< ") ||
				strings.TrimSpace(next) == "" {
				break
			}
			msg = append(msg, next)
		}
		return &APIError{
			Status:     status,
			NotionCode: m[2],
			Message:    strings.TrimSpace(strings.Join(msg, "\n")),
		}, true
	}
	return nil, false
}
