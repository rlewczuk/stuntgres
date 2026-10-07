//go:build reference

package reference_test

import (
	"strings"
	"testing"
)

func TestReferenceMathSignatures(t *testing.T) {
	p := newReferencePair(t)
	for _, prepared := range []bool{false, true} {
		s := referenceSelect("SELECT round(4),round('4'),round(4.0),round(4,4),trunc(4),sign(4),scale(4),min_scale(4),trim_scale(4),mod(9,4)",
			[]string{"round", "round", "round", "round", "trunc", "sign", "scale", "min_scale", "trim_scale", "mod"},
			[]string{"FLOAT8", "FLOAT8", "NUMERIC", "NUMERIC", "FLOAT8", "FLOAT8", "INT4", "INT4", "NUMERIC", "INT4"},
			[][]any{{float64(4), float64(4), "4", "4.0000", float64(4), float64(1), int64(0), int64(0), "4", int64(1)}})
		s.prepared = prepared
		p.step(t, s)
	}
	p.step(t, referenceExec("CREATE TABLE widths(s SMALLINT,i INT,b BIGINT,n NUMERIC,r REAL,d DOUBLE PRECISION)", 0))
	p.step(t, referenceExec("INSERT INTO widths VALUES(9,9,9,9.00,2.5,3.5)", 1))
	s := referenceSelect("SELECT mod(s,s),mod(s,i),mod(i,b),mod(i,n),round(s),round(r),round(d),round(n) FROM widths",
		[]string{"mod", "mod", "mod", "mod", "round", "round", "round", "round"},
		[]string{"INT2", "INT4", "INT8", "NUMERIC", "FLOAT8", "FLOAT8", "FLOAT8", "NUMERIC"},
		[][]any{{int64(0), int64(0), int64(0), "0.00", float64(9), float64(2), float64(4), "9"}})
	p.step(t, s)
	p.step(t, referencePrepared(s))
	for _, tc := range []struct{ input, want any }{{"1.25", "1.3"}, {"-1.25", "-1.3"}, {nil, nil}} {
		s := referencePrepared(referenceSelect("SELECT round($1,$2)", []string{"round"}, []string{"NUMERIC"}, [][]any{{tc.want}}))
		s.args = []any{tc.input, 1}
		p.step(t, s)
	}
	p.step(t, referenceExec("CREATE TABLE minima(s SMALLINT,sd SMALLINT,i INTEGER,idiv INTEGER,b BIGINT,bd BIGINT)", 0))
	p.step(t, referenceExec("INSERT INTO minima VALUES(-32768,-1,-2147483648,-1,-9223372036854775808,-1)", 1))
	s = referenceSelect("SELECT mod(s,sd),mod(i,idiv),mod(b,bd) FROM minima", []string{"mod", "mod", "mod"}, []string{"INT2", "INT4", "INT8"}, [][]any{{int64(0), int64(0), int64(0)}})
	p.step(t, s)
	p.step(t, referencePrepared(s))
}

func TestReferenceMathNumericRoundScale(t *testing.T) {
	t.Run("limits", referenceMathScaleLimits)
	p := newReferencePair(t)
	for _, tc := range []struct {
		expression, name, typ string
		want                  any
	}{
		{"round(2.5)", "round", "NUMERIC", "3"},
		{"round(-2.5)", "round", "NUMERIC", "-3"},
		{"round(9.995,2)", "round", "NUMERIC", "10.00"},
		{"round(-9.995,2)", "round", "NUMERIC", "-10.00"},
		{"round(-1500,-3)", "round", "NUMERIC", "-2000"},
		{"trunc(-1500,-3)", "trunc", "NUMERIC", "-1000"},
		{"trunc(-1.25,1)", "trunc", "NUMERIC", "-1.2"},
		{"round(1.23,4)", "round", "NUMERIC", "1.2300"},
		{"scale(8.4100)", "scale", "INT4", int64(4)},
		{"min_scale(8.4100)", "min_scale", "INT4", int64(2)},
		{"trim_scale(8.4100)", "trim_scale", "NUMERIC", "8.41"},
		{"trim_scale(0.0000)", "trim_scale", "NUMERIC", "0"},
		{"scale('NaN')", "scale", "INT4", nil},
		{"min_scale('Infinity')", "min_scale", "INT4", nil},
		{"round(NULL,2)", "round", "NUMERIC", nil},
		{"round(NULL)", "round", "FLOAT8", nil},
		{"trunc(NULL,2)", "trunc", "NUMERIC", nil},
		{"trim_scale(NULL)", "trim_scale", "NUMERIC", nil},
		{"scale(NULL)", "scale", "INT4", nil},
		{"min_scale(NULL)", "min_scale", "INT4", nil},
		{"sign(NULL)", "sign", "FLOAT8", nil},
		{"mod(NULL,1)", "mod", "INT4", nil},
		{"trim_scale('NaN')", "trim_scale", "NUMERIC", "NaN"},
		{"round('Infinity',2)", "round", "NUMERIC", "Infinity"},
		{"trunc('-Infinity',2)", "trunc", "NUMERIC", "-Infinity"},
	} {
		s := referenceSelect("SELECT "+tc.expression, []string{tc.name}, []string{tc.typ}, [][]any{{tc.want}})
		p.step(t, s)
		p.step(t, referencePrepared(s))
	}
}

