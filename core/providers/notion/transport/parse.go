// SPDX-License-Identifier: GPL-3.0-or-later

package transport

import (
	"bufio"
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// statusLine matches the status line of ntn's verbose stderr: "< 200 OK".
var statusLine = regexp.MustCompile(`^< (\d{3}) `)

// apiErrorLine matches ntn's error message:
// error: Public API request failed (404 Not Found object_not_found): message
var apiErrorLine = regexp.MustCompile(
	`^error: Public API request failed \((\d{3}) [^)]*? ([a-z_]+)\): (.*)$`)

// ParseStatusAndHeaders extracts the HTTP status and the response headers
// from the verbose stderr. Header names are normalized to lowercase. ok is
// false if no status line is present (ntn did not reach the API).
//
// The scanner's error is returned, never swallowed: a line beyond the 1 MiB
// cap truncates the trace, so a header can be missing without anything
// reporting it — `retry-after` in particular. A status read before the
// truncation is not returned as valid: what was lost after it is unknown.
func ParseStatusAndHeaders(stderr []byte) (int, map[string][]string, bool, error) {
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
	if err := sc.Err(); err != nil {
		return 0, headers, false, fmt.Errorf(
			"unreadable ntn -v trace (%d bytes read): %w", len(stderr), err)
	}
	if status == 0 {
		return 0, headers, false, nil
	}
	return status, headers, true, nil
}

// ParseAPIError extracts the API error from stderr. The message can span
// several lines (the validation_error case): everything that follows the
// error line and does not start with "> " or "< " is part of it.
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
