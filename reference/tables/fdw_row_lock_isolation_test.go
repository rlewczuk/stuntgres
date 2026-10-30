package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// This value is computed INSIDE the real enlisted source transaction, not from
// request fields or a callback counter. The same server/mapping also owns locks.
func TestReferenceFDWRowLockIsolation(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		fdwRowLockOwnerCharacteristics(t, prepared)
		for _, isolation := range []string{"READ COMMITTED", "REPEATABLE READ", "SERIALIZABLE"} {
			t.Run(fdwRowLockModeName(prepared)+"/"+isolation, func(t *testing.T) {
				fdwRowLockScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, source *sql.DB, run func(fixture.Step)) {
					fixture.LockExec(t, p.Ctx, source, "CREATE VIEW fdw_source.isolation AS SELECT 1::integer AS id,current_setting('transaction_isolation') AS label,(current_setting('transaction_read_only')='on') AS flag", 0)
					run(fixture.Exec("CREATE FOREIGN TABLE fdw_isolation(id integer,label text,flag boolean) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'isolation')", 0))
					run(fixture.Exec("BEGIN ISOLATION LEVEL "+isolation, 0))
					defer fdwRowLockRollback(t, c)
					want := "repeatable read"
					if isolation == "SERIALIZABLE" {
						want = strings.ToLower(isolation)
					}
					q := "SELECT id,label,flag FROM fdw_isolation"
					if prepared {
						stmt, err := c.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						for range 2 {
							rows, err := stmt.QueryContext(p.Ctx)
							fdwRowLockValues(t, fixture.RelationOutcome{Rows: rows, Err: err}, [][]any{{int64(1), want, false}})
						}
					} else {
						for range 2 {
							fdwRowLockClose(t, fdwRowLockRead(t, p.Ctx, c, q, false, [][]any{{int64(1), want, false}}))
						}
					}
					fdwRowLockClose(t, fdwRowLockRead(t, p.Ctx, c, "SELECT id,label,flag FROM fdw_data WHERE id=1 FOR UPDATE", prepared, [][]any{{int64(1), "one", true}}))
					run(fixture.Exec("ROLLBACK", 0))
				})
			})
		}
	}
}

func fdwRowLockOwnerCharacteristics(t *testing.T, prepared bool) {
	for _, late := range []bool{false, true} {
		for _, readonly := range []bool{false, true} {
			t.Run("owner/"+fdwRowLockModeName(prepared)+"/late="+fdwRowLockModeName(late)+"/readonly="+fdwRowLockModeName(readonly), func(t *testing.T) {
				fdwRowLockConfiguredScenario(t, late, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, source *sql.DB, _ *fdwSQLRowLocker, run func(fixture.Step)) {
					fixture.LockExec(t, p.Ctx, source, "CREATE VIEW fdw_source.characteristics AS SELECT 1::integer AS id,current_setting('transaction_isolation') AS label,(current_setting('transaction_read_only')='on') AS flag", 0)
					fixture.LockExec(t, p.Ctx, source, "CREATE VIEW fdw_source.deferrable AS SELECT 1::integer AS id,current_setting('transaction_deferrable') AS label,(current_setting('transaction_read_only')='on') AS flag", 0)
					run(fixture.Exec("CREATE FOREIGN TABLE fdw_characteristics(id integer,label text,flag boolean) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'characteristics')", 0))
					run(fixture.Exec("CREATE FOREIGN TABLE fdw_deferrable(id integer,label text,flag boolean) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'deferrable')", 0))
					begin := "BEGIN ISOLATION LEVEL READ COMMITTED READ WRITE"
					if readonly {
						begin = "BEGIN ISOLATION LEVEL SERIALIZABLE READ ONLY DEFERRABLE"
					}
					run(fixture.Exec(begin, 0))
					defer fdwRowLockRollback(t, c)
					isolation, access, deferrable := "repeatable read", false, "off"
					if !b.PG && late {
						isolation = "read committed"
					}
					if readonly {
						isolation = "serializable"
						if !b.PG {
							access, deferrable = true, "on"
						}
					}
					for range 2 {
						fdwRowLockClose(t, fdwRowLockRead(t, p.Ctx, c, "SELECT id,label,flag FROM fdw_characteristics", prepared, [][]any{{int64(1), isolation, access}}))
						fdwRowLockClose(t, fdwRowLockRead(t, p.Ctx, c, "SELECT id,label,flag FROM fdw_deferrable", prepared, [][]any{{int64(1), deferrable, access}}))
					}
					run(fixture.Exec("ROLLBACK", 0))
				})
			})
		}
	}
}
