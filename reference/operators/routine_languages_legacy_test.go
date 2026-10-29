package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceRoutineLanguagesLegacyExtension(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"rl_extension_reader"})
	plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer routineLanguagesCleanup(t, c, "DROP EXTENSION IF EXISTS unaccent CASCADE", "DROP OWNED BY rl_extension_reader CASCADE")()
		userOperatorExec(t, p, b, c, "CREATE ROLE rl_extension_reader", "GRANT USAGE,CREATE ON SCHEMA public TO rl_extension_reader")
		var database string
		if err := c.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&database); err != nil {
			t.Fatal(err)
		}
		// No-handler syntax must use extension CREATE privilege, not the custom
		// procedural-language superuser gate. Quote the observed database safely.
		quoted := `"`
		for _, r := range database {
			if r == '"' {
				quoted += `"`
			}
			quoted += string(r)
		}
		quoted += `"`
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if _, err := c.ExecContext(ctx, "RESET SESSION AUTHORIZATION; REVOKE CREATE ON DATABASE "+quoted+" FROM rl_extension_reader"); err != nil {
				t.Error(err)
			}
		}()
		userOperatorExec(t, p, b, c, "SET SESSION AUTHORIZATION rl_extension_reader")
		d := fixture.ControlsReferenceDiagnostic("42501", `permission denied to create extension "unaccent"`)
		d.Hint = "Must have CREATE privilege on current database to create this extension."
		routineLanguagesError(t, p, b, c, "CREATE TRUSTED PROCEDURAL LANGUAGE unaccent", d, false, "execute")
		userOperatorExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT CREATE ON DATABASE "+quoted+" TO rl_extension_reader", "SET SESSION AUTHORIZATION rl_extension_reader", "CREATE TRUSTED PROCEDURAL LANGUAGE unaccent", "RESET SESSION AUTHORIZATION")
		plpgsqlValue(t, p, b, c, "SELECT extversion AS v FROM pg_extension WHERE extname='unaccent'", "TEXT", "1.1")
		plpgsqlValue(t, p, b, c, "SELECT unaccent('Hôtel') AS v", "TEXT", "Hotel")
		routineLanguagesCount(t, p, b, c, "unaccent", 0)
		routineLanguagesError(t, p, b, c, "CREATE LANGUAGE unaccent", fixture.ControlsReferenceDiagnostic("42710", `extension "unaccent" already exists`), false, "execute")
		var notices []fixture.Diagnostic
		_, noticeConn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		for _, prepared := range []bool{false, true} {
			s := fixture.Exec("CREATE OR REPLACE TRUSTED LANGUAGE unaccent", 0)
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, noticeConn, recorder, s)
		}
		want := []fixture.Diagnostic{
			{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42710", Message: `extension "unaccent" already exists, skipping`},
			{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42710", Message: `extension "unaccent" already exists, skipping`},
		}
		if !reflect.DeepEqual(notices, want) {
			t.Fatalf("legacy skip notices: %#v want %#v", notices, want)
		}
		routineLanguagesCount(t, p, b, c, "unaccent", 0)
		missing := fixture.ControlsReferenceDiagnostic("0A000", `extension "rl_missing_extension" is not available`)
		if b.PG {
			missing.Hint = "The extension must first be installed on the system where PostgreSQL is running."
		}
		routineLanguagesError(t, p, b, c, "CREATE LANGUAGE rl_missing_extension", missing, false, "execute")
		routineLanguagesCount(t, p, b, c, "rl_missing_extension", 0)
	})
}
