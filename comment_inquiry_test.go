package stuntgres_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
)

func commentInquiryRows(t *testing.T, rows *sql.Rows, err error, label string, want []any) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(names, []string{label}) {
		t.Fatal(names, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "TEXT" || types[0].ScanType() != reflect.TypeFor[string]() {
		t.Fatal(types, err)
	}
	got := []any{}
	for rows.Next() {
		var v any
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		got = append(got, v)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("independent text values", got, want)
	}
}

func TestCommentInquiryPublic(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	for _, q := range []string{"CREATE TABLE t(a INT,b TEXT,c BIGINT)", "CREATE TABLE u(a INT)", "COMMENT ON TABLE t IS 'whole'", "COMMENT ON TABLE u IS 'other'", "COMMENT ON COLUMN t.a IS 'first'", "COMMENT ON COLUMN t.b IS 'second'", "COMMENT ON COLUMN t.c IS 'third'", "COMMENT ON ROLE postgres IS 'shared'", "INSERT INTO t VALUES(1,'x',3),(2,'y',4)"} {
		execSQL(t, db, q)
	}
	for _, tc := range []struct {
		expr, label string
		want        any
	}{
		{"obj_description('t'::regclass,'pg_class'::text)", "obj_description", "whole"},
		{"obj_description('t'::regclass)", "obj_description", "whole"},
		{"col_description('t'::regclass,0)", "col_description", "whole"},
		{"col_description('t'::regclass,1)", "col_description", "first"},
		{"col_description('t'::regclass,2)", "col_description", "second"},
		{"col_description('t'::regclass,3)", "col_description", "third"},
		{"shobj_description(10,'pg_authid'::text)", "shobj_description", "shared"},
		{"obj_description(10,'pg_authid')", "obj_description", nil},
		{"shobj_description('t'::regclass,'pg_class')", "shobj_description", nil},
		{"col_description(NULL,1)", "col_description", nil}, {"col_description(1,NULL)", "col_description", nil},
		{"obj_description(NULL)", "obj_description", nil}, {"obj_description(1,NULL)", "obj_description", nil},
		{"shobj_description(NULL,'pg_authid')", "shobj_description", nil}, {"shobj_description(10,NULL)", "shobj_description", nil},
		{"obj_description(4294967295::bigint,'pg_class')", "obj_description", nil},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			rows, err := db.Query("SELECT " + tc.expr)
			commentInquiryRows(t, rows, err, tc.label, []any{tc.want})
			rows, err = db.Query("SELECT " + tc.expr + " FROM t")
			commentInquiryRows(t, rows, err, tc.label, []any{tc.want, tc.want})
		})
	}
	for _, cat := range []string{"PG_CLASS", "pg_catalog.pg_class", "\"pg_class\"", " pg_class", "", "missing"} {
		rows, err := db.Query("SELECT obj_description('t'::regclass,$1)", cat)
		commentInquiryRows(t, rows, err, "obj_description", []any{nil})
	}
	for _, tc := range []struct {
		q, label, target string
		args             []any
		initial          any
	}{
		{"SELECT obj_description($1::regclass,$2)", "obj_description", "TABLE t", []any{"t", "pg_class"}, "whole"},
		{"SELECT obj_description($1::regclass)", "obj_description", "TABLE t", []any{"t"}, "whole"},
		{"SELECT col_description($1::regclass,$2)", "col_description", "COLUMN t.a", []any{"t", 1}, "first"},
		{"SELECT shobj_description($1,$2)", "shobj_description", "ROLE postgres", []any{10, "pg_authid"}, "shared"},
	} {
		t.Run(tc.q, func(t *testing.T) {
			execSQL(t, db, "COMMENT ON "+tc.target+" IS 'initial'")
			stmt, err := db.Prepare(tc.q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			for _, val := range []string{"initial", "replacement", "", " "} {
				execSQL(t, db, "COMMENT ON "+tc.target+" IS '"+val+"'")
				var want any = val
				if val == "" {
					want = nil
				}
				rows, err := stmt.Query(tc.args...)
				commentInquiryRows(t, rows, err, tc.label, []any{want})
			}
			args := append([]any(nil), tc.args...)
			args[0] = nil
			rows, err := stmt.Query(args...)
			commentInquiryRows(t, rows, err, tc.label, []any{nil})
		})
	}
	execSQL(t, db, "ALTER TABLE t DROP b")
	for _, tc := range []struct {
		n    int
		want any
	}{{-1, nil}, {2, nil}, {3, "third"}, {99, nil}} {
		rows, err := db.Query("SELECT col_description('t'::regclass,$1)", tc.n)
		commentInquiryRows(t, rows, err, "col_description", []any{tc.want})
	}
}

