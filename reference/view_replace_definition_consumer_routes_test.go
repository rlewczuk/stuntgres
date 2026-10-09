package reference_test

import (
	"testing"
)

func TestReferenceViewReplaceDefinitionAndMetadataConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		db := backend.db
		for _, q := range []string{"CREATE TABLE t(id INT,txt TEXT)", "CREATE TABLE u(id INT,txt TEXT)", "INSERT INTO t VALUES(1,'a')", "INSERT INTO u VALUES(2,'b')", "CREATE VIEW v AS SELECT id,txt FROM t", "CREATE VIEW w AS SELECT id FROM v"} {
			updateReturningReferenceExec(t, db, q)
		}
		var oid, rule int64
		if err := db.QueryRow("SELECT oid FROM pg_class WHERE relname='v'").Scan(&oid); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow("SELECT oid FROM pg_rewrite WHERE ev_class=$1", oid).Scan(&rule); err != nil {
			t.Fatal(err)
		}
		updateReturningReferenceExec(t, db, "CREATE OR REPLACE VIEW v AS SELECT id+10 AS id,txt,TRUE AS extra FROM u")
		var gotOID, gotRule int64
		if err := db.QueryRow("SELECT oid FROM pg_class WHERE relname='v'").Scan(&gotOID); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow("SELECT oid FROM pg_rewrite WHERE ev_class=$1", oid).Scan(&gotRule); err != nil || gotOID != oid || gotRule != rule {
			t.Fatal(gotOID, gotRule, oid, rule, err)
		}
		rows, err := db.Query("SELECT * FROM v")
		deleteReferenceConsume(t, backend.pg, rows, err, referenceStep{columns: []string{"id", "txt", "extra"}, types: []string{"INT4", "TEXT", "BOOL"}, rows: [][]any{{int64(12), "b", true}}, bag: false})
		rows, err = db.Query("SELECT * FROM v WHERE FALSE")
		deleteReferenceConsume(t, backend.pg, rows, err, referenceStep{columns: []string{"id", "txt", "extra"}, types: []string{"INT4", "TEXT", "BOOL"}, rows: [][]any{}, bag: false})
		updateReturningReferenceExec(t, db, "DROP TABLE t")
		updateReturningReferenceExec(t, db, "INSERT INTO u VALUES(3,'c')")
		rows, err = db.Query("SELECT id FROM w ORDER BY id")
		deleteReferenceConsume(t, backend.pg, rows, err, referenceStep{columns: []string{"id"}, types: []string{"INT4"}, rows: [][]any{{int64(12)}, {int64(13)}}, bag: false})
	}
}

func TestReferenceViewReplacePreparedConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		db := backend.db
		for _, q := range []string{"CREATE TABLE t(id INT)", "INSERT INTO t VALUES(1)", "CREATE VIEW v AS SELECT id FROM t"} {
			updateReturningReferenceExec(t, db, q)
		}
		id, err := db.Prepare("SELECT id FROM v")
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := id.Close(); err != nil {
				t.Error(err)
			}
		}()
		star, err := db.Prepare("SELECT * FROM v")
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := star.Close(); err != nil {
				t.Error(err)
			}
		}()
		replace, err := db.Prepare("CREATE OR REPLACE VIEW v AS SELECT id+10 AS id,TRUE AS extra FROM t")
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := replace.Close(); err != nil {
				t.Error(err)
			}
		}()
		rows, err := id.Query()
		deleteReferenceConsume(t, backend.pg, rows, err, referenceStep{columns: []string{"id"}, types: []string{"INT4"}, rows: [][]any{{int64(1)}}, bag: false})
		for range 2 {
			r, err := replace.Exec()
			if err != nil {
				t.Fatal(err)
			}
			if n, err := r.RowsAffected(); err != nil || n != 0 {
				t.Fatal(n, err)
			}
		}
		rows, err = id.Query()
		deleteReferenceConsume(t, backend.pg, rows, err, referenceStep{columns: []string{"id"}, types: []string{"INT4"}, rows: [][]any{{int64(11)}}, bag: false})
		rows, err = star.Query()
		if rows != nil {
			rows.Close()
			t.Fatal("changed shape returned rows")
		}
		updateReturningReferenceDiagnostic(t, err, referenceDiagnostic{Code: "0A000", Message: "cached plan must not change result type"})
		for _, q := range []string{"ALTER VIEW v RENAME COLUMN id TO key", "ALTER VIEW v RENAME TO renamed", "ALTER VIEW renamed SET(security_barrier=true)", "ALTER VIEW renamed RESET(security_barrier)", "DROP VIEW renamed"} {
			stmt, err := db.Prepare(q)
			if err != nil {
				t.Fatal(q, err)
			}
			r, err := stmt.Exec()
			if closeErr := stmt.Close(); closeErr != nil {
				t.Fatal(closeErr)
			}
			if err != nil {
				t.Fatal(q, err)
			}
			if n, err := r.RowsAffected(); err != nil || n != 0 {
				t.Fatal(q, n, err)
			}
		}
	}
}

func TestReferenceViewReplaceAlterDropConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	schemaReferenceCleanup(t, "s")
	for _, backend := range p.backends() {
		db := backend.db
		for _, q := range []string{"CREATE SCHEMA s", "CREATE TABLE t(id INT)", "INSERT INTO t VALUES(7)", "CREATE VIEW v AS SELECT id FROM t", "CREATE VIEW w AS SELECT id FROM v", "ALTER VIEW v RENAME COLUMN id TO key", "ALTER VIEW v RENAME TO renamed", "ALTER VIEW renamed SET SCHEMA s", "CREATE VIEW v AS SELECT 99 AS id FROM t"} {
			updateReturningReferenceExec(t, db, q)
		}
		rows, err := db.Query("SELECT id FROM w")
		deleteReferenceConsume(t, backend.pg, rows, err, referenceStep{columns: []string{"id"}, types: []string{"INT4"}, rows: [][]any{{int64(7)}}, bag: false})
		_, err = db.Exec("DROP VIEW s.renamed")
		updateReturningReferenceDiagnostic(t, err, referenceDiagnostic{Code: "2BP01", Message: "cannot drop view s.renamed because other objects depend on it", Detail: "view w depends on view s.renamed", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
		_, err = db.Exec("DROP VIEW v,w,missing")
		updateReturningReferenceDiagnostic(t, err, referenceDiagnostic{Code: "42P01", Message: "view \"missing\" does not exist"})
		rows, err = db.Query("SELECT id FROM w")
		deleteReferenceConsume(t, backend.pg, rows, err, referenceStep{columns: []string{"id"}, types: []string{"INT4"}, rows: [][]any{{int64(7)}}, bag: false})
		updateReturningReferenceExec(t, db, "DROP VIEW s.renamed,w RESTRICT")
		updateReturningReferenceExec(t, db, "DROP VIEW v")
		rows, err = db.Query("SELECT id FROM t")
		deleteReferenceConsume(t, backend.pg, rows, err, referenceStep{columns: []string{"id"}, types: []string{"INT4"}, rows: [][]any{{int64(7)}}, bag: false})
	}
}
