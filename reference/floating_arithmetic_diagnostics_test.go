package reference_test

import (
	"math"
	"testing"
)

func TestReferenceFloatingArithmeticIEEE(t *testing.T) {
	referenceFloatingScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		referenceFloatingRun(t, p, b, referenceExec("CREATE TABLE unit(id INT)", 0))
		referenceFloatingRun(t, p, b, referenceExec("INSERT INTO unit VALUES(1)", 1))
		for _, width := range []struct{ sql, typ string }{{"real", "FLOAT4"}, {"double precision", "FLOAT8"}} {
			cast := func(s string) string { return "'" + s + "'::" + width.sql }
			for _, tc := range []struct {
				a, op, c string
				v        float64
			}{{"NaN", "/", "0", math.NaN()}, {"NaN", "/", "-0", math.NaN()}, {"Infinity", "/", "Infinity", math.NaN()}, {"Infinity", "-", "Infinity", math.NaN()}, {"Infinity", "+", "-Infinity", math.NaN()}, {"Infinity", "*", "0", math.NaN()}, {"1", "/", "Infinity", 0}, {"1", "/", "-Infinity", math.Copysign(0, -1)}, {"-1", "/", "Infinity", math.Copysign(0, -1)}, {"-1", "/", "-Infinity", 0}, {"1", "-", "1", 0}} {
				q := "SELECT " + cast(tc.a) + tc.op + cast(tc.c) + " AS result FROM unit"
				for _, prepared := range []bool{false, true} {
					referenceScalarFloatQuery(t, p.ctx, b.db, b.recorder, referenceScalarFloatCase{q, q, width.typ, tc.v, 0}, prepared)
				}
			}
			for _, tc := range []struct {
				op, input string
				v         float64
			}{{"+", "-0", math.Copysign(0, -1)}, {"-", "-0", 0}, {"+", "Infinity", math.Inf(1)}, {"-", "Infinity", math.Inf(-1)}, {"-", "-Infinity", math.Inf(1)}, {"-", "NaN", math.NaN()}} {
				q := "SELECT " + tc.op + cast(tc.input) + " AS result FROM unit"
				for _, prepared := range []bool{false, true} {
					referenceScalarFloatQuery(t, p.ctx, b.db, b.recorder, referenceScalarFloatCase{q, q, width.typ, tc.v, 0}, prepared)
				}
			}
			for _, numerator := range []string{"1", "0", "Infinity"} {
				for _, zero := range []string{"0", "-0"} {
					referenceFloatingError(t, p, b, "SELECT "+cast(numerator)+"/"+cast(zero)+" FROM unit", "22012", "division by zero")
				}
			}
			tiny := "5e-324"
			want := math.Float64frombits(1)
			if width.typ == "FLOAT4" {
				tiny = "1e-45"
				want = float64(math.Float32frombits(1))
				if b.pg {
					want = 1e-45
				}
			}
			for _, op := range []string{"*", "/"} {
				q := "SELECT " + cast(tiny) + op + cast("1") + " AS result FROM unit"
				for _, prepared := range []bool{false, true} {
					referenceScalarFloatQuery(t, p.ctx, b.db, b.recorder, referenceScalarFloatCase{q, q, width.typ, want, 0}, prepared)
				}
			}
		}
		for _, tc := range []struct{ expr, kind string }{{"'3e38'::real+'3e38'::real", "overflow"}, {"'-3e38'::real-'3e38'::real", "overflow"}, {"'1e308'::double precision*'2'::double precision", "overflow"}, {"'1e308'::double precision/'1e-308'::double precision", "overflow"}, {"'1e-30'::real*'1e-20'::real", "underflow"}, {"'1e-200'::double precision*'1e-200'::double precision", "underflow"}, {"'1e-300'::double precision/'1e100'::double precision", "underflow"}} {
			referenceFloatingError(t, p, b, "/*é*/ SELECT "+tc.expr+" FROM unit", "22003", "value out of range: "+tc.kind)
		}
	})
}

func TestReferenceFloatingArithmeticUnavailableModulo(t *testing.T) {
	referenceFloatingScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		referenceFloatingRun(t, p, b, referenceExec("CREATE TABLE f(r REAL,d DOUBLE PRECISION,n NUMERIC,i INT)", 0))
		for _, tc := range []struct{ expr, signature string }{{"r%r", "real % real"}, {"d%d", "double precision % double precision"}, {"r%d", "real % double precision"}, {"d%r", "double precision % real"}, {"r%n", "real % numeric"}, {"n%r", "numeric % real"}, {"r%i", "real % integer"}, {"'1'%r", "unknown % real"}, {"NULL::real%NULL::real", "real % real"}} {
			for _, prefix := range []string{"", "/*é*/ "} {
				q := prefix + "SELECT " + tc.expr + " FROM f"
				pos := len(prefix) + 8
				for i, c := range tc.expr {
					if c == '%' {
						pos += i
						break
					}
				}
				if prefix != "" {
					pos--
				}
				s := referenceImplicitError(q, referenceDiagnostic{Code: "42883", Message: "operator does not exist: " + tc.signature, Position: referencePosition(pos), Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."})
				s.selectRows = true
				referenceFloatingRun(t, p, b, s)
				s.errorStage = "prepare"
				referenceFloatingRun(t, p, b, referencePrepared(s))
			}
		}
	})
}
