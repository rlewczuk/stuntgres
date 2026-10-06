package stuntgres_test

import (
	"bytes"
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
)

type byteaFailedReader struct{ err error }

func (r byteaFailedReader) Read([]byte) (int, error) { return 0, r.err }
func TestByteaControlsAndCancellation(t *testing.T) {
	ctx := context.Background()
	d := stuntgres.NewDriver()
	db := openLocal(t, d, t.Name())
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	execSQL(t, db, "CREATE TABLE t(id INTEGER NOT NULL,v BYTEA,r BYTEA NOT NULL)")
	execSQL(t, db, `INSERT INTO t VALUES(1,'',''),(2,'\x0080ff5c27','\x0080ff'),(3,NULL,'\xff')`)
	raw, err := d.Open(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	held, err := raw.(driver.QueryerContext).QueryContext(ctx, "SELECT v FROM t ORDER BY id", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	for _, piece := range []string{`"v" BYTEA, "r" BYTEA CONSTRAINT "t_r_not_null" NOT NULL`, `'\x'`, `'\x0080ff5c27'`, `'\x0080ff'`, `'\xff'`, "NULL"} {
		if !strings.Contains(dump.String(), piece) {
			t.Fatal(piece, dump.String())
		}
	}
	if _, err := conn.ExecContext(ctx, "SET bytea_output=escape"); err != nil {
		t.Fatal(err)
	}
	var escape bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &escape); err != nil || !bytes.Equal(dump.Bytes(), escape.Bytes()) {
		t.Fatal(escape.String(), err)
	}
	if _, err := conn.ExecContext(ctx, "SET standard_conforming_strings=off"); err != nil {
		t.Fatal(err)
	}
	execSQL(t, db, "INSERT INTO t VALUES(4,$1,$1)", []byte{77})
	if err := stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	for _, setting := range []struct{ name, want string }{{"bytea_output", "escape"}, {"standard_conforming_strings", "off"}} {
		var got string
		if err := conn.QueryRowContext(ctx, "SHOW "+setting.name).Scan(&got); err != nil || got != setting.want {
			t.Fatal(got, err)
		}
	}
	rows, err := db.Query("SELECT v,r FROM t ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 2 {
		t.Fatal(types, err)
	}
	for i, name := range []string{"v", "r"} {
		column := types[i]
		if column.Name() != name || column.DatabaseTypeName() != "BYTEA" || column.ScanType() != reflect.TypeFor[[]byte]() {
			t.Fatal("restored BYTEA schema", column)
		}
		if nullable, known := column.Nullable(); !known || nullable != (i == 0) {
			t.Fatal("restored declaration nullability", name, nullable, known)
		}
	}
	expected := [][]any{{[]byte{}, []byte{}}, {[]byte{0, 128, 255, 92, 39}, []byte{0, 128, 255}}, {nil, []byte{255}}}
	for i := range expected {
		if !rows.Next() {
			t.Fatal("missing restored row", i, rows.Err())
		}
		var nullable, required any
		if err := rows.Scan(&nullable, &required); err != nil || !reflect.DeepEqual([]any{nullable, required}, expected[i]) {
			t.Fatal("restored bytes", i, nullable, required, err)
		}
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("extra restored rows", rows.Err())
	}
	rows.Close()
	// A later required-BYTEA NULL must publish neither the good prefix nor itself.
	_, err = db.Exec("INSERT INTO t VALUES(4,NULL,$1),(5,NULL,NULL)", []byte{})
	typeCoreDiagnostic(t, err, stuntgres.Error{
		Code: "23502", Message: `null value in column "r" of relation "t" violates not-null constraint`,
		Detail: "Failing row contains (5, null, null).", Schema: "public", Table: "t", Column: "r",
	})
	wantTuples(t, db, "SELECT v,r FROM t ORDER BY id", expected)
	wantTuples(t, db, "SELECT v FROM t ORDER BY id", [][]any{{[]byte{}}, {[]byte{0, 128, 255, 92, 39}}, {nil}})
	dst := make([]driver.Value, 1)
	for _, want := range []any{[]byte{}, []byte{0, 128, 255, 92, 39}, nil} {
		if err := held.Next(dst); err != nil || !reflect.DeepEqual(dst[0], want) {
			t.Fatal(dst, err)
		}
		if b, ok := dst[0].([]byte); ok && len(b) > 0 {
			b[0] = 99
		}
	}
	if err := held.Next(dst); err != io.EOF {
		t.Fatal(err)
	}
	if held.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(0) != "BYTEA" || held.(driver.RowsColumnTypeScanType).ColumnTypeScanType(0) != reflect.TypeFor[[]byte]() {
		t.Fatal("retained schema")
	}
	sentinel := errors.New("reader failed")
	for _, failure := range []struct {
		ctx    context.Context
		reader io.Reader
		want   error
	}{
		{ctx, strings.NewReader(`CREATE TABLE replacement(v BYTEA);INSERT INTO replacement VALUES('\x41');INSERT INTO replacement VALUES('\q');`), nil},
		{ctx, byteaFailedReader{sentinel}, sentinel},
	} {
		err := stuntgres.Restore(failure.ctx, db, failure.reader)
		if err == nil || failure.want != nil && !errors.Is(err, failure.want) {
			t.Fatal(err)
		}
		wantTuples(t, db, "SELECT v FROM t ORDER BY id", [][]any{{[]byte{}}, {[]byte{0, 128, 255, 92, 39}}, {nil}})
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := stuntgres.Restore(canceled, db, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(canceled, "INSERT INTO t VALUES(4,$1,$1)", []byte{1}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	cursorCtx, stop := context.WithCancel(ctx)
	cursor, err := raw.(driver.QueryerContext).QueryContext(cursorCtx, "SELECT v FROM t ORDER BY id", nil)
	if err != nil {
		t.Fatal(err)
	}
	stop()
	if err := cursor.Next(dst); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	cursor.Close()
	wantTuples(t, db, "SELECT v FROM t ORDER BY id", [][]any{{[]byte{}}, {[]byte{0, 128, 255, 92, 39}}, {nil}})
}
