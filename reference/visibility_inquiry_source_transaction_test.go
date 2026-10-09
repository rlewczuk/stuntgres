package reference_test

import "testing"

// The original consumer owns a pooled physical session through DB.Begin, not
// Conn.BeginTx. Keep the recorded Context companion in the maintained scenario.
func TestReferenceVisibilityInquiryPrivateSequenceSourceConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			db := backend.db
			db.SetMaxOpenConns(1)
			t.Cleanup(func() {
				for _, q := range []string{"SET search_path=public", "DROP SCHEMA IF EXISTS a CASCADE", "DROP SCHEMA IF EXISTS b CASCADE"} {
					if _, err := db.Exec(q); err != nil {
						t.Error(q, err)
					}
				}
			})
			for _, q := range []string{"CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE SEQUENCE b.same", "SET search_path=a,b"} {
				r, err := db.Exec(q)
				deleteReferenceCount(t, r, err, 0)
			}
			var b int64
			if err := db.QueryRow("SELECT oid FROM pg_catalog.pg_class WHERE relname='same'").Scan(&b); err != nil || b <= 0 || b > 4294967295 {
				t.Fatal("committed sequence identity", b, err)
			}
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			r, err := tx.Exec("CREATE SEQUENCE a.same")
			deleteReferenceCount(t, r, err, 0)
			rows, err := tx.Query("SELECT pg_table_is_visible($1)", b)
			visibilityInquiryRows(t, rows, err, "pg_table_is_visible", []any{false})
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			rows, err = db.Query("SELECT pg_table_is_visible($1)", b)
			visibilityInquiryRows(t, rows, err, "pg_table_is_visible", []any{true})
		})
	}
}
