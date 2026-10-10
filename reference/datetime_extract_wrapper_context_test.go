package reference_test

import (
	"database/sql"
	"testing"
)

// Context expectations were independently pinned by root's tests-5 and tests-8
// oracles, including both direct and prepared execution. No discovery filter remains.
func TestReferenceDatetimeExtractWrapperContext(t *testing.T) {
	datetimeExtractScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, step := range []referenceStep{
			referenceExec("SET application_name TO 'timezone'", 0),
			referenceExec("CREATE TABLE dx_wrapper_context(f text,s date)", 0),
			referenceExec("INSERT INTO dx_wrapper_context VALUES ('timezone',DATE '2000-01-01')", 1),
		} {
			referenceRunStep(t, p.ctx, c, b.recorder, step)
		}
		for _, tc := range []struct {
			name, q string
			args    []any
			wrapper bool
		}{
			{"field_column_date_literal", "SELECT date_part(f,DATE '2000-01-01') FROM dx_wrapper_context", nil, false},
			{"field_expression_date_literal", "SELECT date_part(lower(f),DATE '2000-01-01') FROM dx_wrapper_context", nil, false},
			{"field_literal_date_column", "SELECT date_part('timezone',s) FROM dx_wrapper_context", nil, false},
			{"field_column_date_parameter", "SELECT date_part(f,$1::date) FROM dx_wrapper_context", []any{"2000-01-01"}, false},
			{"both_literal_control", "SELECT date_part('timezone',DATE '2000-01-01')", nil, true},
			{"field_parameter_date_literal_control", "SELECT date_part($1::text,DATE '2000-01-01')", []any{"timezone"}, true},
			{"stable_field_date_literal", "SELECT date_part(CASE WHEN CURRENT_DATE::text IS NOT NULL THEN 'timezone' ELSE 'year' END,DATE '2000-01-01')", nil, false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				d := datetimeExtractFieldDiagnostic("0A000", "timezone", "timestamp without time zone")
				if tc.wrapper {
					d = datetimeExtractDateWrapperDiagnostic(d)
				}
				datetimeExtractFailure(t, p, b, c, tc.q, d, "execute", tc.args...)
			})
		}
		// Adjacent function/predicate admission is not expanded by extraction.
		// Keep the original pinned PG outcomes and exact existing native boundary.
		for _, tc := range []struct{ name, q, code, message, position string }{
			{"current_setting_boundary", "SELECT date_part(current_setting('application_name'),DATE '2000-01-01')", "0A000", "functions are unsupported", "18"},
			{"random_predicate_boundary", "SELECT date_part(CASE WHEN random()>-1 THEN 'timezone' ELSE 'year' END,DATE '2000-01-01')", "42601", `syntax error at or near ">"`, "36"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				d := datetimeExtractFieldDiagnostic("0A000", "timezone", "timestamp without time zone")
				stage := "execute"
				if !b.pg {
					d = controlsReferenceDiagnostic(tc.code, tc.message)
					d.Position = tc.position
					stage = "prepare"
				}
				datetimeExtractFailure(t, p, b, c, tc.q, d, stage)
			})
		}
	})
}