func TestReferenceMathSignModulo(t *testing.T) {
	t.Run("float", referenceMathFloat)
	p := newReferencePair(t)
	for _, tc := range []struct {
		expression, name, typ string
		want                  any
	}{
		{"sign(-13.000)", "sign", "NUMERIC", "-1"},
		{"sign(0.0000)", "sign", "NUMERIC", "0"},
		{"sign('NaN')", "sign", "FLOAT8", float64(0)},
		{"sign(COALESCE('NaN',1.0))", "sign", "NUMERIC", "NaN"},
		{"mod(-9,4)", "mod", "INT4", int64(-1)},
		{"mod(9,-4)", "mod", "INT4", int64(1)},
		{"mod(-2147483648,-1)", "mod", "INT4", int64(0)},
		{"mod(-9223372036854775808,-1)", "mod", "INT8", int64(0)},
		{"mod(-9.00,4.0)", "mod", "NUMERIC", "-1.00"},
		{"mod(9.00,-4.000)", "mod", "NUMERIC", "1.000"},
		{"mod(8.00,4.000)", "mod", "NUMERIC", "0.000"},
		{"mod(9007199254740993.25,2.0)", "mod", "NUMERIC", "1.25"},
		{"mod('NaN',0.0)", "mod", "NUMERIC", "NaN"},
		{"mod(1.2300,'Infinity')", "mod", "NUMERIC", "1.2300"},
		{"mod(1,NULL)", "mod", "INT4", nil},
	} {
		s := referenceSelect("SELECT "+tc.expression, []string{tc.name}, []string{tc.typ}, [][]any{{tc.want}})
		p.step(t, s)
		p.step(t, referencePrepared(s))
	}
}

func TestReferenceMathDiagnostics(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE signatures(d DOUBLE PRECISION,n NUMERIC,b BIGINT)", 0))
	hint := "No function matches the given name and argument types. You might need to add explicit type casts."
	for _, tc := range []struct{ query, code, message, position, hint string }{
		{"SELECT mod('1','2')", "42725", "function mod(unknown, unknown) is not unique", "8", "Could not choose a best candidate function. You might need to add explicit type casts."},
		{"SELECT trunc(NULL)", "42725", "function trunc(unknown) is not unique", "8", "Could not choose a best candidate function. You might need to add explicit type casts."},
		{"SELECT trunc('-0.5')", "42725", "function trunc(unknown) is not unique", "8", "Could not choose a best candidate function. You might need to add explicit type casts."},
		{"SELECT round(NULL,'oops')", "22P02", `invalid input syntax for type integer: "oops"`, "19", ""},
		{"SELECT round('oops')", "22P02", `invalid input syntax for type double precision: "oops"`, "14", ""},
		{"SELECT scale('oops')", "22P02", `invalid input syntax for type numeric: "oops"`, "14", ""},
		{"SELECT round(1.0,'oops')", "22P02", `invalid input syntax for type integer: "oops"`, "18", ""},
		{"SELECT round(d,2) FROM signatures", "42883", "function round(double precision, integer) does not exist", "8", hint},
		{"SELECT round(n,b) FROM signatures", "42883", "function round(numeric, bigint) does not exist", "8", hint},
		{"SELECT scale(d) FROM signatures", "42883", "function scale(double precision) does not exist", "8", hint},
		{"/*é*/ SELECT mod('1','2')", "42725", "function mod(unknown, unknown) is not unique", "14", "Could not choose a best candidate function. You might need to add explicit type casts."},
	} {
		s := referenceImplicitError(tc.query, referenceDiagnostic{Code: tc.code, Message: tc.message, Position: tc.position, Hint: tc.hint})
		s.selectRows = true
		s.errorStage = "execute"
		p.step(t, s)
		s.errorStage = "prepare"
		p.step(t, referencePrepared(s))
	}
	for _, q := range []string{"SELECT mod(1,0)", "SELECT mod(1.0,0.0)", "SELECT mod('Infinity',0.0)", "SELECT mod('-Infinity',0.0)", "SELECT round(NULL,mod(1,0))"} {
		s := referenceImplicitError(q, referenceDiagnostic{Code: "22012", Message: "division by zero"})
		s.errorStage = "execute"
		p.step(t, s)
		p.step(t, referencePrepared(s))
	}
	overLimit := referenceImplicitError("SELECT round("+strings.Repeat("1,", 100)+"1)", referenceDiagnostic{Code: "54023", Message: "cannot pass more than 100 arguments to a function", Position: "8"})
	overLimit.selectRows = true
	overLimit.errorStage = "execute"
	p.step(t, overLimit)
	overLimit.errorStage = "prepare"
	p.step(t, referencePrepared(overLimit))
	ambiguousParameter := referenceImplicitError("SELECT mod($1,$2)", referenceDiagnostic{Code: "42725", Message: "function mod(unknown, unknown) is not unique", Position: "8", Hint: "Could not choose a best candidate function. You might need to add explicit type casts."})
	ambiguousParameter.selectRows = true
	ambiguousParameter.errorStage = "prepare"
	p.step(t, referencePrepared(ambiguousParameter))
}
