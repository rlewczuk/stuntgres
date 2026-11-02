package reference_test

import (
	"database/sql"
	"testing"

	"github.com/lib/pq"
	"stuntgres/reference/fixture"
)

func TestReferencePasswordCheckFeatureSmoke(t *testing.T) {
	passwordCheckScenario(t, []string{"pc_smoke", "pc_smoke_short", "pc_smoke_control"}, func(p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, notices *[]fixture.Diagnostic) {
		run := passwordCheckRun(t, p, c, r, false)
		passwordCheckInventory(t, p, b, c, r, false)
		run(passwordCheckShort("CREATE ROLE pc_smoke_short PASSWORD 'Abcdef1'", 8))
		run(fixture.RoleTransactionExists("pc_smoke_short", false))
		run(fixture.Exec("CREATE ROLE pc_smoke LOGIN CONNECTION LIMIT 1 PASSWORD 'Abcdef12'", 0))
		passwordCheckPlainCredential(t, p, b, "pc_smoke", "Abcdef12")
		oid := passwordCheckOID(t, p, c, "pc_smoke")
		var before sql.NullString
		if b.PG { before = passwordCheckReadCredential(t, p, b, "pc_smoke") }
		query := "ALTER ROLE pc_smoke NOLOGIN CONNECTION LIMIT 0 PASSWORD 'Abcdef12'"
		stmt, err := c.PrepareContext(p.Ctx, query)
		if err != nil { t.Fatal("held ALTER preparation", err) }
		defer stmt.Close()
		run(fixture.Exec("SET passwordcheck.min_password_length=12", 0))
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}, r, passwordCheckShort(query, 12))
		if after := passwordCheckOID(t, p, c, "pc_smoke"); after != oid { t.Fatal("failed ALTER changed identity", oid, after) }
		passwordCheckCredential(t, p, b, "pc_smoke", before)
		if b.PG {
			run(fixture.Prepared(fixture.Select("SELECT rolcanlogin,rolconnlimit FROM pg_authid WHERE rolname='pc_smoke'", []string{"rolcanlogin", "rolconnlimit"}, []string{"BOOL", "INT4"}, [][]any{{true, int64(1)}})))
		}
		*notices = nil
		run(passwordCheckError("ALTER ROLE pc_smoke PASSWORD "+pq.QuoteLiteral(passwordCheckMD5("pc_smoke", "pc_smoke")), "password must not equal user name", ""))
		passwordCheckNotices(t, *notices)
		passwordCheckCredential(t, p, b, "pc_smoke", before)
		run(fixture.Exec("ALTER ROLE pc_smoke PASSWORD NULL", 0))
		passwordCheckCredential(t, p, b, "pc_smoke", sql.NullString{})
		passwordCheckNotices(t, *notices)
		passwordCheckControls(t, p, b, c, r, true, "pc_smoke_control")
	})
}
