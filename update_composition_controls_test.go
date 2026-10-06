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

func TestUpdateCoreCompositionControls(t *testing.T) {
	ctx := context.Background()
	d := stuntgres.NewDriver()
	db := openLocal(t, d, t.Name())
	execSQL(t, db, "CREATE TABLE t(id INT,d DATE,b BYTEA)")
	execSQL(t, db, `INSERT INTO t VALUES(1,'2000-01-01','\x01'),(2,NULL,'\x')`)
	raw, err := d.Open(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	held, err := raw.(driver.QueryerContext).QueryContext(ctx, "SELECT id,d,b FROM t ORDER BY id", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	values := make([]driver.Value, 3)
	if err = held.Next(values); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(values, []driver.Value{int64(1), "2000-01-01", []byte{1}}) {
		t.Fatal(values)
	}
	retained := values[2].([]byte)
	retained[0] = 77
	wantTuples(t, db, "SELECT d,b FROM t ORDER BY id", [][]any{{"2000-01-01", []byte{1}}, {nil, []byte{}}})
	payload := []byte{0, 92, 255}
	execSQL(t, db, "UPDATE t SET d=$1,b=$2 WHERE id=1", "2024-02-29", payload)
	payload[0] = 99
	stmt, err := db.Prepare("UPDATE t SET d=$1,b=$2 WHERE id=2")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	if _, err = stmt.Exec("2030-01-02", []byte{}); err != nil {
		t.Fatal(err)
	}
	current := [][]any{{int64(1), "2024-02-29", []byte{0, 92, 255}}, {int64(2), "2030-01-02", []byte{}}}
	wantTuples(t, db, "SELECT id,d,b FROM t ORDER BY id", current)
	// A result from the updated immutable head exports detached bytes as well.
	updated, err := raw.(driver.QueryerContext).QueryContext(ctx, "SELECT b FROM t ORDER BY id", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer updated.Close()
	exported := make([]driver.Value, 1)
	if err = updated.Next(exported); err != nil {
		t.Fatal(err)
	}
	detached := exported[0].([]byte)
	detached[0] = 88
	wantTuples(t, db, "SELECT id,d,b FROM t ORDER BY id", current)
	var dump bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	for _, piece := range []string{`"d" DATE`, `"b" BYTEA`, `'2024-02-29'`, `'2030-01-02'`, `'\x005cff'`, `'\x'`} {
		if !strings.Contains(dump.String(), piece) {
			t.Fatal(piece, dump.String())
		}
	}
	if strings.Contains(dump.String(), "2000-01-01") || strings.Contains(dump.String(), "UPDATE") {
		t.Fatal("dump serialized predecessor/history", dump.String())
	}
	setting, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer setting.Close()
	if _, err = setting.ExecContext(ctx, "SET bytea_output=escape"); err != nil {
		t.Fatal(err)
	}
	var escape bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &escape); err != nil || !bytes.Equal(escape.Bytes(), dump.Bytes()) {
		t.Fatal("canonical output depended on presentation", escape.String(), err)
	}
	if err = stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE replacement(d DATE,b BYTEA);INSERT INTO replacement VALUES('bad','');")); err == nil {
		t.Fatal("invalid Restore succeeded")
	}
	wantTuples(t, db, "SELECT id,d,b FROM t ORDER BY id", current)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err = stuntgres.Restore(canceled, db, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	wantTuples(t, db, "SELECT id,d,b FROM t ORDER BY id", current)
	if err = stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err = stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	wantTuples(t, db, "SELECT id,d,b FROM t ORDER BY id", current)
	var again bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &again); err != nil || !bytes.Equal(again.Bytes(), dump.Bytes()) {
		t.Fatal("current DATE/BYTEA roundtrip", again.String(), err)
	}
	if retained[0] != 77 || detached[0] != 88 {
		t.Fatal("retained detached bytes changed", retained, detached)
	}
	if err = held.Next(values); err != nil || !reflect.DeepEqual(values, []driver.Value{int64(2), nil, []byte{}}) {
		t.Fatal("old head changed across UPDATE/control", values, err)
	}
	if err = held.Next(values); err != io.EOF {
		t.Fatal(err)
	}
	if held.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(1) != "DATE" || held.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(2) != "BYTEA" {
		t.Fatal("retained schema changed")
	}
	if err = updated.Next(exported); err != nil || !reflect.DeepEqual(exported[0], []byte{}) {
		t.Fatal("updated retained head changed", exported, err)
	}
	if err = updated.Next(exported); err != io.EOF {
		t.Fatal(err)
	}
}
