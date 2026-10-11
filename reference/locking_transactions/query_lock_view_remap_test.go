package reference_test

import (
	"bytes"
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryLockViewTargetRemap(t *testing.T) {
	fixture.QueryLockScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
		fixture.LockExec(t, p.Ctx, a, "CREATE VIEW unaliased AS SELECT id,v FROM t FOR UPDATE OF t SKIP LOCKED FOR SHARE OF t NOWAIT", 0)
		fixture.LockExec(t, p.Ctx, a, "CREATE VIEW wrapped AS SELECT id,v FROM unaliased", 0)
		stmt, err := a.PrepareContext(p.Ctx, "SELECT id FROM wrapped WHERE id=1")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		fixture.LockExec(t, p.Ctx, a, "ALTER TABLE t RENAME TO renamed", 0)
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			if p.Postgres != nil {
				if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
					t.Fatal("renamed emitted OF SQL failed replay", err)
				}
				fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Select("SELECT id FROM wrapped ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}}))
				if _, err := p.Postgres.ExecContext(p.Ctx, "DROP VIEW wrapped,unaliased; DROP TABLE renamed,u"); err != nil {
					t.Fatal(err)
				}
			}
		}
		rows, err := stmt.QueryContext(p.Ctx)
		queryLockMigratedRows(t, rows, err, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}})
		holder := fixture.QueryLockBegin(t, p.Ctx, peer)
		fixture.QueryLockRows(t, p, b, holder, "SELECT id FROM renamed WHERE id=1 FOR NO KEY UPDATE", false, "id", 1)
		fixture.QueryLockNowait(t, p, b, a, "SELECT id FROM wrapped WHERE id=1", true, "renamed")
		if err := holder.Rollback(); err != nil {
			t.Fatal(err)
		}
		fixture.RowAffected(t, p.Ctx, a, "UPDATE renamed SET v=v WHERE id=1", true, 1)
	})
}
