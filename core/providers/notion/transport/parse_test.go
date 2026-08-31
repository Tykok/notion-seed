package transport

import (
	"strings"
	"testing"
)

const stderr200 = `> GET https://api.notion.com/v1/data_sources/66666666-6666-4666-8666-666666666666
> authorization: <redacted>
> notion-version: 2025-09-03
< 200 OK
< content-type: application/json; charset=utf-8
< x-notion-request-id: 99999999-9999-4999-8999-999999999999
`

const stderr404 = `> GET https://api.notion.com/v1/data_sources/00000000-0000-0000-0000-000000000000
< 404 Not Found
< content-length: 363
error: Public API request failed (404 Not Found object_not_found): Could not find data_source with ID: 00000000-0000-0000-0000-000000000000.
`

const stderr429 = `> POST https://api.notion.com/v1/pages
< 429 Too Many Requests
< retry-after: 3
error: Public API request failed (429 Too Many Requests rate_limited): Rate limited.
`

const stderr400 = `error: Public API request failed (400 Bad Request validation_error): body failed validation: body.parent.page_id should be a valid uuid, instead was ` + "`\"nope\"`" + `.
`

const stderr400MultiLine = `error: Public API request failed (400 Bad Request validation_error): body failed validation. Fix one:
` + `body.properties.Status.status.options[4].group should be ` + "`\"To-do\"`" + `, ` + "`\"In progress\"`" + `, ` + "`\"Complete\"`" + `, or ` + "`\"undefined\"`" + `, instead was ` + "`\"Waiting on someone\"`" + `.
`

func TestParseStatusAndHeaders(t *testing.T) {
	tests := []struct {
		name       string
		stderr     string
		wantStatus int
		wantOK     bool
		headerKey  string
		headerVal  string
	}{
		{"200 avec headers", stderr200, 200, true, "x-notion-request-id", "99999999-9999-4999-8999-999999999999"},
		{"404", stderr404, 404, true, "content-length", "363"},
		{"429 avec retry-after", stderr429, 429, true, "retry-after", "3"},
		{"pas de ligne de statut", stderr400, 0, false, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, headers, ok := ParseStatusAndHeaders([]byte(tt.stderr))
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if status != tt.wantStatus {
				t.Errorf("status = %d, want %d", status, tt.wantStatus)
			}
			if tt.headerKey != "" {
				got := headers[tt.headerKey]
				if len(got) != 1 || got[0] != tt.headerVal {
					t.Errorf("headers[%q] = %v, want [%q]", tt.headerKey, got, tt.headerVal)
				}
			}
		})
	}
}

func TestParseAPIError(t *testing.T) {
	tests := []struct {
		name     string
		stderr   string
		wantOK   bool
		status   int
		code     string
		contains string
	}{
		{"404 object_not_found", stderr404, true, 404, "object_not_found", "Could not find data_source"},
		{"400 validation_error", stderr400, true, 400, "validation_error", "should be a valid uuid"},
		{"400 validation_error multi-line", stderr400MultiLine, true, 400, "validation_error", "instead was"},
		{"429 rate_limited", stderr429, true, 429, "rate_limited", "Rate limited"},
		{"succès, pas d'erreur", stderr200, false, 0, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			apiErr, ok := ParseAPIError([]byte(tt.stderr))
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if apiErr.Status != tt.status {
				t.Errorf("Status = %d, want %d", apiErr.Status, tt.status)
			}
			if apiErr.NotionCode != tt.code {
				t.Errorf("NotionCode = %q, want %q", apiErr.NotionCode, tt.code)
			}
			if !strings.Contains(apiErr.Message, tt.contains) {
				t.Errorf("Message = %q, want it to contain %q", apiErr.Message, tt.contains)
			}
		})
	}
}

func TestAPIErrorRetryable(t *testing.T) {
	tests := []struct {
		status int
		want   bool
	}{
		{400, false}, {401, false}, {403, false}, {404, false}, {409, false},
		{429, true}, {500, true}, {502, true}, {503, true}, {504, true}, {529, true},
	}
	for _, tt := range tests {
		e := &APIError{Status: tt.status}
		if got := e.Retryable(); got != tt.want {
			t.Errorf("status %d: Retryable() = %v, want %v", tt.status, got, tt.want)
		}
	}
}
