package stuntgres_test

import (
	"stuntgres"
	"testing"
)

func TestLikeUpdateWhereConsumers(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(id INT,v TEXT,p TEXT,e TEXT,mark INT)")
	execSQL(t, db, "INSERT INTO t VALUES(1,'a%b','a!%b','!',0),(2,'Ab','a%','!',0),(3,NULL,NULL,NULL,0)")
	for _, tc := range []struct {
		predicate string
		args      []any
		count     int64
		marks     [][]any
	}{
		{"v LIKE 'a%'", nil, 1, [][]any{{int64(1)}, {int64(0)}, {int64(0)}}},
		{"v ILIKE $1", []any{"a%"}, 2, [][]any{{int64(1)}, {int64(1)}, {int64(0)}}},
		{"v NOT LIKE 'a%'", nil, 1, [][]any{{int64(0)}, {int64(1)}, {int64(0)}}},
		{"v NOT ILIKE 'a%'", nil, 0, [][]any{{int64(0)}, {int64(0)}, {int64(0)}}},
		{"v LIKE p ESCAPE e", nil, 1, [][]any{{int64(1)}, {int64(0)}, {int64(0)}}},
		{"v ILIKE p ESCAPE e", nil, 2, [][]any{{int64(1)}, {int64(1)}, {int64(0)}}},
	} {
		t.Run(tc.predicate, func(t *testing.T) {
			q := "UPDATE t SET mark=1 WHERE " + tc.predicate
			stmt, err := db.Prepare(q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			for _, prepared := range []bool{false, true} {
				execSQL(t, db, "UPDATE t SET mark=0")
				var count int64
				if prepared {
					r, err := stmt.Exec(tc.args...)
					if err != nil {
						t.Fatal(err)
					}
					count, err = r.RowsAffected()
					if err != nil {
						t.Fatal(err)
					}
				} else {
					r, err := db.Exec(q, tc.args...)
					if err != nil {
						t.Fatal(err)
					}
					count, err = r.RowsAffected()
					if err != nil {
						t.Fatal(err)
					}
				}
				if count != tc.count {
					t.Fatal(count, tc.count)
				}
				rows, err := db.Query("SELECT mark FROM t ORDER BY id")
				compareOpsRows(t, rows, err, []string{"INT4"}, tc.marks)
			}
			selected := make([][]any, 0)
			for i, row := range tc.marks {
				if row[0] == int64(1) {
					selected = append(selected, []any{int64(i + 1)})
				}
			}
			query := "SELECT id FROM t WHERE " + tc.predicate + " ORDER BY id"
			rows, err := db.Query(query, tc.args...)
			compareOpsRows(t, rows, err, []string{"INT4"}, selected)
			held, err := db.Prepare(query)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			rows, err = held.Query(tc.args...)
			compareOpsRows(t, rows, err, []string{"INT4"}, selected)
		})
	}
	execSQL(t, db, "UPDATE t SET mark=0")
	execSQL(t, db, "UPDATE t SET e='ab' WHERE id=2")
	query := "UPDATE t SET mark=1 WHERE v ILIKE p ESCAPE e"
	stmt, err := db.Prepare(query)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	_, err = db.Exec(query)
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22025", Message: "invalid escape string", Hint: "Escape string must be empty or one character."})
	rows, err := db.Query("SELECT mark FROM t ORDER BY id")
	compareOpsRows(t, rows, err, []string{"INT4"}, [][]any{{int64(0)}, {int64(0)}, {int64(0)}})
	_, err = stmt.Exec()
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22025", Message: "invalid escape string", Hint: "Escape string must be empty or one character."})
	rows, err = db.Query("SELECT mark FROM t ORDER BY id")
	compareOpsRows(t, rows, err, []string{"INT4"}, [][]any{{int64(0)}, {int64(0)}, {int64(0)}})
}
