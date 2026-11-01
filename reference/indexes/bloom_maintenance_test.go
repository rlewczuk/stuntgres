package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func bloomDML(run func(fixture.Step), b fixture.Backend, table string) {
	run(fixture.Exec("INSERT INTO "+table+" VALUES(9,'z')", 1))
	run(fixture.Prepared(fixture.Exec("UPDATE "+table+" SET i=$1 WHERE i=$2", 3, 9, 7)))
	run(fixture.Exec("DELETE FROM "+table+" WHERE i=8", 2))
	bloomBoth(run, bloomSelect(b, "SELECT i,s FROM "+table, bloomFinal()))
	bloomBoth(run, bloomSelect(b, "SELECT i,s FROM "+table+" WHERE i=7", nil))
	bloomBoth(run, bloomSelect(b, "SELECT i,s FROM "+table+" WHERE i=9 AND s='a'", [][]any{{int64(9), "a"}, {int64(9), "a"}}))
}

func TestReferenceBloomMaintenance(t *testing.T) {
	bloomScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := bloomRun(t, p, b, c)
		run(fixture.Exec("CREATE EXTENSION bloom", 0))
		bloomSeed(run, "bloom_maintenance")
		run(fixture.Exec("CREATE INDEX bloom_maintenance_idx ON bloom_maintenance USING bloom(i,s) WITH(length=1,col1=1,col2=1)", 0))
		held := bloomHeld(t, p, c, "SELECT i,s FROM bloom_maintenance WHERE i=$1")
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("INSERT INTO bloom_maintenance VALUES(42,'private')", 1))
		run(fixture.Prepared(fixture.Exec("UPDATE bloom_maintenance SET i=6 WHERE i=7", 3)))
		private := [][]any{{int64(6), "a"}, {int64(6), "a"}, {int64(6), nil}}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, bloomSelect(b, held.SQL, private, 6))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, bloomSelect(b, held.SQL, nil, 7))
		run(fixture.Exec("SAVEPOINT bloom_save", 0))
		run(fixture.Exec("DELETE FROM bloom_maintenance WHERE i=8", 2))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, bloomSelect(b, held.SQL, nil, 8))
		run(fixture.Exec("ROLLBACK TO SAVEPOINT bloom_save", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, bloomSelect(b, held.SQL, [][]any{{int64(8), "b"}, {int64(8), "a"}}, 8))
		run(fixture.Exec("RELEASE SAVEPOINT bloom_save", 0))
		run(fixture.Exec("ROLLBACK", 0))
		bloomBoth(run, bloomSelect(b, "SELECT i,s FROM bloom_maintenance", bloomInitial()))

		// Native eager results outlive later publication; pq streams are closed
		// before writes, while the shared RR transaction proves historical SQL.
		var old *sql.Rows
		if !b.PG {
			ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder)
			var err error
			old, err = b.DB.QueryContext(ctx, "SELECT i,s FROM bloom_maintenance")
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
		}
		run(fixture.Exec("BEGIN ISOLATION LEVEL REPEATABLE READ", 0))
		bloomBoth(run, bloomSelect(b, "SELECT i,s FROM bloom_maintenance", bloomInitial()))
		writer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer writer.Close()
		bloomDML(bloomRun(t, p, b, writer), b, "bloom_maintenance")
		fixture.RunStep(t, p.Ctx, held, b.Recorder, bloomSelect(b, held.SQL, [][]any{{int64(7), "a"}, {int64(7), "a"}, {int64(7), nil}}, 7))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, bloomSelect(b, held.SQL, nil, 9))
		run(fixture.Exec("COMMIT", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, bloomSelect(b, held.SQL, [][]any{{int64(9), "a"}, {int64(9), "a"}, {int64(9), nil}, {int64(9), "z"}}, 9))
		if old != nil {
			fixture.DeleteReferenceConsume(t, false, old, nil, bloomSelect(b, "SELECT i,s FROM bloom_maintenance", bloomInitial()))
		}
		for _, s := range []fixture.Step{
			fixture.Exec("ALTER INDEX bloom_maintenance_idx SET(length=17,col1=3)", 0),
			fixture.Prepared(fixture.Exec("REINDEX INDEX bloom_maintenance_idx", 0)),
		} {
			run(s)
			bloomBoth(run, bloomSelect(b, "SELECT i,s FROM bloom_maintenance", bloomFinal()))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, bloomSelect(b, held.SQL, nil, 7))
		}
		run(fixture.Exec("DROP INDEX bloom_maintenance_idx", 0))
		run(fixture.Prepared(fixture.Exec("CREATE INDEX bloom_maintenance_idx ON bloom_maintenance USING bloom(i,s)", 0)))
		bloomBoth(run, bloomSelect(b, "SELECT i,s FROM bloom_maintenance WHERE i=9", [][]any{{int64(9), "a"}, {int64(9), "a"}, {int64(9), nil}, {int64(9), "z"}}))
	})
}
