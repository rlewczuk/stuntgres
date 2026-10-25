package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCompatibleSettingsValidation(t *testing.T) {
	for _, tc := range compatibleSettings {
		if tc.name == "allow_alter_system" {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			physicalCases(t, nil, func(s *physicalCase) {
				valid := []struct{ input, value string }{{"true", "on"}, {"false", "off"}, {"yes", "on"}, {"no", "off"}, {"on", "on"}, {"off", "off"}, {"1", "on"}, {"0", "off"}, {"TrUe", "on"}, {"FaLsE", "off"}}
				invalid := []string{"", "o", " true ", "2", "broken"}
				if tc.name == "backslash_quote" {
					valid = append(valid, struct{ input, value string }{"SAFE_ENCODING", "safe_encoding"})
					invalid = []string{"t", "of", "safe", "", " safe_encoding ", "broken"}
				} else {
					valid = append(valid, []struct{ input, value string }{{"t", "on"}, {"tr", "on"}, {"f", "off"}, {"y", "on"}, {"n", "off"}, {"of", "off"}}...)
				}
				for _, prepared := range []bool{false, true} {
					for _, pair := range valid {
						step := fixture.Exec("SET "+tc.name+"="+physicalLiteral(pair.input), 0)
						step.Prepared = prepared
						s.run(step)
						s.compatibleReads(tc, pair.value, prepared)
						step = clientText("SELECT set_config($1,$2,false)", "set_config", pair.value)
						step.Args, step.Prepared = []any{tc.name, pair.input}, prepared
						s.run(step)
					}
					step := fixture.Exec("SET "+tc.name+"="+physicalLiteral(tc.alternate), 0)
					step.Prepared = prepared
					s.run(step)
					for _, input := range invalid {
						d := physicalDiagnostic("22023", fmt.Sprintf("parameter %q requires a Boolean value", tc.name))
						if tc.name == "backslash_quote" {
							d.Message = fmt.Sprintf(`invalid value for parameter "backslash_quote": "%s"`, input)
							d.Hint = "Available values: safe_encoding, on, off."
						}
						s.run(physicalError("SET "+tc.name+"="+physicalLiteral(input), d, prepared, false))
						s.compatibleReads(tc, tc.alternate, prepared)
						s.compatibleMetadata(tc, tc.alternate, "session", prepared)
						step := physicalError("SELECT set_config($1,$2,false)", d, prepared, true)
						step.Args = []any{tc.name, input}
						s.run(step)
						s.compatibleReads(tc, tc.alternate, prepared)
						s.compatibleMetadata(tc, tc.alternate, "session", prepared)
					}
					s.run(fixture.Exec("RESET "+tc.name, 0))
				}
			})
		})
	}
}
