package sliver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// AVLookupTimeout bounds the external identification call. The upstream service
// answers in well under a second for a few hundred rows; anything slower is a
// network problem and the operator should be told rather than left waiting.
const AVLookupTimeout = 30 * time.Second

// DefaultAVLookup is the process-identification endpoint and rule set used when
// the operator has not overridden them. The service takes a tasklist-shaped
// blob and returns the rows it recognises, tagged by category.
const (
	DefaultAVLookupURL = "https://av8.de5.net/api.php"
	DefaultAVDatabase  = "radioXiaoXiang"
)

// AVProcess is one identified process, as returned by the lookup service.
type AVProcess struct {
	PID        string `json:"pid"`
	Key        string `json:"key"`
	Value      string `json:"value"`
	Category   string `json:"category"`
	Identified bool   `json:"identified"`
}

// AVStats summarises a lookup: how many rows were sent and how many matched.
type AVStats struct {
	Total      int `json:"total"`
	Identified int `json:"identified"`
}

// AVLookupResult is the decoded response plus the rows the service could not
// place, so the caller can show both halves of the picture.
type AVLookupResult struct {
	Success     bool        `json:"success"`
	Processes   []AVProcess `json:"processes"`
	Stats       AVStats     `json:"stats"`
	Timestamp   string      `json:"timestamp"`
	Unmatched   []string    `json:"unmatched"`
	Database    string      `json:"database"`
	Description string      `json:"description"`
}

// AVLookupClient talks to the process-identification service.
type AVLookupClient struct {
	URL      string
	Database string
	HTTP     *http.Client
}

// NewAVLookupClient builds a client for the given endpoint. Empty values fall
// back to the built-in defaults.
func NewAVLookupClient(url, database string) *AVLookupClient {
	if strings.TrimSpace(url) == "" {
		url = DefaultAVLookupURL
	}
	if strings.TrimSpace(database) == "" {
		database = DefaultAVDatabase
	}
	return &AVLookupClient{
		URL:      url,
		Database: database,
		HTTP:     &http.Client{Timeout: AVLookupTimeout},
	}
}

// AVRow is one row of the tasklist-shaped input the service parses. Only the
// executable name and pid are read; the service ignores the remaining columns
// that a real tasklist dump would carry.
type AVRow struct {
	Executable string
	PID        int32
}

// htmlTagRE strips the presentation markup the service embeds in its labels.
var htmlTagRE = regexp.MustCompile(`</?font[^>]*>|</?[a-zA-Z][^>]*>`)

// stripMarkup removes the <font color=...> wrappers the service returns, so the
// console can render the text itself and colour it from Category instead.
func stripMarkup(s string) string {
	s = htmlTagRE.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}

// BuildTasklistText renders rows in the CSV shape the lookup service parses.
// It is exported for tests and for the raw-RPC console.
func BuildTasklistText(rows []AVRow) string {
	var b strings.Builder
	b.WriteString("\"映像名称\",\"PID\"\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "\"%s\",\"%d\"\n", strings.ReplaceAll(r.Executable, "\"", ""), r.PID)
	}
	return b.String()
}

// Lookup sends the supplied tasklist-shaped text and decodes the reply.
func (a *AVLookupClient) Lookup(ctx context.Context, text string) (*AVLookupResult, error) {
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("no process list to identify")
	}

	payload, err := json.Marshal(map[string]string{
		"text":     text,
		"database": a.Database,
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.URL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	// The endpoint sits behind Cloudflare, which rejects requests that do not
	// look like they came from a browser with a 403 before they reach the app.
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) "+
		"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36")

	resp, err := a.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("process identification service unreachable: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("process identification service returned HTTP %d", resp.StatusCode)
	}

	var out AVLookupResult
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("unexpected response from the identification service: %w", err)
	}
	if !out.Success {
		return nil, fmt.Errorf("identification service reported failure")
	}

	for i := range out.Processes {
		out.Processes[i].Value = stripMarkup(out.Processes[i].Value)
	}
	out.Database = a.Database
	out.Unmatched = unmatchedRows(text, out.Processes)
	return &out, nil
}

// unmatchedRows returns the executable names present in the input that the
// service did not identify, in first-seen order.
func unmatchedRows(text string, matched []AVProcess) []string {
	known := make(map[string]struct{}, len(matched))
	for _, p := range matched {
		known[strings.ToLower(p.Key)] = struct{}{}
	}

	seen := map[string]struct{}{}
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.Contains(line, "映像名称") {
			continue
		}
		name := parseTasklistName(line)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		if _, ok := known[key]; !ok {
			out = append(out, name)
		}
	}
	return out
}

// parseTasklistName pulls the executable name out of a tasklist CSV row, or
// falls back to treating a bare line as a name.
func parseTasklistName(line string) string {
	if !strings.HasPrefix(line, "\"") {
		return strings.TrimSpace(line)
	}
	end := strings.Index(line[1:], "\"")
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(line[1 : 1+end])
}
