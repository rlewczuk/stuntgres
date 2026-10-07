package stuntgres_test

import (
	"errors"
	"stuntgres"
	"testing"
)

func TestQueryAggregateExclusionsHaveNoPrefixEffects(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(v INT)")
	for _, q := range []string{
		"SELECT count(*)", "SELECT count(v) FROM t", "SELECT count() FROM t", "SELECT sum(v) FROM t",
		"SELECT count(*) FILTER (WHERE TRUE) FROM t", "SELECT count(DISTINCT v) FROM t", "SELECT count(* ORDER BY v) FROM t", "SELECT count(*) OVER () FROM t",
		"SELECT v FROM t GROUP BY ROLLUP(v)", "SELECT v FROM t GROUP BY CUBE(v)", "SELECT v FROM t GROUP BY GROUPING SETS ((v))", "SELECT v FROM t GROUP BY ()", "SELECT v FROM t GROUP BY DISTINCT v", "SELECT v FROM t GROUP BY ALL v",
	} {
		t.Run(q, func(t *testing.T) {
			_, err := db.Exec("INSERT INTO t VALUES(1);" + q)
			var diagnostic *stuntgres.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" {
				t.Fatal("excluded suffix diagnostic", err)
			}
			stmt, err := db.Prepare("INSERT INTO t VALUES(1);" + q)
			if stmt != nil {
				stmt.Close()
				t.Fatal("excluded prepare exposed statement")
			}
			if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" {
				t.Fatal(err)
			}
			rows, err := db.Query("SELECT v FROM t")
			queryCoreRows(t, rows, err, []string{"v"}, []string{"INT4"}, [][]any{}, true)
		})
	}
}
