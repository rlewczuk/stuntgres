package engine

import (
	"testing"

	"stuntgres/internal/engine/session"
)

func deleteCount(t *testing.T, s *session.Session, query string, want int64) {
	t.Helper()
	result := sessionSQL(t, s, query)
	if result.Count != want || result.Rows != nil {
		t.Fatalf("%s: count=%d rows=%v, want %d/no relation", query, result.Count, result.Rows, want)
	}
}
