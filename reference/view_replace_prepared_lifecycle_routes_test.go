package reference_test

import (
	"testing"
)

func TestReferenceViewReplacePreparedTargetLifecycle(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		db := backend.db
		updateReturningReferenceExec(t, db, "CREATE TABLE t(id INT)")
		updateReturningReferenceExec(t, db, "INSERT INTO t VALUES(7)")
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
				updateReturningReferenceExec(t, db, "CREATE VIEW v AS SELECT id FROM t")
				if tc.query == "ALTER VIEW v SET SCHEMA s" {
					updateReturningReferenceExec(t, db, "CREATE SCHEMA s")
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
					deleteReferenceConsume(t, backend.pg, rows, err, referenceStep{columns: []string{"id"}, types: []string{"INT4"}, rows: [][]any{{int64(7)}}, bag: false})
				}
				switch tc.query {
				case "ALTER VIEW v RENAME TO renamed":
					updateReturningReferenceExec(t, db, "DROP VIEW renamed")
				case "ALTER VIEW v SET SCHEMA s":
					updateReturningReferenceExec(t, db, "DROP VIEW s.v")
				default:
					if tc.check != "" {
						updateReturningReferenceExec(t, db, "DROP VIEW v")
					}
				}
			})
		}
	}
}
