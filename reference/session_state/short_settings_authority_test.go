package reference_test

import (
	"context"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceShortSettingsAuthority(t *testing.T) {
	physicalCases(t, []string{"short_acl_login", "short_acl_member"}, func(s *physicalCase) {
		s.run(fixture.Exec("CREATE ROLE short_acl_login LOGIN PASSWORD 'role-core-fixture'", 0))
		s.run(fixture.Exec("CREATE ROLE short_acl_member", 0))
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			// Restore administrative authority even if an assertion stopped
			// execution while the owner had switched to the login role.
			if _, err := s.conn.ExecContext(ctx, "RESET ROLE"); err != nil { s.t.Error("role reset cleanup", err) }
			for _, d := range shortSettings {
				if d.context != "superuser" && d.name != "unix_socket_directories" { continue }
				for _, role := range []string{"short_acl_login", "short_acl_member"} {
					if _, err := s.conn.ExecContext(ctx, "REVOKE SET ON PARAMETER "+d.name+" FROM "+role); err != nil { s.t.Error("parameter privilege cleanup", d.name, role, err) }
				}
			}
			for _, query := range []string{
				"REVOKE pg_read_all_settings FROM short_acl_member",
				"REVOKE short_acl_member FROM short_acl_login",
			} {
				if _, err := s.conn.ExecContext(ctx, query); err != nil { s.t.Error("membership cleanup", err) }
			}
		}()
		ordinary := s.login("short_acl_login")
		s.run(fixture.Exec("GRANT short_acl_member TO short_acl_login WITH INHERIT TRUE", 0))
		for _, prepared := range []bool{false, true} {
			ordinary.shortVisible(false, prepared)
			for _, d := range shortSettings {
				if d.name == "unix_socket_directories" { continue }
				show, raw, source, _ := shortInitial(d, s.pg)
				ordinary.shortRead(d.name, show, raw, source, prepared)
			}
			for _, query := range []string{"SHOW unix_socket_directories", "SELECT current_setting('unix_socket_directories')"} {
				step := physicalError(query, physicalDenied("unix_socket_directories"), prepared, true)
				if prepared && query == "SHOW unix_socket_directories" { step.ErrorStage = "prepare" }
				ordinary.run(step)
			}
		}
		for _, d := range shortSettings {
			if d.context != "superuser" { continue }
			input := "on"; if d.kind == "enum" { input = "error" }
			for _, prepared := range []bool{false, true} {
				for _, query := range []string{"SET "+d.name+"='bogus'", "SET "+d.name+"=DEFAULT", "RESET "+d.name} { ordinary.run(physicalError(query, developerDenied(d.name), prepared, false)) }
				for _, value := range []any{"bogus", nil} { ordinary.run(physicalError("SELECT set_config($1,$2,false)", developerDenied(d.name), prepared, true, d.name, value)) }
				ordinary.shortRead(d.name, d.show, d.boot, "default", prepared)
				// Superuser authority and direct parameter grants use the same path.
				s.developerExec("SET "+d.name+"="+physicalLiteral(input), prepared)
				s.shortRead(d.name, input, input, "session", prepared)
				s.developerExec("RESET "+d.name, prepared)
				s.run(fixture.Exec("GRANT SET ON PARAMETER "+d.name+" TO short_acl_login", 0))
				ordinary.developerExec("SET "+d.name+"="+physicalLiteral(input), prepared)
				ordinary.shortRead(d.name, input, input, "session", prepared)
				config := fixture.Select("SELECT set_config($1,$2,false)", []string{"set_config"}, []string{"TEXT"}, [][]any{{input}}, d.name, input)
				config.Prepared = prepared; ordinary.run(config)
				query := "SET "+d.name+"="+physicalLiteral(input)
				held, err := ordinary.conn.PrepareContext(s.p.Ctx, query); if err != nil { s.t.Fatal(err) }
				s.run(fixture.Exec("REVOKE SET ON PARAMETER "+d.name+" FROM short_acl_login", 0))
				fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: ordinary.conn, Statement: held, SQL: query}, ordinary.recorder, physicalError(query, developerDenied(d.name), false, false))
				held.Close()
				ordinary.run(physicalError("RESET "+d.name, developerDenied(d.name), prepared, false))
				ordinary.developerExec("RESET ALL", prepared)
				ordinary.shortRead(d.name, d.show, d.boot, "default", prepared)
				// Inherited grant, followed by SET ROLE to the unprivileged login.
				s.run(fixture.Exec("GRANT SET ON PARAMETER "+d.name+" TO short_acl_member", 0))
				ordinary.developerExec(query, prepared)
				ordinary.developerExec("RESET ALL", prepared)
				s.developerExec("SET ROLE short_acl_login", prepared)
				s.developerExec(query, prepared)
				s.developerExec("RESET ALL", prepared)
				s.developerExec("RESET ROLE", prepared)
				s.run(fixture.Exec("REVOKE SET ON PARAMETER "+d.name+" FROM short_acl_member", 0))
				roleQuery := "SET "+d.name+"="+physicalLiteral(input)
				roleHeld, err := s.conn.PrepareContext(s.p.Ctx, roleQuery); if err != nil { s.t.Fatal(err) }
				s.developerExec("SET ROLE short_acl_login", prepared)
				fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: s.conn, Statement: roleHeld, SQL: roleQuery}, s.recorder, physicalError(roleQuery, developerDenied(d.name), false, false))
				roleHeld.Close()
				s.developerExec("RESET ROLE", prepared)
				ordinary.run(physicalError(query, developerDenied(d.name), prepared, false))
			}
		}
		s.run(fixture.Exec("GRANT SET ON PARAMETER unix_socket_directories TO short_acl_login", 0))
		ordinary.run(physicalError("SELECT current_setting('unix_socket_directories')", physicalDenied("unix_socket_directories"), false, true))
		ordinary.shortVisible(false, true)
		s.run(fixture.Exec("GRANT pg_read_all_settings TO short_acl_member", 0))
		ordinary.shortVisible(true, true)
		for _, d := range shortSettings { if d.name == "unix_socket_directories" {
			show, raw, source, _ := shortInitial(d, s.pg)
			ordinary.shortRead(d.name, show, raw, source, true)
		} }
	})
}

