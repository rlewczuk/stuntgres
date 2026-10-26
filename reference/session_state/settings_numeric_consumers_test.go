package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func settingsPostgresNumericConsumers(t *testing.T, p *fixture.Pair) {
	t.Helper()
	a, err := p.Postgres.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	for _, prepared := range []bool{false, true} {
		run := func(s fixture.Step) {
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, a, p.PostgresRecord, s)
			if s.State != "" {
				for _, tc := range []struct{ name, want string }{{"geqo_threshold", "9"}, {"random_page_cost", "4.94066e-324"}, {"bytea_output", "hex"}, {"statement_timeout", "0"}} {
					check := fixture.Select("SHOW "+tc.name, []string{tc.name}, []string{"TEXT"}, [][]any{{tc.want}})
					check.Prepared = prepared
					fixture.RunStep(t, p.Ctx, a, p.PostgresRecord, check)
				}
			}
		}
		for _, tc := range []struct{ name, input, want string }{
			{"geqo_threshold", "2", "2"}, {"geqo_threshold", "2.5", "2"}, {"geqo_threshold", "3.5", "4"}, {"geqo_threshold", "010", "8"}, {"geqo_threshold", "0xA", "10"},
			{"geqo_threshold", "010.5", "10"},
			{"geqo_threshold", "010e1", "100"}, {"geqo_threshold", "0x1.8p1", "3"}, {"geqo_threshold", " 2.5 ", "2"}, {"geqo_threshold", "9223372036854775808e-18", "9"},
			{"random_page_cost", "010", "10"}, {"random_page_cost", "0xA", "10"}, {"random_page_cost", "08.5", "8.5"}, {"random_page_cost", "0x1p0", "1"}, {"random_page_cost", " .5", "0.5"}, {"random_page_cost", "0e-999", "0"},
			{"random_page_cost", "1.25", "1.25"}, {"random_page_cost", "2.1234567", "2.12346"}, {"random_page_cost", "1e-3", "0.001"},
			{"random_page_cost", "0x1p-1074", "4.94066e-324"},
			{"vacuum_cost_delay", "1500us", "1500us"}, {"vacuum_cost_delay", "0.0001", "0.1us"}, {"vacuum_cost_delay", "0.0001ms", "0"},
			{"bytea_output", "HEX", "hex"},
		} {
			run(fixture.Exec("SET "+tc.name+" TO '"+tc.input+"'", 0))
			run(fixture.Select("SHOW "+tc.name, []string{tc.name}, []string{"TEXT"}, [][]any{{tc.want}}))
		}
		for _, s := range []fixture.Step{settingsReferenceError("SET bytea_output TO 'bad'", "22023", `invalid value for parameter "bytea_output": "bad"`, "Available values: escape, hex.", "")} {
			run(s)
		}
		run(settingsReferenceError("SET geqo_threshold TO '1'", "22023", `1 is outside the valid range for parameter "geqo_threshold" (2 .. 2147483647)`, "", ""))
		run(settingsReferenceError("SET geqo_threshold TO '1kB'", "22023", `invalid value for parameter "geqo_threshold": "1kB"`, "", ""))
		run(settingsReferenceError("SET random_page_cost TO 'NaN'", "22023", `invalid value for parameter "random_page_cost": "NaN"`, "", ""))
		for _, input := range []string{"08.5", "0x1p0"} {
			run(settingsReferenceError("SET geqo_threshold TO '"+input+"'", "22023", `invalid value for parameter "geqo_threshold": "`+input+`"`, "", ""))
		}
		run(settingsReferenceError("SET random_page_cost TO '0x1.1p-1074'", "22023", `invalid value for parameter "random_page_cost": "0x1.1p-1074"`, "", ""))
		run(settingsReferenceError("SET random_page_cost TO '1e-320'", "22023", `invalid value for parameter "random_page_cost": "1e-320"`, "", ""))
		run(settingsReferenceError("SET random_page_cost TO 'Infinity'", "22023", `Infinity is outside the valid range for parameter "random_page_cost" (0 .. 1.79769e+308)`, "", ""))
		for _, input := range []string{"+.5", "-.5", " .5", "0b10", "0o10", "1_000", "1e", "0x", "0x1p-1074", "1e999", "Infinity", "-inf"} {
			run(settingsReferenceError("SET geqo_threshold TO '"+input+"'", "22023", `invalid value for parameter "geqo_threshold": "`+input+`"`, "", ""))
		}
		run(settingsReferenceError("SET geqo_threshold TO '.5'", "22023", `0 is outside the valid range for parameter "geqo_threshold" (2 .. 2147483647)`, "", ""))
		for _, input := range []string{"0b10", "0o10", "1_000", "1e", "0x", "1e-999", "1e999"} {
			run(settingsReferenceError("SET random_page_cost TO '"+input+"'", "22023", `invalid value for parameter "random_page_cost": "`+input+`"`, "", ""))
		}
		run(settingsReferenceError("SET random_page_cost TO '-inf'", "22023", `-Infinity is outside the valid range for parameter "random_page_cost" (0 .. 1.79769e+308)`, "", ""))
		for _, input := range []string{"08.5", "0x1p0"} {
			run(settingsReferenceError("SET statement_timeout TO '"+input+"'", "22023", `invalid value for parameter "statement_timeout": "`+input+`"`, `Valid units for this parameter are "us", "ms", "s", "min", "h", and "d".`, ""))
		}
		for _, name := range []string{"geqo_threshold", "random_page_cost", "vacuum_cost_delay", "bytea_output"} {
			run(fixture.Exec("RESET "+name, 0))
		}
	}
}
