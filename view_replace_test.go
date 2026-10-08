package stuntgres_test

import (
	"bytes"
	"context"
	"stuntgres"
	"testing"
)

func TestViewReplaceDefinitionAndMetadata(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	for _, q := range []string{"CREATE TABLE t(id INT,txt TEXT)", "CREATE TABLE u(id INT,txt TEXT)", "INSERT INTO t VALUES(1,'a')", "INSERT INTO u VALUES(2,'b')", "CREATE VIEW v AS SELECT id,txt FROM t", "CREATE VIEW w AS SELECT id FROM v"} {
		execSQL(t, db, q)
	}
	var oid, rule int64
	if err := db.QueryRow("SELECT oid FROM pg_class WHERE relname='v'").Scan(&oid); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT oid FROM pg_rewrite WHERE ev_class=$1", oid).Scan(&rule); err != nil {
		t.Fatal(err)
	}
	execSQL(t, db, "CREATE OR REPLACE VIEW v AS SELECT id+10 AS id,txt,TRUE AS extra FROM u")
	var gotOID, gotRule int64
	if err := db.QueryRow("SELECT oid FROM pg_class WHERE relname='v'").Scan(&gotOID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT oid FROM pg_rewrite WHERE ev_class=$1", oid).Scan(&gotRule); err != nil || gotOID != oid || gotRule != rule {
		t.Fatal(gotOID, gotRule, oid, rule, err)
	}
	rows, err := db.Query("SELECT * FROM v")
	queryCoreRows(t, rows, err, []string{"id", "txt", "extra"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(12), "b", true}}, false)
	rows, err = db.Query("SELECT * FROM v WHERE FALSE")
	queryCoreRows(t, rows, err, []string{"id", "txt", "extra"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{}, false)
	execSQL(t, db, "DROP TABLE t")
	execSQL(t, db, "INSERT INTO u VALUES(3,'c')")
	rows, err = db.Query("SELECT id FROM w ORDER BY id")
	queryCoreRows(t, rows, err, []string{"id"}, []string{"INT4"}, [][]any{{int64(12)}, {int64(13)}}, false)
}

func TestViewReplacePrepared(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	for _, q := range []string{"CREATE TABLE t(id INT)", "INSERT INTO t VALUES(1)", "CREATE VIEW v AS SELECT id FROM t"} {
		execSQL(t, db, q)
	}
	id, err := db.Prepare("SELECT id FROM v")
	if err != nil {
		t.Fatal(err)
	}
	defer id.Close()
	star, err := db.Prepare("SELECT * FROM v")
	if err != nil {
		t.Fatal(err)
	}
	defer star.Close()
	replace, err := db.Prepare("CREATE OR REPLACE VIEW v AS SELECT id+10 AS id,TRUE AS extra FROM t")
	if err != nil {
		t.Fatal(err)
	}
	defer replace.Close()
	rows, err := id.Query()
	queryCoreRows(t, rows, err, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}, false)
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
	queryCoreRows(t, rows, err, []string{"id"}, []string{"INT4"}, [][]any{{int64(11)}}, false)
	rows, err = star.Query()
	if rows != nil {
		rows.Close()
		t.Fatal("changed shape returned rows")
	}
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "0A000", Message: "cached plan must not change result type"})
	for _, q := range []string{"ALTER VIEW v RENAME COLUMN id TO key", "ALTER VIEW v RENAME TO renamed", "ALTER VIEW renamed SET(security_barrier=true)", "ALTER VIEW renamed RESET(security_barrier)", "DROP VIEW renamed"} {
		stmt, err := db.Prepare(q)
		if err != nil {
			t.Fatal(q, err)
		}
		r, err := stmt.Exec()
		stmt.Close()
		if err != nil {
			t.Fatal(q, err)
		}
		if n, err := r.RowsAffected(); err != nil || n != 0 {
			t.Fatal(q, n, err)
		}
	}
}

