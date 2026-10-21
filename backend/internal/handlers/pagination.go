package handlers

// Bounded pagination for the list/export endpoints.
//
// Several list endpoints read an entire table (or a users × devices join)
// and materialise every row in memory before answering. A large tenant could
// therefore exhaust the process's memory with a single GET. Every such
// endpoint now takes a `limit` (and, where paging makes sense, an `offset`)
// query parameter that is CLAMPED server-side: a client can ask for fewer
// rows, never for more than the hard maximum.
//
// The bounds match the ones already used elsewhere in this codebase
// (apps.go clamps its list limit to 1000, scim.go clamps `count` to 1000).

import (
	"net/http"
	"strconv"
)

const (
	// maxListLimit is the hard maximum number of rows a list endpoint will
	// return, regardless of what the client asks for.
	maxListLimit = 1000
	// maxListOffset bounds the OFFSET so a huge offset cannot turn into an
	// unbounded scan.
	maxListOffset = 100000
)

// listPageParams parses and clamps the `limit` and `offset` query parameters.
//
// defLimit is the default number of rows (used when the client sends no
// limit, or an invalid one); it is clamped to maxListLimit as well. The
// offset is clamped to maxListOffset. Negative or malformed values fall back
// to the defaults rather than failing the request — an out-of-range page
// size is not a client error worth a 4xx.
func listPageParams(r *http.Request, defLimit int) (limit, offset int) {
	limit = defLimit
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}
	offset = 0
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			offset = n
		}
	}
	if offset > maxListOffset {
		offset = maxListOffset
	}
	return limit, offset
}
