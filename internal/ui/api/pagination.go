package api

import (
	"net/http"
	"strconv"
)

// paginate slices a server-side collection according to the optional limit and
// offset query parameters.
//
// Both parameters are optional and absent means "everything", which is what
// these endpoints have always returned. That default is deliberate: the console
// frontend keeps working unchanged, and paging becomes something a client opts
// into rather than a breaking change to every list response.
//
// It exists because some of these collections grow with the campaign rather than
// with the request. The credential vault, the event log and the host list all
// accumulate for as long as the server runs, so a console left up for months
// would otherwise serialise its entire history to render the newest page. A
// limit lets a client bound that without the server having to guess a window.
//
// A malformed or negative value is a 400 rather than a silent fallback. A typo
// that quietly returned a different window would be worse than an error: the
// client would page over a moving target and never notice the gap.
//
// The returned bool reports whether the caller should continue; false means a
// response has already been written.
func paginate[T any](w http.ResponseWriter, r *http.Request, items []T) ([]T, bool) {
	q := r.URL.Query()

	offset := 0
	if raw := q.Get("offset"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			writeErr(w, http.StatusBadRequest, "offset must be a non-negative integer")
			return nil, false
		}
		offset = n
	}

	// An offset at or past the end is an empty page, not an error: it is what a
	// client asking for "the next page" gets when it has already read the last
	// one. Returning a non-nil empty slice keeps the JSON as [] rather than
	// null, so a client can iterate the response without a nil check.
	if offset >= len(items) {
		return []T{}, true
	}
	items = items[offset:]

	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			writeErr(w, http.StatusBadRequest, "limit must be a non-negative integer")
			return nil, false
		}
		if n < len(items) {
			items = items[:n]
		}
	}
	return items, true
}
