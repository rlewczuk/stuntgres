package reference_test

import (
	"math"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceMathLogPowerPowDiagnostics(t *testing.T) {
	mathPowScenario(t, func(c *mathPowCase) {
		// Independently inherited power contracts: execution errors have no
		// position/context, unlike binding errors. Every remaining diagnostic
		// field is required empty by ImplicitError, not silently ignored.
		const zero = "zero raised to a negative power is undefined"
		const complex = "a negative number raised to a non-integer power yields a complex result"
		for _, name := range []string{"pow", "power"} {
			c.diagnostic("SELECT "+name+"(0, -1)", fixture.Diagnostic{Code: "2201F", Message: zero}, false)
			c.diagnostic("SELECT "+name+"(-8, 1.0/3)", fixture.Diagnostic{Code: "2201F", Message: complex}, false)
			for _, typ := range []string{"numeric", "float8"} {
				for _, tc := range []struct{ args, message string }{
					{"0::" + typ + ",(-1)::" + typ, zero},
					{"(-8)::" + typ + ",(1.0/3)::" + typ, complex},
				} {
					c.diagnostic("SELECT "+name+"("+tc.args+")", fixture.Diagnostic{Code: "2201F", Message: tc.message}, false)
				}
				c.diagnostic("SELECT "+name+"(NULL::"+typ+",ln(0::"+typ+")) AS result", fixture.Diagnostic{Code: "2201E", Message: "cannot take logarithm of zero"}, false)
			}
			c.diagnostic("SELECT "+name+"(1e308::float8,2::float8) AS result", fixture.Diagnostic{Code: "22003", Message: "value out of range: overflow"}, false)
			c.diagnostic("SELECT "+name+"(2::float8,(-1075)::float8) AS result", fixture.Diagnostic{Code: "22003", Message: "value out of range: underflow"}, false)
		}
		c.run(fixture.Exec("CREATE TABLE pow_demand (n numeric,f float8)", 0))
		c.run(fixture.Exec("INSERT INTO pow_demand VALUES (2,2)", 1))
		for _, tc := range []struct{ typ, column string }{{"numeric", "n"}, {"float8", "f"}} {
			c.diagnostic("SELECT pow(NULLIF("+tc.column+","+tc.column+"),ln(0::"+tc.typ+")) AS result FROM pow_demand", fixture.Diagnostic{Code: "2201E", Message: "cannot take logarithm of zero"}, false)
		}
		// No tolerance for categories, signs, subnormal boundaries or integer
		// results; only the finite irrational FLOAT8 witness admits two ULP.
		for _, tc := range []struct {
			args string
			want float64
		}{
			{"'-0'::float8,3::float8", math.Copysign(0, -1)},
			{"'-0'::float8,2::float8", 0},
			{"'-Infinity'::float8,(-3)::float8", math.Copysign(0, -1)},
			{"'-Infinity'::float8,3::float8", math.Inf(-1)},
			{"'-Infinity'::float8,2::float8", math.Inf(1)},
			{"'NaN'::float8,0::float8", 1},
			{"1::float8,'NaN'::float8", 1},
			{"'NaN'::float8,1::float8", math.NaN()},
			{"2::float8,'Infinity'::float8", math.Inf(1)},
			{"0.5::float8,'Infinity'::float8", 0},
			{"(-2)::float8,3::float8", -8},
			{"(-2)::float8,4::float8", 16},
			{"2::float8,(-3)::float8", 0.125},
			{"2::float8,(-1074)::float8", math.SmallestNonzeroFloat64},
		} {
			c.floating("SELECT pow("+tc.args+") AS result", tc.want, 0)
		}
		c.floating("SELECT pow(2::float8,0.5::float8) AS result", 1.4142135623730951, 2)
		for _, tc := range []struct{ args, want string }{
			{"'NaN'::numeric,0::numeric", "1"},
			{"1::numeric,'NaN'::numeric", "1"},
			{"'NaN'::numeric,1::numeric", "NaN"},
			{"0::numeric,0::numeric", "1.0000000000000000"},
			{"'-Infinity'::numeric,3::numeric", "-Infinity"},
			{"'-Infinity'::numeric,2::numeric", "Infinity"},
			{"2::numeric,'Infinity'::numeric", "Infinity"},
			{"0.5::numeric,'Infinity'::numeric", "0"},
			{"(-2)::numeric,3::numeric", "-8.0000000000000000"},
			{"2::numeric,(-3)::numeric", "0.1250000000000000"},
			{"2.00,0.5", "1.4142135623730950"},
		} {
			c.both(fixture.Select("SELECT pow("+tc.args+") AS result", []string{"result"}, []string{"NUMERIC"}, [][]any{{tc.want}}))
		}
		for _, typ := range []string{"numeric", "float8"} {
			wantType := "NUMERIC"
			if typ == "float8" {
				wantType = "FLOAT8"
			}
			for _, args := range []string{"NULL::" + typ + ",3::" + typ, "2::" + typ + ",NULL::" + typ} {
				c.both(fixture.Select("SELECT pow("+args+")", []string{"pow"}, []string{wantType}, [][]any{{nil}}))
			}
		}
	})
}
