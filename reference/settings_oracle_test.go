//go:build reference

package reference_test

import (
	"context"
	"database/sql"
	"net/url"
	"testing"
	"time"
)

func TestReferenceSettingsPrivilegeAndTimeoutOracle(t *testing.T) {
	p := newReferencePair(t)
	ctx, cancel := context.WithTimeout(p.ctx, 10*time.Second)
	defer cancel()
	if _, err := referenceAdmin.ExecContext(ctx, "CREATE ROLE settings_oracle_reader LOGIN PASSWORD 'settings_oracle_reader'"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := referenceAdmin.ExecContext(context.Background(), "DROP ROLE settings_oracle_reader"); err != nil {
			t.Error(err)
		}
	})
	u, err := url.Parse(referenceDSN)
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
			s.prepared = prepared
			referenceRunStep(t, ctx, a, p.postgresRecord, s)
		}
	}
	c, err := p.postgres.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, prepared := range []bool{false, true} {
		var sleep *sql.Stmt
		recorded := context.WithValue(ctx, referenceRecorderKey{}, p.postgresRecord)
		if prepared {
			sleep, err = c.PrepareContext(recorded, "SELECT pg_sleep(1)")
			if err != nil {
				t.Fatal("prepare at timeout0", err)
			}
		}
		referenceRunStep(t, ctx, c, p.postgresRecord, referenceExec("SET statement_timeout TO '20ms'", 0))
		step := settingsReferenceError("SELECT pg_sleep(1)", "57014", "canceling statement due to statement timeout", "", "")
		step.selectRows = true
		if prepared {
			rows, err := sleep.QueryContext(recorded)
			if rows != nil {
				rows.Close()
			}
			sleep.Close()
			referenceAssertDiagnostic(t, err, step.diagnostic)
			t.Log("prepared pg_sleep: execute57014")
		} else {
			referenceRunStep(t, ctx, c, p.postgresRecord, step)
		}
		referenceRunStep(t, ctx, c, p.postgresRecord, referenceSelect("SHOW statement_timeout", []string{"statement_timeout"}, []string{"TEXT"}, [][]any{{"20ms"}}))
		referenceRunStep(t, ctx, c, p.postgresRecord, referenceExec("RESET statement_timeout", 0))
	}
}
