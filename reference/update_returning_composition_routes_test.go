package reference_test

import (
	"testing"
)

func TestReferenceUpdateReturningOwnPredecessor(t *testing.T) {
	updateReturningCompositionContextReplay(t)
	for _, commit := range []bool{false, true} {
		p := newReferenceScenario(t)
		for _, backend := range p.backends() {
			db := backend.db
			updateReturningReferenceExec(t, db, "CREATE TABLE t(id INT,a INT)")
			updateReturningReferenceExec(t, db, "INSERT INTO t VALUES(1,10)")
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			for _, q := range []string{"INSERT INTO t VALUES(3,30)", "UPDATE t SET a=11 WHERE id=1"} {
				if _, err = tx.Exec(q); err != nil {
					t.Fatal(err)
				}
			}
			for _, tc := range []struct {
				q    string
				want [][]any
			}{
				{"UPDATE t SET a=a+1 WHERE id=3 RETURNING old.a,new.a", [][]any{{int64(30), int64(31)}}},
				{"UPDATE t SET a=a+1 WHERE id=1 RETURNING old.a,new.a", [][]any{{int64(11), int64(12)}}},
			} {
				rows, err := tx.Query(tc.q)
				deleteReferenceConsume(t, backend.pg, rows, err, referenceStep{columns: []string{"a", "a"}, types: []string{"INT4", "INT4"}, rows: tc.want, bag: false})
			}
			rows, err := tx.Query("DELETE FROM t WHERE id=3 RETURNING a")
			deleteReferenceConsume(t, backend.pg, rows, err, referenceStep{columns: []string{"a"}, types: []string{"INT4"}, rows: [][]any{{int64(31)}}, bag: false})
			rows, err = db.Query("SELECT id,a FROM t")
			deleteReferenceConsume(t, backend.pg, rows, err, referenceStep{columns: []string{"id", "a"}, types: []string{"INT4", "INT4"}, rows: [][]any{{int64(1), int64(10)}}, bag: false})
			if commit {
				err = tx.Commit()
			} else {
				err = tx.Rollback()
			}
			if err != nil {
				t.Fatal(err)
			}
			a := int64(10)
			if commit {
				a = 12
			}
			rows, err = db.Query("SELECT id,a FROM t")
			deleteReferenceConsume(t, backend.pg, rows, err, referenceStep{columns: []string{"id", "a"}, types: []string{"INT4", "INT4"}, rows: [][]any{{int64(1), a}}, bag: false})
		}
	}
}

func updateReturningCompositionContextReplay(t *testing.T) {
	t.Helper()
	for _, commit := range []bool{false, true} {
		p := newReferenceScenario(t)
		for _, backend := range p.backends() {
			t.Run(backend.name, func(t *testing.T) {
				db := backend.db
				run := func(s referenceStep) { referenceRunStep(t, p.ctx, db, backend.recorder, s) }
				run(referenceExec("CREATE TABLE t(id INT,a INT)", 0))
				run(referenceExec("INSERT INTO t VALUES(1,10)", 1))
				tx, err := db.BeginTx(p.ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				for _, s := range []referenceStep{
					referenceExec("INSERT INTO t VALUES(3,30)", 1), referenceExec("UPDATE t SET a=11 WHERE id=1", 1),
					referenceSelect("UPDATE t SET a=a+1 WHERE id=3 RETURNING old.a,new.a", []string{"a", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(30), int64(31)}}),
					referenceSelect("UPDATE t SET a=a+1 WHERE id=1 RETURNING old.a,new.a", []string{"a", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(11), int64(12)}}),
					referenceSelect("DELETE FROM t WHERE id=3 RETURNING a", []string{"a"}, []string{"INT4"}, [][]any{{int64(31)}}),
				} {
					referenceRunStep(t, p.ctx, tx, backend.recorder, s)
				}
				run(referenceSelect("SELECT id,a FROM t", []string{"id", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}}))
				if commit {
					err = tx.Commit()
				} else {
					err = tx.Rollback()
				}
				if err != nil {
					t.Fatal(err)
				}
				value := int64(10)
				if commit {
					value = 12
				}
				run(referenceSelect("SELECT id,a FROM t", []string{"id", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(1), value}}))
			})
		}
	}
}
