package stuntgres_test

import (
	"testing"

	"stuntgres"
)

func TestQueryOrderRegression(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "query-order-regression")
	execSQL(t, db, "CREATE TABLE t(id INT NOT NULL,a INT,b INT)")
	execSQL(t, db, "INSERT INTO t VALUES(1,2,1),(2,1,5),(3,NULL,2),(4,2,0)")
	for _, tc := range []struct {
		query        string
		names, types []string
		want         [][]any
	}{
		{"SELECT id,a+b AS k FROM t ORDER BY k DESC NULLS LAST,id", []string{"id", "k"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(6)}, {int64(1), int64(3)}, {int64(4), int64(2)}, {int64(3), nil}}},
		{"SELECT id FROM t ORDER BY a ASC NULLS FIRST,b DESC,id", []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(2)}, {int64(1)}, {int64(4)}}},
	} {
		t.Run(tc.query, func(t *testing.T) {
			t.Run("direct", func(t *testing.T) {
				rows, err := db.Query(tc.query)
				queryCoreRows(t, rows, err, tc.names, tc.types, tc.want, false)
			})
			t.Run("prepared", func(t *testing.T) {
				stmt, err := db.Prepare(tc.query)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				rows, err := stmt.Query()
				queryCoreRows(t, rows, err, tc.names, tc.types, tc.want, false)
			})
		})
	}
	t.Run("ambiguous-alias", func(t *testing.T) {
		query := "SELECT a AS x,b AS x FROM t ORDER BY x"
		want := stuntgres.Error{Code: "42702", Message: `ORDER BY "x" is ambiguous`, Position: "38"}
		t.Run("direct", func(t *testing.T) {
			rows, err := db.Query(query)
			if rows != nil {
				rows.Close()
				t.Fatal("analysis failure exposed rows")
			}
			typeCoreDiagnostic(t, err, want)
		})
		t.Run("prepared", func(t *testing.T) {
			stmt, err := db.Prepare(query)
			if stmt != nil {
				stmt.Close()
				t.Fatal("analysis failure exposed statement")
			}
			typeCoreDiagnostic(t, err, want)
		})
	})
	for _, key := range []string{"n", "1", "nextval('s')"} {
		for _, prepared := range []bool{false, true} {
			t.Run(key+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				v := openLocal(t, stuntgres.NewDriver(), t.Name())
				execSQL(t, v, "CREATE TABLE v(id INT)")
				execSQL(t, v, "INSERT INTO v VALUES(1),(2),(3)")
				execSQL(t, v, "CREATE SEQUENCE s START WITH 1")
				query := "SELECT nextval('s') AS n FROM v ORDER BY " + key + " DESC"
				if prepared {
					stmt, err := v.Prepare(query)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					rows, err := stmt.Query()
					queryCoreRows(t, rows, err, []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}, {int64(2)}, {int64(1)}}, false)
				} else {
					rows, err := v.Query(query)
					queryCoreRows(t, rows, err, []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}, {int64(2)}, {int64(1)}}, false)
				}
				var next int64
				if err := v.QueryRow("SELECT nextval('s')").Scan(&next); err != nil || next != 4 {
					t.Fatal(next, err)
				}
			})
		}
	}
}
