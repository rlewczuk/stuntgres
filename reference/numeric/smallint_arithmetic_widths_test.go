package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSmallintArithmeticWidths(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(s SMALLINT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
			for _, tc := range []struct {
				expr, typ string
				value     int64
			}{
				{"(-32768)::smallint % (-1)::smallint", "INT2", 0},
				{"32767::smallint+0::smallint", "INT2", 32767},
				{"(-32768)::smallint-0::smallint", "INT2", -32768},
				{"(-32768)::smallint*1::smallint", "INT2", -32768},
				{"32767::smallint/1::smallint", "INT2", 32767},
				{"32767::smallint+1", "INT4", 32768},
				{"(-32768)::smallint/(-1)::int4", "INT4", 32768},
				{"(-32768)::smallint/(-1)::int8", "INT8", 32768},
				{"-((-32768)::smallint)::int4", "INT4", 32768},
				{"(-2147483648)::int4%(-1)::smallint", "INT4", 0},
				{"(-9223372036854775808)::int8%(-1)::smallint", "INT8", 0},
				{"9007199254740993::bigint+1::smallint", "INT8", 9007199254740994},
				{"9007199254740993::bigint*2::smallint", "INT8", 18014398509481986},
				{"9007199254740993::bigint/3::smallint", "INT8", 3002399751580331},
			} {
				s := fixture.Select("SELECT "+tc.expr+" FROM t", []string{"?column?"}, []string{tc.typ}, [][]any{{tc.value}})
				run(s)
				run(fixture.Prepared(s))
			}
			for _, tc := range []struct{ expr, message string }{
				{"32767::smallint+1::smallint", "smallint out of range"},
				{"(-32768)::smallint-1::smallint", "smallint out of range"},
				{"32767::smallint*2::smallint", "smallint out of range"},
				{"(-32768)::smallint*(-1)::smallint", "smallint out of range"},
				{"(-32768)::smallint/(-1)::smallint", "smallint out of range"},
				{"-((-32768)::smallint)", "smallint out of range"},
				{"(-((-32768)::smallint))::int4", "smallint out of range"},
				{"(32767::smallint+1::smallint)::bigint", "smallint out of range"},
				{"(-2147483648)::int4/(-1)::smallint", "integer out of range"},
				{"(-9223372036854775808)::int8/(-1)::smallint", "bigint out of range"},
				{"2147483647::int4+1::smallint", "integer out of range"},
				{"1::smallint+2147483647::int4", "integer out of range"},
				{"9223372036854775807::int8+1::smallint", "bigint out of range"},
				{"1::smallint+9223372036854775807::int8", "bigint out of range"},
				{"(-2147483648)::int4*(-1)::smallint", "integer out of range"},
				{"(-1)::smallint*(-2147483648)::int4", "integer out of range"},
				{"(-9223372036854775808)::int8*(-1)::smallint", "bigint out of range"},
				{"(-1)::smallint*(-9223372036854775808)::int8", "bigint out of range"},
			} {
				s := fixture.ImplicitError("SELECT "+tc.expr+" FROM t", fixture.Diagnostic{Code: "22003", Message: tc.message})
				s.SelectRows = true
				s.ErrorStage = "execute"
				run(s)
				run(fixture.Prepared(s))
			}
			for _, op := range []string{"/", "%"} {
				for _, pair := range []string{"1::smallint" + op + "0::smallint", "1::smallint" + op + "0::int4", "1::int4" + op + "0::smallint", "1::smallint" + op + "0::int8", "1::int8" + op + "0::smallint"} {
					s := fixture.ImplicitError("SELECT "+pair+" FROM t", fixture.Diagnostic{Code: "22012", Message: "division by zero"})
					s.SelectRows = true
					s.ErrorStage = "execute"
					run(s)
					run(fixture.Prepared(s))
				}
			}
		})
	}
}

func TestReferenceSmallintArithmeticDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(s SMALLINT,txt TEXT)", 0))
			for _, populated := range []bool{false, true} {
				if populated {
					run(fixture.Exec("INSERT INTO t VALUES(7,'x')", 1))
				}
				for _, tc := range []struct{ q, code, message, pos, hint string }{
					{"SELECT s+TRUE FROM t", "42883", "operator does not exist: smallint + boolean", "9", "No operator matches the given name and argument types. You might need to add explicit type casts."},
					{"SELECT -txt FROM t", "42883", "operator does not exist: - text", "8", "No operator matches the given name and argument type. You might need to add an explicit type cast."},
					{"SELECT s%-1 FROM t", "42883", "operator does not exist: smallint %- integer", "9", "No operator matches the given name and argument types. You might need to add explicit type casts."},
					{"SELECT s+'oops' FROM t", "22P02", `invalid input syntax for type smallint: "oops"`, "10", ""},
					{"SELECT s+'32768' FROM t", "22003", `value "32768" is out of range for type smallint`, "10", ""},
					{"SELECT /*é*/ s+TRUE FROM t", "42883", "operator does not exist: smallint + boolean", "15", "No operator matches the given name and argument types. You might need to add explicit type casts."},
				} {
					s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: tc.pos, Hint: tc.hint})
					s.SelectRows = true
					run(s)
					s.ErrorStage = "prepare"
					run(fixture.Prepared(s))
				}
			}
		})
	}
}
