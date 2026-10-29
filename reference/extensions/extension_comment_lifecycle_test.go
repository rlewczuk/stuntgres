package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceExtensionCommentLifecycle(t *testing.T) {
	for _, removed := range []bool{false, true} {
		t.Run(fmt.Sprintf("removed=%v", removed), func(t *testing.T) {
			fixture.SchemaReferenceCleanup(t, "comment_ext_move")
			fixture.RoleReferenceCleanup(t, []string{"comment_ext_owner", "comment_ext_successor"})
			extensionCommentCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c, peer *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec("CREATE USER comment_ext_owner SUPERUSER", 0))
				run(fixture.Exec("CREATE USER comment_ext_successor", 0))
				run(fixture.Exec("SET SESSION AUTHORIZATION comment_ext_owner", 0))
				defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
				run(fixture.Exec("CREATE EXTENSION fuzzystrmatch VERSION '1.1'", 0))
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				oid := extensionCommentOID(t, p, c, "fuzzystrmatch")
				literal, want := "'custom 雪 ''quoted'''", any("custom 雪 'quoted'")
				if removed {
					literal, want = "NULL", nil
				}
				run(fixture.Exec("COMMENT ON EXTENSION fuzzystrmatch IS "+literal, 0))
				var notices []fixture.Diagnostic
				_, noticeConn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
				for _, tc := range []struct{ sql, code, message string }{
					{"CREATE EXTENSION IF NOT EXISTS fuzzystrmatch VERSION '1.1'", "42710", `extension "fuzzystrmatch" already exists, skipping`},
					{"ALTER EXTENSION fuzzystrmatch UPDATE TO '1.1'", "00000", `version "1.1" of extension "fuzzystrmatch" is already installed`},
				} {
					notices = nil
					fixture.RunStep(t, p.Ctx, noticeConn, recorder, fixture.Exec(tc.sql, 0))
					fixture.NoticeWitnesses(t, b.PG, notices, fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: tc.code, Message: tc.message})
					extensionCommentValue(t, p, b, c, oid, want)
				}
				run(fixture.Exec("ALTER EXTENSION fuzzystrmatch UPDATE TO '1.2'", 0))
				run(fixture.Select("SELECT extversion FROM pg_extension WHERE extname='fuzzystrmatch'", []string{"extversion"}, []string{"TEXT"}, [][]any{{"1.2"}}))
				run(fixture.Exec("CREATE SCHEMA comment_ext_move", 0))
				run(fixture.Exec("ALTER EXTENSION fuzzystrmatch SET SCHEMA comment_ext_move", 0))
				run(fixture.Exec("CREATE TABLE comment_ext_move.extra(v INT)", 0))
				for _, verb := range []string{"ADD", "DROP"} {
					run(fixture.Exec("ALTER EXTENSION fuzzystrmatch "+verb+" TABLE comment_ext_move.extra", 0))
					extensionCommentValue(t, p, b, c, oid, want)
				}
				run(fixture.Exec("REASSIGN OWNED BY comment_ext_owner TO comment_ext_successor", 0))
				if after := extensionCommentOID(t, p, c, "fuzzystrmatch"); after != oid {
					t.Fatal("identity changed", oid, after)
				}
				extensionCommentValue(t, p, b, c, oid, want)
				run(fixture.Select("SELECT rolname FROM pg_roles WHERE oid=(SELECT extowner FROM pg_extension WHERE extname='fuzzystrmatch')", []string{"rolname"}, []string{"NAME"}, [][]any{{"comment_ext_successor"}}))
				run(fixture.Exec("SET SESSION AUTHORIZATION comment_ext_successor", 0))
				run(fixture.Exec("COMMENT ON EXTENSION fuzzystrmatch IS "+literal, 0))
				extensionCommentValue(t, p, b, c, oid, want)
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				run(fixture.Exec("ALTER USER comment_ext_owner NOSUPERUSER", 0))
				run(fixture.Exec("SET SESSION AUTHORIZATION comment_ext_owner", 0))
				run(fixture.ImplicitError("COMMENT ON EXTENSION fuzzystrmatch IS 'former owner'", fixture.Diagnostic{Code: "42501", Message: "must be owner of extension fuzzystrmatch"}))
				extensionCommentValue(t, p, b, c, oid, want)
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				run(fixture.Select("SELECT comment_ext_move.soundex('Anne')", []string{"soundex"}, []string{"TEXT"}, [][]any{{"A500"}}))
				run(fixture.Exec("DROP EXTENSION fuzzystrmatch", 0))
				extensionCommentValue(t, p, b, c, oid, nil)
				run(fixture.Exec("CREATE EXTENSION fuzzystrmatch VERSION '1.1'", 0))
				fresh := extensionCommentOID(t, p, c, "fuzzystrmatch")
				if fresh == oid {
					t.Fatal("drop/recreate reused identity", oid)
				}
				extensionCommentValue(t, p, b, c, fresh, extensionCommentDefault)
			})
		})
	}
}
