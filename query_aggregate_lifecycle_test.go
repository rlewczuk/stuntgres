package stuntgres_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"stuntgres"
	"testing"
)

func TestQueryAggregateFinalRowsCancellation(t *testing.T) {
	d := stuntgres.NewDriver()
	db := openLocal(t, d, t.Name())
	execSQL(t, db, "CREATE TABLE t(k TEXT,v INT)")
	execSQL(t, db, "INSERT INTO t VALUES('a',1),('a',NULL),(NULL,2)")
	raw, err := d.Open(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rows, err := raw.(driver.QueryerContext).QueryContext(ctx, "SELECT k,count(*) FROM t GROUP BY k ORDER BY k", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	values := make([]driver.Value, 2)
	if err := rows.Next(values); err != nil || !reflect.DeepEqual(values, []driver.Value{"a", int64(2)}) {
		t.Fatal(values, err)
	}
	cancel()
	for range 2 {
		if err := rows.Next(values); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(rows.Columns(), []string{"k", "count"}) || rows.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(1) != "INT8" {
		t.Fatal(rows.Columns())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Next(values); err != io.EOF {
		t.Fatal(err)
	}
}
