package api

import (
	"net/http"
	"strconv"
	"strings"

	"c2tool/internal/ui/sliver"
)

// ---------------------------------------------------------------------------
// Process identification (AV / EDR / software fingerprinting)
// ---------------------------------------------------------------------------

// handleAVScan enumerates the process list on the target, sends the executable
// names to the identification service and returns whatever it recognises,
// merged with the local process details so the console can display both.
//
// The lookup itself happens server-side rather than in the browser: the service
// sends no CORS headers, and routing through the API keeps the operator's
// browser from being the thing that talks to a third party.
func (s *Server) handleAVScan(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}

	var req struct {
		// Optional filter: only send processes whose name contains this string.
		Filter string `json:"filter"`
		// Overrides for the endpoint and rule set.
		URL      string `json:"url"`
		Database string `json:"database"`
	}
	if !decodeBody(w, r, &req) {
		return
	}

	procs, err := c.Ps(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	rows := make([]sliver.AVRow, 0, len(procs))
	for _, p := range procs {
		name := p.Executable
		if name == "" {
			continue
		}
		if req.Filter != "" && !containsFold(name, req.Filter) {
			continue
		}
		rows = append(rows, sliver.AVRow{Executable: name, PID: p.PID})
	}
	if len(rows) == 0 {
		writeErr(w, http.StatusBadRequest, "no processes matched the filter")
		return
	}

	client := sliver.NewAVLookupClient(req.URL, req.Database)
	result, err := client.Lookup(r.Context(), sliver.BuildTasklistText(rows))
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}

	// Merge the service's verdicts with the local process table so the UI can
	// show owner, command line and parent alongside the identification.
	byPID := make(map[int32]sliver.ProcessView, len(procs))
	byName := make(map[string][]sliver.ProcessView, len(procs))
	for _, p := range procs {
		byPID[p.PID] = p
		key := lower(p.Executable)
		byName[key] = append(byName[key], p)
	}

	type match struct {
		sliver.AVProcess
		Process *sliver.ProcessView `json:"process,omitempty"`
	}

	matches := make([]match, 0, len(result.Processes))
	for _, m := range result.Processes {
		hit := match{AVProcess: m}
		if pid, ok := parsePID(m.PID); ok {
			if p, found := byPID[pid]; found {
				cp := p
				hit.Process = &cp
			}
		}
		// The service can omit the pid; fall back to matching on name.
		if hit.Process == nil {
			if list := byName[lower(m.Key)]; len(list) > 0 {
				cp := list[0]
				hit.Process = &cp
			}
		}
		matches = append(matches, hit)
	}

	// Group by category so the UI can render sections without re-sorting.
	groups := map[string][]match{}
	for _, m := range matches {
		groups[m.Category] = append(groups[m.Category], m)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success":   true,
		"database":  result.Database,
		"timestamp": result.Timestamp,
		"stats": map[string]any{
			"sent":       len(rows),
			"total":      result.Stats.Total,
			"identified": result.Stats.Identified,
		},
		"matches":   matches,
		"groups":    groups,
		"unmatched": result.Unmatched,
	})
}

// handleAVTest verifies the identification endpoint is reachable without
// touching a session, so an operator can confirm connectivity first.
func (s *Server) handleAVTest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL      string `json:"url"`
		Database string `json:"database"`
	}
	if !decodeBody(w, r, &req) {
		return
	}

	client := sliver.NewAVLookupClient(req.URL, req.Database)
	// A fixed probe: one security product and one benign system process.
	probe := "\"映像名称\",\"PID\"\n\"HipsDaemon.exe\",\"1\"\n\"explorer.exe\",\"2\"\n"
	result, err := client.Lookup(r.Context(), probe)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success":  true,
		"database": result.Database,
		"resolved": len(result.Processes),
		"sample":   result.Processes,
	})
}

// containsFold is a case-insensitive substring test.
func containsFold(haystack, needle string) bool {
	return strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
}

// lower is a short alias so the merge loops stay readable.
func lower(s string) string { return strings.ToLower(s) }

// parsePID reads the pid the service echoes back. It can be "N/A" when the
// input rows did not carry one, in which case the caller falls back to matching
// on the executable name.
func parsePID(s string) (int32, bool) {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 32)
	if err != nil {
		return 0, false
	}
	return int32(n), true
}
