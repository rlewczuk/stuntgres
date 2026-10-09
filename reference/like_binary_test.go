package reference_test

import "testing"

func TestReferenceLikeBinary(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE t(b BYTEA)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES('\\x00ff'),(NULL)", 2))
	for _, expr := range []string{"b LIKE '\\x5f5f'", "b ~~ '\\x5f5f'", "b NOT LIKE '\\x5f'", "b !~~ '\\x5f'", "b LIKE '\\x0025'::bytea ESCAPE '\\x'::bytea"} {
		s := referenceSelect("SELECT "+expr+" AS matched FROM t", []string{"matched"}, []string{"BOOL"}, [][]any{{true}, {nil}})
		s.checkMetadata = likeReferenceMetadata
		p.step(t, s)
		p.step(t, referencePrepared(s))
	}
	for _, tc := range []struct{ op, symbol string }{{"ILIKE", "~~*"}, {"NOT ILIKE", "!~~*"}, {"~~*", "~~*"}, {"!~~*", "!~~*"}} {
		q := "SELECT b " + tc.op + " b FROM t"
		s := referenceImplicitError(q, referenceDiagnostic{Code: "42883", Message: "operator does not exist: bytea " + tc.symbol + " bytea", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts.", Position: "10"})
		s.selectRows = true
		s.errorStage = "execute"
		s.checkMetadata = likeReferenceMetadata
		p.step(t, s)
		s.errorStage = "prepare"
		p.step(t, referencePrepared(s))
	}
	s := referenceImplicitError("SELECT b LIKE '\\x25' ESCAPE '\\xc3a9'::bytea FROM t", referenceDiagnostic{Code: "22025", Message: "invalid escape string", Hint: "Escape string must be empty or one character."})
	s.selectRows = true
	s.errorStage = "execute"
	s.checkMetadata = likeReferenceMetadata
	p.step(t, s)
	p.step(t, referencePrepared(s))
}