func TestViewReplaceAlterDrop(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	for _, q := range []string{"CREATE SCHEMA s", "CREATE TABLE t(id INT)", "INSERT INTO t VALUES(7)", "CREATE VIEW v AS SELECT id FROM t", "CREATE VIEW w AS SELECT id FROM v", "ALTER VIEW v RENAME COLUMN id TO key", "ALTER VIEW v RENAME TO renamed", "ALTER VIEW renamed SET SCHEMA s", "CREATE VIEW v AS SELECT 99 AS id FROM t"} {
		execSQL(t, db, q)
	}
	rows, err := db.Query("SELECT id FROM w")
	queryCoreRows(t, rows, err, []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}, false)
	_, err = db.Exec("DROP VIEW s.renamed")
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "2BP01", Message: "cannot drop view s.renamed because other objects depend on it", Detail: "view w depends on view s.renamed", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
	_, err = db.Exec("DROP VIEW v,w,missing")
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42P01", Message: "view \"missing\" does not exist"})
	rows, err = db.Query("SELECT id FROM w")
	queryCoreRows(t, rows, err, []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}, false)
	execSQL(t, db, "DROP VIEW s.renamed,w RESTRICT")
	execSQL(t, db, "DROP VIEW v")
	rows, err = db.Query("SELECT id FROM t")
	queryCoreRows(t, rows, err, []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}, false)
}

func TestViewReplaceNativeControls(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	for _, q := range []string{"CREATE SCHEMA s", "CREATE TABLE t(id INT,txt TEXT)", "INSERT INTO t VALUES(2,'b')", "CREATE VIEW v AS SELECT id,txt FROM t", "CREATE VIEW w AS SELECT id FROM v", "CREATE OR REPLACE VIEW v WITH(security_barrier=true,security_invoker=true) AS SELECT id+10 AS id,txt,TRUE AS extra FROM t", "ALTER VIEW v RENAME COLUMN id TO key", "ALTER VIEW v RENAME TO renamed", "ALTER VIEW renamed SET SCHEMA s", "ALTER VIEW s.renamed OWNER TO CURRENT_USER", "ALTER VIEW s.renamed RESET(security_invoker)"} {
		execSQL(t, db, q)
	}
	for i := range 2 {
		var dump bytes.Buffer
		if err := stuntgres.Dump(ctx, db, &dump); err != nil {
			t.Fatal(err)
		}
		fresh := openLocal(t, stuntgres.NewDriver(), t.Name()+string(rune('a'+i)))
		fresh.SetMaxOpenConns(1)
		execSQL(t, fresh, "CREATE TEMPORARY SEQUENCE retained_one")
		execSQL(t, fresh, "CREATE TEMPORARY SEQUENCE retained_two")
		if err := stuntgres.Restore(ctx, fresh, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		db = fresh
		rows, err := db.Query("SELECT key,txt,extra FROM s.renamed")
		queryCoreRows(t, rows, err, []string{"key", "txt", "extra"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(12), "b", true}}, false)
	}
	execSQL(t, db, "INSERT INTO t VALUES(3,'c')")
	rows, err := db.Query("SELECT id FROM w ORDER BY id")
	queryCoreRows(t, rows, err, []string{"id"}, []string{"INT4"}, [][]any{{int64(12)}, {int64(13)}}, false)
	if err := stuntgres.Restore(ctx, db, bytes.NewBufferString("CREATE TABLE lost(id INT); CREATE VIEW broken AS SELECT missing FROM lost;")); err == nil {
		t.Fatal("invalid restore accepted")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := stuntgres.Restore(canceled, db, bytes.NewBufferString("CREATE TABLE lost(id INT);")); err == nil {
		t.Fatal("canceled restore accepted")
	}
	rows, err = db.Query("SELECT id FROM w ORDER BY id")
	queryCoreRows(t, rows, err, []string{"id"}, []string{"INT4"}, [][]any{{int64(12)}, {int64(13)}}, false)
}
