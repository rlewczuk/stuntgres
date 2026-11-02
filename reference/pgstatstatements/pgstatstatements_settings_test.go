package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgStatStatementsSettings(t *testing.T) {
	t.Run("metadata_and_diagnostics", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"pgss_settings_user"})
		pgssCase(t, true, func(s *pgssScenario) {
			s.both(fixture.Select("SELECT name,setting,context,vartype,boot_val,reset_val,min_val,max_val,pending_restart FROM pg_settings WHERE name LIKE 'pg_stat_statements.%' ORDER BY name",
				[]string{"name", "setting", "context", "vartype", "boot_val", "reset_val", "min_val", "max_val", "pending_restart"},
				[]string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "BOOL"}, [][]any{
					{"pg_stat_statements.max", "5000", "postmaster", "integer", "5000", "5000", "100", "1073741823", false},
					{"pg_stat_statements.save", "on", "sighup", "bool", "on", "on", nil, nil, false},
					{"pg_stat_statements.track", "none", "superuser", "enum", "top", "top", nil, nil, false},
					{"pg_stat_statements.track_planning", "off", "superuser", "bool", "off", "off", nil, nil, false},
					{"pg_stat_statements.track_utility", "on", "superuser", "bool", "on", "on", nil, nil, false},
				}))
			for _, c := range []struct{ q, code, message, hint string }{
				{"SET pg_stat_statements.track = 'invalid'", "22023", `invalid value for parameter "pg_stat_statements.track": "invalid"`, "Available values: none, top, all."},
				{"SET pg_stat_statements.track_planning = 'invalid'", "22023", `parameter "pg_stat_statements.track_planning" requires a Boolean value`, ""},
				{"SET pg_stat_statements.track_utility = 'invalid'", "22023", `parameter "pg_stat_statements.track_utility" requires a Boolean value`, ""},
				{"SET pg_stat_statements.max = 100", "55P02", `parameter "pg_stat_statements.max" cannot be changed without restarting the server`, ""},
				{"SET pg_stat_statements.save = off", "55P02", `parameter "pg_stat_statements.save" cannot be changed now`, ""},
			} {
				for _, prepared := range []bool{false, true} {
					step := fixture.ImplicitError(c.q, fixture.Diagnostic{Code: c.code, Message: c.message, Hint: c.hint})
					step.Prepared = prepared
					s.run(step)
				}
			}
			s.both(fixture.Select("SELECT current_setting('pg_stat_statements.track') AS track,current_setting('pg_stat_statements.max') AS max,current_setting('pg_stat_statements.track_planning') AS planning,current_setting('pg_stat_statements.track_utility') AS utility", []string{"track", "max", "planning", "utility"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"none", "5000", "off", "on"}}))
			s.exec("CREATE ROLE pgss_settings_user")
			s.exec("SET ROLE pgss_settings_user")
			for _, name := range []string{"track", "track_planning", "track_utility"} {
				value := "off"
				if name == "track" {
					value = "none"
				}
				s.run(fixture.ImplicitError("SET pg_stat_statements."+name+" = '"+value+"'", fixture.Diagnostic{Code: "42501", Message: `permission denied to set parameter "pg_stat_statements.` + name + `"`}))
			}
			s.exec("RESET ROLE")
		})
	})
	t.Run("effective_policy_and_local_restoration", func(t *testing.T) {
		pgssCase(t, true, func(s *pgssScenario) {
			s.exec("CREATE TABLE pgss_part.pgss_settings_rows(k integer)")
			s.run(fixture.Exec("INSERT INTO pgss_part.pgss_settings_rows VALUES(1)", 1))
			q := "SELECT k AS pgss_settings_value FROM pgss_part.pgss_settings_rows"
			step := fixture.Select(q, []string{"pgss_settings_value"}, []string{"INT4"}, [][]any{{int64(1)}})
			s.counts(q, 0, 0, 0)
			s.work(fixture.Exec("SET pg_stat_statements.track = none", 0))
			s.work(step)
			s.counts(q, 0, 0, 0)
			s.work(fixture.Exec("RESET pg_stat_statements.track", 0))
			s.work(fixture.Select("SHOW pg_stat_statements.track", []string{"pg_stat_statements.track"}, []string{"TEXT"}, [][]any{{"top"}}))
			s.work(step)
			s.counts(q, 1, 1, 1)
			s.work(fixture.Exec("BEGIN", 0))
			defer s.worker.ExecContext(s.p.Ctx, "ROLLBACK")
			s.work(fixture.Exec("SET LOCAL pg_stat_statements.track = none", 0))
			s.work(step)
			s.counts(q, 1, 1, 1)
			s.work(fixture.Exec("SAVEPOINT policy", 0))
			s.work(fixture.Exec("SET LOCAL pg_stat_statements.track = 'all'", 0))
			s.work(fixture.Exec("SET LOCAL pg_stat_statements.track_planning = on", 0))
			s.work(step)
			s.planCounts(q, 1, 1, 2, 2)
			s.work(fixture.Exec("ROLLBACK TO policy", 0))
			s.work(fixture.Select("SELECT current_setting('pg_stat_statements.track') AS track,current_setting('pg_stat_statements.track_planning') AS planning", []string{"track", "planning"}, []string{"TEXT", "TEXT"}, [][]any{{"none", "off"}}))
			s.work(step)
			s.counts(q, 1, 2, 2)
			s.work(fixture.Exec("COMMIT", 0))
			s.work(fixture.Select("SELECT current_setting('pg_stat_statements.track') AS track,current_setting('pg_stat_statements.track_planning') AS planning", []string{"track", "planning"}, []string{"TEXT", "TEXT"}, [][]any{{"top", "off"}}))
			s.work(step)
			s.planCounts(q, 1, 1, 3, 3)
		})
	})
}
