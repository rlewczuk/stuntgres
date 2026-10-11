package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTimestampGenericNumericPrecisionExecution(t *testing.T) {
	p := newTimestampReferenceScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.TemporalRunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			for _, head := range []string{`"timestamp"`, `U&"t\0069mestamp"`} {
				for _, tc := range []struct{ number, raw, code string }{
					{".5", ".5", "22P02"}, {"1.5e+3", "1.5e+3", "22P02"}, {"1e+2", "1e+2", "22P02"}, {"1.", "1.", "22P02"},
					{"1_0.5", "1_0.5", "22P02"}, {"1.5e+3_0", "1.5e+3_0", "22P02"},
					{"1.5", "1.5", "22P02"}, {"1e2", "1e2", "22P02"},
					{"+.5", ".5", "42601"}, {"-1.5e+3", "-1.5e+3", "22P02"}, {"+1e+2", "1e+2", "42601"}, {"-1.", "-1.", "22P02"},
					{"+3", "3", "42601"}, {"+0", "0", "42601"}, {"+2147483648", "2147483648", "42601"},
					{"+7", "7", "42601"},
					{"2147483648", "2147483648", "22003"}, {"2147483649", "2147483649", "22003"},
					{"-2147483649", "-2147483649", "22003"}, {"999999999999999999999999999999", "999999999999999999999999999999", "22003"},
					{"2_147_483_648", "2_147_483_648", "22003"}, {"0x80000000", "0x80000000", "22003"},
					{"0o20000000000", "0o20000000000", "22003"}, {"0b10000000000000000000000000000000", "0b10000000000000000000000000000000", "22003"},
					{"-2147483648", "-2147483648", "22023"},
				} {
					for _, table := range []string{"bad", `"é"`} {
						q := "CREATE TABLE " + table + "(v " + head + "(" + tc.number + "))"
						message := `invalid input syntax for type integer: "` + tc.raw + `"`
						if tc.code == "22003" {
							message = `value "` + tc.raw + `" is out of range for type integer`
						}
						if tc.code == "22023" {
							message = "TIMESTAMP(-2147483648) precision must not be negative"
						}
						if tc.code == "42601" {
							message = "type modifiers must be simple constants or identifiers"
						}
						for _, prepared := range []bool{false, true} {
							s := fixture.ImplicitError(q, fixture.Diagnostic{Code: tc.code, Message: message, Position: "20"})
							s.Prepared, s.ErrorStage = prepared, "execute"
							run(s)
						}
						run(fixture.Exec("CREATE TABLE "+table+"(v TIMESTAMP)", 0))
						for _, prepared := range []bool{false, true} {
							s := fixture.Exec("CREATE TABLE IF NOT EXISTS "+table+"(v "+head+"("+tc.number+"))", 0)
							s.Prepared = prepared
							run(s)
						}
						run(fixture.Select("SELECT v FROM "+table, []string{"v"}, []string{"TIMESTAMP"}, [][]any{}))
						if tc.number == ".5" {
							position := "46"
							if head != `"timestamp"` {
								position = "52"
							}
							for _, prepared := range []bool{false, true} {
								s := fixture.ImplicitError("CREATE TABLE IF NOT EXISTS "+table+"(v "+head+"(1.5foo))", fixture.Diagnostic{Code: "42601", Message: `trailing junk after numeric literal at or near "1.5foo"`, Position: position})
								s.Prepared, s.ErrorStage = prepared, "execute"
								if prepared {
									s.ErrorStage = "prepare"
								}
								run(s)
							}
						}
						run(fixture.Exec("DROP TABLE "+table, 0))
					}
				}
			}
		})
	}
}
