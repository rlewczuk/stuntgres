//go:build reference

package reference_test

import "testing"

func TestReferenceBinarySliceSemanticSpelling(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(v BYTEA,s BYTEA,i INTEGER,n INTEGER)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES('abcdef','cd',3,2),('abcdef','cd',3,2)", 2))
	for _, step := range []referenceStep{
		referenceSelect("SELECT overlay(v PLACING s FROM i FOR n) AS o,count(*) AS c FROM t GROUP BY overlay(v,s,i,n) HAVING overlay(v PLACING s FROM i FOR n)='abcdef'::bytea", []string{"o", "c"}, []string{"BYTEA", "INT8"}, [][]any{{"abcdef", int64(2)}}),
		referenceSelect("SELECT position(s IN v) AS p,count(*) AS c FROM t GROUP BY pg_catalog.position(v,s) HAVING position(s IN v)=3", []string{"p", "c"}, []string{"INT4", "INT8"}, [][]any{{int64(3), int64(2)}}),
		referenceSelect("SELECT overlay(v PLACING s FROM i FOR n) AS same,overlay(v,s,i,n) AS same FROM t ORDER BY same", []string{"same", "same"}, []string{"BYTEA", "BYTEA"}, [][]any{{"abcdef", "abcdef"}, {"abcdef", "abcdef"}}),
		referenceSelect("SELECT position(s IN v) AS same,pg_catalog.position(v,s) AS same FROM t ORDER BY same", []string{"same", "same"}, []string{"INT4", "INT4"}, [][]any{{int64(3), int64(3)}, {int64(3), int64(3)}}),
	} {
		p.step(t, step)
		p.step(t, referencePrepared(step))
	}
}

func TestReferenceBinarySliceUnaryMissingSignatures(t *testing.T) {
	p := newReferencePair(t)
	hint := "No function matches the given name and argument types. You might need to add explicit type casts."
	for _, tc := range []struct{ query, name, typ string }{
		{"SELECT substr('abc'::text)", "substr", "text"},
		{"SELECT substr('abc')", "substr", "unknown"},
		{"SELECT substring('abc'::text)", "substring", "text"},
		{"SELECT substring('abc')", "substring", "unknown"},
		{"SELECT pg_catalog.position('abc'::text)", "pg_catalog.position", "text"},
		{"SELECT pg_catalog.position('abc')", "pg_catalog.position", "unknown"},
		{`SELECT "position"('abc'::text)`, "position", "text"},
		{`SELECT "position"('abc')`, "position", "unknown"},
	} {
		step := referenceImplicitError(tc.query, referenceDiagnostic{Code: "42883", Message: "function " + tc.name + "(" + tc.typ + ") does not exist", Position: "8", Hint: hint})
		step.selectRows = true
		step.errorStage = "execute"
		p.step(t, step)
		step.errorStage = "prepare"
		p.step(t, referencePrepared(step))
	}
}
