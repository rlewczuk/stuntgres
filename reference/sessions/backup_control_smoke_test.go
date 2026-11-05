package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceBackupControlFeatureSmoke(t *testing.T) {
	const role = "backup_smoke_reader"
	fixture.RoleReferenceCleanup(t, []string{role})
	recoveryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE ROLE "+role, 0))
		defer backupRestore(t, p, b, c, role)()
		run(fixture.Exec("CREATE TABLE backup_smoke_sentinel(v integer)", 0))
		run(fixture.Exec("INSERT INTO backup_smoke_sentinel VALUES(7)", 1))
		backupArithmeticWrappers(t, p, b, c)
		q := "SELECT pg_backup_start($1::text,true)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, backupSelect(b.PG, q, []string{"pg_backup_start"}, []string{"PG_LSN"}, [][]any{{nil}}, nil))
		run(fixture.Prepared(backupSelect(b.PG, "SELECT * FROM pg_backup_stop($1::boolean)", backupFields, backupTypes, [][]any{{nil, nil, nil}}, nil)))
		run(backupAbsent("SELECT * FROM pg_backup_stop(false)", b.PG))
		run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
		denied := recoveryControlDenied(q, "pg_backup_start")
		denied.Args = []any{"backup_feature_smoke"}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, denied)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		for _, id := range backupIdentities {
			run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+id+" TO "+role, 0))
		}
		run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
		backupPair(t, p, b, c, held, q, "backup_feature_smoke", "SELECT * FROM pg_backup_stop(false)", true, "backup_feature_smoke")
		run(backupAbsent("SELECT pg_backup_stop(false)", b.PG))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_backup_start(text,boolean) FROM "+role, 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, denied)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		backupPair(t, p, b, c, held, q, strings.Repeat("é", 512), "SELECT * FROM pg_backup_stop(false)", false, strings.Repeat("é", 512))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, backupError(q, "22023", "backup label too long (max 1024 bytes)", "", strings.Repeat("é", 512)+"a"))
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_backup_start(text,boolean) TO "+role, 0))
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
				t.Fatal(err)
			}
			run(fixture.Select("SELECT v FROM backup_smoke_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
			run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
			backupPair(t, p, b, c, held, q, "restored", "SELECT * FROM pg_backup_stop(false)", false, "restored")
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		}
	})
}
