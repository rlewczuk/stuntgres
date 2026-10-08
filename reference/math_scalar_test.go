//go:build reference

package reference_test

import (
	"math/big"
	"strings"
	"testing"
)

func TestReferenceMathScalarValues(t *testing.T) {
	p := newReferencePair(t)
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
			s := referenceSelect("SELECT "+tc.expr, []string{tc.name}, []string{tc.typ}, [][]any{{tc.want}})
			p.step(t, s)
			p.step(t, referencePrepared(s))
		})
	}
}

func TestReferenceMathScalarWidthsAndParameters(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE m(s SMALLINT,i INTEGER,b BIGINT,r REAL,d DOUBLE PRECISION,n NUMERIC(8,3))", 0))
	p.step(t, referenceExec("INSERT INTO m VALUES(-17,-17,-17,-17.5,-17.5,-17.400)", 1))
	s := referenceSelect("SELECT abs(s),abs(i),abs(b),abs(r),abs(d),abs(n),ceil(s),ceiling(r),floor(n) FROM m", []string{"abs", "abs", "abs", "abs", "abs", "abs", "ceil", "ceiling", "floor"}, []string{"INT2", "INT4", "INT8", "FLOAT4", "FLOAT8", "NUMERIC", "FLOAT8", "FLOAT8", "NUMERIC"}, [][]any{{int64(17), int64(17), int64(17), float64(17.5), float64(17.5), "17.400", float64(-17), float64(-17), "-18"}})
	p.step(t, s)
	p.step(t, referencePrepared(s))
	for _, tc := range []struct{ a, b, want any }{{"9007199254740993", "2", "4503599627370496"}, {nil, "2", nil}, {"-9.00", "4.000", "-2"}} {
		s := referencePrepared(referenceSelect("SELECT div($1,$2)", []string{"div"}, []string{"NUMERIC"}, [][]any{{tc.want}}, tc.a, tc.b))
		p.step(t, s)
	}
}

func TestReferenceMathScalarDiagnostics(t *testing.T) {
	p := newReferencePair(t)
	hint := "No function matches the given name and argument types. You might need to add explicit type casts."
	for _, name := range []string{"abs", "ceil", "ceiling", "floor", "exp", "div", "factorial"} {
		s := referenceImplicitError("SELECT "+name+"()", referenceDiagnostic{Code: "42883", Message: "function " + name + "() does not exist", Position: "8", Hint: hint})
		s.selectRows = true
		s.errorStage = "execute"
		p.step(t, s)
		s.errorStage = "prepare"
		p.step(t, referencePrepared(s))
	}
	for _, tc := range []struct{ q, code, message, pos string }{
		{"SELECT abs('oops')", "22P02", `invalid input syntax for type double precision: "oops"`, "12"}, {"SELECT ceil('oops')", "22P02", `invalid input syntax for type double precision: "oops"`, "13"},
		{"SELECT exp('oops')", "22P02", `invalid input syntax for type double precision: "oops"`, "12"}, {"SELECT div('oops',1)", "22P02", `invalid input syntax for type numeric: "oops"`, "12"}, {"SELECT factorial('oops')", "22P02", `invalid input syntax for type bigint: "oops"`, "18"},
	} {
		s := referenceImplicitError(tc.q, referenceDiagnostic{Code: tc.code, Message: tc.message, Position: tc.pos})
		s.selectRows = true
		s.errorStage = "execute"
		p.step(t, s)
		s.errorStage = "prepare"
		p.step(t, referencePrepared(s))
	}
	for _, tc := range []struct{ q, code, message string }{
		{"SELECT div(1,0)", "22012", "division by zero"}, {"SELECT div(NULL,div(1,0))", "22012", "division by zero"}, {"SELECT factorial(-1)", "22003", "factorial of a negative number is undefined"}, {"SELECT factorial(32178)", "22003", "value overflows numeric format"}, {"SELECT exp(6000.0)", "22003", "value overflows numeric format"}, {"SELECT abs(-2147483648)", "22003", "integer out of range"},
		{"SELECT div(1e131071,0.1)", "22003", "value overflows numeric format"},
	} {
		s := referenceImplicitError(tc.q, referenceDiagnostic{Code: tc.code, Message: tc.message})
		s.errorStage = "execute"
		p.step(t, s)
		p.step(t, referencePrepared(s))
	}
	p.step(t, referenceExec("CREATE TABLE minima(s SMALLINT,b BIGINT)", 0))
	p.step(t, referenceExec("INSERT INTO minima VALUES(-32768,-9223372036854775808)", 1))
	for _, tc := range []struct{ col, message string }{{"s", "smallint out of range"}, {"b", "bigint out of range"}} {
		s := referenceImplicitError("SELECT abs("+tc.col+") FROM minima", referenceDiagnostic{Code: "22003", Message: tc.message})
		s.selectRows = true
		s.errorStage = "execute"
		p.step(t, s)
		p.step(t, referencePrepared(s))
	}
}

