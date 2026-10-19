package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceExtensionDropDiagnostics(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ext_drop_reader", "ext_drop_owner"})
	extensionDropCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		var notices []fixture.Diagnostic
		_, owner, recorder := fixture.NoticeReferencePool(t, p, pg, &notices)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, owner, recorder, s) }
		for _, prepared := range []bool{false, true} {
			s := extensionError("DROP EXTENSION ext_drop_missing", "42704", `extension "ext_drop_missing" does not exist`)
			s.Prepared, s.ErrorStage = prepared, "execute"
			run(s)
			s = fixture.Exec("DROP EXTENSION IF EXISTS ext_drop_missing", 0)
			s.Prepared = prepared
			run(s)
		}
		extensionDropNotices(t, notices, []fixture.Diagnostic{
			{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `extension "ext_drop_missing" does not exist, skipping`},
			{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `extension "ext_drop_missing" does not exist, skipping`},
		})
		notices = nil
		for _, tc := range []struct{ query, token, position string }{
			{"DROP EXTENSION", "", "15"},
			{"DROP EXTENSION public.unaccent", ".", "22"},
			{"DROP EXTENSION NULL", "NULL", "16"},
			{"DROP EXTENSION $1", "$1", "16"},
			{"DROP EXTENSION unaccent junk", "junk", "25"},
			{"DROP EXTENSION unaccent CASCADE RESTRICT", "RESTRICT", "33"},
		} {
			message := `syntax error at or near "` + tc.token + `"`
			if tc.token == "" {
				message = "syntax error at end of input"
			}
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: "42601", Message: message, Position: tc.position})
				s.Prepared, s.ErrorStage = prepared, "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				run(s)
			}
		}
		run(fixture.Exec("CREATE SCHEMA ext_addon", 0))
		run(fixture.Exec("CREATE USER ext_drop_reader", 0))
		run(fixture.Exec("CREATE USER ext_drop_owner", 0))
		var database string
		if err := owner.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&database); err != nil {
			t.Fatal(err)
		}
		quoted := extensionTempIdentifier(database)
		run(fixture.Exec("GRANT CREATE ON DATABASE "+quoted+" TO ext_drop_owner", 0))
		defer owner.ExecContext(p.Ctx, "REVOKE CREATE ON DATABASE "+quoted+" FROM ext_drop_owner")
		run(fixture.Exec("SET SESSION AUTHORIZATION ext_drop_owner", 0))
		run(fixture.Exec("CREATE EXTENSION unaccent SCHEMA ext_addon", 0))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		id := extensionTempCapture(t, p, owner)
		run(fixture.Exec("ALTER SCHEMA ext_addon OWNER TO ext_drop_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION ext_drop_reader", 0))
		for _, q := range []string{"DROP EXTENSION unaccent", "DROP EXTENSION IF EXISTS unaccent CASCADE"} {
			for _, prepared := range []bool{false, true} {
				s := extensionError(q, "42501", "must be owner of extension unaccent")
				s.Prepared, s.ErrorStage = prepared, "execute"
				run(s)
			}
		}
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		extensionTempPresent(t, p, c, r, id)
		for _, q := range []string{"DROP EXTENSION unaccent", "DROP EXTENSION IF EXISTS ext_drop_missing"} {
			run(fixture.Exec("BEGIN READ ONLY", 0))
			stmt, err := owner.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal("read-only Prepare", err)
			}
			stmt.Close()
			run(extensionError(q, "25006", "cannot execute DROP EXTENSION in a read-only transaction"))
			run(extensionError("SELECT 1", "25P02", "current transaction is aborted, commands ignored until end of transaction block"))
			run(fixture.Exec("ROLLBACK", 0))
		}
		run(fixture.Exec("SET SESSION AUTHORIZATION ext_drop_owner", 0))
		// Trusted installation members belong to the bootstrap superuser; namespace
		// ownership and CREATE permission are not extension DROP authority.
		run(fixture.Select("SELECT count(*) FROM pg_proc WHERE pronamespace=$1 AND proname='unaccent' AND proowner=10", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}, id.namespace))
		run(fixture.Prepared(fixture.Exec("DROP EXTENSION unaccent RESTRICT", 0)))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		extensionTempGone(t, p, c, r, id)
		extensionDropNotices(t, notices, nil)
	})
}
