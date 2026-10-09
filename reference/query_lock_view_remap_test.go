package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
)

func TestReferenceQueryLockViewTargetRemap(t *testing.T) {
	queryLockScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, a, peer *sql.Conn) {
		referenceLockExec(t, p.ctx, a, "CREATE VIEW unaliased AS SELECT id,v FROM t FOR UPDATE OF t SKIP LOCKED FOR SHARE OF t NOWAIT", 0)
		referenceLockExec(t, p.ctx, a, "CREATE VIEW wrapped AS SELECT id,v FROM unaliased", 0)
		stmt, err := a.PrepareContext(p.ctx, "SELECT id FROM wrapped WHERE id=1")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		referenceLockExec(t, p.ctx, a, "ALTER TABLE t RENAME TO renamed", 0)
		if !b.pg {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			if p.postgres != nil {
				if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
					t.Fatal("renamed emitted OF SQL failed replay", err)
				}
				referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceSelect("SELECT id FROM wrapped ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}}))
				if _, err := p.postgres.ExecContext(p.ctx, "DROP VIEW wrapped,unaliased; DROP TABLE renamed,u"); err != nil {
					t.Fatal(err)
				}
			}
		}
		rows, err := stmt.QueryContext(p.ctx)
		queryLockMigratedRows(t, rows, err, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}})
		holder := queryLockBegin(t, p.ctx, peer)
		queryLockRows(t, p, b, holder, "SELECT id FROM renamed WHERE id=1 FOR NO KEY UPDATE", false, "id", 1)
		queryLockNowait(t, p, b, a, "SELECT id FROM wrapped WHERE id=1", true, "renamed")
		if err := holder.Rollback(); err != nil {
			t.Fatal(err)
		}
		referenceRowAffected(t, p.ctx, a, "UPDATE renamed SET v=v WHERE id=1", true, 1)
	})
}
