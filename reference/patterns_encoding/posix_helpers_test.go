package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func posixSelect(query string, names, types []string, rows [][]any, args ...any) fixture.Step {
	s := fixture.Select(query, names, types, rows, args...)
	s.CheckMetadata = similarReferenceMetadata
	return s
}

func posixBool(t *testing.T, p *fixture.Pair, b fixture.Backend, source, pattern string, want bool) {
	t.Helper()
	similarReferenceRoutes(t, p, b, posixSelect(`SELECT $1::text ~ $2::text AS m`, []string{"m"}, []string{"BOOL"}, [][]any{{want}}, source, pattern))
}

func posixError(query, reason string, args ...any) fixture.Step {
	return similarReferenceRegexError(query, reason, "", args...)
}
