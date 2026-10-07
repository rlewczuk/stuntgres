package stuntgres_test

import (
	"stuntgres"
	"testing"
)

func TestQueryOrderBehavior(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(id INT NOT NULL,a INT,b INT)")
	execSQL(t, db, "INSERT INTO t VALUES(1,2,1),(2,1,5),(3,NULL,2),(4,2,0)")
	for _, tc := range []struct {
		q            string
		names, types []string
		want         [][]any
	}{
		{"SELECT id FROM t ORDER BY a,id", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(1)}, {int64(4)}, {int64(3)}}},
		{"SELECT id FROM t ORDER BY a ASC NULLS LAST,id", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(1)}, {int64(4)}, {int64(3)}}},
		{"SELECT id FROM t ORDER BY a DESC,id", []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(1)}, {int64(4)}, {int64(2)}}},
		{"SELECT id FROM t ORDER BY a DESC NULLS FIRST,id", []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(1)}, {int64(4)}, {int64(2)}}},
		{"SELECT id FROM t ORDER BY a DESC NULLS LAST,id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(4)}, {int64(2)}, {int64(3)}}},
		{"SELECT a AS b,id FROM t ORDER BY b,id", []string{"b", "id"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {int64(2), int64(1)}, {int64(2), int64(4)}, {nil, int64(3)}}},
		{"SELECT a AS b,id FROM t ORDER BY t.b,id", []string{"b", "id"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(4)}, {int64(2), int64(1)}, {nil, int64(3)}, {int64(1), int64(2)}}},
		{"SELECT a AS b,id FROM t ORDER BY b+0,id", []string{"b", "id"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(4)}, {int64(2), int64(1)}, {nil, int64(3)}, {int64(1), int64(2)}}},
		{"SELECT id,a FROM t ORDER BY 2 DESC NULLS LAST,1", []string{"id", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {int64(4), int64(2)}, {int64(2), int64(1)}, {int64(3), nil}}},
		{"SELECT a AS x,a AS x FROM t ORDER BY x,id", []string{"x", "x"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(2), int64(2)}, {nil, nil}}},
		{"SELECT a,count(*) AS n FROM t GROUP BY a ORDER BY n DESC,a NULLS LAST", []string{"a", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(2), int64(2)}, {int64(1), int64(1)}, {nil, int64(1)}}},
		{"SELECT a FROM t GROUP BY a ORDER BY count(*) DESC,a NULLS LAST", []string{"a"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(1)}, {nil}}},
		{"SELECT count(*) AS n FROM t ORDER BY count(*)", []string{"n"}, []string{"INT8"}, [][]any{{int64(4)}}},
	} {
		t.Run(tc.q, func(t *testing.T) {
			rows, err := db.Query(tc.q)
			queryCoreRows(t, rows, err, tc.names, tc.types, tc.want, false)
			stmt, err := db.Prepare(tc.q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			rows, err = stmt.Query()
			queryCoreRows(t, rows, err, tc.names, tc.types, tc.want, false)
		})
	}
	stmt, err := db.Prepare("SELECT id FROM t ORDER BY a*$1 NULLS LAST,id")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for i, arg := range []any{1, -1, nil, 1} {
		ids := []int64{2, 1, 4, 3}
		if i == 1 {
			ids = []int64{1, 4, 2, 3}
		}
		if i == 2 {
			ids = []int64{1, 2, 3, 4}
		}
		want := make([][]any, len(ids))
		for j, id := range ids {
			want[j] = []any{id}
		}
		rows, err := stmt.Query(arg)
		queryCoreRows(t, rows, err, []string{"id"}, []string{"INT4"}, want, false)
	}
}

func TestQueryOrderDiagnostics(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(id INT,a INT,b INT)")
	for _, population := range []bool{false, true} {
		if population {
			execSQL(t, db, "INSERT INTO t VALUES(1,2,3)")
		}
		for _, tc := range []struct{ q, code, message, position string }{
			{"SELECT id FROM t ORDER BY 0", "42P10", "ORDER BY position 0 is not in select list", "27"},
			{"SELECT id FROM t ORDER BY -1", "42P10", "ORDER BY position -1 is not in select list", "27"},
			{"SELECT id FROM t ORDER BY (0)", "42P10", "ORDER BY position 0 is not in select list", "28"},
			{"SELECT id FROM t ORDER BY 2", "42P10", "ORDER BY position 2 is not in select list", "27"},
			{"SELECT id FROM t ORDER BY NULL", "42601", "non-integer constant in ORDER BY", "27"},
			{"SELECT id FROM t ORDER BY 1.5", "42601", "non-integer constant in ORDER BY", "27"},
			{"SELECT id AS x FROM t ORDER BY x+0", "42703", `column "x" does not exist`, "32"},
			{"SELECT id AS x FROM t ORDER BY t.x", "42703", `column t.x does not exist`, "32"},
			{"SELECT count(*) FROM t ORDER BY a", "42803", `column "t.a" must appear in the GROUP BY clause or be used in an aggregate function`, "33"},
			{"SELECT id FROM t ORDER BY id NULLS", "42601", `syntax error at or near "NULLS"`, "30"},
		} {
			want := stuntgres.Error{Code: tc.code, Message: tc.message, Position: tc.position}
			rows, err := db.Query(tc.q)
			if rows != nil {
				rows.Close()
				t.Fatal("failure exposed rows", tc.q)
			}
			typeCoreDiagnostic(t, err, want)
			stmt, err := db.Prepare(tc.q)
			if stmt != nil {
				stmt.Close()
				t.Fatal("failure exposed statement", tc.q)
			}
			typeCoreDiagnostic(t, err, want)
		}
	}
}
