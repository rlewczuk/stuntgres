package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func autoExplainDomainCases(t *testing.T) {
	t.Run("complete_domains_and_metadata", func(t *testing.T) {
		autoExplainScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("LOAD 'auto_explain'", 0))
			for _, name := range []string{"log_analyze", "log_verbose", "log_buffers", "log_wal", "log_timing", "log_triggers", "log_nested_statements"} {
				for _, prepared := range []bool{false, true} {
					step := fixture.ErrorDiagnostic("SET auto_explain."+name+"='bogus'", fixture.ControlsReferenceDiagnostic("22023", fmt.Sprintf(`parameter "auto_explain.%s" requires a Boolean value`, name)))
					step.Prepared, step.ErrorStage = prepared, "execute"
					run(step)
				}
			}
			for _, tc := range []struct{ name, value, message, hint string }{
				{"sample_rate", "-0.1", `-0.1 is outside the valid range for parameter "auto_explain.sample_rate" (0 .. 1)`, ""},
				{"sample_rate", "bogus", `invalid value for parameter "auto_explain.sample_rate": "bogus"`, ""},
				{"log_min_duration", "2147483648", `invalid value for parameter "auto_explain.log_min_duration": "2147483648"`, "Value exceeds integer range."},
				{"log_parameter_max_length", "2147483648", `invalid value for parameter "auto_explain.log_parameter_max_length": "2147483648"`, "Value exceeds integer range."},
			} {
				d := fixture.ControlsReferenceDiagnostic("22023", tc.message)
				d.Hint = tc.hint
				for _, prepared := range []bool{false, true} {
					step := fixture.ErrorDiagnostic("SET auto_explain."+tc.name+"='"+tc.value+"'", d)
					step.Prepared, step.ErrorStage = prepared, "execute"
					run(step)
				}
			}
			rows, err := c.QueryContext(p.Ctx, "SHOW ALL")
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			seen := map[string]bool{}
			for rows.Next() {
				var name, value, description string
				if err := rows.Scan(&name, &value, &description); err != nil {
					t.Fatal(err)
				}
				if strings.HasPrefix(name, "auto_explain.") && name != "auto_explain.log_settings" {
					if description == "" || seen[name] {
						t.Fatal("loaded metadata missing/duplicated", name, description)
					}
					seen[name] = true
				}
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			rows.Close()
			if len(seen) != 12 {
				t.Fatal("selected typed definitions missing from SHOW ALL", seen)
			}
		})
	})
}
