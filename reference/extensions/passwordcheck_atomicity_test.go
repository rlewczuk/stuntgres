package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"github.com/lib/pq"
	"stuntgres/reference/fixture"
)

func TestReferencePasswordCheckAtomicity(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%v", prepared), func(t *testing.T) {
			passwordCheckScenario(t, []string{"pc_atomic", "pc_atomic_parent", "pc_atomic_new"}, func(p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, notices *[]fixture.Diagnostic) {
				run := passwordCheckRun(t, p, c, r, prepared)
				stored := passwordCheckSCRAM(t, "role-attributes-fixture")
				run(fixture.Exec("CREATE ROLE pc_atomic_parent", 0))
				run(fixture.Exec("CREATE ROLE pc_atomic LOGIN CONNECTION LIMIT 1 PASSWORD "+pq.QuoteLiteral(stored)+" IN ROLE pc_atomic_parent", 0))
				run(fixture.Exec("COMMENT ON ROLE pc_atomic IS 'unchanged comment'", 0))
				oid := passwordCheckOID(t, p, c, "pc_atomic")
				check := func() {
					if after := passwordCheckOID(t, p, c, "pc_atomic"); after != oid { t.Fatal("failed password update changed role identity", oid, after) }
					passwordCheckCredential(t, p, b, "pc_atomic", sql.NullString{String: stored, Valid: true})
					run(fixture.Select("SELECT shobj_description(oid,'pg_authid') AS comment FROM pg_roles WHERE rolname='pc_atomic'", []string{"comment"}, []string{"TEXT"}, [][]any{{"unchanged comment"}}))
					run(fixture.Select("SELECT pg_has_role('pc_atomic','pc_atomic_parent','MEMBER') AS member", []string{"member"}, []string{"BOOL"}, [][]any{{true}}))
					if b.PG {
						run(fixture.Select("SELECT rolcanlogin,rolconnlimit FROM pg_authid WHERE rolname='pc_atomic'", []string{"rolcanlogin", "rolconnlimit"}, []string{"BOOL", "INT4"}, [][]any{{true, int64(1)}}))
					}
				}
				for _, password := range []string{"Abcdef1", passwordCheckMD5("pc_atomic", "pc_atomic"), passwordCheckSCRAM(t, "pc_atomic")} {
					*notices = nil
					query := "ALTER ROLE pc_atomic NOLOGIN CONNECTION LIMIT 0 PASSWORD "+pq.QuoteLiteral(password)
					if password == "Abcdef1" { run(passwordCheckShort(query, 8)) } else { run(passwordCheckError(query, "password must not equal user name", "")) }
					passwordCheckNotices(t, *notices)
					check()
				}
				// A real physical login and the sole-slot limit expose preserved
				// attributes even when the native catalog omits those columns.
				owner, err := fixture.RoleAttributesPhysical(t, p, b, "pc_atomic")
				if err != nil { t.Fatal("failed ALTER lost LOGIN or credential", err) }
				defer owner.Close()
				extra, err := fixture.RoleAttributesPhysical(t, p, b, "pc_atomic")
				if extra != nil { extra.Close(); t.Fatal("CONNECTION LIMIT 1 was not retained") }
				fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: "53300", Message: `too many connections for role "pc_atomic"`})
				if err := owner.Close(); err != nil { t.Fatal(err) }
				query := "CREATE ROLE pc_atomic_new LOGIN PASSWORD 'Abcdef1' IN ROLE pc_atomic_parent"
				*notices = nil
				run(passwordCheckShort(query, 8))
				passwordCheckNotices(t, *notices)
				run(fixture.RoleTransactionExists("pc_atomic_new", false))
				run(fixture.Select("SELECT count(*) AS edges FROM pg_auth_members JOIN pg_roles ON pg_auth_members.member=pg_roles.oid WHERE pg_roles.rolname='pc_atomic_new'", []string{"edges"}, []string{"INT8"}, [][]any{{int64(0)}}))
				run(fixture.Exec("CREATE ROLE pc_atomic_new PASSWORD 'Abcdef12'", 0))
				run(fixture.Select("SELECT pg_has_role('pc_atomic_new','pc_atomic_parent','MEMBER') AS member", []string{"member"}, []string{"BOOL"}, [][]any{{false}}))
				// Statement failure aborts the explicit transaction; recovering its
				// savepoint must not publish the rejected role candidate.
				run(fixture.Exec("BEGIN", 0))
				defer c.ExecContext(p.Ctx, "ROLLBACK")
				run(fixture.Exec("SAVEPOINT pc_password_save", 0))
				run(passwordCheckShort("ALTER ROLE pc_atomic NOLOGIN CONNECTION LIMIT 0 PASSWORD 'Abcdef1'", 8))
				s := fixture.ImplicitError("SELECT 1", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
				s.SelectRows = true
				if prepared { s.ErrorStage = "prepare" }
				run(s)
				run(fixture.Exec("ROLLBACK TO pc_password_save", 0))
				check()
				run(fixture.Exec("COMMIT", 0))
				check()
			})
		})
	}
}
