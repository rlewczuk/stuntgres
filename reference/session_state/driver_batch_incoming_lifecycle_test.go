package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDriverBatchHoldTrueEnd(t *testing.T) {
	batchScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
		batchSeed(t, p, b, a)
		batchExec(t, p, b, a, "INSERT INTO batch_t VALUES(8)", 1)
		batchExec(t, p, b, a, "CREATE SEQUENCE batch_hold_seq START 51", 0)
		q := "DECLARE batch_hold CURSOR WITH HOLD FOR SELECT nextval('batch_hold_seq') AS n,i FROM batch_t ORDER BY i; SELECT last_value,is_called FROM batch_hold_seq"
		fixture.BatchSets(t, batchQuery(t, p, a, q), fixture.BatchSet{[]string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(51), false}}, false})
		fixture.BatchSets(t, batchQuery(t, p, a, "FETCH ALL FROM batch_hold"), fixture.BatchSet{[]string{"n", "i"}, []string{"INT8", "INT4"}, [][]any{{int64(51), int64(7)}, {int64(52), int64(8)}}, false})
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT last_value,is_called FROM batch_hold_seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(52), true}}))
		batchExec(t, p, b, a, "CLOSE batch_hold", 0)
		q = "DECLARE batch_hold_error CURSOR WITH HOLD FOR SELECT nextval('batch_hold_seq') AS n,i FROM batch_t ORDER BY i; SELECT missing FROM batch_t"
		w := batchDiag("42703", `column "missing" does not exist`)
		w.Position = batchPosition(q, "missing")
		batchError(t, p, a, q, w)
		batchError(t, p, a, "FETCH ALL FROM batch_hold_error", batchDiag("34000", `cursor "batch_hold_error" does not exist`))
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT last_value,is_called FROM batch_hold_seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(52), true}}))
	})
}

func TestReferenceDriverBatchForeignKeyComposition(t *testing.T) {
	for _, fail := range []bool{false, true} {
		name := "commit"
		if fail {
			name = "late_failure"
		}
		t.Run(name, func(t *testing.T) {
			batchScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
				q := "CREATE TABLE batch_fk_parent(id INT PRIMARY KEY); CREATE TABLE batch_fk_child(id INT,pid INT,CONSTRAINT batch_fk FOREIGN KEY(pid) REFERENCES batch_fk_parent(id)); INSERT INTO batch_fk_parent VALUES(1); INSERT INTO batch_fk_child VALUES(2,1); SELECT id,pid FROM batch_fk_child"
				w := batchDiag("23503", `insert or update on table "batch_fk_child" violates foreign key constraint "batch_fk"`)
				w.Detail = `Key (pid)=(99) is not present in table "batch_fk_parent".`
				w.Schema, w.Table, w.Constraint = "public", "batch_fk_child", "batch_fk"
				if fail {
					batchError(t, p, a, q+"; INSERT INTO batch_fk_child VALUES(3,99)", w)
					for _, table := range []string{"batch_fk_parent", "batch_fk_child"} {
						query := "SELECT id FROM " + table
						missing := batchDiag("42P01", `relation "`+table+`" does not exist`)
						missing.Position = batchPosition(query, table)
						batchError(t, p, peer, query, missing)
					}
					return
				}
				rows := fixture.BatchSet{[]string{"id", "pid"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(1)}}, false}
				fixture.BatchSets(t, batchQuery(t, p, a, q), rows)
				fixture.BatchSets(t, batchQuery(t, p, peer, "SELECT id,pid FROM batch_fk_child"), rows)
				batchError(t, p, peer, "INSERT INTO batch_fk_child VALUES(3,99)", w)
				fixture.BatchSets(t, batchQuery(t, p, peer, "SELECT id,pid FROM batch_fk_child"), rows)
			})
		})
	}
}
