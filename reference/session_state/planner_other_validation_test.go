package reference_test

import (
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePlannerOtherValidation(t *testing.T) {
	for _, d := range plannerOtherSettings {
		t.Run(d.name, func(t *testing.T) {
			physicalCases(t, nil, func(s *physicalCase) {
				const setQuery = "SELECT set_config($1,$2,false)"
				held, err := s.conn.PrepareContext(s.p.Ctx, setQuery)
				if err != nil {
					s.t.Fatal(err)
				}
				defer held.Close()
				valid := []struct{ input, canonical string }{{d.first, d.first}, {d.second, d.second}, {d.boot, d.boot}}
				if d.min != "" {
					valid = append(valid, struct{ input, canonical string }{d.min, d.min}, struct{ input, canonical string }{d.max, d.max})
				}
				if d.name == "constraint_exclusion" {
					for _, alias := range []string{"true", "YES", "1", "false", "NO", "0", "ON", "OFF", "PARTITION"} {
						canonical := strings.ToLower(alias)
						switch canonical {
						case "true", "yes", "1":
							canonical = "on"
						case "false", "no", "0":
							canonical = "off"
						}
						valid = append(valid, struct{ input, canonical string }{alias, canonical})
					}
				}
				if d.name == "jit" {
					for _, alias := range []string{"true", "YES", "1", "false", "NO", "0", "ON", "OFF", "t", "f"} {
						canonical := "off"
						if alias == "true" || alias == "YES" || alias == "1" || alias == "ON" || alias == "t" {
							canonical = "on"
						}
						valid = append(valid, struct{ input, canonical string }{alias, canonical})
					}
				}
				if d.name == "plan_cache_mode" {
					valid = append(valid, struct{ input, canonical string }{"FORCE_GENERIC_PLAN", "force_generic_plan"})
				}
				bounds := func(value string) fixture.Diagnostic {
					return physicalDiagnostic("22023", fmt.Sprintf("%s is outside the valid range for parameter %q (%s .. %s)", value, d.name, d.min, d.max))
				}
				invalid := []struct {
					input      string
					diagnostic fixture.Diagnostic
				}{{"bogus", resourceInvalid(d.name, "bogus")}}
				switch d.kind {
				case "bool":
					invalid[0].diagnostic = physicalDiagnostic("22023", `parameter "jit" requires a Boolean value`)
					invalid = append(invalid, struct {
						input      string
						diagnostic fixture.Diagnostic
					}{"o", invalid[0].diagnostic})
				case "enum":
					invalid[0].diagnostic.Hint = "Available values: " + strings.ReplaceAll(strings.Trim(d.enums, "{}"), ",", ", ") + "."
				case "integer":
					invalid = append(invalid, struct {
						input      string
						diagnostic fixture.Diagnostic
					}{"0", bounds("0")})
					overflow := resourceInvalid(d.name, "2147483648")
					overflow.Hint = "Value exceeds integer range."
					invalid = append(invalid, struct {
						input      string
						diagnostic fixture.Diagnostic
					}{"2147483648", overflow})
					if d.name == "default_statistics_target" {
						invalid = append(invalid, struct {
							input      string
							diagnostic fixture.Diagnostic
						}{"10001", bounds("10001")})
					}
				case "real":
					for _, input := range []string{"NaN", "1e999"} {
						invalid = append(invalid, struct {
							input      string
							diagnostic fixture.Diagnostic
						}{input, resourceInvalid(d.name, input)})
					}
					if d.name == "cursor_tuple_fraction" {
						for _, input := range []string{"-1", "2"} {
							invalid = append(invalid, struct {
								input      string
								diagnostic fixture.Diagnostic
							}{input, bounds(input)})
						}
					} else {
						invalid = append(invalid, struct {
							input      string
							diagnostic fixture.Diagnostic
						}{"0", bounds("0")}, struct {
							input      string
							diagnostic fixture.Diagnostic
						}{"1000001", bounds("1e+06")})
					}
				}
				for _, prepared := range []bool{false, true} {
					for _, v := range valid {
						s.plannerCostSet(d.name, v.input, prepared)
						s.resourceValue(d.name, v.canonical, v.canonical, "session", prepared)
						step := fixture.Select("SELECT set_config($1,$2,false)", []string{"set_config"}, []string{"TEXT"}, [][]any{{v.canonical}}, d.name, v.input)
						step.Prepared = prepared
						s.run(step)
						fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: s.conn, Statement: held, SQL: setQuery}, s.recorder, fixture.Select(setQuery, []string{"set_config"}, []string{"TEXT"}, [][]any{{v.canonical}}, d.name, v.input))
					}
					s.plannerCostSet(d.name, d.first, prepared)
					for _, v := range invalid {
						s.run(physicalError("SET "+d.name+"="+physicalLiteral(v.input), v.diagnostic, prepared, false))
						s.resourceValue(d.name, d.first, d.first, "session", prepared)
						s.run(physicalError("SELECT set_config($1,$2,false)", v.diagnostic, prepared, true, d.name, v.input))
						s.resourceValue(d.name, d.first, d.first, "session", prepared)
						fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: s.conn, Statement: held, SQL: setQuery}, s.recorder, physicalError(setQuery, v.diagnostic, false, true, d.name, v.input))
						s.resourceValue(d.name, d.first, d.first, "session", prepared)
					}
				}
			})
		})
	}
}