func TestReferenceShortSettingsStatistics(t *testing.T) {
	physicalCases(t, nil, func(s *physicalCase) {
		s.shortCreateSentinel()
		for _, prepared := range []bool{false, true} {
			for _, stage := range shortStages {
				s.developerExec("RESET ALL", prepared)
				query := "SET "+stage+"=on"
				held, err := s.conn.PrepareContext(s.p.Ctx, query); if err != nil { s.t.Fatal(err) }
				s.developerExec("SET log_statement_stats=on", prepared)
				statementValues := []string{"off", "off", "off", "on"}
				statementSources := []string{"default", "default", "default", "session"}
				s.shortSnapshot(statementValues, statementSources, prepared)
				s.run(physicalError(query, shortConflict(stage), prepared, false))
				s.shortSnapshot(statementValues, statementSources, prepared)
				s.run(physicalError("SELECT set_config($1,$2,$3)", shortConflict(stage), prepared, true, stage, "on", false))
				s.shortSnapshot(statementValues, statementSources, prepared)
				fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: s.conn, Statement: held, SQL: query}, s.recorder, physicalError(query, shortConflict(stage), false, false))
				held.Close()
				s.shortSnapshot(statementValues, statementSources, prepared)
				s.developerExec("BEGIN", prepared)
				s.developerExec("SAVEPOINT short_stats_save", prepared)
				s.run(physicalError("SET LOCAL "+stage+"=on", shortConflict(stage), prepared, false))
				s.developerExec("ROLLBACK TO short_stats_save", prepared)
				s.shortSnapshot(statementValues, statementSources, prepared)
				s.run(physicalError("SELECT set_config($1,$2,$3)", shortConflict(stage), prepared, true, stage, "on", true))
				s.developerExec("ROLLBACK TO short_stats_save", prepared)
				s.shortSnapshot(statementValues, statementSources, prepared)
				s.developerExec("SET LOCAL log_statement_stats=off", prepared)
				s.developerExec("SET LOCAL "+stage+"=on", prepared)
				s.developerExec("ROLLBACK", prepared)
				s.shortSnapshot(statementValues, statementSources, prepared)
				s.developerExec("RESET ALL", prepared)
				s.developerExec(query, prepared)
				values, sources := []string{"off", "off", "off", "off"}, []string{"default", "default", "default", "default"}
				for i, name := range shortStats { if name == stage { values[i], sources[i] = "on", "session" } }
				s.shortSnapshot(values, sources, prepared)
				for _, query := range []string{"SET log_statement_stats=on", "SET log_statement_stats=true"} {
					s.run(physicalError(query, shortConflict("log_statement_stats"), prepared, false))
					s.shortSnapshot(values, sources, prepared)
				}
				s.run(physicalError("SELECT set_config($1,$2,$3)", shortConflict("log_statement_stats"), prepared, true, "log_statement_stats", "on", false))
				s.shortSnapshot(values, sources, prepared)
				for _, query := range []string{"RESET log_statement_stats", "SET log_statement_stats=DEFAULT"} { s.developerExec(query, prepared) }
				step := fixture.Select("SELECT set_config('log_statement_stats',NULL,false)", []string{"set_config"}, []string{"TEXT"}, [][]any{{"off"}}); step.Prepared = prepared; s.run(step)
				s.shortSnapshot(values, sources, prepared)
				s.developerExec("BEGIN", prepared)
				s.developerExec("SAVEPOINT short_reverse_save", prepared)
				s.run(physicalError("SET LOCAL log_statement_stats=on", shortConflict("log_statement_stats"), prepared, false))
				s.developerExec("ROLLBACK TO short_reverse_save", prepared)
				s.shortSnapshot(values, sources, prepared)
				s.run(physicalError("SELECT set_config($1,$2,$3)", shortConflict("log_statement_stats"), prepared, true, "log_statement_stats", "on", true))
				s.developerExec("ROLLBACK TO short_reverse_save", prepared)
				s.shortSnapshot(values, sources, prepared)
				s.developerExec("SET LOCAL "+stage+"=off", prepared)
				local := fixture.Select("SELECT set_config('log_statement_stats','on',true)", []string{"set_config"}, []string{"TEXT"}, [][]any{{"on"}})
				local.Prepared = prepared; s.run(local)
				s.developerExec("ROLLBACK", prepared)
				s.shortSnapshot(values, sources, prepared)
			}
			s.developerExec("RESET ALL", prepared)
			for _, stage := range shortStages { s.developerExec("SET "+stage+"=on", prepared) }
			s.shortSnapshot([]string{"on", "on", "on", "off"}, []string{"session", "session", "session", "default"}, prepared)
			s.developerExec("BEGIN", prepared)
			s.developerExec("RESET ALL", prepared)
			s.shortSnapshot([]string{"off", "off", "off", "off"}, []string{"default", "default", "default", "default"}, prepared)
			s.developerExec("ROLLBACK", prepared)
			s.shortSnapshot([]string{"on", "on", "on", "off"}, []string{"session", "session", "session", "default"}, prepared)
			for _, stage := range shortStages { s.developerExec("SET "+stage+"=off", prepared) }
			s.developerExec("SET log_statement_stats=on", prepared)
			s.developerExec("RESET ALL", prepared)
		}
	})
}
