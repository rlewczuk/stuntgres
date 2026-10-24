package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceStatisticsSettingsSources(t *testing.T) {
	statisticsSettingsCases(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, pg bool, run statisticsSettingsRun) {
		var peer *sql.Conn
		for _, backend := range p.Backends() {
			if backend.PG == pg {
				var err error
				peer, err = backend.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer peer.Close()
			}
		}
		for _, prepared := range []bool{false, true} {
			statisticsSettingsTimeout(run, "0", "default", prepared)
			run(fixture.Exec("SET statement_timeout=0", 0))
			statisticsSettingsTimeout(run, "0", "session", prepared)
			run(fixture.Exec("RESET statement_timeout", 0))
			statisticsSettingsTimeout(run, "0", "default", prepared)
			run(fixture.Exec("SET statement_timeout='2s'", 0))
			statisticsSettingsTimeout(run, "2000", "session", prepared)
			var other string
			if err := peer.QueryRowContext(p.Ctx, "SELECT setting FROM pg_settings WHERE name='statement_timeout'").Scan(&other); err != nil || other != "0" {
				t.Fatal(other, err)
			}
			run(fixture.Exec("RESET ALL", 0))
			statisticsSettingsTimeout(run, "0", "default", prepared)
		}
		run(fixture.Exec("SET application_name='discarded'", 0))
		run(fixture.Exec("DISCARD ALL", 0))
		source := "default"
		run(fixture.Select("SELECT setting,source,reset_val FROM pg_settings WHERE name='application_name'", []string{"setting", "source", "reset_val"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"", source, ""}}))
	})
}

func TestReferenceStatisticsSettingsTransactions(t *testing.T) {
	statisticsSettingsCases(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, pg bool, run statisticsSettingsRun) {
		run(fixture.Exec("BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY", 0))
		defer conn.ExecContext(p.Ctx, "ROLLBACK")
		for _, tc := range []struct{ name, value string }{
			{"transaction_isolation", "repeatable read"},
			{"transaction_read_only", "on"},
			{"transaction_deferrable", "off"},
		} {
			run(fixture.Select("SELECT current_setting($1)", []string{"current_setting"}, []string{"TEXT"}, [][]any{{tc.value}}, tc.name))
			run(fixture.Prepared(fixture.Select("SELECT setting FROM pg_settings WHERE name=$1", []string{"setting"}, []string{"TEXT"}, [][]any{{tc.value}}, tc.name)))
		}
		run(fixture.Exec("SET LOCAL application_name='outer'", 0))
		run(fixture.Exec("SAVEPOINT stats_mark", 0))
		run(fixture.Exec("SET LOCAL application_name='inner'", 0))
		run(fixture.Select("SELECT setting,source,reset_val FROM pg_settings WHERE name='application_name'", []string{"setting", "source", "reset_val"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"inner", "session", ""}}))
		run(fixture.Exec("ROLLBACK TO stats_mark", 0))
		run(fixture.Select("SELECT setting,source FROM pg_settings WHERE name='application_name'", []string{"setting", "source"}, []string{"TEXT", "TEXT"}, [][]any{{"outer", "session"}}))
		// Native sources describe captured defaults and explicit SQL overrides;
		// transaction-owner projections do not create session overrides.
		want := [][]any{
			{"transaction_deferrable", "off", "default", "off", "off"},
			{"transaction_isolation", "repeatable read", "default", "read committed", "read committed"},
			{"transaction_read_only", "on", "default", "off", "off"},
		}
		if pg {
			// PostgreSQL gives these transaction-owner values special sources.
			want = [][]any{
				{"transaction_deferrable", "off", "override", "off", "off"},
				{"transaction_isolation", "repeatable read", "session", "read committed", "read committed"},
				{"transaction_read_only", "on", "session", "off", "off"},
			}
		}
		run(fixture.Select("SELECT name,setting,source,boot_val,reset_val FROM pg_settings WHERE name IN ('transaction_isolation','transaction_read_only','transaction_deferrable') ORDER BY name", []string{"name", "setting", "source", "boot_val", "reset_val"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, want))
		run(fixture.Exec("ROLLBACK", 0))
		run(fixture.Select("SELECT setting FROM pg_settings WHERE name='application_name'", []string{"setting"}, []string{"TEXT"}, [][]any{{""}}))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("SET LOCAL statement_timeout='1s'", 0))
		statisticsSettingsTimeout(run, "1000", "session", false)
		run(fixture.Exec("COMMIT AND CHAIN", 0))
		statisticsSettingsTimeout(run, "0", "default", true)
		run(fixture.Exec("ROLLBACK", 0))
		run(fixture.Exec("SET default_transaction_isolation='serializable'", 0))
		run(fixture.Exec("SET default_transaction_read_only=on", 0))
		run(fixture.Exec("SET default_transaction_deferrable=on", 0))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Select("SELECT name,setting FROM pg_settings WHERE name IN ('transaction_isolation','transaction_read_only','transaction_deferrable') ORDER BY name", []string{"name", "setting"}, []string{"TEXT", "TEXT"}, [][]any{{"transaction_deferrable", "on"}, {"transaction_isolation", "serializable"}, {"transaction_read_only", "on"}}))
		run(fixture.Exec("COMMIT AND CHAIN", 0))
		run(fixture.Select("SELECT setting FROM pg_settings WHERE name='transaction_read_only'", []string{"setting"}, []string{"TEXT"}, [][]any{{"on"}}))
		run(fixture.Exec("ROLLBACK AND CHAIN", 0))
		run(fixture.Select("SELECT setting FROM pg_settings WHERE name='transaction_isolation'", []string{"setting"}, []string{"TEXT"}, [][]any{{"serializable"}}))
		run(fixture.Exec("ROLLBACK", 0))
		run(fixture.Exec("RESET ALL", 0))
		run(fixture.Exec("SET ROLE NONE", 0))
		run(fixture.Select("SELECT current_user=session_user AS same_identity", []string{"same_identity"}, []string{"BOOL"}, [][]any{{true}}))
		run(fixture.Select("SELECT name FROM pg_settings WHERE name IN ('role','session_authorization')", []string{"name"}, []string{"TEXT"}, nil))
	})
}

