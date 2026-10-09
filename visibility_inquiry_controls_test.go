package stuntgres_test

import (
	"bytes"
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
)

func TestVisibilityInquiryControlsAndViews(t *testing.T) {
	ctx := context.Background()
	d := stuntgres.NewDriver()
	db := openLocal(t, d, t.Name())
	db.SetMaxOpenConns(2)
	for _, q := range []string{"CREATE SCHEMA a", "CREATE TABLE a.t(id OID)", "INSERT INTO a.t VALUES(23),(0),(NULL)", "CREATE SEQUENCE a.s", "CREATE VIEW a.v AS SELECT pg_type_is_visible(id) AS visible FROM a.t", "CREATE VIEW a.null_v AS SELECT pg_table_is_visible(NULL) AS visible FROM a.t", "CREATE TABLE a.checked(id OID CHECK(pg_type_is_visible(id) IS NOT NULL))", "INSERT INTO a.checked VALUES(23)"} {
		execSQL(t, db, q)
	}
	check := func() {
		t.Helper()
		wantTuples(t, db, "SELECT visible FROM a.v ORDER BY visible", [][]any{{true}, {nil}, {nil}})
		wantTuples(t, db, "SELECT visible FROM a.null_v", [][]any{{nil}, {nil}, {nil}})
		for _, expr := range []string{"pg_table_is_visible('a.t'::regclass)", "pg_table_is_visible('a.s'::regclass)", "pg_table_is_visible('a.v'::regclass)"} {
			rows, err := db.Query("SELECT " + expr)
			visibilityInquiryRows(t, rows, err, "pg_table_is_visible", []any{false})
		}
		wantTuples(t, db, "SELECT pg_collation_is_visible(811),pg_conversion_is_visible(4402)", [][]any{{true, true}})
	}
	check()
	rows, err := db.Query("SELECT pg_type_is_visible(id) FROM a.t ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	visibilityInquiryRows(t, rows, nil, "pg_type_is_visible", []any{nil, true, nil})
	check()
	other := openLocal(t, d, t.Name()+"-foreign")
	if err := stuntgres.Restore(ctx, other, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	wantTuples(t, other, "SELECT visible FROM a.v ORDER BY visible", [][]any{{true}, {nil}, {nil}})
	if err := stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE broken(id OID); INSERT INTO broken VALUES('bad');")); err == nil {
		t.Fatal("invalid Restore succeeded")
	}
	check()
}

func TestVisibilityInquiryPreparedInputsAndCancellation(t *testing.T) {
	d := stuntgres.NewDriver()
	db := openLocal(t, d, t.Name())
	for _, fn := range []string{"pg_table_is_visible", "pg_type_is_visible", "pg_collation_is_visible", "pg_conversion_is_visible"} {
		stmt, err := db.Prepare("SELECT " + fn + "($1)")
		if err != nil {
			t.Fatal(err)
		}
		rows, err := stmt.Query("bad")
		if rows != nil {
			rows.Close()
			t.Fatal("bad input exposed rows")
		}
		var diagnostic *stuntgres.Error
		want := &stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: `invalid input syntax for type oid: "bad"`, Context: "unnamed portal parameter $1 = '...'"}
		if !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) {
			t.Fatal(err, diagnostic, want)
		}
		stmt.Close()
	}
	for _, q := range []string{"CREATE TABLE ids(id OID)", "INSERT INTO ids VALUES(23),(23)"} {
		execSQL(t, db, q)
	}
	raw, err := d.Open(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cursor, err := raw.(driver.QueryerContext).QueryContext(ctx, "SELECT pg_type_is_visible(id) FROM ids", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close()
	values := make([]driver.Value, 1)
	if err := cursor.Next(values); err != nil || values[0] != true {
		t.Fatal(values, err)
	}
	cancel()
	if err := cursor.Next(values); !errors.Is(err, context.Canceled) {
		t.Fatal("iteration cancellation", err)
	}
	if _, err := db.QueryContext(ctx, "SELECT pg_type_is_visible(23)"); !errors.Is(err, context.Canceled) {
		t.Fatal("pre-canceled query", err)
	}
}

func TestVisibilityInquiryForeignTemporaryNamespace(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	db.SetMaxOpenConns(2)
	ctx := context.Background()
	owner, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	peer, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	if _, err := owner.ExecContext(ctx, "CREATE TEMP SEQUENCE subject"); err != nil {
		t.Fatal(err)
	}
	var oid int64
	var path string
	if err := owner.QueryRowContext(ctx, "SELECT oid FROM pg_catalog.pg_class WHERE relname='subject'").Scan(&oid); err != nil {
		t.Fatal(err)
	}
	if err := owner.QueryRowContext(ctx, "SELECT current_schemas(true)").Scan(&path); err != nil {
		t.Fatal(err)
	}
	temporary := strings.Split(strings.Trim(path, "{}"), ",")[0]
	if !strings.HasPrefix(temporary, "pg_temp_") {
		t.Fatal("missing independently discovered TEMP identity", path)
	}
	rows, err := owner.QueryContext(ctx, "SELECT pg_table_is_visible($1)", oid)
	visibilityInquiryRows(t, rows, err, "pg_table_is_visible", []any{true})
	if _, err := peer.ExecContext(ctx, "SET search_path="+temporary+",public"); err != nil {
		t.Fatal(err)
	}
	rows, err = peer.QueryContext(ctx, "SELECT pg_table_is_visible($1)", oid)
	visibilityInquiryRows(t, rows, err, "pg_table_is_visible", []any{false})
}
