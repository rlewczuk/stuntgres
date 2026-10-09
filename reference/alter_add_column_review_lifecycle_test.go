package reference_test

import (
	"database/sql"
	"fmt"
	"testing"
)

func TestReferenceAlterAddColumnReviewMixedRewrite(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			alterAddReferenceScenario(t, func(p *referencePair, b referenceBackend) {
				run := func(s referenceStep) { s.prepared = prepared; referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
				run(referenceExec("CREATE TABLE t(id INT,a TEXT,b INT)", 0))
				run(referenceExec("INSERT INTO t VALUES(1,'12',5),(2,'21',6)", 2))
				run(referenceExec("ALTER TABLE t ADD n INT DEFAULT 7, ALTER a TYPE BIGINT USING a::BIGINT+b, DROP b, ADD note TEXT", 0))
				run(referenceSelect("SELECT * FROM t ORDER BY id", []string{"id", "a", "n", "note"}, []string{"INT4", "INT8", "INT4", "TEXT"}, [][]any{{int64(1), int64(17), int64(7), nil}, {int64(2), int64(27), int64(7), nil}}))
				referenceTableDDLMetadata(t, p.ctx, b.db, b.pg, true, "SELECT * FROM t", []string{"id", "a", "n", "note"}, []string{"INT4", "INT8", "INT4", "TEXT"}, []bool{true, true, true, true}, 2)
				run(referenceExec("CREATE SEQUENCE s START 1 CACHE 1", 0))
				run(referenceExec("CREATE TABLE v(id INT,a TEXT)", 0))
				run(referenceExec("INSERT INTO v VALUES(1,'12'),(2,'21'),(2,'21')", 3))
				run(referenceExec("ALTER TABLE v ADD n BIGINT DEFAULT nextval('s'), ALTER a TYPE BIGINT USING a::BIGINT+100", 0))
				run(referenceSelect("SELECT id,a FROM v ORDER BY id", []string{"id", "a"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(112)}, {int64(2), int64(121)}, {int64(2), int64(121)}}))
				bag := referenceSelect("SELECT n FROM v", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}})
				bag.bag = true
				run(bag)
				referenceTableDDLMetadata(t, p.ctx, b.db, b.pg, false, "SELECT * FROM v", []string{"id", "a", "n"}, []string{"INT4", "INT8", "INT8"}, []bool{true, true, true}, 3)
				run(referenceSelect("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(4)}}))
			})
		})
	}
}

func TestReferenceAlterAddColumnReviewVolatileSavepoint(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(fmt.Sprint(commit), func(t *testing.T) {
			alterAddReferenceScenario(t, func(p *referencePair, b referenceBackend) {
				run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
				run(referenceExec("CREATE SEQUENCE s START 1 CACHE 1", 0))
				run(referenceExec("CREATE TABLE t(id INT,old INT DEFAULT 3)", 0))
				run(referenceExec("INSERT INTO t VALUES(1,10),(2,20),(3,30)", 3))
				tx, err := b.db.BeginTx(p.ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				inside := func(s referenceStep) { referenceRunStep(t, p.ctx, tx, b.recorder, s) }
				inside(referenceSelect("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}))
				inside(referenceExec("UPDATE t SET old=11 WHERE id=1", 1))
				inside(referenceExec("DELETE FROM t WHERE id=2", 1))
				inside(referenceExec("INSERT INTO t VALUES(4,40)", 1))
				inside(referenceExec("SAVEPOINT before_add", 0))
				inside(referenceExec("ALTER TABLE t ADD v BIGINT DEFAULT nextval('s') NOT NULL CHECK(v>0)", 0))
				bag := referenceSelect("SELECT v FROM t", []string{"v"}, []string{"INT8"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}})
				bag.bag = true
				inside(bag)
				inside(referenceSelect("SELECT id,old FROM t ORDER BY id", []string{"id", "old"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(11)}, {int64(3), int64(30)}, {int64(4), int64(40)}}))
				inside(referenceExec("INSERT INTO t(id) VALUES(5)", 1))
				inside(referenceExec("ROLLBACK TO before_add", 0))
				inside(referenceSelect("SELECT * FROM t ORDER BY id", []string{"id", "old"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(11)}, {int64(3), int64(30)}, {int64(4), int64(40)}}))
				inside(referenceSelect("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(5)}}))
				inside(referenceExec("ALTER TABLE t ADD v BIGINT DEFAULT nextval('s')", 0))
				bag.rows = [][]any{{int64(6)}, {int64(7)}, {int64(8)}}
				inside(bag)
				if commit {
					err = tx.Commit()
				} else {
					err = tx.Rollback()
				}
				if err != nil {
					t.Fatal(err)
				}
				if commit {
					run(referenceSelect("SELECT id,old FROM t ORDER BY id", []string{"id", "old"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(11)}, {int64(3), int64(30)}, {int64(4), int64(40)}}))
					run(bag)
				} else {
					run(referenceSelect("SELECT * FROM t ORDER BY id", []string{"id", "old"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}, {int64(3), int64(30)}}))
				}
				run(referenceSelect("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(9)}}))
			})
		})
	}
}

func TestReferenceAlterAddColumnReviewEarlySavepointShapes(t *testing.T) {
	alterAddReferenceScenario(t, func(p *referencePair, b referenceBackend) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
		run(referenceExec("CREATE TABLE t(id INT,b INT DEFAULT 3)", 0))
		run(referenceExec("INSERT INTO t VALUES(1,10),(2,20),(2,20)", 3))
		tx, err := b.db.BeginTx(p.ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		inside := func(s referenceStep) { referenceRunStep(t, p.ctx, tx, b.recorder, s) }
		inside(referenceExec("SAVEPOINT before_first_snapshot", 0))
		inside(referenceSelect("SELECT * FROM t ORDER BY id", []string{"id", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}, {int64(2), int64(20)}}))
		inside(referenceExec("ALTER TABLE t DROP b, ADD b INT DEFAULT 7", 0))
		inside(referenceExec("ALTER TABLE t ADD c INT DEFAULT 8", 0))
		inside(referenceSelect("SELECT * FROM t ORDER BY id", []string{"id", "b", "c"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(7), int64(8)}, {int64(2), int64(7), int64(8)}, {int64(2), int64(7), int64(8)}}))
		inside(referenceExec("INSERT INTO t(id) VALUES(3)", 1))
		inside(referenceExec("ROLLBACK TO before_first_snapshot", 0))
		inside(referenceSelect("SELECT * FROM t ORDER BY id", []string{"id", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}, {int64(2), int64(20)}}))
		inside(referenceExec("ALTER TABLE t ADD c INT DEFAULT 9", 0))
		inside(referenceExec("INSERT INTO t(id) VALUES(3)", 1))
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		run(referenceSelect("SELECT * FROM t ORDER BY id", []string{"id", "b", "c"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(10), int64(9)}, {int64(2), int64(20), int64(9)}, {int64(2), int64(20), int64(9)}, {int64(3), int64(3), int64(9)}}))
	})
}
