package api

import "net/http"

// writeResult answers with the outcome of one client call.
//
// Handlers that read a value and reply with it were each spelling out the same
// five lines: call, check the error, answer with the status the error deserves,
// then write the value as 200 JSON. That shape now lives in one place, so the
// error policy has a single home and the handler reads as the call plus its
// response.
//
// Handlers that inspect the value before replying -- a nil result that is a
// 404, a different success status, a body assembled from several calls -- keep
// their own body. This is only the straight-through case.
func writeResult[T any](w http.ResponseWriter, v T, err error) {
	if err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}
