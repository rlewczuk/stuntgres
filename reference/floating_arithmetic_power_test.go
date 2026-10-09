package reference_test

import (
	"math"
	"testing"
)

func TestReferenceFloatingArithmeticPower(t *testing.T) {
	referenceFloatingScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		referenceFloatingRun(t, p, b, referenceExec("CREATE TABLE unit(id INT)", 0))
		referenceFloatingRun(t, p, b, referenceExec("INSERT INTO unit VALUES(1)", 1))
		for _, tc := range []struct {
			expr string
			v    float64
			ulps uint64
		}{
			{"2^3^2", 64, 0}, {"2^(3^2)", 512, 0}, {"2*3^2", 18, 0}, {"2^3*2", 16, 0}, {"-2^2", 4, 0}, {"-(2^2)", -4, 0}, {"2 ^ -3", 0.125, 0}, {"2 OPERATOR(pg_catalog.^) 3 * 2", 64, 0},
			{"'9'::real^'0.5'::real", 3, 0}, {"'2'::double precision^'0.5'::double precision", 1.4142135623730951, 2}, {"'2'::numeric^'3'::real", 8, 0}, {"'2'::real^'3'::numeric", 8, 0},
		} {
			q := "SELECT " + tc.expr + " AS result FROM unit"
			for _, prepared := range []bool{false, true} {
				referenceScalarFloatQuery(t, p.ctx, b.db, b.recorder, referenceScalarFloatCase{tc.expr, q, "FLOAT8", tc.v, tc.ulps}, prepared)
			}
		}
		cast := func(s string) string { return "'" + s + "'::double precision" }
		for _, tc := range []struct {
			a, e string
			v    float64
			ulps uint64
		}{
			{"0", "0", 1, 0}, {"NaN", "0", 1, 0}, {"1", "NaN", 1, 0}, {"NaN", "NaN", math.NaN(), 0}, {"2", "NaN", math.NaN(), 0}, {"-1", "NaN", math.NaN(), 0}, {"0", "NaN", math.NaN(), 0}, {"-1", "Infinity", 1, 0},
			{"Infinity", "0", 1, 0}, {"-Infinity", "0", 1, 0}, {"Infinity", "-3", 0, 0}, {"-Infinity", "-3", math.Copysign(0, -1), 0}, {"-Infinity", "-2", 0, 0}, {"-Infinity", "3", math.Inf(-1), 0}, {"-Infinity", "2", math.Inf(1), 0},
			{"0.5", "Infinity", 0, 0}, {"0.5", "-Infinity", math.Inf(1), 0}, {"2", "Infinity", math.Inf(1), 0}, {"2", "-Infinity", 0, 0}, {"-0", "3", math.Copysign(0, -1), 0}, {"-0", "2", 0, 0},
			{"0", "1e300", 0, 0}, {"1", "1e300", 1, 0}, {"-1", "1e300", 1, 0}, {"-1", "-1e300", 1, 0}, {"-1", "9007199254740991", -1, 0}, {"-1", "9007199254740992", 1, 0},
			// Exact dyadics and independently rounded decimal reciprocals. Nonzero
			// subnormals remain nonzero even when a one-subnormal-ULP tolerance applies.
			{"2", "-1022", math.Float64frombits(0x0010000000000000), 0}, {"2", "-1023", math.Float64frombits(0x0008000000000000), 0}, {"2", "-1074", math.Float64frombits(1), 0}, {"0.5", "1074", math.Float64frombits(1), 0}, {"-2", "-1073", math.Float64frombits(0x8000000000000002), 0},
			{"2", "-1074.5", math.Float64frombits(1), 0}, {"10", "-310", 1e-310, 1}, {"-10", "-311", -1e-311, 1}, {"2", "1023", math.Float64frombits(0x7fe0000000000000), 0}, {"1.7976931348623157e308", "1", math.MaxFloat64, 0},
			{"1.7976931348623157e308", "0.9999999999999999", 1.797693134862174e308, 2}, {"2", "1023.9999999999999", 1.7976931348621742e308, 2},
			// Nearest binary64 neighbors on each side of the half-min-subnormal tie.
			{"2", "-1074.9999999999998", math.Float64frombits(1), 0},
		} {
			q := "SELECT " + cast(tc.a) + " ^ " + cast(tc.e) + " AS result FROM unit"
			for _, prepared := range []bool{false, true} {
				referenceScalarFloatQuery(t, p.ctx, b.db, b.recorder, referenceScalarFloatCase{q, q, "FLOAT8", tc.v, tc.ulps}, prepared)
			}
			if tc.v != 0 && !math.IsNaN(tc.v) && !math.IsInf(tc.v, 0) && math.Abs(tc.v) < math.Float64frombits(0x0010000000000000) {
				var v float64
				if err := b.db.QueryRowContext(p.ctx, q).Scan(&v); err != nil || v == 0 {
					t.Fatal("subnormal disappeared", q, v, err)
				}
			}
		}
		for _, tc := range []struct{ a, e, code, message string }{
			{"0", "-1", "2201F", "zero raised to a negative power is undefined"}, {"-0", "-1", "2201F", "zero raised to a negative power is undefined"}, {"0", "-Infinity", "2201F", "zero raised to a negative power is undefined"}, {"-0", "-Infinity", "2201F", "zero raised to a negative power is undefined"}, {"0", "-1e300", "2201F", "zero raised to a negative power is undefined"},
			{"-2", "0.5", "2201F", "a negative number raised to a non-integer power yields a complex result"}, {"-Infinity", "0.5", "2201F", "a negative number raised to a non-integer power yields a complex result"},
			{"1e200", "2", "22003", "value out of range: overflow"}, {"1e-200", "2", "22003", "value out of range: underflow"}, {"2", "-1075", "22003", "value out of range: underflow"}, {"2", "-1075.0000000000002", "22003", "value out of range: underflow"}, {"2", "-1076", "22003", "value out of range: underflow"}, {"2", "1024", "22003", "value out of range: overflow"}, {"1.7976931348623157e308", "1.0000000000000002", "22003", "value out of range: overflow"},
			{"1.0000000000000002", "1e300", "22003", "value out of range: overflow"}, {"0.9999999999999999", "-1e300", "22003", "value out of range: overflow"}, {"1.0000000000000002", "-1e300", "22003", "value out of range: underflow"}, {"0.9999999999999999", "1e300", "22003", "value out of range: underflow"},
		} {
			referenceFloatingError(t, p, b, "SELECT "+cast(tc.a)+" ^ "+cast(tc.e)+" FROM unit", tc.code, tc.message)
		}
		s := referenceImplicitError("SELECT 2^-3 FROM unit", referenceDiagnostic{Code: "42883", Message: "operator does not exist: integer ^- integer", Position: "9", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."})
		s.selectRows = true
		referenceFloatingRun(t, p, b, s)
		s.errorStage = "prepare"
		referenceFloatingRun(t, p, b, referencePrepared(s))
	})
}
