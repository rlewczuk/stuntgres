package reference_test

import (
	"database/sql/driver"
	"io"
	"reflect"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceNullCountCapturedResults(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(1,NULL),(2,1)", 2))
	step := nullCountReferenceSelect("SELECT num_nulls(v),num_nonnulls(v) FROM t ORDER BY id", [][]any{{int64(1), int64(0)}, {int64(0), int64(1)}})
	nullCountReferenceStep(t, p, step, 1)
	nullCountReferenceStep(t, p, fixture.Prepared(step), 1)
	conn, err := p.Native.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	err = conn.Raw(func(raw any) error {
		cursor, err := raw.(driver.QueryerContext).QueryContext(p.Ctx, step.Query, nil)
		if err != nil {
			return err
		}
		defer cursor.Close()
		fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Exec("INSERT INTO t VALUES(3,NULL)", 1))
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			return err
		}
		if !reflect.DeepEqual(cursor.Columns(), step.Columns) {
			t.Fatal(cursor.Columns())
		}
		meta := cursor.(driver.RowsColumnTypeDatabaseTypeName)
		if meta.ColumnTypeDatabaseTypeName(0) != "INT4" || meta.ColumnTypeDatabaseTypeName(1) != "INT4" {
			t.Fatal("captured metadata changed")
		}
		dst := make([]driver.Value, 2)
		for _, want := range [][]driver.Value{{int64(1), int64(0)}, {int64(0), int64(1)}} {
			if err := cursor.Next(dst); err != nil || !reflect.DeepEqual(dst, want) {
				t.Fatal(dst, want, err)
			}
		}
		if err := cursor.Next(dst); err != io.EOF {
			t.Fatal(err)
		}
		return cursor.Close()
	})
	if err != nil {
		t.Fatal(err)
	}
}
