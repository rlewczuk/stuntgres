package reference_test

import (
	"context"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceStatisticsSettingsAuthority(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"statistics_metadata_reader"})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			if b.PG {
				t.Cleanup(func() {
					ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
					defer cancel()
					cleanup, err := b.DB.Conn(ctx)
					if err != nil {
						t.Error("pg_settings cleanup connection", err)
						return
					}
					defer cleanup.Close()
					for _, query := range []string{
						"RESET SESSION AUTHORIZATION",
						"GRANT SELECT ON pg_catalog.pg_settings TO PUBLIC",
						"DROP OWNED BY statistics_metadata_reader CASCADE",
					} {
						if _, err := cleanup.ExecContext(ctx, query); err != nil {
							t.Error("pg_settings cleanup", err)
						}
					}
				})
			}
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
			run(fixture.Exec("CREATE ROLE statistics_metadata_reader", 0))
			run(fixture.Exec("GRANT pg_monitor TO statistics_metadata_reader", 0))
			run(fixture.Exec("SET ROLE statistics_metadata_reader", 0))
			defer conn.ExecContext(context.Background(), "RESET ROLE")
			for _, prepared := range []bool{false, true} {
				s := fixture.Select("SELECT name,setting FROM pg_settings WHERE name='log_statement_stats'", []string{"name", "setting"}, []string{"TEXT", "TEXT"}, [][]any{{"log_statement_stats", "off"}})
				s.Prepared = prepared
				run(s)
				e := fixture.ImplicitError("SET log_statement_stats='invalid'", fixture.ControlsReferenceDiagnostic("42501", `permission denied to set parameter "log_statement_stats"`))
				e.Prepared = prepared
				e.ErrorStage = "execute"
				run(e)
				unchanged := fixture.Select("SELECT setting,source,boot_val,reset_val FROM pg_settings WHERE name='log_statement_stats'", []string{"setting", "source", "boot_val", "reset_val"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"off", "default", "off", "off"}})
				unchanged.Prepared = prepared
				run(unchanged)
			}
			run(fixture.Select("SHOW track_counts", []string{"track_counts"}, []string{"TEXT"}, [][]any{{"on"}}))
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION statistics_metadata_reader", 0))
			defer conn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			run(fixture.Select("SELECT current_user='statistics_metadata_reader' AS current_matches,session_user='statistics_metadata_reader' AS session_matches", []string{"current_matches", "session_matches"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
			run(fixture.Select("SELECT name FROM pg_settings WHERE name IN ('role','session_authorization')", []string{"name"}, []string{"TEXT"}, nil))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			{
				// Registered source ACLs govern direct and retained consumers.
				run(fixture.Exec("REVOKE SELECT ON pg_catalog.pg_settings FROM PUBLIC", 0))
				defer func() {
					conn.ExecContext(context.Background(), "RESET ROLE")
					conn.ExecContext(context.Background(), "REVOKE SELECT(name) ON pg_catalog.pg_settings FROM statistics_metadata_reader")
					conn.ExecContext(context.Background(), "GRANT SELECT ON pg_catalog.pg_settings TO PUBLIC")
				}()
				run(fixture.Exec("GRANT SELECT(name) ON pg_catalog.pg_settings TO statistics_metadata_reader", 0))
				run(fixture.Exec("SET ROLE statistics_metadata_reader", 0))
				run(fixture.Select("SELECT name FROM pg_settings WHERE name='application_name'", []string{"name"}, []string{"TEXT"}, [][]any{{"application_name"}}))
				const nameQuery = "SELECT name FROM pg_settings WHERE name='application_name'"
				held, err := conn.PrepareContext(p.Ctx, nameQuery)
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				for _, query := range []string{"SELECT setting FROM pg_settings WHERE false", "SELECT * FROM pg_settings WHERE false"} {
					s := fixture.ImplicitError(query, fixture.ControlsReferenceDiagnostic("42501", "permission denied for view pg_settings"))
					s.SelectRows = true
					s.ErrorStage = "execute"
					run(s)
				}
				run(fixture.Exec("RESET ROLE", 0))
				run(fixture.Exec("REVOKE SELECT(name) ON pg_catalog.pg_settings FROM statistics_metadata_reader", 0))
				run(fixture.Exec("SET ROLE statistics_metadata_reader", 0))
				denied := fixture.ImplicitError(nameQuery, fixture.ControlsReferenceDiagnostic("42501", "permission denied for view pg_settings"))
				denied.SelectRows, denied.ErrorStage = true, "execute"
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: held, SQL: nameQuery}, b.Recorder, denied)
				run(fixture.Exec("RESET ROLE", 0))
				run(fixture.Exec("GRANT SELECT ON pg_catalog.pg_settings TO PUBLIC", 0))
			}
			{
				// Table authority survives independent column revocation.
				const metadataQuery = "SELECT name,setting,boot_val,reset_val,source FROM pg_catalog.pg_settings WHERE name='log_statement_stats'"
				metadata := fixture.Select(metadataQuery, []string{"name", "setting", "boot_val", "reset_val", "source"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"log_statement_stats", "off", "off", "off", "default"}})
				held, err := conn.PrepareContext(p.Ctx, metadataQuery)
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				for _, query := range []string{
					"GRANT SELECT ON pg_catalog.pg_settings TO statistics_metadata_reader",
					"REVOKE SELECT ON pg_catalog.pg_settings FROM PUBLIC",
					"GRANT SELECT(name) ON pg_catalog.pg_settings TO statistics_metadata_reader",
					"REVOKE SELECT(name) ON pg_catalog.pg_settings FROM statistics_metadata_reader",
				} {
					for _, prepared := range []bool{false, true} {
						s := fixture.Exec(query, 0)
						s.Prepared = prepared
						run(s)
						run(metadata)
						run(fixture.Prepared(metadata))
						run(fixture.Exec("SET ROLE statistics_metadata_reader", 0))
						run(metadata)
						run(fixture.Prepared(metadata))
						fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: held, SQL: metadataQuery}, b.Recorder, metadata)
						run(fixture.Select("SELECT name FROM pg_catalog.pg_settings WHERE false", []string{"name"}, []string{"TEXT"}, nil))
						run(fixture.Exec("RESET ROLE", 0))
					}
				}
			}
		})
	}
}
