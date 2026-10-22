package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func statisticsSettingsWriteDiagnostic(verb, phrase string) fixture.Diagnostic {
	return fixture.Diagnostic{
		Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55000",
		Message: "cannot " + verb + ` view "pg_settings"`,
		Detail:  "Views that do not select from a single table or view are not automatically updatable.",
		Hint:    "To enable " + phrase + ", provide an INSTEAD OF " + statisticsSettingsCommand(verb) + " trigger or an unconditional ON " + statisticsSettingsCommand(verb) + " DO INSTEAD rule.",
	}
}

func statisticsSettingsCommand(verb string) string {
	switch verb {
	case "insert into":
		return "INSERT"
	case "delete from":
		return "DELETE"
	default:
		return "UPDATE"
	}
}

func TestReferenceStatisticsSettingsWriteBoundary(t *testing.T) {
	statisticsSettingsCases(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, pg bool, run statisticsSettingsRun) {
		for _, tc := range []struct{ query, verb, phrase string }{
			{"INSERT INTO pg_settings(name,setting) VALUES ('application_name','oracle')", "insert into", "inserting into the view"},
			{"DELETE FROM pg_settings WHERE name='application_name'", "delete from", "deleting from the view"},
			{"DELETE FROM pg_settings WHERE false", "delete from", "deleting from the view"},
			{"UPDATE pg_settings SET setting='oracle' WHERE name='application_name'", "update", "updating the view"},
			{"UPDATE pg_settings SET setting='oracle' WHERE false", "update", "updating the view"},
		} {
			for _, prepared := range []bool{false, true} {
				if pg && tc.verb == "update" {
					run(fixture.Exec("BEGIN", 0))
					// The rewrite rule returns no rows even though set_config changes state.
					s := fixture.Exec(tc.query, 0)
					if prepared {
						s = fixture.Prepared(s)
					}
					run(s)
					want := "oracle"
					if tc.query == "UPDATE pg_settings SET setting='oracle' WHERE false" {
						want = ""
					}
					run(fixture.Select("SELECT current_setting('application_name')", []string{"current_setting"}, []string{"TEXT"}, [][]any{{want}}))
					run(fixture.Exec("ROLLBACK", 0))
				} else {
					s := fixture.ErrorDiagnostic(tc.query, statisticsSettingsWriteDiagnostic(tc.verb, tc.phrase))
					if prepared {
						s = fixture.Prepared(s)
						s.ErrorStage = "prepare"
					} else {
						s.ErrorStage = "execute"
					}
					run(s)
				}
				run(fixture.Select("SELECT current_setting('application_name')", []string{"current_setting"}, []string{"TEXT"}, [][]any{{""}}))
			}
		}
		for _, prepared := range []bool{false, true} {
			for _, begin := range []string{"BEGIN", "BEGIN READ ONLY"} {
				run(fixture.Exec(begin, 0))
				defer conn.ExecContext(p.Ctx, "ROLLBACK")
				s := fixture.ErrorDiagnostic("INSERT INTO pg_settings(name,setting) VALUES ('application_name','broken')", statisticsSettingsWriteDiagnostic("insert into", "inserting into the view"))
				s.Prepared = prepared
				s.ErrorStage = "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				run(s)
				aborted := fixture.ImplicitError("SELECT current_setting('application_name')", fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block"))
				aborted.SelectRows = true
				run(aborted)
				run(fixture.Exec("ROLLBACK", 0))
				run(fixture.Select("SELECT setting FROM pg_settings WHERE name='application_name'", []string{"setting"}, []string{"TEXT"}, [][]any{{""}}))
			}
		}
		for _, query := range []string{"SET track_commit_timestamp='invalid'", "SET server_encoding='invalid'", "SET log_statement_stats='invalid'"} {
			if pg {
				continue
			} // PostgreSQL bootstrap may set the real logging collector.
			code, message := "55P02", `parameter "track_commit_timestamp" cannot be changed without restarting the server`
			if query == "SET server_encoding='invalid'" {
				message = `parameter "server_encoding" cannot be changed`
			}
			if query == "SET log_statement_stats='invalid'" {
				code, message = "42501", `permission denied to set parameter "log_statement_stats"`
			}
			run(fixture.ImplicitError(query, fixture.ControlsReferenceDiagnostic(code, message)))
		}
	})
}
