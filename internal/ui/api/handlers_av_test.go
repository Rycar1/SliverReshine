package api

import (
	"testing"

	"sliverreshine/internal/ui/sliver"
)

// buildAVRows is the filter the AV scan applies before anything leaves the
// console. It must drop nameless processes and honour the optional filter.
func TestBuildAVRows(t *testing.T) {
	procs := []sliver.ProcessView{
		{PID: 1, Executable: "explorer.exe"},
		{PID: 2, Executable: ""},
		{PID: 3, Executable: "chrome.exe"},
		{PID: 4, Executable: "HipsDaemon.exe"},
	}

	all := buildAVRows(procs, "")
	if len(all) != 3 {
		t.Fatalf("unfiltered: got %d rows, want 3", len(all))
	}
	if all[0].Executable != "explorer.exe" || all[0].PID != 1 {
		t.Errorf("first row = %+v", all[0])
	}
	for _, r := range all {
		if r.Executable == "" {
			t.Error("a nameless process leaked into the rows")
		}
	}

	filtered := buildAVRows(procs, "CHROME")
	if len(filtered) != 1 || filtered[0].Executable != "chrome.exe" {
		t.Fatalf("filtered: got %+v, want just chrome.exe", filtered)
	}

	if none := buildAVRows(procs, "nothing-matches"); len(none) != 0 {
		t.Errorf("no-match filter returned %d rows", len(none))
	}
	if empty := buildAVRows(nil, ""); len(empty) != 0 {
		t.Errorf("nil input returned %d rows", len(empty))
	}
}
