package sliver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBuildTasklistTextShape(t *testing.T) {
	got := BuildTasklistText([]AVRow{
		{Executable: "svchost.exe", PID: 1504},
		{Executable: "lsass.exe", PID: 1972},
	})

	if !strings.HasPrefix(got, "\"映像名称\",\"PID\"\n") {
		t.Fatalf("missing header row, got %q", got)
	}
	if !strings.Contains(got, "\"svchost.exe\",\"1504\"\n") {
		t.Fatalf("missing svchost row, got %q", got)
	}
	if !strings.Contains(got, "\"lsass.exe\",\"1972\"\n") {
		t.Fatalf("missing lsass row, got %q", got)
	}
}

func TestStripMarkupRemovesFontTags(t *testing.T) {
	cases := map[string]string{
		`<font color=red>火绒安全软件-安全服务模块</font>`:           "火绒安全软件-安全服务模块",
		`<font color=blue>Everything文件搜索工具</font>`:       "Everything文件搜索工具",
		`<font color=green>Windows资源管理器 (LOLBAS)</font>`: "Windows资源管理器 (LOLBAS)",
		"plain text": "plain text",
	}
	for in, want := range cases {
		if got := stripMarkup(in); got != want {
			t.Errorf("stripMarkup(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUnmatchedRowsListsUnknownProcesses(t *testing.T) {
	text := BuildTasklistText([]AVRow{
		{Executable: "HipsDaemon.exe", PID: 8556},
		{Executable: "my-implant.exe", PID: 45760},
		{Executable: "explorer.exe", PID: 12448},
	})
	// The service only reports what it recognised.
	matched := []AVProcess{
		{Key: "HipsDaemon.exe"},
		{Key: "explorer.exe"},
	}

	got := unmatchedRows(text, matched)
	if len(got) != 1 || got[0] != "my-implant.exe" {
		t.Fatalf("unmatchedRows = %v, want [my-implant.exe]", got)
	}
}

func TestUnmatchedRowsDeduplicatesByName(t *testing.T) {
	text := BuildTasklistText([]AVRow{
		{Executable: "svchost.exe", PID: 1504},
		{Executable: "svchost.exe", PID: 2144},
		{Executable: "svchost.exe", PID: 2192},
	})
	if got := unmatchedRows(text, nil); len(got) != 1 {
		t.Fatalf("unmatchedRows = %v, want a single deduplicated entry", got)
	}
}

func TestParseTasklistName(t *testing.T) {
	cases := map[string]string{
		`"chrome.exe","26572","Console","1","448,752 K"`: "chrome.exe",
		`"my implant (1).exe","45760"`:                   "my implant (1).exe",
		"bare.exe":                                       "bare.exe",
		`"unterminated`:                                  "",
	}
	for in, want := range cases {
		if got := parseTasklistName(in); got != want {
			t.Errorf("parseTasklistName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLookupSendsExpectedRequestAndDecodesResponse(t *testing.T) {
	var gotText, gotDB, gotUA string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q", ct)
		}
		gotUA = r.Header.Get("User-Agent")

		var body struct {
			Text     string `json:"text"`
			Database string `json:"database"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotText, gotDB = body.Text, body.Database

		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"processes": []map[string]any{
				{
					"pid":        "8556",
					"key":        "HipsDaemon.exe",
					"value":      "<font color=red>火绒安全软件-安全服务模块</font>",
					"category":   "security",
					"identified": true,
				},
			},
			"stats":     map[string]int{"total": 2, "identified": 1},
			"timestamp": "2026-09-29 04:29:54",
		})
	}))
	defer srv.Close()

	client := NewAVLookupClient(srv.URL, "radioXiaoXiang")
	text := BuildTasklistText([]AVRow{
		{Executable: "HipsDaemon.exe", PID: 8556},
		{Executable: "my-implant.exe", PID: 45760},
	})

	res, err := client.Lookup(context.Background(), text)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}

	if gotDB != "radioXiaoXiang" {
		t.Errorf("database = %q", gotDB)
	}
	if gotText != text {
		t.Errorf("text was not forwarded verbatim")
	}
	// Cloudflare rejects requests without a browser-shaped agent.
	if !strings.Contains(gotUA, "Mozilla/5.0") {
		t.Errorf("User-Agent = %q, want a browser-shaped agent", gotUA)
	}

	if len(res.Processes) != 1 {
		t.Fatalf("got %d processes, want 1", len(res.Processes))
	}
	if res.Processes[0].Category != "security" {
		t.Errorf("category = %q", res.Processes[0].Category)
	}
	// Markup must be stripped so the console can render it itself.
	if strings.Contains(res.Processes[0].Value, "<font") {
		t.Errorf("value still contains markup: %q", res.Processes[0].Value)
	}
	if res.Processes[0].Value != "火绒安全软件-安全服务模块" {
		t.Errorf("value = %q", res.Processes[0].Value)
	}
	if len(res.Unmatched) != 1 || res.Unmatched[0] != "my-implant.exe" {
		t.Errorf("unmatched = %v", res.Unmatched)
	}
	if res.Database != "radioXiaoXiang" {
		t.Errorf("result database = %q", res.Database)
	}
}

func TestLookupRejectsEmptyAndFailedResponses(t *testing.T) {
	client := NewAVLookupClient("http://127.0.0.1:1", "")
	if _, err := client.Lookup(context.Background(), "   "); err == nil {
		t.Error("expected an error for an empty process list")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	c2 := NewAVLookupClient(srv.URL, "")
	_, err := c2.Lookup(context.Background(), BuildTasklistText([]AVRow{{Executable: "x.exe", PID: 1}}))
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Errorf("expected an HTTP 403 error, got %v", err)
	}

	// A 200 that says success=false is still a failure.
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"success": false})
	}))
	defer srv2.Close()

	c3 := NewAVLookupClient(srv2.URL, "")
	if _, err := c3.Lookup(context.Background(), BuildTasklistText([]AVRow{{Executable: "x.exe", PID: 1}})); err == nil {
		t.Error("expected an error when success is false")
	}
}

func TestNewAVLookupClientDefaults(t *testing.T) {
	c := NewAVLookupClient("", "")
	if c.URL != DefaultAVLookupURL {
		t.Errorf("URL = %q, want %q", c.URL, DefaultAVLookupURL)
	}
	if c.Database != DefaultAVDatabase {
		t.Errorf("Database = %q, want %q", c.Database, DefaultAVDatabase)
	}

	// Explicit values win.
	c2 := NewAVLookupClient("https://example.test/api.php", "other")
	if c2.URL != "https://example.test/api.php" || c2.Database != "other" {
		t.Errorf("overrides not honoured: %q %q", c2.URL, c2.Database)
	}
}
