package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceForeignActionsConcurrentNewRows(t *testing.T) {
	for _, direction := range []string{"DELETE", "UPDATE"} {
		for _, move := range []bool{false, true} {
			for _, isolation := range []sql.IsolationLevel{sql.LevelRepeatableRead, sql.LevelSerializable} {
				for _, prepared := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/move=%t/%s/prepared=%t", direction, move, isolation, prepared), func(t *testing.T) {
						foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
							run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
							run(fixture.Exec("CREATE TABLE c(id INT PRIMARY KEY,pid INT CONSTRAINT fk REFERENCES p ON DELETE CASCADE ON UPDATE CASCADE,note TEXT)", 0))
							run(fixture.Exec("INSERT INTO p VALUES(1),(9)", 2))
							if move {
								run(fixture.Exec("INSERT INTO c VALUES(10,9,'before')", 1))
							}
							a, z := fixture.LockConnections(t, p.Ctx, b.DB)
							reader, err := a.BeginTx(p.Ctx, &sql.TxOptions{Isolation: isolation})
							if err != nil {
								t.Fatal(err)
							}
							defer reader.Rollback()
							step := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, reader, b.Recorder, s) }
							step(foreignKeyIntRows("SELECT id FROM p ORDER BY id", "id", 1, 9))
							var oldRows [][]any
							if move {
								oldRows = [][]any{{int64(10), int64(9), "before"}}
							}
							step(fixture.Select("SELECT id,pid,note FROM c", []string{"id", "pid", "note"}, []string{"INT4", "INT4", "TEXT"}, oldRows))
							step(fixture.Exec("SAVEPOINT before_action", 0))
							writer, err := z.BeginTx(p.Ctx, nil)
							if err != nil {
								t.Fatal(err)
							}
							defer writer.Rollback()
							q := "INSERT INTO c VALUES(10,1,'committed')"
							if move {
								q = "UPDATE c SET pid=1,note='committed' WHERE id=10"
							}
							fixture.RunStep(t, p.Ctx, writer, b.Recorder, fixture.Exec(q, 1))
							if err = writer.Commit(); err != nil {
								t.Fatal(err)
							}
							// This read independently proves A retains the old/empty child image.
							step(fixture.Select("SELECT id,pid,note FROM c", []string{"id", "pid", "note"}, []string{"INT4", "INT4", "TEXT"}, oldRows))
							q = "DELETE FROM p WHERE id=1"
							if direction == "UPDATE" {
								q = "UPDATE p SET id=2 WHERE id=1"
							}
							// REL_18_0 ri_PerformCheck uses latest test + transaction crosscheck
							// snapshots; execMain's crosscheck rejects the newly visible tuple.
							d := foreignActionsContext(foreignKeyError("40001", "could not serialize access due to concurrent update", "", "", ""), foreignActionsSQL("c", "pid", "CASCADE", direction))
							step(foreignKeyFail(q, d, prepared))
							failed := foreignKeyFail("SELECT id FROM p", foreignKeyError("25P02", "current transaction is aborted, commands ignored until end of transaction block", "", "", ""), false)
							failed.SelectRows = true
							step(failed)
							step(fixture.Exec("ROLLBACK TO before_action", 0))
							step(foreignKeyIntRows("SELECT id FROM p ORDER BY id", "id", 1, 9))
							step(fixture.Select("SELECT id,pid,note FROM c", []string{"id", "pid", "note"}, []string{"INT4", "INT4", "TEXT"}, oldRows))
							if err = reader.Commit(); err != nil {
								t.Fatal(err)
							}
							run(foreignKeyIntRows("SELECT id FROM p ORDER BY id", "id", 1, 9))
							run(fixture.Select("SELECT id,pid,note FROM c", []string{"id", "pid", "note"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(10), int64(1), "committed"}}))
						})
					})
				}
			}
		}
	}
}
