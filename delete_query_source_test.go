package stuntgres_test

import (
	"reflect"
	"stuntgres"
	"testing"
)

func TestDeleteCoreInsertQuerySource(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, commit := range []bool{false, true} {
			name := map[bool]string{false: "direct", true: "prepared"}[prepared] + "/" + map[bool]string{false: "rollback", true: "commit"}[commit]
			t.Run(name, func(t *testing.T) {
				db := openLocal(t, stuntgres.NewDriver(), "delete-query-source-"+name)
				execSQL(t, db, "CREATE TABLE t(id INT)")
				execSQL(t, db, "INSERT INTO t VALUES(1),(2),(2),(3)")
				execSQL(t, db, "CREATE TABLE destination(id INT)")
				retained, err := db.Query("SELECT id FROM t ORDER BY id")
				if err != nil {
					t.Fatal(err)
				}
				defer retained.Close()
				copySQL := "INSERT INTO destination SELECT id FROM t ORDER BY id"
				held, err := db.Prepare(copySQL)
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				execSQL(t, db, "DELETE FROM t WHERE id=1")
				if prepared {
					result, err := held.Exec()
					insertSelectCount(t, result, err, 3)
				} else {
					result, err := db.Exec(copySQL)
					insertSelectCount(t, result, err, 3)
				}
				wantTuples(t, db, "SELECT id FROM destination ORDER BY id", [][]any{{int64(2)}, {int64(2)}, {int64(3)}})
				execSQL(t, db, "DELETE FROM destination")
				tx, err := db.Begin()
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				for _, sql := range []string{"INSERT INTO t VALUES(7)", "DELETE FROM t WHERE id=2 OR id=7"} {
					if _, err := tx.Exec(sql); err != nil {
						t.Fatal(err)
					}
				}
				for _, step := range []struct {
					sql   string
					count int64
				}{{"INSERT INTO t SELECT id FROM t", 1}, {copySQL, 2}, {"DELETE FROM t", 2}, {copySQL, 0}} {
					if prepared {
						stmt, err := tx.Prepare(step.sql)
						if err != nil {
							t.Fatal(err)
						}
						result, err := stmt.Exec()
						stmt.Close()
						insertSelectCount(t, result, err, step.count)
					} else {
						result, err := tx.Exec(step.sql)
						insertSelectCount(t, result, err, step.count)
					}
				}
				wantTuples(t, db, "SELECT id FROM t ORDER BY id", [][]any{{int64(2)}, {int64(2)}, {int64(3)}})
				wantTuples(t, db, "SELECT id FROM destination", [][]any{})
				if commit {
					err = tx.Commit()
				} else {
					err = tx.Rollback()
				}
				if err != nil {
					t.Fatal(err)
				}
				if commit {
					wantTuples(t, db, "SELECT id FROM t", [][]any{})
					wantTuples(t, db, "SELECT id FROM destination", [][]any{{int64(3)}, {int64(3)}})
				} else {
					wantTuples(t, db, "SELECT id FROM t ORDER BY id", [][]any{{int64(2)}, {int64(2)}, {int64(3)}})
					wantTuples(t, db, "SELECT id FROM destination", [][]any{})
				}
				var original []int64
				for retained.Next() {
					var id int64
					if err := retained.Scan(&id); err != nil {
						t.Fatal(err)
					}
					original = append(original, id)
				}
				if err := retained.Err(); err != nil || !reflect.DeepEqual(original, []int64{1, 2, 2, 3}) {
					t.Fatal("returned source changed", original, err)
				}
			})
		}
	}
}
