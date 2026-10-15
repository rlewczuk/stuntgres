package reference_test

import (
	"context"
	"database/sql"
	"math"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceMathLogPowerValues(t *testing.T) {
	mathLogPowerScenario(t, func(t *testing.T, ctx context.Context, conn *sql.Conn, run func(fixture.Step)) {
		for _, tc := range []struct {
			expression string
			want       float64
		}{
			{"lgamma(1::float8)", 0}, {"lgamma(2::float8)", 0},
			{"ln(1::float8)", 0}, {"log(100::float8)", 2}, {"log10(1000::float8)", 3},
			{"power(2::float8,3::float8)", 8}, {"sqrt(4::float8)", 2},
			{"power((-2)::float8,3::float8)", -8}, {"power((-2)::float8,4::float8)", 16},
		} {
			mathLogPowerBoth(run, fixture.Select("SELECT "+tc.expression+" AS result", []string{"result"}, []string{"FLOAT8"}, [][]any{{tc.want}}))
		}
		for _, tc := range []struct {
			expression string
			want       float64
		}{
			{"lgamma(-0.5::float8)", 1.2655121234846454}, {"lgamma(0.5::float8)", 0.5723649429247001},
			{"ln(2::float8)", 0.6931471805599453}, {"log(2::float8)", 0.3010299956639812},
			{"sqrt(2::float8)", 1.4142135623730951}, {"power(2::float8,0.5::float8)", 1.4142135623730951},
		} {
			mathLogPowerFloat(t, ctx, conn, "SELECT "+tc.expression+" AS result", tc.want, 2)
		}
	})
}

func TestReferenceMathLogPowerNumericPrecision(t *testing.T) {
	mathLogPowerScenario(t, func(t *testing.T, ctx context.Context, conn *sql.Conn, run func(fixture.Step)) {
		// All text, including trailing zeros and the 1000-place logarithm, was
		// independently reasoned then recorded against pinned18.0 tests-1.
		const negativeThousandLn10 = "-2302.5850929940456840179914546843642076011014886287729760333279009675726096773524802359972050895982983419677840422862486334095254650828067566662873690987816894829072083255546808437998948262331985283935053089653777326288461633662222876982198867465436674744042432743651550489343149393914796194044002221051017141748003688084012647080685567743216228355220114804663715659121373450747856947683463616792101806445070648000277502684916746550586856935673420670581136429224554405758925724208241314695689016758940256776311356919292033376587141660230105703089634572075440370847469940168269282808481184289314848524948644871927809676271275775397027668605952496716674183485704422507197965004714951050492214776567636938662976979522110718264549734772662425709429322582798502585509785265383207606726317164309505995087807523710333101197857547331541421808427543863591778117054309827482385045648019095610299291824318237525357709750539565187697510374970888692180205189339507238539205144634197265287286965110862571492198849978749"
		for _, tc := range []struct{ expression, want string }{
			{"ln(1::numeric)", "0.0000000000000000"}, {"ln(2.0)", "0.6931471805599453"},
			{"ln(1.000000000000000000000000000001)", "0.0000000000000000000000000000010000000000000000"},
			{"ln(1.0000000000000000000000000000000000000000000000000000000000000001)", "0.00000000000000000000000000000000000000000000000000000000000000010000000000000000"},
			{"ln(1e1000::numeric)", "2302.5850929940457"}, {"ln(1e-1000::numeric)", negativeThousandLn10},
			{"log(100::numeric)", "2.0000000000000000"}, {"log10(1000.0)", "3.0000000000000000"},
			{"log(2.0,64.0)", "6.0000000000000000"}, {"log(2,64)", "6.0000000000000000"},
			{"log(1.000000000000000000000000000001,1.000000000000000000000000000002)", "1.999999999999999999999999999999"},
			{"sqrt(2.0)", "1.414213562373095"}, {"sqrt(4.0000)", "2.000000000000000"},
			{"sqrt(10000000000000000000000000000000000000001::numeric)", "100000000000000000000"},
			{"sqrt(1e1000::numeric)", "1" + strings.Repeat("0", 500)},
			{"sqrt(1e-1000::numeric)", "0." + strings.Repeat("0", 499) + "1" + strings.Repeat("0", 500)},
			{"power(2::numeric,3::numeric)", "8.0000000000000000"}, {"power(2.00,0.5)", "1.4142135623730950"},
			{"power(9007199254740993::numeric,2::numeric)", "81129638414606699710187514626049"},
		} {
			mathLogPowerBoth(run, fixture.Select("SELECT "+tc.expression+" AS result", []string{"result"}, []string{"NUMERIC"}, [][]any{{tc.want}}))
		}
	})
}

func TestReferenceMathLogPowerIEEE(t *testing.T) {
	mathLogPowerScenario(t, func(t *testing.T, ctx context.Context, conn *sql.Conn, run func(fixture.Step)) {
		for _, name := range []string{"ln", "log", "log10", "sqrt"} {
			for _, tc := range []struct {
				input, decimal string
				floating       float64
			}{{"NaN", "NaN", math.NaN()}, {"Infinity", "Infinity", math.Inf(1)}} {
				mathLogPowerFloat(t, ctx, conn, "SELECT "+name+"('"+tc.input+"'::float8) AS result", tc.floating, 0)
				mathLogPowerBoth(run, fixture.Select("SELECT "+name+"('"+tc.input+"'::numeric) AS result", []string{"result"}, []string{"NUMERIC"}, [][]any{{tc.decimal}}))
			}
		}
		for _, tc := range []struct {
			a, b, decimal string
			floating      float64
		}{
			{"'NaN'", "0", "1", 1}, {"1", "'NaN'", "1", 1}, {"'NaN'", "1", "NaN", math.NaN()}, {"0", "0", "1.0000000000000000", 1},
			{"'-Infinity'", "3", "-Infinity", math.Inf(-1)}, {"'-Infinity'", "2", "Infinity", math.Inf(1)},
			{"2", "'Infinity'", "Infinity", math.Inf(1)}, {"0.5", "'Infinity'", "0", 0},
		} {
			mathLogPowerFloat(t, ctx, conn, "SELECT power("+tc.a+"::float8,"+tc.b+"::float8) AS result", tc.floating, 0)
			mathLogPowerBoth(run, fixture.Select("SELECT power("+tc.a+"::numeric,"+tc.b+"::numeric) AS result", []string{"result"}, []string{"NUMERIC"}, [][]any{{tc.decimal}}))
		}
		for _, tc := range []struct{ args, want string }{
			{"'NaN'::numeric,0::numeric", "NaN"}, {"0::numeric,'NaN'::numeric", "NaN"}, {"'Infinity'::numeric,'Infinity'::numeric", "NaN"},
			{"'Infinity'::numeric,2::numeric", "0"}, {"2::numeric,'Infinity'::numeric", "Infinity"}, {"1::numeric,'Infinity'::numeric", "Infinity"},
		} {
			mathLogPowerBoth(run, fixture.Select("SELECT log("+tc.args+") AS result", []string{"result"}, []string{"NUMERIC"}, [][]any{{tc.want}}))
		}
		for _, tc := range []struct {
			expression string
			want       float64
		}{
			{"lgamma('NaN'::float8)", math.NaN()}, {"lgamma('Infinity'::float8)", math.Inf(1)}, {"lgamma('-Infinity'::float8)", math.Inf(1)},
			{"sqrt('-0'::float8)", math.Copysign(0, -1)}, {"power('-0'::float8,3::float8)", math.Copysign(0, -1)},
			{"power('-Infinity'::float8,(-3)::float8)", math.Copysign(0, -1)}, {"power(2::float8,(-1074)::float8)", math.SmallestNonzeroFloat64},
		} {
			mathLogPowerFloat(t, ctx, conn, "SELECT "+tc.expression+" AS result", tc.want, 0)
		}
	})
}

func TestReferenceMathLogPowerDiagnostics(t *testing.T) {
	mathLogPowerScenario(t, func(t *testing.T, ctx context.Context, conn *sql.Conn, run func(fixture.Step)) {
		// PostgreSQL18.0 tests-2: runtime positions are absent. SQL wrappers
		// retain Context for constant/parameter evaluation, not column inputs.
		for _, typ := range []string{"numeric", "float8"} {
			for _, name := range []string{"ln", "log", "log10"} {
				for _, input := range []string{"0", "-1", "-Infinity"} {
					message := "cannot take logarithm of a negative number"
					if input == "0" {
						message = "cannot take logarithm of zero"
					}
					want := fixture.Diagnostic{Code: "2201E", Message: message}
					if typ == "numeric" && name != "ln" {
						want.Context = "SQL function \"" + name + "\" statement 1"
					}
					mathLogPowerErrors(run, "SELECT "+name+"('"+input+"'::"+typ+") AS result", want, false)
				}
			}
			for _, input := range []string{"-1", "-Infinity"} {
				mathLogPowerErrors(run, "SELECT sqrt('"+input+"'::"+typ+") AS result",
					fixture.Diagnostic{Code: "2201F", Message: "cannot take square root of a negative number"}, false)
			}
			for _, tc := range []struct{ args, message string }{
				{"0::" + typ + ",(-1)::" + typ, "zero raised to a negative power is undefined"},
				{"(-1)::" + typ + ",0.5::" + typ, "a negative number raised to a non-integer power yields a complex result"},
			} {
				mathLogPowerErrors(run, "SELECT power("+tc.args+")", fixture.Diagnostic{Code: "2201F", Message: tc.message}, false)
			}
			mathLogPowerErrors(run, "SELECT power(NULL::"+typ+",ln(0::"+typ+")) AS result",
				fixture.Diagnostic{Code: "2201E", Message: "cannot take logarithm of zero"}, false)
		}
		for _, expression := range []string{"lgamma(0::float8)", "lgamma('-0'::float8)", "lgamma((-1)::float8)", "lgamma(1e308::float8)", "power(1e308::float8,2::float8)"} {
			mathLogPowerErrors(run, "SELECT "+expression+" AS result", fixture.Diagnostic{Code: "22003", Message: "value out of range: overflow"}, false)
		}
		mathLogPowerErrors(run, "SELECT power(2::float8,(-1075)::float8) AS result", fixture.Diagnostic{Code: "22003", Message: "value out of range: underflow"}, false)
		mathLogPowerErrors(run, "SELECT ln('-0'::float8) AS result", fixture.Diagnostic{Code: "2201E", Message: "cannot take logarithm of zero"}, false)
		for _, tc := range []struct{ args, code, message string }{
			{"1::numeric,2::numeric", "22012", "division by zero"},
			{"0::numeric,(-1)::numeric", "2201E", "cannot take logarithm of zero"},
			{"(-1)::numeric,0::numeric", "2201E", "cannot take logarithm of a negative number"},
			{"1::numeric,0::numeric", "2201E", "cannot take logarithm of zero"},
		} {
			mathLogPowerErrors(run, "SELECT log("+tc.args+") AS result", fixture.Diagnostic{Code: tc.code, Message: tc.message}, false)
		}
		for _, tc := range []struct {
			query, message, context string
			args                    []any
		}{
			{"SELECT ln($1)", "invalid input syntax for type double precision: \"oops\"", "unnamed portal parameter $1 = '...'", []any{"oops"}},
			{"SELECT log($1,$2)", "invalid input syntax for type numeric: \"oops\"", "unnamed portal parameter $1 = '...'", []any{"oops", "2"}},
			{"SELECT width_bucket($1,$2,$3,$4)", "invalid input syntax for type integer: \"oops\"", "unnamed portal parameter $4 = '...'", []any{"1", "0", "10", "oops"}},
		} {
			mathLogPowerErrors(run, tc.query, fixture.Diagnostic{Code: "22P02", Message: tc.message, Context: tc.context}, false, tc.args...)
		}
		run(fixture.Exec("CREATE TABLE mlp_wrapper_probe (n numeric)", 0))
		run(fixture.Exec("INSERT INTO mlp_wrapper_probe VALUES (0),(-1)", 2))
		for _, name := range []string{"log", "log10"} {
			mathLogPowerErrors(run, "SELECT "+name+"($1::numeric) AS result",
				fixture.Diagnostic{Code: "2201E", Message: "cannot take logarithm of a negative number", Context: "SQL function \"" + name + "\" statement 1"}, false, "-1")
			mathLogPowerErrors(run, "SELECT "+name+"(n) AS result FROM mlp_wrapper_probe WHERE n=0",
				fixture.Diagnostic{Code: "2201E", Message: "cannot take logarithm of zero"}, false)
		}
		mathLogPowerBoth(run, fixture.Select("SELECT ln(NULL::numeric) AS result", []string{"result"}, []string{"NUMERIC"}, [][]any{{nil}}))
		mathLogPowerBoth(run, fixture.Select("SELECT sqrt('0'::numeric) AS result", []string{"result"}, []string{"NUMERIC"}, [][]any{{"0.000000000000000"}}))
		mathLogPowerBoth(run, fixture.Select("SELECT sqrt('0'::float8) AS result", []string{"result"}, []string{"FLOAT8"}, [][]any{{float64(0)}}))
	})
}
