package reference_test

import (
	"context"
	"errors"
	"math"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMathScalarValues(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, tc := range []struct {
		expr, name, typ string
		want            any
	}{
		{"abs(-17.400)", "abs", "NUMERIC", "17.400"}, {"abs(-17)", "abs", "INT4", int64(17)},
		{"ceil(42.8)", "ceil", "NUMERIC", "43"}, {"ceil(-42.8)", "ceil", "NUMERIC", "-42"}, {"ceiling(-42.8)", "ceiling", "NUMERIC", "-42"}, {"floor(-42.8)", "floor", "NUMERIC", "-43"},
		{"ceil(-0.0001)", "ceil", "NUMERIC", "0"}, {"ceiling(0.0001)", "ceiling", "NUMERIC", "1"}, {"floor(-0.0001)", "floor", "NUMERIC", "-1"}, {"floor(42.000)", "floor", "NUMERIC", "42"},
		{"div(-9.00,4.000)", "div", "NUMERIC", "-2"}, {"div(9.00,-4.000)", "div", "NUMERIC", "-2"}, {"div(-9.00,-4.000)", "div", "NUMERIC", "2"}, {"div(1.00,2.0)", "div", "NUMERIC", "0"},
		{"div(9007199254740993,2)", "div", "NUMERIC", "4503599627370496"}, {"div(12345678901234567890,123)", "div", "NUMERIC", "100371373180768844"},
		{"div('NaN',0.0)", "div", "NUMERIC", "NaN"}, {"div('Infinity',-2.0)", "div", "NUMERIC", "-Infinity"}, {"div(1.0,'Infinity')", "div", "NUMERIC", "0"}, {"div('Infinity','Infinity')", "div", "NUMERIC", "NaN"},
		{"exp(0.0)", "exp", "NUMERIC", "1.0000000000000000"}, {"exp(1.0)", "exp", "NUMERIC", "2.7182818284590452"}, {"exp(-1.0)", "exp", "NUMERIC", "0.3678794411714423"},
		{"exp(1." + strings.Repeat("0", 70) + ")", "exp", "NUMERIC", "2.7182818284590452353602874713526624977572470936999595749669676277240766"},
		{"exp(0." + strings.Repeat("0", 1001) + ")", "exp", "NUMERIC", "1." + strings.Repeat("0", 1000)},
		{"exp(-6000.0)", "exp", "NUMERIC", "0." + strings.Repeat("0", 1000)},
		{"factorial(0)", "factorial", "NUMERIC", "1"}, {"factorial(1)", "factorial", "NUMERIC", "1"}, {"factorial(5)", "factorial", "NUMERIC", "120"}, {"factorial(20)", "factorial", "NUMERIC", "2432902008176640000"},
		{"abs(NULL)", "abs", "FLOAT8", nil}, {"ceil(NULL)", "ceil", "FLOAT8", nil}, {"ceiling(NULL)", "ceiling", "FLOAT8", nil}, {"floor(NULL)", "floor", "FLOAT8", nil}, {"exp(NULL)", "exp", "FLOAT8", nil}, {"div(NULL,1)", "div", "NUMERIC", nil}, {"div(1,NULL)", "div", "NUMERIC", nil}, {"factorial(NULL)", "factorial", "NUMERIC", nil},
		{"pg_catalog.abs(-17)", "abs", "INT4", int64(17)},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			s := fixture.Select("SELECT "+tc.expr, []string{tc.name}, []string{tc.typ}, [][]any{{tc.want}})
			p.Step(t, s)
			p.Step(t, fixture.Prepared(s))
		})
	}
}

func TestReferenceMathScalarWidthsAndParameters(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE m(s SMALLINT,i INTEGER,b BIGINT,r REAL,d DOUBLE PRECISION,n NUMERIC(8,3))", 0))
	p.Step(t, fixture.Exec("INSERT INTO m VALUES(-17,-17,-17,-17.5,-17.5,-17.400)", 1))
	s := fixture.Select("SELECT abs(s),abs(i),abs(b),abs(r),abs(d),abs(n),ceil(s),ceiling(r),floor(n) FROM m", []string{"abs", "abs", "abs", "abs", "abs", "abs", "ceil", "ceiling", "floor"}, []string{"INT2", "INT4", "INT8", "FLOAT4", "FLOAT8", "NUMERIC", "FLOAT8", "FLOAT8", "NUMERIC"}, [][]any{{int64(17), int64(17), int64(17), float64(17.5), float64(17.5), "17.400", float64(-17), float64(-17), "-18"}})
	p.Step(t, s)
	p.Step(t, fixture.Prepared(s))
	for _, tc := range []struct{ a, b, want any }{{"9007199254740993", "2", "4503599627370496"}, {nil, "2", nil}, {"-9.00", "4.000", "-2"}} {
		s := fixture.Prepared(fixture.Select("SELECT div($1,$2)", []string{"div"}, []string{"NUMERIC"}, [][]any{{tc.want}}, tc.a, tc.b))
		p.Step(t, s)
	}
}

func TestReferenceMathScalarDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	hint := "No function matches the given name and argument types. You might need to add explicit type casts."
	for _, name := range []string{"abs", "ceil", "ceiling", "floor", "exp", "div", "factorial"} {
		s := fixture.ImplicitError("SELECT "+name+"()", fixture.Diagnostic{Code: "42883", Message: "function " + name + "() does not exist", Position: "8", Hint: hint})
		s.SelectRows = true
		s.ErrorStage = "execute"
		p.Step(t, s)
		s.ErrorStage = "prepare"
		p.Step(t, fixture.Prepared(s))
	}
	for _, tc := range []struct{ q, code, message, pos string }{
		{"SELECT abs('oops')", "22P02", `invalid input syntax for type double precision: "oops"`, "12"}, {"SELECT ceil('oops')", "22P02", `invalid input syntax for type double precision: "oops"`, "13"},
		{"SELECT exp('oops')", "22P02", `invalid input syntax for type double precision: "oops"`, "12"}, {"SELECT div('oops',1)", "22P02", `invalid input syntax for type numeric: "oops"`, "12"}, {"SELECT factorial('oops')", "22P02", `invalid input syntax for type bigint: "oops"`, "18"},
	} {
		s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: tc.pos})
		s.SelectRows = true
		s.ErrorStage = "execute"
		p.Step(t, s)
		s.ErrorStage = "prepare"
		p.Step(t, fixture.Prepared(s))
	}
	for _, tc := range []struct{ q, code, message, pos, hint string }{
		{"SELECT factorial(5.0)", "42883", "function factorial(numeric) does not exist", "8", hint},
		{"/*é*/ SELECT factorial(5.0)", "42883", "function factorial(numeric) does not exist", "14", hint},
		{"SELECT abs(" + strings.Repeat("1,", 100) + "1)", "54023", "cannot pass more than 100 arguments to a function", "8", ""},
	} {
		s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: tc.pos, Hint: tc.hint})
		s.SelectRows, s.ErrorStage = true, "execute"
		p.Step(t, s)
		s.ErrorStage = "prepare"
		p.Step(t, fixture.Prepared(s))
	}
	for _, tc := range []struct{ q, code, message string }{
		{"SELECT div(1,0)", "22012", "division by zero"}, {"SELECT div(NULL,div(1,0))", "22012", "division by zero"}, {"SELECT factorial(-1)", "22003", "factorial of a negative number is undefined"}, {"SELECT factorial(32178)", "22003", "value overflows numeric format"}, {"SELECT exp(6000.0)", "22003", "value overflows numeric format"}, {"SELECT abs(-2147483648)", "22003", "integer out of range"},
		{"SELECT div(1e131071,0.1)", "22003", "value overflows numeric format"},
	} {
		s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message})
		s.ErrorStage = "execute"
		p.Step(t, s)
		p.Step(t, fixture.Prepared(s))
	}
	p.Step(t, fixture.Exec("CREATE TABLE minima(s SMALLINT,b BIGINT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO minima VALUES(-32768,-9223372036854775808)", 1))
	for _, tc := range []struct{ col, message string }{{"s", "smallint out of range"}, {"b", "bigint out of range"}} {
		s := fixture.ImplicitError("SELECT abs("+tc.col+") FROM minima", fixture.Diagnostic{Code: "22003", Message: tc.message})
		s.SelectRows = true
		s.ErrorStage = "execute"
		p.Step(t, s)
		p.Step(t, fixture.Prepared(s))
	}
}

