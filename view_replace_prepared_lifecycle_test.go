package stuntgres_test

import (
	"stuntgres"
	"testing"
)

func TestViewReplacePreparedTargetLifecycle(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(id INT)")
	execSQL(t, db, "INSERT INTO t VALUES(7)")
	for _, tc := range []struct{ query, check string }{
		{"ALTER VIEW v RENAME TO renamed", "SELECT id FROM renamed"},
		{"ALTER VIEW v SET SCHEMA s", "SELECT id FROM s.v"},
		{"ALTER VIEW v SET(security_barrier=true)", "SELECT id FROM v"},
		{"ALTER VIEW v OWNER TO CURRENT_USER", "SELECT id FROM v"},
		{"DROP VIEW v", ""},
	} {
		t.Run(tc.query, func(t *testing.T) {
			stmt, err := db.Prepare(tc.query)
			if err != nil {
				t.Fatal("prepare resolved utility target", err)
			}
			defer stmt.Close()
			execSQL(t, db, "CREATE VIEW v AS SELECT id FROM t")
			if tc.query == "ALTER VIEW v SET SCHEMA s" {
				execSQL(t, db, "CREATE SCHEMA s")
			}
			r, err := stmt.Exec()
			if err != nil {
				t.Fatal(err)
			}
			if n, err := r.RowsAffected(); err != nil || n != 0 {
				t.Fatal(n, err)
			}
			if tc.check != "" {
				rows, err := db.Query(tc.check)
				queryCoreRows(t, rows, err, []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}, false)
			}
			switch tc.query {
			case "ALTER VIEW v RENAME TO renamed":
				execSQL(t, db, "DROP VIEW renamed")
			case "ALTER VIEW v SET SCHEMA s":
				execSQL(t, db, "DROP VIEW s.v")
			default:
				if tc.check != "" {
					execSQL(t, db, "DROP VIEW v")
				}
			}
		})
	}
}
