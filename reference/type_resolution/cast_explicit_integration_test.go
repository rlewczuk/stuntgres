package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCastExplicitIntegratedExpressions(t *testing.T) {
	p := fixture.NewScenario(t)
	fixture.ScalarStep(t, p, fixture.Exec("CREATE TABLE t(id INT4,i INT4,b BOOL)", 0))
	fixture.ScalarStep(t, p, fixture.Exec("INSERT INTO t VALUES(1,2,TRUE),(2,0,FALSE),(3,NULL,NULL)", 3))
	for _, step := range []fixture.Step{
		fixture.Select("SELECT i::int8>1,NOT i::bool,(i::bool AND b) OR FALSE,num_nulls(i::text,b::int4) FROM t ORDER BY id", []string{"?column?", "?column?", "?column?", "num_nulls"}, []string{"BOOL", "BOOL", "BOOL", "INT4"}, [][]any{{true, false, true, int64(0)}, {false, true, false, int64(0)}, {nil, nil, nil, int64(2)}}),
		fixture.Select("SELECT id FROM t WHERE NOT i::bool OR i::int8>1 ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}),
		fixture.Select("SELECT num_nulls(NULL::int4),num_nonnulls(int4('42'::text))", []string{"num_nulls", "num_nonnulls"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}}),
		fixture.Select("SELECT i::text IS NULL,i::bool IS TRUE,i::int8 IS DISTINCT FROM 0,0 IS NOT DISTINCT FROM i::int8 FROM t ORDER BY id", []string{"?column?", "?column?", "?column?", "?column?"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{false, true, true, false}, {false, false, false, true}, {true, false, true, false}}),
		fixture.Select("SELECT NULL::int4 IS DISTINCT FROM i::int4,i::int4 IS DISTINCT FROM NULL FROM t ORDER BY id", []string{"?column?", "?column?"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}, {true, true}, {false, false}}),
		fixture.Select("SELECT b::BOOL AND b,i::INT<id,(i)<(id) FROM t ORDER BY id", []string{"?column?", "?column?", "?column?"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, false, false}, {false, true, true}, {nil, nil, nil}}),
		fixture.Select("SELECT id FROM t WHERE (id)=1", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}),
	} {
		fixture.ScalarStep(t, p, step)
		fixture.ScalarStep(t, p, fixture.Prepared(step))
	}
	for _, arg := range []any{"42", nil, "0"} {
		step := fixture.Select("SELECT int4($1::text) IS NULL,int4($1::text)::bool IS TRUE,int4($1::text) IS DISTINCT FROM 0 FROM t WHERE id=1", []string{"?column?", "?column?", "?column?"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{arg == nil, arg == "42", arg != "0"}}, arg)
		fixture.ScalarStep(t, p, step)
		fixture.ScalarStep(t, p, fixture.Prepared(step))
	}
	for _, q := range []string{"SELECT int8(TRUE) IS NULL FROM t", "SELECT int8(TRUE) IS DISTINCT FROM i FROM t"} {
		step := castReferenceError(q, fixture.Diagnostic{Code: "42883", Message: "function int8(boolean) does not exist", Position: "8", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."}, false)
		fixture.ScalarStep(t, p, step)
		step.ErrorStage = "prepare"
		fixture.ScalarStep(t, p, fixture.Prepared(step))
	}
	fixture.ScalarStep(t, p, fixture.Exec("CREATE TABLE e(i INT4)", 0))
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
			step := castReferenceError(q, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`}, false)
			fixture.ScalarStep(t, p, step)
			fixture.ScalarStep(t, p, fixture.Prepared(step))
		})
	}
}

func TestReferenceCastExplicitIntegratedParameterPreflight(t *testing.T) {
	p := fixture.NewScenario(t)
	fixture.ScalarStep(t, p, fixture.Exec("CREATE TABLE e(i INT4)", 0))
	for _, q := range []string{
		"SELECT int4($1::text) IS NULL FROM e",
		"SELECT bool(int4($1::text)) IS TRUE FROM e",
		"SELECT int4($1::text) IS DISTINCT FROM i FROM e",
		"SELECT i IS NOT DISTINCT FROM int4($1::text) FROM e",
	} {
		for _, backend := range p.Backends() {
			held, err := backend.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := held.Close(); err != nil {
					t.Error(err)
				}
			}()
			executor := fixture.HeldStatement{backend.DB, held, q}
			for _, arg := range []any{"oops", "42", nil, "oops"} {
				step := fixture.Select(q, []string{"?column?"}, []string{"BOOL"}, nil, arg)
				if arg == "oops" {
					step = castReferenceError(q, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`}, false, arg)
				}
				fixture.ScalarRunStep(t, p.Ctx, backend.DB, backend.Recorder, step)
				fixture.ScalarRunStep(t, p.Ctx, executor, backend.Recorder, step)
			}
			held.Close()
		}

	}
}
