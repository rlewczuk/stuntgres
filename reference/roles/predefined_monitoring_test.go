package reference_test

import (
	"database/sql"
	"path/filepath"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePredefinedMonitoringSettings(t *testing.T) {
	predefinedCases(t, []string{"pdef_actor"}, func(s *membershipScenario, db *sql.DB) {
		s.exec("SET application_name='pdef_monitor_witness'")
		if s.backend.PG {
			for _, role := range []string{"pg_read_all_settings", "pg_read_all_stats", "pg_stat_scan_tables"} {
				s.flags(role, "pg_monitor", s.bootstrap, 1, false, true, true)
			}
		}
		for _, role := range []string{"pg_monitor", "pg_read_all_settings", "pg_read_all_stats", "pg_stat_scan_tables"} {
			s.exec("GRANT " + role + " TO pdef_actor")
			s.actor("pdef_actor")
			s.exec("SET ROLE " + role)
			predefinedSelect(s, "SHOW lc_messages", []string{"lc_messages"}, []string{"TEXT"}, [][]any{{"C"}})
			predefinedSelect(s, "SELECT current_setting('application_name')", []string{"current_setting"}, []string{"TEXT"}, [][]any{{"pdef_monitor_witness"}})
			for _, setting := range []string{"lc_messages", "log_statement_stats"} {
				value := "'POSIX'"
				if setting == "log_statement_stats" {
					value = "on"
				}
				predefinedDenied(s, "SET "+setting+"="+value, `permission denied to set parameter "`+setting+`"`, false)
			}
			predefinedSelect(s, "SHOW lc_messages", []string{"lc_messages"}, []string{"TEXT"}, [][]any{{"C"}})
			predefinedSelect(s, "SHOW log_statement_stats", []string{"log_statement_stats"}, []string{"TEXT"}, [][]any{{"off"}})
			s.actor("")
			s.exec("REVOKE " + role + " FROM pdef_actor")
		}
		if !s.backend.PG {
			s.exec("GRANT pg_monitor TO pdef_actor")
			s.actor("pdef_actor")
			predefinedFailure(s, "SELECT query FROM pg_stat_activity", fixture.Diagnostic{Code: "42P01", Message: `relation "pg_stat_activity" does not exist`, Position: "19"}, true, "prepare")
			predefinedMissingFunction(s, "SELECT pgrowlocks('t')", "pgrowlocks(unknown)")
			s.actor("")
			return
		}
		// Independent PG protected-read provider. Native has no such represented GUC.
		s.actor("pdef_actor")
		predefinedFailure(s, "SHOW data_directory", fixture.Diagnostic{Code: "42501", Message: `permission denied to examine "data_directory"`, Detail: `Only roles with privileges of the "pg_read_all_settings" role may examine this parameter.`}, true, "prepare")
		s.actor("")
		s.exec("GRANT pg_read_all_settings TO pdef_actor")
		s.actor("pdef_actor")
		rows, err := s.c.QueryContext(s.p.Ctx, "SHOW data_directory")
		if err != nil {
			s.t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil || len(columns) != 1 || columns[0] != "data_directory" {
			rows.Close()
			s.t.Fatal(columns, err)
		}
		types, err := rows.ColumnTypes()
		if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "TEXT" {
			rows.Close()
			s.t.Fatal(types, err)
		}
		var directory string
		if !rows.Next() {
			rows.Close()
			s.t.Fatal("missing protected setting")
		}
		if err := rows.Scan(&directory); err != nil || !filepath.IsAbs(directory) {
			rows.Close()
			s.t.Fatal(directory, err)
		}
		if rows.Next() || rows.Err() != nil {
			rows.Close()
			s.t.Fatal("protected setting multiplicity", rows.Err())
		}
		if err := rows.Close(); err != nil {
			s.t.Fatal(err)
		}
		s.actor("")
		s.exec("REVOKE pg_read_all_settings FROM pdef_actor")
		peer, err := db.Conn(s.p.Ctx)
		if err != nil {
			s.t.Fatal(err)
		}
		defer peer.Close()
		var pid int64
		if err := peer.QueryRowContext(s.p.Ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil || pid <= 0 {
			s.t.Fatal(pid, err)
		}
		fixture.RunStep(s.t, s.p.Ctx, peer, s.record, fixture.Select("SELECT 7 AS pdef_stats_witness", []string{"pdef_stats_witness"}, []string{"INT4"}, [][]any{{int64(7)}}))
		s.actor("pdef_actor")
		step := fixture.Select("SELECT query FROM pg_stat_activity WHERE pid=$1", []string{"query"}, []string{"TEXT"}, [][]any{{"<insufficient privilege>"}})
		step.Args = []any{pid}
		s.run(step)
		s.actor("")
		s.exec("GRANT pg_monitor TO pdef_actor")
		s.actor("pdef_actor")
		step.Rows = [][]any{{"SELECT 7 AS pdef_stats_witness"}}
		s.run(step)
		s.actor("")
	})
}
