package reference_test

import (
	"strings"
	"testing"
)

func timezoneViewsReadOnlyDiagnostic(view, operation string) referenceDiagnostic {
	message := "cannot update view \"" + view + "\""
	action := "updating the view"
	switch operation {
	case "insert":
		message = "cannot insert into view \"" + view + "\""
		action = "inserting into the view"
	case "delete":
		message = "cannot delete from view \"" + view + "\""
		action = "deleting from the view"
	}
	detail := "Views that do not select from a single table or view are not automatically updatable."
	if view == "pg_timezone_abbrevs" {
		detail = "Views containing UNION, INTERSECT, or EXCEPT are not automatically updatable."
	}
	return referenceDiagnostic{
		Code: "55000", Message: message, Detail: detail,
		Hint: "To enable " + action + ", provide an INSTEAD OF " + strings.ToUpper(operation) + " trigger or an unconditional ON " + strings.ToUpper(operation) + " DO INSTEAD rule.",
	}
}

func TestReferenceTimezoneViewsDiagnostics(t *testing.T) {
	timezoneViewsScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c referenceExecutor) {
		for _, tc := range []struct{ query, column, position string }{
			{"SELECT bogus FROM pg_catalog.pg_timezone_names", "bogus", "8"},
			{"SELECT name FROM pg_catalog.pg_timezone_abbrevs", "name", "8"},
			{"SELECT /*é*/ bogus FROM pg_catalog.pg_timezone_names", "bogus", "14"},
		} {
			for _, prepared := range []bool{false, true} {
				t.Run(tc.query+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
					s := referenceImplicitError(tc.query, referenceDiagnostic{Code: "42703", Message: "column \"" + tc.column + "\" does not exist", Position: tc.position})
					s.selectRows, s.prepared, s.errorStage = true, prepared, "execute"
					if prepared {
						s.errorStage = "prepare"
					}
					referenceRunStep(t, p.ctx, c, b.recorder, s)
				})
			}
		}
	})
}

func TestReferenceTimezoneViewsReadOnly(t *testing.T) {
	timezoneViewsScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c referenceExecutor) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		run(referenceExec("CREATE TABLE timezone_write_sentinel(id int)", 0))
		run(referenceExec("INSERT INTO timezone_write_sentinel VALUES(7)", 1))
		for _, view := range []string{"pg_timezone_names", "pg_timezone_abbrevs"} {
			values := "('UTC',INTERVAL '0 seconds',false)"
			if view == "pg_timezone_names" {
				values = "('UTC','UTC',INTERVAL '0 seconds',false)"
			}
			for _, tc := range []struct{ operation, query string }{
				{"insert", "INSERT INTO pg_catalog." + view + " VALUES" + values},
				{"update", "UPDATE pg_catalog." + view + " SET is_dst=true WHERE abbrev='UTC'"},
				{"delete", "DELETE FROM pg_catalog." + view + " WHERE abbrev='UTC'"},
			} {
				for _, prepared := range []bool{false, true} {
					t.Run(view+"/"+tc.operation+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
						s := referenceImplicitError(tc.query, timezoneViewsReadOnlyDiagnostic(view, tc.operation))
						s.prepared, s.errorStage = prepared, "execute"
						if prepared {
							s.errorStage = "prepare"
						}
						referenceRunStep(t, p.ctx, c, b.recorder, s)
					})
				}
			}
		}
		run(referenceSelect("SELECT id FROM timezone_write_sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		run(timezoneViewsNames("SELECT * FROM pg_timezone_names WHERE name='UTC'", [][]any{{"UTC", "UTC", "00:00:00", false}}))
		run(timezoneViewsAbbrevs("SELECT * FROM pg_timezone_abbrevs WHERE abbrev='UTC'", [][]any{{"UTC", "00:00:00", false}}))
	})
}
