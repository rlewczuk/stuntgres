package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUpdateReturningOwnPredecessor(t *testing.T) {
	updateReturningCompositionContextReplay(t)
	for _, commit := range []bool{false, true} {
		p := fixture.NewScenario(t)
		for _, backend := range p.Backends() {
			db := backend.DB
			fixture.UpdateReturningReferenceExec(t, db, "CREATE TABLE t(id INT,a INT)")
			fixture.UpdateReturningReferenceExec(t, db, "INSERT INTO t VALUES(1,10)")
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
				fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Step{Columns: []string{"a", "a"}, Types: []string{"INT4", "INT4"}, Rows: tc.want, Bag: false})
			}
			rows, err := tx.Query("DELETE FROM t WHERE id=3 RETURNING a")
			fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Step{Columns: []string{"a"}, Types: []string{"INT4"}, Rows: [][]any{{int64(31)}}, Bag: false})
			rows, err = db.Query("SELECT id,a FROM t")
			fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Step{Columns: []string{"id", "a"}, Types: []string{"INT4", "INT4"}, Rows: [][]any{{int64(1), int64(10)}}, Bag: false})
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
			fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Step{Columns: []string{"id", "a"}, Types: []string{"INT4", "INT4"}, Rows: [][]any{{int64(1), a}}, Bag: false})
		}
	}
}

func updateReturningCompositionContextReplay(t *testing.T) {
	t.Helper()
	for _, commit := range []bool{false, true} {
		p := fixture.NewScenario(t)
		for _, backend := range p.Backends() {
			t.Run(backend.Name, func(t *testing.T) {
				db := backend.DB
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, db, backend.Recorder, s) }
				run(fixture.Exec("CREATE TABLE t(id INT,a INT)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
				tx, err := db.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				for _, s := range []fixture.Step{
					fixture.Exec("INSERT INTO t VALUES(3,30)", 1), fixture.Exec("UPDATE t SET a=11 WHERE id=1", 1),
					fixture.Select("UPDATE t SET a=a+1 WHERE id=3 RETURNING old.a,new.a", []string{"a", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(30), int64(31)}}),
					fixture.Select("UPDATE t SET a=a+1 WHERE id=1 RETURNING old.a,new.a", []string{"a", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(11), int64(12)}}),
					fixture.Select("DELETE FROM t WHERE id=3 RETURNING a", []string{"a"}, []string{"INT4"}, [][]any{{int64(31)}}),
				} {
					fixture.RunStep(t, p.Ctx, tx, backend.Recorder, s)
				}
				run(fixture.Select("SELECT id,a FROM t", []string{"id", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}}))
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
				run(fixture.Select("SELECT id,a FROM t", []string{"id", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(1), value}}))
			})
		}
	}
}