func referenceScalarProduct(lo, hi int64) *big.Int {
	if lo > hi {
		return big.NewInt(1)
	}
	if lo == hi {
		return big.NewInt(lo)
	}
	mid := (lo + hi) / 2
	return new(big.Int).Mul(referenceScalarProduct(lo, mid), referenceScalarProduct(mid+1, hi))
}

func TestReferenceMathScalarFactorialMaximum(t *testing.T) {
	p := newReferencePair(t)
	want := referenceScalarProduct(2, 32177).String()
	s := referenceSelect("SELECT factorial(32177)", []string{"factorial"}, []string{"NUMERIC"}, [][]any{{want}})
	p.step(t, s)
	p.step(t, referencePrepared(s))
}

func TestReferenceMathScalarConsumers(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE m(id INT,n NUMERIC,d NUMERIC)", 0))
	p.step(t, referenceExec("INSERT INTO m VALUES(1,-9.00,4),(2,9.00,0),(3,NULL,4)", 3))
	p.step(t, referenceExec("CREATE TABLE copied(n NUMERIC)", 0))
	for _, q := range []string{"UPDATE m SET n=div(n,d)", "INSERT INTO copied SELECT div(n,d) FROM m"} {
		s := referenceImplicitError(q, referenceDiagnostic{Code: "22012", Message: "division by zero"})
		s.errorStage = "execute"
		p.step(t, s)
		p.step(t, referenceSelect("SELECT n FROM m ORDER BY id", []string{"n"}, []string{"NUMERIC"}, [][]any{{"-9.00"}, {"9.00"}, {nil}}))
		p.step(t, referenceSelect("SELECT n FROM copied", []string{"n"}, []string{"NUMERIC"}, [][]any{}))
	}
	p.step(t, referenceSelect("SELECT abs(n),count(*) FROM m GROUP BY 1 ORDER BY 1", []string{"abs", "count"}, []string{"NUMERIC", "INT8"}, [][]any{{"9.00", int64(2)}, {nil, int64(1)}}))
	p.step(t, referenceExec("UPDATE m SET n=abs(n)", 3))
	p.step(t, referenceExec("INSERT INTO copied SELECT div(n,4) FROM m", 3))
	p.step(t, referenceExec("CREATE VIEW scalar_view AS SELECT ceil(n) AS c,ceiling(n) AS cc,floor(n) AS f,exp(0.0) AS e,factorial(5) AS fac FROM m", 0))
	s := referenceSelect("SELECT c,cc,f,e,fac FROM scalar_view", []string{"c", "cc", "f", "e", "fac"}, []string{"NUMERIC", "NUMERIC", "NUMERIC", "NUMERIC", "NUMERIC"}, [][]any{{"9", "9", "9", "1.0000000000000000", "120"}, {"9", "9", "9", "1.0000000000000000", "120"}, {nil, nil, nil, "1.0000000000000000", "120"}})
	s.bag = true
	p.step(t, s)
	p.step(t, referencePrepared(s))
}