func TestCommentInquiryCapturedAndCanceled(t *testing.T) {
	d := stuntgres.NewDriver()
	db := openLocal(t, d, t.Name())
	ctx := context.Background()
	for _, q := range []string{"CREATE TABLE t(a INT)", "INSERT INTO t VALUES(1),(2)", "COMMENT ON TABLE t IS 'old'", "COMMENT ON ROLE postgres IS 'old shared'"} {
		execSQL(t, db, q)
	}
	raw, err := d.Open(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	cursor, err := raw.(driver.QueryerContext).QueryContext(ctx, "SELECT obj_description('t'::regclass,'pg_class'),shobj_description(10,'pg_authid') FROM t", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close()
	execSQL(t, db, "COMMENT ON TABLE t IS 'new'")
	execSQL(t, db, "COMMENT ON ROLE postgres IS 'new shared'")
	if err := stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	v := make([]driver.Value, 2)
	for range 2 {
		if err := cursor.Next(v); err != nil || !reflect.DeepEqual(v, []driver.Value{"old", "old shared"}) {
			t.Fatal(v, err)
		}
	}
	if err := cursor.Next(v); err != io.EOF {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	rows, err := db.QueryContext(cancelled, "SELECT obj_description(11,'pg_namespace')")
	if rows != nil {
		rows.Close()
		t.Fatal("canceled cursor")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	rows, err = db.Query("SELECT shobj_description(10,'pg_authid')")
	commentInquiryRows(t, rows, err, "shobj_description", []any{"new shared"})
}

func TestCommentInquiryControls(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	ctx := context.Background()
	dump := func() string {
		var b bytes.Buffer
		if err := stuntgres.Dump(ctx, db, &b); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}
	for _, tc := range []struct {
		name string
		oid  int
		seed string
	}{{"pg_catalog", 11, "system catalog schema"}, {"public", 2200, "standard public schema"}} {
		rows, err := db.Query("SELECT obj_description($1,'pg_namespace')", tc.oid)
		commentInquiryRows(t, rows, err, "obj_description", []any{tc.seed})
		execSQL(t, db, "COMMENT ON SCHEMA "+tc.name+" IS NULL")
		text := dump()
		if !strings.Contains(text, "COMMENT ON SCHEMA \""+tc.name+"\" IS NULL;") {
			t.Fatal(text)
		}
		if err := stuntgres.Reset(ctx, db); err != nil {
			t.Fatal(err)
		}
		rows, err = db.Query("SELECT obj_description($1,'pg_namespace')", tc.oid)
		commentInquiryRows(t, rows, err, "obj_description", []any{tc.seed})
		if err := stuntgres.Restore(ctx, db, strings.NewReader(text)); err != nil {
			t.Fatal(err)
		}
		rows, err = db.Query("SELECT obj_description($1,'pg_namespace')", tc.oid)
		commentInquiryRows(t, rows, err, "obj_description", []any{nil})
		if dump() != text {
			t.Fatal("noncanonical removal replay")
		}
	}
	for _, q := range []string{"CREATE TABLE t(a INT,b TEXT)", "COMMENT ON TABLE t IS 'whole'", "COMMENT ON COLUMN t.b IS E'quote '' slash \\\\ newline \\n é'", "CREATE VIEW v AS SELECT col_description('t'::regclass,2) FROM t", "INSERT INTO t VALUES(1,'row')"} {
		execSQL(t, db, q)
	}
	before := dump()
	if err := stuntgres.Restore(ctx, db, strings.NewReader(before+"COMMENT ON COLUMN t.missing IS 'bad';")); err == nil {
		t.Fatal("late restore accepted")
	}
	if dump() != before {
		t.Fatal("failed replay changed state")
	}
	if err := stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(ctx, db, strings.NewReader(before)); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query("SELECT col_description('t'::regclass,2)")
	commentInquiryRows(t, rows, err, "col_description", []any{"quote ' slash \\ newline \n é"})
	execSQL(t, db, "COMMENT ON COLUMN t.b IS 'fresh'")
	wantTuples(t, db, "SELECT * FROM v", [][]any{{"fresh"}})
}

func TestCommentInquiryDiagnostics(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
	for _, tc := range []struct {
		q    string
		want stuntgres.Error
	}{
		{"SELECT col_description()", stuntgres.Error{Code: "42883", Message: "function col_description() does not exist", Position: "8", Hint: hint}},
		{"SELECT /*é*/ col_description()", stuntgres.Error{Code: "42883", Message: "function col_description() does not exist", Position: "14", Hint: hint}},
		{"SELECT col_description(1::oid,1::bigint)", stuntgres.Error{Code: "42883", Message: "function col_description(oid, bigint) does not exist", Position: "8", Hint: hint}},
		{"SELECT col_description('1'::text,1)", stuntgres.Error{Code: "42883", Message: "function col_description(text, integer) does not exist", Position: "8", Hint: hint}},
		{"SELECT obj_description(1,TRUE)", stuntgres.Error{Code: "42883", Message: "function obj_description(integer, boolean) does not exist", Position: "8", Hint: hint}},
		{"SELECT shobj_description(1)", stuntgres.Error{Code: "42883", Message: "function shobj_description(integer) does not exist", Position: "8", Hint: hint}},
		{"SELECT col_description('bad',1)", stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type oid: "bad"`, Position: "24"}},
		{"SELECT col_description(NULL,'bad')", stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Position: "29"}},
	} {
		nullCountError(t, db, tc.q, tc.want)
	}
	stmt, err := db.Prepare("SELECT col_description($1,$2)")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for _, args := range [][]any{{"bad", 1}, {nil, "bad"}} {
		rows, err := stmt.Query(args...)
		if rows != nil {
			rows.Close()
			t.Fatal("bad input exposed cursor")
		}
		var d *stuntgres.Error
		typ := "oid"
		if args[0] == nil {
			typ = "integer"
		}
		want := &stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: `invalid input syntax for type ` + typ + `: "bad"`}
		if !errors.As(err, &d) || !reflect.DeepEqual(d, want) {
			t.Fatal(d, want, err)
		}
	}
}

func TestCommentInquiryCompositionAndSequence(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	for _, q := range []string{"CREATE TABLE t(a INT)", "INSERT INTO t VALUES(1),(1),(2)", "COMMENT ON TABLE t IS 'whole'", "CREATE SEQUENCE s", "COMMENT ON SEQUENCE s IS 'old'"} {
		execSQL(t, db, q)
	}
	wantTuples(t, db, "SELECT obj_description('t'::regclass,'pg_class'),count(*) FROM t GROUP BY obj_description('t'::regclass,'pg_class')", [][]any{{"whole", int64(3)}})
	wantTuples(t, db, "SELECT CASE WHEN a=1 THEN obj_description('t'::regclass,'pg_class') ELSE col_description(NULL,1) END FROM t ORDER BY a", [][]any{{"whole"}, {"whole"}, {nil}})
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec("COMMENT ON SEQUENCE s IS 'pending'"); err != nil {
		t.Fatal(err)
	}
	rows, err := tx.Query("SELECT obj_description('s'::regclass,'pg_class')")
	commentInquiryRows(t, rows, err, "obj_description", []any{"pending"})
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	rows, err = db.Query("SELECT obj_description('s'::regclass,'pg_class')")
	commentInquiryRows(t, rows, err, "obj_description", []any{"old"})
}

func TestCommentInquiryEmptyAndIterationCancellation(t *testing.T) {
	d := stuntgres.NewDriver()
	db := openLocal(t, d, t.Name())
	execSQL(t, db, "CREATE TABLE t(a INT)")
	rows, err := db.Query("SELECT col_description(NULL,1) FROM t")
	commentInquiryRows(t, rows, err, "col_description", []any{})
	execSQL(t, db, "INSERT INTO t VALUES(1),(2)")
	execSQL(t, db, "COMMENT ON TABLE t IS 'whole'")
	raw, err := d.Open(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cursor, err := raw.(driver.QueryerContext).QueryContext(ctx, "SELECT obj_description('t'::regclass,'pg_class') FROM t", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close()
	value := make([]driver.Value, 1)
	if err := cursor.Next(value); err != nil || value[0] != "whole" {
		t.Fatal(value, err)
	}
	cancel()
	if err := cursor.Next(value); !errors.Is(err, context.Canceled) {
		t.Fatal("iteration cancellation", err)
	}
	if err := cursor.Close(); err != nil {
		t.Fatal(err)
	}
	rows, err = db.Query("SELECT obj_description('t'::regclass,'pg_class')")
	commentInquiryRows(t, rows, err, "obj_description", []any{"whole"})
}