func TestReferenceMathScalarConsumers(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE m(id INT,n NUMERIC,d NUMERIC,i BIGINT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO m VALUES(1,-9.00,4,5),(2,9.00,0,-1),(3,NULL,4,NULL)", 3))
	p.Step(t, fixture.Exec("CREATE TABLE copied(n NUMERIC)", 0))
	for _, step := range []fixture.Step{
		fixture.Select("SELECT abs(floor(n)) FROM m ORDER BY id", []string{"abs"}, []string{"NUMERIC"}, [][]any{{"9"}, {"9"}, {nil}}),
		fixture.Select("SELECT factorial(count(*)) FROM m", []string{"factorial"}, []string{"NUMERIC"}, [][]any{{"6"}}),
	} {
		p.Step(t, step)
		p.Step(t, fixture.Prepared(step))
	}
	for _, q := range []string{"UPDATE m SET n=div(n,d)", "INSERT INTO copied SELECT div(n,d) FROM m"} {
		s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22012", Message: "division by zero"})
		s.ErrorStage = "execute"
		p.Step(t, s)
		p.Step(t, fixture.Select("SELECT n FROM m ORDER BY id", []string{"n"}, []string{"NUMERIC"}, [][]any{{"-9.00"}, {"9.00"}, {nil}}))
		p.Step(t, fixture.Select("SELECT n FROM copied", []string{"n"}, []string{"NUMERIC"}, [][]any{}))
	}
	failed := fixture.ImplicitError("UPDATE m SET n=factorial(i)", fixture.Diagnostic{Code: "22003", Message: "factorial of a negative number is undefined"})
	failed.ErrorStage = "execute"
	p.Step(t, failed)
	for _, step := range []fixture.Step{
		fixture.Select("SELECT n FROM m ORDER BY id", []string{"n"}, []string{"NUMERIC"}, [][]any{{"-9.00"}, {"9.00"}, {nil}}),
		fixture.Select("SELECT n FROM copied", []string{"n"}, []string{"NUMERIC"}, nil),
	} {
		p.Step(t, step)
		p.Step(t, fixture.Prepared(step))
	}
	p.Step(t, fixture.Select("SELECT abs(n),count(*) FROM m GROUP BY 1 ORDER BY 1", []string{"abs", "count"}, []string{"NUMERIC", "INT8"}, [][]any{{"9.00", int64(2)}, {nil, int64(1)}}))
	p.Step(t, fixture.Exec("UPDATE m SET n=abs(n)", 3))
	p.Step(t, fixture.Exec("INSERT INTO copied SELECT div(n,4) FROM m", 3))
	p.Step(t, fixture.Exec("CREATE VIEW scalar_view AS SELECT ceil(n) AS c,ceiling(n) AS cc,floor(n) AS f,exp(0.0) AS e,factorial(5) AS fac FROM m", 0))
	s := fixture.Select("SELECT c,cc,f,e,fac FROM scalar_view", []string{"c", "cc", "f", "e", "fac"}, []string{"NUMERIC", "NUMERIC", "NUMERIC", "NUMERIC", "NUMERIC"}, [][]any{{"9", "9", "9", "1.0000000000000000", "120"}, {"9", "9", "9", "1.0000000000000000", "120"}, {nil, nil, nil, "1.0000000000000000", "120"}})
	s.Bag = true
	p.Step(t, s)
	p.Step(t, fixture.Prepared(s))
	p.Step(t, fixture.Exec("CREATE VIEW scalar_full_view AS SELECT id,ceil(n) AS c,ceiling(n) AS cc,floor(n) AS f,div(n,4) AS q,exp(0.0) AS e,factorial(5) AS fac FROM m", 0))
	held, err := p.Memory.QueryContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, p.MemoryRecord), "SELECT n FROM copied")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if p.Postgres != nil {
		for _, name := range []string{"scalar_view", "scalar_full_view"} {
			if _, err := p.Postgres.ExecContext(p.Ctx, "DROP VIEW "+name); err != nil {
				t.Fatal(err)
			}
		}
	}
	fixture.ArithmeticRoundtrip(t, p, "copied", "m")
	full := fixture.Select("SELECT c,cc,f,q,e,fac FROM scalar_full_view ORDER BY id", []string{"c", "cc", "f", "q", "e", "fac"}, []string{"NUMERIC", "NUMERIC", "NUMERIC", "NUMERIC", "NUMERIC", "NUMERIC"}, [][]any{{"9", "9", "9", "2", "1.0000000000000000", "120"}, {"9", "9", "9", "2", "1.0000000000000000", "120"}, {nil, nil, nil, nil, "1.0000000000000000", "120"}})
	p.Step(t, full)
	p.Step(t, fixture.Prepared(full))
	if !held.Next() {
		t.Fatal(held.Err())
	}
	var old string
	if err := held.Scan(&old); err != nil || old != "2" {
		t.Fatal(old, err)
	}
	if !held.Next() {
		t.Fatal(held.Err())
	}
	if err := held.Scan(&old); err != nil || old != "2" {
		t.Fatal(old, err)
	}
	if !held.Next() {
		t.Fatal(held.Err())
	}
	var null any
	if err := held.Scan(&null); err != nil || null != nil {
		t.Fatal(null, err)
	}
	if held.Next() || held.Err() != nil {
		t.Fatal("held copied end", held.Err())
	}
	if err := held.Close(); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(p.Ctx)
	cancel()
	for _, backend := range p.Backends() {
		result, err := backend.DB.ExecContext(canceled, "UPDATE m SET n=exp(n)")
		if result != nil || !errors.Is(err, context.Canceled) {
			t.Fatal(result, err)
		}
		var zero float64
		if err := backend.DB.QueryRowContext(p.Ctx, "SELECT ceil('-0.5')").Scan(&zero); err != nil || zero != 0 || !math.Signbit(zero) {
			t.Fatal(zero, err)
		}
	}
	remaining := fixture.Select("SELECT n FROM m ORDER BY id", []string{"n"}, []string{"NUMERIC"}, [][]any{{"9.00"}, {"9.00"}, {nil}})
	p.Step(t, remaining)
	p.Step(t, fixture.Prepared(remaining))
}
