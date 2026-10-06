//go:build reference

package reference_test

import "testing"

func TestReferenceCastExplicitIntegratedExpressions(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(id INT4,i INT4,b BOOL)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(1,2,TRUE),(2,0,FALSE),(3,NULL,NULL)", 3))
	for _, step := range []referenceStep{
		referenceSelect("SELECT i::int8>1,NOT i::bool,(i::bool AND b) OR FALSE,num_nulls(i::text,b::int4) FROM t ORDER BY id", []string{"?column?", "?column?", "?column?", "num_nulls"}, []string{"BOOL", "BOOL", "BOOL", "INT4"}, [][]any{{true, false, true, int64(0)}, {false, true, false, int64(0)}, {nil, nil, nil, int64(2)}}),
		referenceSelect("SELECT id FROM t WHERE NOT i::bool OR i::int8>1 ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}),
		referenceSelect("SELECT num_nulls(NULL::int4),num_nonnulls(int4('42'::text))", []string{"num_nulls", "num_nonnulls"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}}),
		referenceSelect("SELECT i::text IS NULL,i::bool IS TRUE,i::int8 IS DISTINCT FROM 0,0 IS NOT DISTINCT FROM i::int8 FROM t ORDER BY id", []string{"?column?", "?column?", "?column?", "?column?"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{false, true, true, false}, {false, false, false, true}, {true, false, true, false}}),
		referenceSelect("SELECT NULL::int4 IS DISTINCT FROM i::int4,i::int4 IS DISTINCT FROM NULL FROM t ORDER BY id", []string{"?column?", "?column?"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}, {true, true}, {false, false}}),
	} {
		castReferenceBoth(t, p, step)
		castReferenceBoth(t, p, referencePrepared(step))
	}
	for _, arg := range []any{"42", nil, "0"} {
		step := referenceSelect("SELECT int4($1::text) IS NULL,int4($1::text)::bool IS TRUE,int4($1::text) IS DISTINCT FROM 0 FROM t WHERE id=1", []string{"?column?", "?column?", "?column?"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{arg == nil, arg == "42", arg != "0"}}, arg)
		castReferenceBoth(t, p, step)
		castReferenceBoth(t, p, referencePrepared(step))
	}
	for _, q := range []string{"SELECT int8(TRUE) IS NULL FROM t", "SELECT int8(TRUE) IS DISTINCT FROM i FROM t"} {
		step := castReferenceError(q, referenceDiagnostic{Code: "42883", Message: "function int8(boolean) does not exist", Position: "8", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."}, true)
		castReferenceBoth(t, p, step)
		castReferenceBoth(t, p, referencePrepared(step))
	}
	p.step(t, referenceExec("CREATE TABLE e(i INT4)", 0))
	for _, q := range []string{
		"SELECT i>int4('oops'::text) FROM e",
		"SELECT TRUE AND bool(int4('oops'::text)) FROM e",
		"SELECT num_nulls(int4('oops'::text)) FROM e",
		"SELECT num_nulls(int4('oops'::text))",
		"SELECT int4('oops'::text) IS NULL FROM e",
		"SELECT bool(int4('oops'::text)) IS TRUE FROM e",
		"SELECT int4('oops'::text) IS DISTINCT FROM i FROM e",
		"SELECT i IS NOT DISTINCT FROM int4('oops'::text) FROM e",
		"SELECT int4('oops'::text) IS DISTINCT FROM NULL FROM e",
	} {
		t.Run(q, func(t *testing.T) {
			step := castReferenceError(q, referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`}, false)
			castReferenceBoth(t, p, step)
			castReferenceBoth(t, p, referencePrepared(step))
		})
	}
	for _, q := range []string{
		"SELECT int4($1::text) IS NULL FROM e",
		"SELECT bool(int4($1::text)) IS TRUE FROM e",
		"SELECT int4($1::text) IS DISTINCT FROM i FROM e",
		"SELECT i IS NOT DISTINCT FROM int4($1::text) FROM e",
	} {
		step := castReferenceError(q, referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`}, false)
		step.args = []any{"oops"}
		castReferenceBoth(t, p, step)
		castReferenceBoth(t, p, referencePrepared(step))
	}
}
