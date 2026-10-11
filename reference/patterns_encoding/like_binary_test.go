package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceLikeBinary(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(b BYTEA)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES('\\x00ff'),(NULL)", 2))
	for _, expr := range []string{"b LIKE '\\x5f5f'", "b ~~ '\\x5f5f'", "b NOT LIKE '\\x5f'", "b !~~ '\\x5f'", "b LIKE '\\x0025'::bytea ESCAPE '\\x'::bytea"} {
		s := fixture.Select("SELECT "+expr+" AS matched FROM t", []string{"matched"}, []string{"BOOL"}, [][]any{{true}, {nil}})
		s.CheckMetadata = likeReferenceMetadata
		p.Step(t, s)
		p.Step(t, fixture.Prepared(s))
	}
	for _, tc := range []struct{ op, symbol string }{{"ILIKE", "~~*"}, {"NOT ILIKE", "!~~*"}, {"~~*", "~~*"}, {"!~~*", "!~~*"}} {
		q := "SELECT b " + tc.op + " b FROM t"
		s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42883", Message: "operator does not exist: bytea " + tc.symbol + " bytea", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts.", Position: "10"})
		s.SelectRows = true
		s.ErrorStage = "execute"
		s.CheckMetadata = likeReferenceMetadata
		p.Step(t, s)
		s.ErrorStage = "prepare"
		p.Step(t, fixture.Prepared(s))
	}
	s := fixture.ImplicitError("SELECT b LIKE '\\x25' ESCAPE '\\xc3a9'::bytea FROM t", fixture.Diagnostic{Code: "22025", Message: "invalid escape string", Hint: "Escape string must be empty or one character."})
	s.SelectRows = true
	s.ErrorStage = "execute"
	s.CheckMetadata = likeReferenceMetadata
	p.Step(t, s)
	p.Step(t, fixture.Prepared(s))
}
