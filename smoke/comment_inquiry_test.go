package smoke_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
)

func TestCommentInquirySmoke(t *testing.T) {
	ctx := context.Background()
	d := stuntgres.NewDriver()
	connector, err := d.OpenConnector(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	exec := func(q string) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(q, err)
		}
	}
	check := func(rows *sql.Rows, err error, label string, want any) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		cols, err := rows.Columns()
		if err != nil || !reflect.DeepEqual(cols, []string{label}) {
			t.Fatal(cols, err)
		}
		types, err := rows.ColumnTypes()
		if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "TEXT" || types[0].ScanType() != reflect.TypeFor[string]() {
			t.Fatal(types, err)
		}
		if !rows.Next() {
			t.Fatal("missing row", rows.Err())
		}
		var got any
		if err := rows.Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatal(got, want)
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal(rows.Err())
		}
	}
	for _, q := range []string{"CREATE TABLE t(a INT,b TEXT,c BIGINT)", "COMMENT ON TABLE t IS 'whole'", "COMMENT ON COLUMN t.a IS 'first'", "COMMENT ON COLUMN t.c IS 'third'", "COMMENT ON ROLE postgres IS 'shared'"} {
		exec(q)
	}
	for _, tc := range []struct {
		q, label string
		want     any
	}{
		{"SELECT obj_description('t'::regclass,'pg_class'::text)", "obj_description", "whole"},
		{"SELECT obj_description('t'::regclass)", "obj_description", "whole"},
		{"SELECT col_description('t'::regclass,0)", "col_description", "whole"},
		{"SELECT col_description('t'::regclass,1)", "col_description", "first"},
		{"SELECT shobj_description(10,'pg_authid'::text)", "shobj_description", "shared"},
		{"SELECT obj_description(11,'pg_namespace')", "obj_description", "system catalog schema"},
		{"SELECT obj_description(23,'pg_type')", "obj_description", "-2 billion to 2 billion integer, 4-byte storage"},
		{"SELECT col_description(NULL,1)", "col_description", nil},
		{"SELECT obj_description(0,'pg_class')", "obj_description", nil},
		{"SELECT shobj_description('t'::regclass,'pg_class')", "shobj_description", nil},
	} {
		rows, err := db.QueryContext(ctx, tc.q)
		check(rows, err, tc.label, tc.want)
	}
	stmt, err := db.PrepareContext(ctx, "SELECT col_description($1::regclass,$2)")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for _, tc := range []struct {
		sql  string
		want any
	}{{"'replacement'", "replacement"}, {"NULL", nil}, {"''", nil}, {"' '", " "}} {
		exec("COMMENT ON COLUMN t.a IS " + tc.sql)
		rows, err := stmt.QueryContext(ctx, "t", 1)
		check(rows, err, "col_description", tc.want)
	}
	exec("ALTER TABLE t DROP b")
	rows, err := stmt.QueryContext(ctx, "t", 2)
	check(rows, err, "col_description", nil)
	rows, err = stmt.QueryContext(ctx, "t", 3)
	check(rows, err, "col_description", "third")
	const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
	for _, tc := range []struct {
		q    string
		want stuntgres.Error
	}{
		{"SELECT col_description()", stuntgres.Error{Code: "42883", Message: "function col_description() does not exist", Position: "8", Hint: hint}},
		{"SELECT col_description(NULL,'bad')", stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Position: "29"}},
	} {
		rows, err := db.QueryContext(ctx, tc.q)
		if rows != nil {
			rows.Close()
			t.Fatal("failed query exposed rows")
		}
		if err := typeCoreDiagnostic(err, tc.want); err != nil {
			t.Fatal(err)
		}
		s, err := db.PrepareContext(ctx, tc.q)
		if s != nil {
			s.Close()
			t.Fatal("failed prepare exposed handle")
		}
		if err := typeCoreDiagnostic(err, tc.want); err != nil {
			t.Fatal(err)
		}
	}
	exec("COMMENT ON SCHEMA public IS NULL")
	var b bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &b); err != nil {
		t.Fatal(err)
	}
	text := b.String()
	if !strings.Contains(text, `COMMENT ON SCHEMA "public" IS NULL;`) {
		t.Fatal(text)
	}
	if err := stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	rows, err = db.QueryContext(ctx, "SELECT obj_description(2200,'pg_namespace')")
	check(rows, err, "obj_description", "standard public schema")
	if err := stuntgres.Restore(ctx, db, strings.NewReader(text)); err != nil {
		t.Fatal(err)
	}
	rows, err = db.QueryContext(ctx, "SELECT obj_description(2200,'pg_namespace')")
	check(rows, err, "obj_description", nil)
	rows, err = db.QueryContext(ctx, "SELECT shobj_description(10,'pg_authid')")
	check(rows, err, "shobj_description", "shared")
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	rows, err = db.QueryContext(cancelled, "SELECT obj_description(NULL)")
	if rows != nil {
		rows.Close()
		t.Fatal("canceled rows")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
