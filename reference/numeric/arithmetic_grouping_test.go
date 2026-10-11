package reference_test

import (
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceArithmeticRebasedBooleanPredicateGrouping(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(id INT,i INT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(1,7),(2,0),(3,NULL)", 3))
	for _, step := range []fixture.Step{
		fixture.Select("SELECT ((i+1)*2),((i+1)=8 AND NOT (i=0)),(i+1) IS NULL FROM t ORDER BY id", []string{"?column?", "?column?", "?column?"}, []string{"INT4", "BOOL", "BOOL"}, [][]any{{int64(16), true, false}, {int64(2), false, false}, {nil, nil, true}}),
		fixture.Select("SELECT (i+1)*2 FROM t WHERE ((i+1)=8 OR (i=0)) AND NOT ((i+1) IS NULL) ORDER BY id", []string{"?column?"}, []string{"INT4"}, [][]any{{int64(16)}, {int64(2)}}),
		fixture.Select("SELECT id FROM t WHERE (id)=1 ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}),
	} {
		p.Step(t, step)
		p.Step(t, fixture.Prepared(step))
	}
}

func TestReferenceArithmeticSelectedFunctionComposition(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(id INT,i INT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(1,7),(2,NULL)", 2))
	for _, step := range []fixture.Step{
		fixture.Select("SELECT num_nulls(+1,NULL)", []string{"num_nulls"}, []string{"INT4"}, [][]any{{int64(1)}}),
		fixture.Select("SELECT num_nonnulls(+1,NULL)", []string{"num_nonnulls"}, []string{"INT4"}, [][]any{{int64(1)}}),
		fixture.Select("SELECT num_nulls(+1,NULL),num_nonnulls(+1,NULL)", []string{"num_nulls", "num_nonnulls"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}}),
		fixture.Select("SELECT num_nulls(i),i+1 FROM t ORDER BY id", []string{"num_nulls", "?column?"}, []string{"INT4", "INT4"}, [][]any{{int64(0), int64(8)}, {int64(1), nil}}),
	} {
		p.Step(t, step)
		p.Step(t, fixture.Prepared(step))
	}
	// These are deliberately different capabilities, not diagnostic parity.
	// Each public backend must independently satisfy its complete expectation.
	for _, tc := range []struct {
		query, column, message, position string
		rows                             [][]any
	}{
		{"SELECT num_nulls(1+2)", "num_nulls", "arithmetic in null-count arguments is unsupported", "19", [][]any{{int64(0)}}},
		{"SELECT num_nonnulls(1+2)", "num_nonnulls", "arithmetic in null-count arguments is unsupported", "22", [][]any{{int64(1)}}},
		{"SELECT num_nulls(i+1) FROM t ORDER BY id", "num_nulls", "arithmetic in null-count arguments is unsupported", "19", [][]any{{int64(0)}, {int64(1)}}},
		{"SELECT num_nonnulls(i+1) FROM t ORDER BY id", "num_nonnulls", "arithmetic in null-count arguments is unsupported", "22", [][]any{{int64(1)}, {int64(0)}}},
	} {
		success := fixture.Select(tc.query, []string{tc.column}, []string{"INT4"}, tc.rows)
		if p.Postgres != nil {
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, success)
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Prepared(success))
		}
		failure := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: "0A000", Message: tc.message, Position: tc.position})
		failure.SelectRows = true
		failure.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, failure)
		failure.ErrorStage = "prepare"
		fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Prepared(failure))
	}
}

func TestReferenceArithmeticSelectedFunctionArgumentBoundary(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(i INT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(7)", 1))
	for _, tc := range []struct {
		query, operator string
		args            []any
	}{
		{"SELECT num_nulls(1+2)", "+", nil},
		{"SELECT num_nulls(1+2) FROM t", "+", nil},
		{"SELECT num_nulls(num_nonnulls(i+1)) FROM t", "+", nil},
		{"SELECT num_nulls(-i) FROM t", "-", nil},
		{"SELECT num_nulls(+$1)", "+", []any{7}},
		{"SELECT num_nulls(+$1) FROM t", "+", []any{7}},
		{"SELECT num_nulls(num_nonnulls(1+2))", "+", nil},
		{"SELECT num_nulls(+ +1)", "+", nil},
	} {
		want := fixture.Diagnostic{Code: "0A000", Message: "arithmetic in null-count arguments is unsupported", Position: strconv.Itoa(strings.Index(tc.query, tc.operator) + 1)}
		failure := fixture.ImplicitError(tc.query, want, tc.args...)
		failure.SelectRows, failure.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, failure)
		failure.ErrorStage = "prepare"
		fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Prepared(failure))
		if p.Postgres != nil {
			success := fixture.Select(tc.query, []string{"num_nulls"}, []string{"INT4"}, [][]any{{int64(0)}}, tc.args...)
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, success)
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Prepared(success))
		}
	}
	for _, step := range []fixture.Step{
		fixture.Select("SELECT num_nulls(+1,-2,+1.5,+0x8000000000000000,NULL)", []string{"num_nulls"}, []string{"INT4"}, [][]any{{int64(1)}}),
		fixture.Select("SELECT num_nulls(num_nonnulls(i)) FROM t", []string{"num_nulls"}, []string{"INT4"}, [][]any{{int64(0)}}),
		fixture.Select("SELECT i=$1,num_nulls($1) FROM t", []string{"?column?", "num_nulls"}, []string{"BOOL", "INT4"}, [][]any{{false, int64(0)}}, 1),
	} {
		p.Step(t, step)
		p.Step(t, fixture.Prepared(step))
	}
}
