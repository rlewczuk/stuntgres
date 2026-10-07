package stuntgres_test

import (
	"stuntgres"
	"testing"
)

var rowLockPublicClauses = []string{"FOR KEY SHARE", "FOR SHARE", "FOR NO KEY UPDATE", "FOR UPDATE"}

func TestRowLockFourModesFailingFirst(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(id INT,v INT)")
	execSQL(t, db, "INSERT INTO t VALUES(1,10)")
	for _, clause := range rowLockPublicClauses {
		t.Run(clause, func(t *testing.T) {
			query := "SELECT id,v FROM t WHERE id=$1 ORDER BY id " + clause
			t.Run("direct", func(t *testing.T) {
				rows, err := db.Query(query, 1)
				queryCoreRows(t, rows, err, []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}}, false)
			})
			t.Run("prepared", func(t *testing.T) {
				stmt, err := db.Prepare(query)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				rows, err := stmt.Query(1)
				queryCoreRows(t, rows, err, []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}}, false)
			})
		})
	}
}

func TestRowLockJoinedFailingFirst(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	for _, query := range []string{"CREATE TABLE a(id INT,v INT)", "CREATE TABLE b(id INT)", "INSERT INTO a VALUES(1,10),(1,10),(2,20)", "INSERT INTO b VALUES(1),(3)"} {
		execSQL(t, db, query)
	}
	query := "SELECT a.id,a.v FROM a INNER JOIN b ON a.id=b.id WHERE a.id=$1 FOR SHARE"
	t.Run("direct", func(t *testing.T) {
		rows, err := db.Query(query, 1)
		queryCoreRows(t, rows, err, []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(1), int64(10)}}, true)
	})
	t.Run("prepared", func(t *testing.T) {
		stmt, err := db.Prepare(query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		rows, err := stmt.Query(1)
		queryCoreRows(t, rows, err, []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(1), int64(10)}}, true)
	})
}

func TestRowLockGroupedFailingFirst(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(id INT,v INT)")
	for _, clause := range rowLockPublicClauses {
		for _, tc := range []struct{ query, suffix string }{
			{"SELECT id FROM t GROUP BY id ", "GROUP BY clause"},
			{"SELECT count(*) FROM t HAVING TRUE ", "HAVING clause"},
			{"SELECT count(*) FROM t ", "aggregate functions"},
		} {
			t.Run(clause+"/"+tc.suffix, func(t *testing.T) {
				query := tc.query + clause
				want := stuntgres.Error{Code: "0A000", Message: clause + " is not allowed with " + tc.suffix}
				rows, err := db.Query(query)
				if rows != nil {
					rows.Close()
					t.Fatal("failed query exposed rows")
				}
				typeCoreDiagnostic(t, err, want)
				stmt, err := db.Prepare(query)
				if stmt != nil {
					stmt.Close()
					t.Fatal("failed prepare exposed statement")
				}
				typeCoreDiagnostic(t, err, want)
			})
		}
	}
}
