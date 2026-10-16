//go:build reference

package reference_test

import (
	"context"
	"database/sql"
	"net/url"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceSettingsPrivilegeAndTimeoutOracle(t *testing.T) {
	p := fixture.NewPair(t)
	ctx, cancel := context.WithTimeout(p.Ctx, 10*time.Second)
	defer cancel()
	if _, err := fixture.Admin.ExecContext(ctx, "CREATE ROLE settings_oracle_reader LOGIN PASSWORD 'settings_oracle_reader'"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := fixture.Admin.ExecContext(context.Background(), "DROP ROLE settings_oracle_reader"); err != nil {
			t.Error(err)
		}
	})
	u, err := url.Parse(fixture.DSN)
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword("settings_oracle_reader", "settings_oracle_reader")
	reader, err := sql.Open("stuntgres_reference_postgres", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	a, err := reader.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	for _, prepared := range []bool{false, true} {
		for _, q := range []string{"SET log_statement_stats TO garbage", "RESET log_statement_stats", "SET log_statement_stats TO DEFAULT"} {
			s := settingsReferenceError(q, "42501", `permission denied to set parameter "log_statement_stats"`, "", "")
			s.Prepared = prepared
			fixture.RunStep(t, ctx, a, p.PostgresRecord, s)
		}
	}
	c, err := p.Postgres.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, prepared := range []bool{false, true} {
		var sleep *sql.Stmt
		recorded := context.WithValue(ctx, fixture.RecorderKey{}, p.PostgresRecord)
		if prepared {
			sleep, err = c.PrepareContext(recorded, "SELECT pg_sleep(1)")
			if err != nil {
				t.Fatal("prepare at timeout0", err)
			}
		}
		fixture.RunStep(t, ctx, c, p.PostgresRecord, fixture.Exec("SET statement_timeout TO '20ms'", 0))
		step := settingsReferenceError("SELECT pg_sleep(1)", "57014", "canceling statement due to statement timeout", "", "")
		step.SelectRows = true
		if prepared {
			rows, err := sleep.QueryContext(recorded)
			if rows != nil {
				rows.Close()
			}
			sleep.Close()
			fixture.AssertDiagnostic(t, err, step.Diagnostic)
			t.Log("prepared pg_sleep: execute57014")
		} else {
			fixture.RunStep(t, ctx, c, p.PostgresRecord, step)
		}
		fixture.RunStep(t, ctx, c, p.PostgresRecord, fixture.Select("SHOW statement_timeout", []string{"statement_timeout"}, []string{"TEXT"}, [][]any{{"20ms"}}))
		fixture.RunStep(t, ctx, c, p.PostgresRecord, fixture.Exec("RESET statement_timeout", 0))
	}
}
