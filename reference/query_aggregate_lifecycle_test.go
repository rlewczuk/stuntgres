package reference_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"testing"
)

func TestReferenceQueryAggregateFinalRowsCancellation(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE t(k TEXT,v INT)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES('a',1),('a',NULL),(NULL,2)", 3))
	const q = "SELECT k,count(*) FROM t GROUP BY k ORDER BY k"
	step := referenceSelect(q, []string{"k", "count"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(2)}, {nil, int64(1)}})
	p.step(t, step)
	p.step(t, referencePrepared(step))
	conn, err := p.native.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.Raw(func(raw any) error {
		ctx, cancel := context.WithCancel(p.ctx)
		defer cancel()
		rows, err := raw.(driver.QueryerContext).QueryContext(ctx, q, nil)
		if err != nil {
			return err
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
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if err := rows.Next(values); err != io.EOF {
			t.Fatal(err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
