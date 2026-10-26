package reference_test

import (
	"fmt"

	"stuntgres/reference/fixture"
)

func (s *physicalCase) loggingNumberCases(tc loggingSetting, prepared bool) {
	var valid []struct{ input, show, raw string }
	var bad, message string
	switch tc.name {
	case "log_min_duration_sample", "log_min_duration_statement":
		valid = []struct{ input, show, raw string }{{"-1", "-1", "-1"}, {"0", "0", "0"}, {"2147483647", "2147483647ms", "2147483647"}, {"1500ms", "1500ms", "1500"}, {"1.5s", "1500ms", "1500"}, {"0.5ms", "0", "0"}}
		bad = "-2"
		message = fmt.Sprintf(`-2 ms is outside the valid range for parameter "%s" (-1 ms .. 2147483647 ms)`, tc.name)
	case "log_parameter_max_length", "log_parameter_max_length_on_error":
		valid = []struct{ input, show, raw string }{{"-1", "-1", "-1"}, {"0", "0", "0"}, {"1073741823", "1073741823B", "1073741823"}, {"1kB", "1kB", "1024"}, {"1.5kB", "1536B", "1536"}}
		bad = "1073741824"
		message = fmt.Sprintf(`1073741824 B is outside the valid range for parameter "%s" (-1 B .. 1073741823 B)`, tc.name)
	case "log_temp_files":
		valid = []struct{ input, show, raw string }{{"-1", "-1", "-1"}, {"0", "0", "0"}, {"2147483647", "2147483647kB", "2147483647"}, {"2MB", "2MB", "2048"}}
		bad = "-2"
		message = `-2 kB is outside the valid range for parameter "log_temp_files" (-1 kB .. 2147483647 kB)`
	case "log_statement_sample_rate", "log_transaction_sample_rate":
		valid = []struct{ input, show, raw string }{{"0", "0", "0"}, {"1", "1", "1"}, {"0.125", "0.125", "0.125"}}
		bad = "1.1"
		message = fmt.Sprintf(`1.1 is outside the valid range for parameter "%s" (0 .. 1)`, tc.name)
	}
	for _, v := range valid {
		s.developerExec("SET "+tc.name+"="+physicalLiteral(v.input), prepared)
		s.loggingRead(tc, v.show, prepared)
		s.loggingMetadata(tc, v.raw, "session", prepared)
		s.loggingConfig(tc, v.input, v.show, false, prepared)
	}
	if len(valid) == 0 {
		s.t.Fatal("missing mutable numeric cases", tc.name)
	}
	last := valid[len(valid)-1]
	for _, function := range []bool{false, true} {
		query := "SET " + tc.name + "=" + physicalLiteral(bad)
		step := physicalError(query, physicalDiagnostic("22023", message), prepared, false)
		if function {
			step = physicalError("SELECT set_config($1,$2,$3)", physicalDiagnostic("22023", message), prepared, true, tc.name, bad, false)
			if tc.name == "log_parameter_max_length_on_error" {
				// The last valid value is 1536B, so PostgreSQL includes all
				// bound values; native simulated logging has no such effect.
				pg := physicalDiagnostic("22023", message)
				pg.Context = "unnamed portal with parameters: $1 = 'log_parameter_max_length_on_error', $2 = '1073741824', $3 = 'false'"
				step.PostgresDiagnostic = &pg
			}
		}
		s.run(step)
		s.loggingRead(tc, last.show, prepared)
		s.loggingMetadata(tc, last.raw, "session", prepared)
	}
	if tc.kind == "real" {
		for _, input := range []string{"NaN", "0.5x"} {
			s.run(physicalError("SET "+tc.name+"="+physicalLiteral(input), loggingInvalid(tc, input), prepared, false))
		}
		s.run(physicalError("SET "+tc.name+"='-0.1'", physicalDiagnostic("22023", fmt.Sprintf(`-0.1 is outside the valid range for parameter "%s" (0 .. 1)`, tc.name)), prepared, false))
	}
	step := fixture.Select("SELECT current_setting($1)", []string{"current_setting"}, []string{"TEXT"}, [][]any{{last.show}}, tc.name)
	step.Prepared = prepared
	s.run(step)
}
