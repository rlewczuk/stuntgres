package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceViewReplaceDefinitionAndMetadataConsumerRoutes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		db := backend.DB
		for _, q := range []string{"CREATE TABLE t(id INT,txt TEXT)", "CREATE TABLE u(id INT,txt TEXT)", "INSERT INTO t VALUES(1,'a')", "INSERT INTO u VALUES(2,'b')", "CREATE VIEW v AS SELECT id,txt FROM t", "CREATE VIEW w AS SELECT id FROM v"} {
			fixture.UpdateReturningReferenceExec(t, db, q)
		}
		var oid, rule int64
		if err := db.QueryRow("SELECT oid FROM pg_class WHERE relname='v'").Scan(&oid); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow("SELECT oid FROM pg_rewrite WHERE ev_class=$1", oid).Scan(&rule); err != nil {
			t.Fatal(err)
		}
		fixture.UpdateReturningReferenceExec(t, db, "CREATE OR REPLACE VIEW v AS SELECT id+10 AS id,txt,TRUE AS extra FROM u")
		var gotOID, gotRule int64
		if err := db.QueryRow("SELECT oid FROM pg_class WHERE relname='v'").Scan(&gotOID); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow("SELECT oid FROM pg_rewrite WHERE ev_class=$1", oid).Scan(&gotRule); err != nil || gotOID != oid || gotRule != rule {
			t.Fatal(gotOID, gotRule, oid, rule, err)
		}
		rows, err := db.Query("SELECT * FROM v")
		fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Step{Columns: []string{"id", "txt", "extra"}, Types: []string{"INT4", "TEXT", "BOOL"}, Rows: [][]any{{int64(12), "b", true}}, Bag: false})
		rows, err = db.Query("SELECT * FROM v WHERE FALSE")
		fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Step{Columns: []string{"id", "txt", "extra"}, Types: []string{"INT4", "TEXT", "BOOL"}, Rows: [][]any{}, Bag: false})
		fixture.UpdateReturningReferenceExec(t, db, "DROP TABLE t")
		fixture.UpdateReturningReferenceExec(t, db, "INSERT INTO u VALUES(3,'c')")
		rows, err = db.Query("SELECT id FROM w ORDER BY id")
		fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Step{Columns: []string{"id"}, Types: []string{"INT4"}, Rows: [][]any{{int64(12)}, {int64(13)}}, Bag: false})
	}
}

func TestReferenceViewReplacePreparedConsumerRoutes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		db := backend.DB
		for _, q := range []string{"CREATE TABLE t(id INT)", "INSERT INTO t VALUES(1)", "CREATE VIEW v AS SELECT id FROM t"} {
			fixture.UpdateReturningReferenceExec(t, db, q)
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
		fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Step{Columns: []string{"id"}, Types: []string{"INT4"}, Rows: [][]any{{int64(1)}}, Bag: false})
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
		fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Step{Columns: []string{"id"}, Types: []string{"INT4"}, Rows: [][]any{{int64(11)}}, Bag: false})
		rows, err = star.Query()
		if rows != nil {
			rows.Close()
			t.Fatal("changed shape returned rows")
		}
		fixture.UpdateReturningReferenceDiagnostic(t, err, fixture.Diagnostic{Code: "0A000", Message: "cached plan must not change result type"})
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
	p := fixture.NewScenario(t)
	fixture.SchemaReferenceCleanup(t, "s")
	for _, backend := range p.Backends() {
		db := backend.DB
		for _, q := range []string{"CREATE SCHEMA s", "CREATE TABLE t(id INT)", "INSERT INTO t VALUES(7)", "CREATE VIEW v AS SELECT id FROM t", "CREATE VIEW w AS SELECT id FROM v", "ALTER VIEW v RENAME COLUMN id TO key", "ALTER VIEW v RENAME TO renamed", "ALTER VIEW renamed SET SCHEMA s", "CREATE VIEW v AS SELECT 99 AS id FROM t"} {
			fixture.UpdateReturningReferenceExec(t, db, q)
		}
		rows, err := db.Query("SELECT id FROM w")
		fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Step{Columns: []string{"id"}, Types: []string{"INT4"}, Rows: [][]any{{int64(7)}}, Bag: false})
		_, err = db.Exec("DROP VIEW s.renamed")
		fixture.UpdateReturningReferenceDiagnostic(t, err, fixture.Diagnostic{Code: "2BP01", Message: "cannot drop view s.renamed because other objects depend on it", Detail: "view w depends on view s.renamed", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
		_, err = db.Exec("DROP VIEW v,w,missing")
		fixture.UpdateReturningReferenceDiagnostic(t, err, fixture.Diagnostic{Code: "42P01", Message: "view \"missing\" does not exist"})
		rows, err = db.Query("SELECT id FROM w")
		fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Step{Columns: []string{"id"}, Types: []string{"INT4"}, Rows: [][]any{{int64(7)}}, Bag: false})
		fixture.UpdateReturningReferenceExec(t, db, "DROP VIEW s.renamed,w RESTRICT")
		fixture.UpdateReturningReferenceExec(t, db, "DROP VIEW v")
		rows, err = db.Query("SELECT id FROM t")
		fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Step{Columns: []string{"id"}, Types: []string{"INT4"}, Rows: [][]any{{int64(7)}}, Bag: false})
	}
}
