package reference_test

import (
	"context"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceArithmeticMatrix(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(id INT,i INT,b BIGINT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(1,7,7),(2,-7,-7),(3,NULL,NULL)", 3))
	for _, tc := range []struct {
		op                 string
		positive, negative int64
	}{{"+", 9, -5}, {"-", 5, -9}, {"*", 14, -14}, {"/", 3, -3}, {"%", 1, -1}} {
		for _, expr := range []struct{ text, typ string }{{"i" + tc.op + "2", "INT4"}, {"b" + tc.op + "2", "INT8"}, {"i" + tc.op + "(b-b+2)", "INT8"}, {"b" + tc.op + "(i-i+2)", "INT8"}} {
			step := fixture.Select("SELECT "+expr.text+" FROM t ORDER BY id", []string{"?column?"}, []string{expr.typ}, [][]any{{tc.positive}, {tc.negative}, {nil}})
			p.Step(t, step)
			p.Step(t, fixture.Prepared(step))
		}
	}
	for _, tc := range []struct {
		expr, typ string
		v         any
	}{{"2+3*4", "INT4", int64(14)}, {"(2+3)*4", "INT4", int64(20)}, {"20/3*2", "INT4", int64(12)}, {"20-3-2", "INT4", int64(15)}, {"- -i", "INT4", int64(7)}, {"-2147483648", "INT4", int64(-2147483648)}, {"-9223372036854775808", "INT8", int64(-9223372036854775808)}, {"-(-2147483648)", "INT8", int64(2147483648)}, {"-(-9223372036854775808)", "NUMERIC", "9223372036854775808"}, {"9007199254740993-9007199254740992", "INT8", int64(1)}} {
		step := fixture.Select("SELECT "+tc.expr+" FROM t WHERE id=1", []string{"?column?"}, []string{tc.typ}, [][]any{{tc.v}})
		p.Step(t, step)
		p.Step(t, fixture.Prepared(step))
	}
	for _, step := range []fixture.Step{
		fixture.Select("SELECT 2147483647+2147483648 FROM t WHERE id=1", []string{"?column?"}, []string{"INT8"}, [][]any{{int64(4294967295)}}),
		fixture.Select("SELECT $1+7,$1+2147483648 FROM t WHERE id=1", []string{"?column?", "?column?"}, []string{"INT4", "INT8"}, [][]any{{nil, nil}}, nil),
		fixture.Select("SELECT $1+7,$1+2147483648 FROM t WHERE id=1", []string{"?column?", "?column?"}, []string{"INT4", "INT8"}, [][]any{{int64(9), int64(2147483650)}}, 2),
		fixture.Select("SELECT $1+2147483648,$1+7 FROM t WHERE id=1", []string{"?column?", "?column?"}, []string{"INT8", "INT8"}, [][]any{{int64(2147483649), int64(8)}}, 1),
	} {
		p.Step(t, step)
		p.Step(t, fixture.Prepared(step))
	}
	for _, step := range []fixture.Step{
		fixture.Select("SELECT +i,-i,+b,-b FROM t ORDER BY id", []string{"?column?", "?column?", "?column?", "?column?"}, []string{"INT4", "INT4", "INT8", "INT8"}, [][]any{{int64(7), int64(-7), int64(7), int64(-7)}, {int64(-7), int64(7), int64(-7), int64(7)}, {nil, nil, nil, nil}}),
		fixture.Select("SELECT i+$1,b+$1 FROM t ORDER BY id", []string{"?column?", "?column?"}, []string{"INT4", "INT8"}, [][]any{{int64(9), int64(9)}, {int64(-5), int64(-5)}, {nil, nil}}, 2),
		fixture.Select("SELECT b+$1,i+$1 FROM t ORDER BY id", []string{"?column?", "?column?"}, []string{"INT8", "INT8"}, [][]any{{int64(9), int64(9)}, {int64(-5), int64(-5)}, {nil, nil}}, 2),
		fixture.Select("SELECT $1+7,$1+2147483648 FROM t WHERE id=1", []string{"?column?", "?column?"}, []string{"INT4", "INT8"}, [][]any{{int64(8), int64(2147483649)}}, 1),
		fixture.Select("SELECT i+1,b+1 FROM t WHERE FALSE", []string{"?column?", "?column?"}, []string{"INT4", "INT8"}, nil),
		fixture.Select("SELECT id FROM t WHERE i+1=8", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}),
	} {
		p.Step(t, step)
		p.Step(t, fixture.Prepared(step))
	}
	p.Step(t, fixture.Exec("CREATE TABLE dest(i INT,b BIGINT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO dest VALUES(7/2,-7%2)", 1))
	p.Step(t, fixture.Select("SELECT i,b FROM dest", []string{"i", "b"}, []string{"INT4", "INT8"}, [][]any{{int64(3), int64(-1)}}))
}

func TestReferenceArithmeticDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(i INT,b BIGINT,s TEXT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(-2147483648,-9223372036854775808,'x')", 1))
	for _, tc := range []struct{ expr, code, primary string }{{"i/0", "22012", "division by zero"}, {"i%0", "22012", "division by zero"}, {"b/0", "22012", "division by zero"}, {"b%0", "22012", "division by zero"}, {"-i", "22003", "integer out of range"}, {"-b", "22003", "bigint out of range"}, {"i/-1", "22003", "integer out of range"}, {"b/-1", "22003", "bigint out of range"}, {"i-1", "22003", "integer out of range"}, {"b-1", "22003", "bigint out of range"}, {"i*-1", "22003", "integer out of range"}, {"b*-1", "22003", "bigint out of range"}} {
		step := fixture.ImplicitError("SELECT "+tc.expr+" FROM t", fixture.Diagnostic{Code: tc.code, Message: tc.primary})
		step.SelectRows = true
		step.ErrorStage = "execute"
		p.Step(t, step)
		p.Step(t, fixture.Prepared(step))
	}
	p.Step(t, fixture.Select("SELECT i% -1,b% -1,i/(b/b*-1) FROM t", []string{"?column?", "?column?", "?column?"}, []string{"INT4", "INT8", "INT8"}, [][]any{{int64(0), int64(0), int64(2147483648)}}))
	for _, tc := range []struct {
		q string
		d fixture.Diagnostic
	}{{"SELECT i+TRUE FROM t", fixture.Diagnostic{Code: "42883", Message: "operator does not exist: integer + boolean", Position: "9", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}}, {"SELECT -s FROM t", fixture.Diagnostic{Code: "42883", Message: "operator does not exist: - text", Position: "8", Hint: "No operator matches the given name and argument type. You might need to add an explicit type cast."}}, {"SELECT i+'oops' FROM t", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`, Position: "10"}}, {"SELECT i+'2147483648' FROM t", fixture.Diagnostic{Code: "22003", Message: `value "2147483648" is out of range for type integer`, Position: "10"}}} {
		step := fixture.ImplicitError(tc.q, tc.d)
		step.SelectRows = true
		step.ErrorStage = "execute"
		p.Step(t, step)
		step.ErrorStage = "prepare"
		p.Step(t, fixture.Prepared(step))
	}
	p.Step(t, fixture.Exec("CREATE TABLE dest(i INT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO dest VALUES(99)", 1))
	failure := fixture.ImplicitError("INSERT INTO dest VALUES(1),(7/0)", fixture.Diagnostic{Code: "22012", Message: "division by zero"})
	failure.ErrorStage = "execute"
	referenceArithmeticFailure(t, p, failure)
	p.Step(t, fixture.Select("SELECT i FROM dest", []string{"i"}, []string{"INT4"}, [][]any{{int64(99)}}))
	p.Step(t, fixture.Exec("DROP TABLE t", 0))
	p.Step(t, fixture.Exec("CREATE TABLE t(id INT,i INT,b BIGINT,s TEXT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(1,2147483647,9223372036854775807,'x'),(2,-2147483648,-9223372036854775808,'x')", 2))
	for _, tc := range []struct{ expr, where, code, primary string }{
		{"i+1", "id=1", "22003", "integer out of range"},
		{"b+1", "id=1", "22003", "bigint out of range"},
		{"i-1", "id=2", "22003", "integer out of range"},
		{"b-1", "id=2", "22003", "bigint out of range"},
		{"i*2", "id=1", "22003", "integer out of range"},
		{"b*2", "id=1", "22003", "bigint out of range"},
		{"-i", "id=2", "22003", "integer out of range"},
		{"-b", "id=2", "22003", "bigint out of range"},
		{"i/-1", "id=2", "22003", "integer out of range"},
		{"b/-1", "id=2", "22003", "bigint out of range"},
		{"i/0", "id=1", "22012", "division by zero"},
		{"b%0", "id=1", "22012", "division by zero"},
	} {
		step := fixture.ImplicitError("SELECT "+tc.expr+" FROM t WHERE "+tc.where, fixture.Diagnostic{Code: tc.code, Message: tc.primary})
		step.SelectRows, step.ErrorStage = true, "execute"
		p.Step(t, step)
		p.Step(t, fixture.Prepared(step))
	}
	remainder := fixture.Select("SELECT i% -1,b% -1 FROM t WHERE id=2", []string{"?column?", "?column?"}, []string{"INT4", "INT8"}, [][]any{{int64(0), int64(0)}})
	p.Step(t, remainder)
	p.Step(t, fixture.Prepared(remainder))
	p.Step(t, fixture.Exec("DROP TABLE dest", 0))
	p.Step(t, fixture.Exec("CREATE TABLE dest(i INT,b BIGINT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO dest VALUES(99,99)", 1))
	for _, tc := range []struct{ query, code, message string }{
		{"INSERT INTO dest VALUES(1,1),(2,7/0)", "22012", "division by zero"},
		{"INSERT INTO dest VALUES(1,1),(2,2147483647+1)", "22003", "integer out of range"},
	} {
		failure := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: tc.code, Message: tc.message})
		failure.ErrorStage = "execute"
		referenceArithmeticFailure(t, p, failure)
		remaining := fixture.Select("SELECT i,b FROM dest", []string{"i", "b"}, []string{"INT4", "INT8"}, [][]any{{int64(99), int64(99)}})
		p.Step(t, remaining)
		p.Step(t, fixture.Prepared(remaining))
	}
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	for _, backend := range p.Backends() {
		result, err := backend.DB.ExecContext(ctx, "INSERT INTO dest VALUES(1+2,3)")
		if result != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled arithmetic insert result=%#v error=%v", result, err)
		}
	}
}
