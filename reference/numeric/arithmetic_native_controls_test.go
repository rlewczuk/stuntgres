package reference_test

import (
	"bytes"
	"database/sql/driver"
	"io"
	"reflect"
	"strconv"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceArithmeticNativeControls(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(i INT,b BIGINT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(7/2,-7%2),(NULL,NULL)", 2))
	step := fixture.Select("SELECT i+1,b*2 FROM t ORDER BY i", []string{"?column?", "?column?"}, []string{"INT4", "INT8"}, [][]any{{int64(4), int64(-2)}, {nil, nil}})
	p.Step(t, step)
	p.Step(t, fixture.Prepared(step))
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
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			return err
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			return err
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			return err
		}
		var again bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil {
			return err
		}
		if !bytes.Equal(dump.Bytes(), again.Bytes()) {
			t.Fatal("native snapshot redump changed")
		}
		for _, tc := range []struct{ script, code, message, position string }{
			{"CREATE TABLE replacement(i INT); INSERT INTO replacement VALUES(1); INSERT INTO replacement VALUES(7/0)", "22012", "division by zero", ""},
			{"CREATE TABLE replacement(i INT); INSERT INTO replacement VALUES((7+$1)*2)", "42P02", "parameters are not allowed in restore", ""},
		} {
			if tc.code == "42P02" {
				tc.position = strconv.Itoa(strings.Index(tc.script, "$1") + 1)
			}
			want := fixture.ImplicitError(tc.script, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: tc.position})
			fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(tc.script)), want.Diagnostic)
			remaining := fixture.Select("SELECT i,b FROM t WHERE i=3", []string{"i", "b"}, []string{"INT4", "INT8"}, [][]any{{int64(3), int64(-1)}})
			fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, remaining)
			fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Prepared(remaining))
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE replacement(i INT); INSERT INTO replacement VALUES((7+1)*2)")); err != nil {
			return err
		}
		replacement := fixture.Select("SELECT i FROM replacement", []string{"i"}, []string{"INT4"}, [][]any{{int64(16)}})
		fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, replacement)
		fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Prepared(replacement))
		meta := cursor.(driver.RowsColumnTypeDatabaseTypeName)
		if meta.ColumnTypeDatabaseTypeName(0) != "INT4" || meta.ColumnTypeDatabaseTypeName(1) != "INT8" {
			t.Fatal("held raw metadata changed")
		}
		dst := make([]driver.Value, 2)
		for _, want := range [][]driver.Value{{int64(4), int64(-2)}, {nil, nil}} {
			if err := cursor.Next(dst); err != nil || !reflect.DeepEqual(dst, want) {
				t.Fatalf("held raw row %#v want %#v: %v", dst, want, err)
			}
		}
		if err := cursor.Next(dst); err != io.EOF {
			t.Fatalf("held raw end: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
