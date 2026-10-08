package smoke_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"stuntgres"
	"testing"
)

func TestViewReplaceSmoke(t *testing.T) {
	ctx := context.Background()
	open := func(name string) *sql.DB {
		connector, err := stuntgres.NewDriver().OpenConnector(name)
		if err != nil {
			t.Fatal(err)
		}
		db := sql.OpenDB(connector)
		t.Cleanup(func() { db.Close() })
		return db
	}
	db := open(t.Name())
	exec := func(q string) {
		t.Helper()
		r, err := db.ExecContext(ctx, q)
		if err != nil {
			t.Fatal(q, err)
		}
		if n, err := r.RowsAffected(); err != nil || n != 0 {
			t.Fatal(q, n, err)
		}
	}
	check := func(q string, names, types []string, want [][]any) {
		t.Helper()
		rows, err := db.QueryContext(ctx, q)
		if err != nil {
			t.Fatal(q, err)
		}
		defer rows.Close()
		gotNames, err := rows.Columns()
		if err != nil || !reflect.DeepEqual(gotNames, names) {
			t.Fatal(gotNames, names, err)
		}
		metadata, err := rows.ColumnTypes()
		if err != nil || len(metadata) != len(types) {
			t.Fatal(metadata, err)
		}
		for i, typ := range metadata {
			if typ.DatabaseTypeName() != types[i] {
				t.Fatal(i, typ.DatabaseTypeName(), types[i])
			}
		}
		got := [][]any{}
		for rows.Next() {
			row := make([]any, len(names))
			dst := make([]any, len(names))
			for i := range row {
				dst[i] = &row[i]
			}
			if err := rows.Scan(dst...); err != nil {
				t.Fatal(err)
			}
			got = append(got, row)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatal(q, got, want)
		}
	}
	diagnostic := func(err error, want stuntgres.Error) {
		t.Helper()
		want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
		var got *stuntgres.Error
		if !errors.As(err, &got) || *got != want {
			t.Fatalf("got %#v (%v), want %#v", got, err, want)
		}
	}
	for _, q := range []string{"CREATE SCHEMA s", "CREATE TABLE t(id INT,txt TEXT)", "CREATE TABLE u(id INT,txt TEXT)", "CREATE VIEW v AS SELECT id,txt FROM t", "CREATE VIEW w AS SELECT id FROM v"} {
		exec(q)
	}
	for _, q := range []string{"INSERT INTO t VALUES(1,'a')", "INSERT INTO u VALUES(2,'b')"} {
		if r, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		} else if n, err := r.RowsAffected(); err != nil || n != 1 {
			t.Fatal(n, err)
		}
	}
	var oid, rule int64
	if err := db.QueryRowContext(ctx, "SELECT oid FROM pg_class WHERE relname='v'").Scan(&oid); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, "SELECT oid FROM pg_rewrite WHERE ev_class=$1", oid).Scan(&rule); err != nil {
		t.Fatal(err)
	}
	retained, err := db.PrepareContext(ctx, "SELECT id FROM v")
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	exec("CREATE OR REPLACE VIEW v WITH(security_barrier=true,security_invoker=true) AS SELECT id+10 AS id,txt,TRUE AS extra FROM u")
	var gotOID, gotRule int64
	if err := db.QueryRowContext(ctx, "SELECT oid FROM pg_class WHERE relname='v'").Scan(&gotOID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, "SELECT oid FROM pg_rewrite WHERE ev_class=$1", oid).Scan(&gotRule); err != nil || gotOID != oid || gotRule != rule {
		t.Fatal(gotOID, gotRule, oid, rule, err)
	}
	check("SELECT * FROM v", []string{"id", "txt", "extra"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(12), "b", true}})
	check("SELECT * FROM v WHERE FALSE", []string{"id", "txt", "extra"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{})
	var id int64
	if err := retained.QueryRowContext(ctx).Scan(&id); err != nil || id != 12 {
		t.Fatal(id, err)
	}
	_, err = db.ExecContext(ctx, "CREATE OR REPLACE VIEW v AS SELECT id AS renamed,txt,TRUE AS extra FROM u")
	diagnostic(err, stuntgres.Error{Code: "42P16", Message: "cannot change name of view column \"id\" to \"renamed\"", Hint: "Use ALTER VIEW ... RENAME COLUMN ... to change name of view column instead."})
	_, err = db.ExecContext(ctx, "DROP VIEW v")
	diagnostic(err, stuntgres.Error{Code: "2BP01", Message: "cannot drop view v because other objects depend on it", Detail: "view w depends on view v", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
	for _, q := range []string{"ALTER VIEW v SET(security_invoker=false)", "ALTER VIEW v RESET(security_barrier)", "CREATE OR REPLACE VIEW v AS SELECT id+10 AS id,txt,TRUE AS extra FROM u", "ALTER VIEW v RENAME COLUMN id TO key", "ALTER VIEW v RENAME TO renamed", "ALTER VIEW renamed SET SCHEMA s", "DROP TABLE t"} {
		exec(q)
	}
	check("SELECT id FROM w", []string{"id"}, []string{"INT4"}, [][]any{{int64(12)}})
	exec("CREATE VIEW cyclic AS SELECT id FROM u")
	exec("CREATE OR REPLACE VIEW cyclic AS SELECT id FROM cyclic")
	rows, err := db.QueryContext(ctx, "SELECT id FROM cyclic")
	if rows != nil {
		rows.Close()
		t.Fatal("cyclic read returned rows")
	}
	diagnostic(err, stuntgres.Error{Code: "42P17", Message: "infinite recursion detected in rules for relation \"cyclic\""})
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &dump); err == nil || err.Error() != "stuntgres: cyclic view dump dependencies" {
		t.Fatal(err)
	}
	exec("CREATE OR REPLACE VIEW cyclic AS SELECT id FROM u")
	dump.Reset()
	if err := stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	db = open(t.Name() + "-restore")
	if err := stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO u VALUES(3,'c')"); err != nil {
		t.Fatal(err)
	}
	check("SELECT id FROM w ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(12)}, {int64(13)}})
	check("SELECT key,txt,extra FROM s.renamed ORDER BY key", []string{"key", "txt", "extra"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(12), "b", true}, {int64(13), "c", true}})
}
