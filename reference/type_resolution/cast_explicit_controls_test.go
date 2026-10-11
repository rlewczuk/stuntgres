package reference_test

import (
	"bytes"
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strconv"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCastExplicitControlsAndSnapshots(t *testing.T) {
	p := castReferenceSetup(t)
	t.Run("native-controls", func(t *testing.T) {
		db := p.Native
		ctx := context.Background()
		fixture.ScalarNativeExec(t, db, "CREATE TABLE atom(i INT4)")
		fixture.ScalarNativeExec(t, db, "INSERT INTO atom VALUES(7)")
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if rows, err := db.QueryContext(canceled, "SELECT i::bool FROM t"); !errors.Is(err, context.Canceled) {
			if rows != nil {
				rows.Close()
			}
			t.Fatal(err)
		}
		if _, err := db.ExecContext(canceled, "INSERT INTO atom VALUES('8'::int4)"); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(canceled, db, strings.NewReader("CREATE TABLE x(i INT)")); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		fixture.ScalarNativeTuples(t, db, "SELECT i FROM atom", [][]any{{int64(7)}})
		fixture.ScalarNativeExec(t, db, "CREATE TABLE saved(id INT,i INT4,n INT8,s TEXT,b BOOL)")
		fixture.ScalarNativeExec(t, db, "INSERT INTO saved VALUES(1,TRUE::int4,'9223372036854775807'::int8,FALSE::text,(-2)::bool),(2,'-2147483648'::int4,'-9223372036854775808'::int8,42::text,0::bool),(3,NULL::int4,NULL::int8,NULL::text,NULL::bool)")
		fixture.ScalarNativeRaw(t, p, func(raw driver.QueryerContext) {
			cursor, err := raw.QueryContext(ctx, "SELECT i::int8,text(b),n::text FROM saved ORDER BY id", nil)
			if err != nil {
				t.Fatal(err)
			}
			defer cursor.Close()
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
			fixture.ScalarNativeTuples(t, db, "SELECT i,n,s,b FROM saved ORDER BY id", [][]any{{int64(1), int64(9223372036854775807), "false", true}, {int64(-2147483648), int64(-9223372036854775808), "42", false}, {nil, nil, nil, nil}})
			var redump bytes.Buffer
			if err := stuntgres.Dump(ctx, db, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) {
				t.Fatal("nondeterministic dump", err)
			}
			for _, script := range []string{"CREATE TABLE replacement(i INT4); INSERT INTO replacement VALUES(CAST($1::text AS int4))", "CREATE TABLE replacement(i INT4); INSERT INTO replacement VALUES(8::int4),('oops'::int4)", "CREATE TABLE replacement(i INT4); INSERT INTO replacement VALUES(8),(2147483648::int4)"} {
				err := stuntgres.Restore(ctx, db, strings.NewReader(script))
				if strings.Contains(script, "$1") {
					fixture.ScalarNativeDiagnostic(t, err, stuntgres.Error{Code: "42P02", Message: "parameters are not allowed in restore", Position: strconv.Itoa(strings.Index(script, "$1") + 1)})
				} else if err == nil {
					t.Fatal("bad staged restore accepted")
				}
				fixture.ScalarNativeTuples(t, db, "SELECT i FROM atom", [][]any{{int64(7)}})
				var after bytes.Buffer
				if err := stuntgres.Dump(ctx, db, &after); err != nil || !bytes.Equal(after.Bytes(), dump.Bytes()) {
					t.Fatal("failed restore changed catalog/data", err)
				}
			}
			if err := stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE replacement(i INT4,b BOOL,s TEXT,n INT8); INSERT INTO replacement VALUES(CAST(TRUE AS int4),bool(-2),text(FALSE),'42'::int8)")); err != nil {
				t.Fatal(err)
			}
			fixture.ScalarNativeTuples(t, db, "SELECT i,b,s,n FROM replacement", [][]any{{int64(1), true, "false", int64(42)}})
			if !reflect.DeepEqual(cursor.Columns(), []string{"i", "text", "n"}) {
				t.Fatal(cursor.Columns())
			}
			meta := cursor.(driver.RowsColumnTypeDatabaseTypeName)
			for i, typ := range []string{"INT8", "TEXT", "TEXT"} {
				if meta.ColumnTypeDatabaseTypeName(i) != typ {
					t.Fatal("retained metadata changed")
				}
			}
			dst := make([]driver.Value, 3)
			for _, want := range [][]driver.Value{{int64(1), "true", "9223372036854775807"}, {int64(-2147483648), "false", "-9223372036854775808"}, {nil, nil, nil}} {
				if err := cursor.Next(dst); err != nil || !reflect.DeepEqual(dst, want) {
					t.Fatal(dst, want, err)
				}
			}
			if err := cursor.Next(dst); err != io.EOF {
				t.Fatal(err)
			}
		})
	})
	for _, backend := range p.Backends() {
		if !backend.PG {
			continue
		}
		t.Run(backend.Name, func(t *testing.T) {
			run := func(step fixture.Step) { fixture.ScalarRunStep(t, p.Ctx, backend.DB, backend.Recorder, step) }
			run(fixture.Exec("CREATE TABLE atom(i INT4)", 0))
			run(fixture.Exec("INSERT INTO atom VALUES(7)", 1))
			canceled, cancel := context.WithCancel(p.Ctx)
			cancel()
			rows, err := backend.DB.QueryContext(canceled, "SELECT i::bool FROM t")
			if rows != nil {
				rows.Close()
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if _, err := backend.DB.ExecContext(canceled, "INSERT INTO atom VALUES('8'::int4)"); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			run(fixture.Select("SELECT i FROM atom", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}}))
			run(fixture.Exec("CREATE TABLE saved(id INT,i INT4,n INT8,s TEXT,b BOOL)", 0))
			run(fixture.Exec("INSERT INTO saved VALUES(1,TRUE::int4,'9223372036854775807'::int8,FALSE::text,(-2)::bool),(2,'-2147483648'::int4,'-9223372036854775808'::int8,42::text,0::bool),(3,NULL::int4,NULL::int8,NULL::text,NULL::bool)", 3))
			run(fixture.Select("SELECT i,n,s,b FROM saved ORDER BY id", []string{"i", "n", "s", "b"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, [][]any{{int64(1), int64(9223372036854775807), "false", true}, {int64(-2147483648), int64(-9223372036854775808), "42", false}, {nil, nil, nil, nil}}))
			run(fixture.Select("SELECT i::int8,text(b),n::text FROM saved ORDER BY id", []string{"i", "text", "n"}, []string{"INT8", "TEXT", "TEXT"}, [][]any{{int64(1), "true", "9223372036854775807"}, {int64(-2147483648), "false", "-9223372036854775808"}, {nil, nil, nil}}))
			run(fixture.Exec("CREATE TABLE replacement(i INT4,b BOOL,s TEXT,n INT8)", 0))
			run(fixture.Exec("INSERT INTO replacement VALUES(CAST(TRUE AS int4),bool(-2),text(FALSE),'42'::int8)", 1))
			run(fixture.Select("SELECT i,b,s,n FROM replacement", []string{"i", "b", "s", "n"}, []string{"INT4", "BOOL", "TEXT", "INT8"}, [][]any{{int64(1), true, "false", int64(42)}}))
		})
	}
}
