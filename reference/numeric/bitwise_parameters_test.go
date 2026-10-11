package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBitwiseParameterResolution(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(s SMALLINT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(1)", 1))
	for _, q := range []string{"SELECT s & $1, CASE WHEN $1 THEN 1 ELSE 0 END FROM t"} {
		s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42804", Message: "argument of CASE/WHEN must be type boolean, not type smallint", Position: "26"}, 1)
		s.SelectRows = true
		s.ErrorStage = "execute"
		p.Step(t, s)
		s.ErrorStage = "prepare"
		p.Step(t, fixture.Prepared(s))
	}
	for _, tc := range []struct {
		q, message, pos string
		args            []any
	}{
		{"SELECT ~$1 FROM t", "operator is not unique: ~ unknown", "8", []any{1}},
		{"SELECT $1 & $2 FROM t", "operator is not unique: unknown & unknown", "11", []any{1, 1}},
		{"SELECT $1 << 1::smallint FROM t", "operator is not unique: unknown << smallint", "11", []any{1}},
		{"SELECT $1 >> 1::smallint FROM t", "operator is not unique: unknown >> smallint", "11", []any{1}},
	} {
		s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "42725", Message: tc.message, Position: tc.pos, Hint: "Could not choose a best candidate operator. You might need to add explicit type casts."}, tc.args...)
		s.SelectRows = true
		s.ErrorStage = "execute"
		p.Step(t, s)
		s.ErrorStage = "prepare"
		p.Step(t, fixture.Prepared(s))
	}
	for _, q := range []string{"SELECT s << 1::smallint FROM t", "SELECT s >> 1::smallint FROM t"} {
		want := int64(2)
		if q == "SELECT s >> 1::smallint FROM t" {
			want = 0
		}
		step := fixture.Select(q, []string{"?column?"}, []string{"INT2"}, [][]any{{want}})
		p.Step(t, step)
		p.Step(t, fixture.Prepared(step))
	}
	step := fixture.Select("SELECT s << 1::smallint,s >> 1::smallint FROM t", []string{"?column?", "?column?"}, []string{"INT2", "INT2"}, [][]any{{int64(2), int64(0)}})
	p.Step(t, step)
	p.Step(t, fixture.Prepared(step))
	for _, tc := range []struct{ query, message, position string }{
		{"SELECT num_nulls(s & 1) FROM t", "arithmetic in null-count arguments is unsupported", "20"},
		{"SELECT num_nulls(~1)", "arithmetic in null-count arguments is unsupported", "18"},
	} {
		failure := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: "0A000", Message: tc.message, Position: tc.position})
		failure.SelectRows, failure.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, failure)
		if p.Postgres != nil {
			success := fixture.Select(tc.query, []string{"num_nulls"}, []string{"INT4"}, [][]any{{int64(0)}})
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, success)
		}
	}
}

func TestReferenceBitwiseSmallintCasts(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE one(v INTEGER,s SMALLINT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO one VALUES(1,1)", 1))
	query := "SELECT CAST('1'::text AS smallint) & 1 FROM one"
	failure := fixture.ImplicitError(query, fixture.Diagnostic{Code: "0A000", Message: "explicit casts are supported only for int4, int8, text and bool", Position: "8"})
	failure.SelectRows, failure.ErrorStage = true, "execute"
	fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, failure)
	failure.ErrorStage = "prepare"
	fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Prepared(failure))
	if p.Postgres != nil {
		success := fixture.Select(query, []string{"?column?"}, []string{"INT4"}, [][]any{{int64(1)}})
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, success)
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Prepared(success))
	}
	for _, prepared := range []bool{false, true} {
		step := fixture.Select("SELECT ~CAST(1 AS smallint),CAST(32767 AS smallint) << CAST(1 AS smallint),CAST(32767::smallint << 1 AS bigint),~NULL::smallint FROM one", []string{"?column?", "?column?", "int8", "?column?"}, []string{"INT2", "INT2", "INT8", "INT2"}, [][]any{{int64(-2), int64(-2), int64(-2), nil}})
		step.Prepared = prepared
		p.Step(t, step)
	}
	p.Step(t, fixture.Exec("CREATE TABLE scoped(v BIGINT DEFAULT CAST(32767::smallint << 1 AS bigint))", 0))
	p.Step(t, fixture.Exec("INSERT INTO scoped DEFAULT VALUES", 1))
	step := fixture.Select("SELECT v FROM scoped", []string{"v"}, []string{"INT8"}, [][]any{{int64(-2)}})
	p.Step(t, step)
	p.Step(t, fixture.Prepared(step))
}