func TestReferenceStatisticsSettingsConsumers(t *testing.T) {
	statisticsSettingsCases(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, pg bool, run statisticsSettingsRun) {
		query := "SELECT " + statisticsSettingsColumnsSQL + " FROM pg_settings WHERE name='statement_timeout'"
		stmt, err := conn.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, value := range []string{"1000", "2000"} {
			run(fixture.Exec("SET statement_timeout='"+value+"ms'", 0))
			statisticsSettingsTimeout(run, value, "session", false)
			statisticsSettingsTimeout(run, value, "session", true)
			rows, err := stmt.QueryContext(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			statisticsSettingsMetadata(t, rows, pg)
			if !pg {
				run(fixture.Exec("RESET statement_timeout", 0))
			}
			cells := make([]any, len(statisticsSettingsColumns))
			dest := make([]any, len(statisticsSettingsColumns))
			for i := range cells {
				dest[i] = &cells[i]
			}
			if !rows.Next() {
				rows.Close()
				t.Fatal("missing held row", rows.Err())
			}
			if err := rows.Scan(dest...); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			for i := range cells {
				cells[i] = fixture.Normalize(cells[i])
			}
			if !reflect.DeepEqual(cells, statisticsSettingsTimeoutRow(value, "session")) {
				t.Errorf("held row=%#v", cells)
			}
			if rows.Next() || rows.Err() != nil {
				t.Error("held tail", rows.Err())
			}
			rows.Close()
			if pg {
				run(fixture.Exec("RESET statement_timeout", 0))
			}
			statisticsSettingsTimeout(run, "0", "default", false)
		}
	})
}
