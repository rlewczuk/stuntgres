package stuntgres_test

import (
	"bytes"
	"context"
	"database/sql"
	"reflect"
	"stuntgres"
	"testing"
)

func TestViewNativeNestedSecondDumpAfterTemporaryRemap(t *testing.T) {
	ctx := context.Background()
	source := openLocal(t, stuntgres.NewDriver(), t.Name()+"_source")
	for _, query := range []string{
		"CREATE TABLE t(id INT)",
		"INSERT INTO t VALUES(7)",
		"CREATE VIEW v AS SELECT id FROM t",
		"CREATE VIEW w AS SELECT id FROM v",
	} {
		execSQL(t, source, query)
	}
	var first bytes.Buffer
	if err := stuntgres.Dump(ctx, source, &first); err != nil {
		t.Fatal(err)
	}

	destination := openLocal(t, stuntgres.NewDriver(), t.Name()+"_destination")
	// Keep the TEMP namespace and both sequences alive on this physical session.
	retained, err := destination.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	for _, name := range []string{"retained_one", "retained_two"} {
		if _, err := retained.ExecContext(ctx, "CREATE TEMPORARY SEQUENCE "+name); err != nil {
			t.Fatal(err)
		}
		var value int64
		if err := retained.QueryRowContext(ctx, "SELECT nextval('"+name+"')").Scan(&value); err != nil || value != 1 {
			t.Fatalf("initial %s: value=%d err=%v", name, value, err)
		}
	}
	if err := stuntgres.Restore(ctx, destination, bytes.NewReader(first.Bytes())); err != nil {
		t.Fatalf("first restore: %v", err)
	}
	check := func(db *sql.DB, stmt *sql.Stmt, want []int64) {
		t.Helper()
		checkRows := func(rows *sql.Rows, err error) {
			t.Helper()
			if err != nil {
				t.Fatal(err)
			}
			metadata, err := rows.ColumnTypes()
			if err != nil || len(metadata) != 1 {
				rows.Close()
				t.Fatalf("metadata=%v err=%v", metadata, err)
			}
			column := metadata[0]
			nullable, nullableKnown := column.Nullable()
			_, lengthKnown := column.Length()
			_, _, decimalKnown := column.DecimalSize()
			if column.Name() != "id" || column.DatabaseTypeName() != "INT4" || column.ScanType() != reflect.TypeFor[int64]() || !nullable || !nullableKnown || lengthKnown || decimalKnown {
				rows.Close()
				t.Fatalf("unexpected id metadata: %+v", column)
			}
			values := make([][]any, len(want))
			for i, value := range want {
				values[i] = []any{value}
			}
			queryCoreRows(t, rows, nil, []string{"id"}, []string{"INT4"}, values, false)
		}
		checkRows(db.Query("SELECT id FROM w ORDER BY id"))
		checkRows(stmt.Query())
	}
	held, err := destination.Prepare("SELECT id FROM w ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	check(destination, held, []int64{7})
	var second bytes.Buffer
	if err := stuntgres.Dump(ctx, destination, &second); err != nil {
		t.Fatal(err)
	}
	check(destination, held, []int64{7})
	for _, name := range []string{"retained_one", "retained_two"} {
		var current, next int64
		if err := retained.QueryRowContext(ctx, "SELECT currval('"+name+"')").Scan(&current); err != nil || current != 1 {
			t.Fatalf("retained %s: currval=%d err=%v", name, current, err)
		}
		if err := retained.QueryRowContext(ctx, "SELECT nextval('"+name+"')").Scan(&next); err != nil || next != 2 {
			t.Fatalf("retained %s: nextval=%d err=%v", name, next, err)
		}
	}
	third := openLocal(t, stuntgres.NewDriver(), t.Name()+"_third")
	if err := stuntgres.Restore(ctx, third, bytes.NewReader(second.Bytes())); err != nil {
		t.Fatalf("second restore: %v", err)
	}
	thirdHeld, err := third.Prepare("SELECT id FROM w ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer thirdHeld.Close()
	check(third, thirdHeld, []int64{7})
	execSQL(t, third, "INSERT INTO t VALUES(8)")
	check(third, thirdHeld, []int64{7, 8})
}
