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
	"unicode/utf8"
)

func TestReferenceSmallintArithmeticControls(t *testing.T) {
	p := fixture.NewScenario(t)
	backends := p.Backends()
	// Seed the oracle before native replay executes the native-generated dump there.
	for index := len(backends) - 1; index >= 0; index-- {
		backend := backends[index]
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(id INT,s SMALLINT,h SMALLINT,i INT,b BIGINT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1,7,2,2,2),(2,NULL,NULL,NULL,NULL)", 2))
			run(fixture.Exec("CREATE TABLE replay(s SMALLINT DEFAULT (7::smallint/2::smallint),b BIGINT DEFAULT (7::smallint+2147483648::bigint))", 0))
			run(fixture.Exec("INSERT INTO replay DEFAULT VALUES", 1))
			run(fixture.Exec("CREATE TABLE unused(s SMALLINT DEFAULT (32767::smallint+1::smallint))", 0))
			run(fixture.Exec("INSERT INTO unused VALUES(7)", 1))
			run(fixture.Exec("CREATE VIEW widths AS SELECT s+h AS small,s+i AS medium,s%b AS wide FROM t", 0))
			computed := fixture.Select("SELECT s+h,s+i,s%b FROM t ORDER BY id", []string{"?column?", "?column?", "?column?"}, []string{"INT2", "INT4", "INT8"}, [][]any{{int64(9), int64(9), int64(1)}, {nil, nil, nil}})
			replay := fixture.Select("SELECT * FROM replay", []string{"s", "b"}, []string{"INT2", "INT8"}, [][]any{{int64(3), int64(2147483655)}})
			view := fixture.Select("SELECT * FROM widths ORDER BY small", []string{"small", "medium", "wide"}, []string{"INT2", "INT4", "INT8"}, [][]any{{int64(9), int64(9), int64(1)}, {nil, nil, nil}})
			run(computed)
			run(fixture.Prepared(computed))
			run(replay)
			run(view)
			overflow := fixture.ImplicitError("INSERT INTO unused DEFAULT VALUES", fixture.Diagnostic{Code: "22003", Message: "smallint out of range"})
			overflow.ErrorStage = "execute"
			run(overflow)
			run(fixture.Prepared(overflow))
			if backend.PG {
				return
			}
			stmt, err := backend.DB.PrepareContext(p.Ctx, computed.Query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{backend.DB, stmt, computed.Query}
			conn, err := p.Native.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			err = conn.Raw(func(raw any) error {
				cursor, err := raw.(driver.QueryerContext).QueryContext(p.Ctx, computed.Query, nil)
				if err != nil {
					return err
				}
				defer cursor.Close()
				var dump, again bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					return err
				}
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					return err
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
					return err
				}
				if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil {
					return err
				}
				if !bytes.Equal(dump.Bytes(), again.Bytes()) {
					t.Fatal("width replay changed canonical dump")
				}
				run(computed)
				run(replay)
				run(view)
				run(overflow)
				if p.Postgres != nil {
					for _, q := range []string{"DROP VIEW widths", "DROP TABLE t,replay,unused"} {
						if _, err := p.Postgres.ExecContext(p.Ctx, q); err != nil {
							return err
						}
					}
					if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
						return err
					}
					for _, s := range []fixture.Step{computed, replay, view, overflow} {
						fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, s)
					}
				}
				for _, tc := range []struct{ script, code, message string }{
					{"CREATE TABLE replacement(s SMALLINT); INSERT INTO replacement VALUES(1); INSERT INTO replacement VALUES(32767::smallint+1::smallint)", "22003", "smallint out of range"},
					{"CREATE TABLE replacement(s SMALLINT); INSERT INTO replacement VALUES(1); INSERT INTO replacement VALUES(1::smallint/0::smallint)", "22012", "division by zero"},
					{"/*é*/ CREATE TABLE replacement(s SMALLINT); INSERT INTO replacement VALUES(($1::smallint+1::smallint)::smallint)", "42P02", "parameters are not allowed in restore"},
				} {
					pos := ""
					if tc.code == "42P02" {
						pos = strconv.Itoa(utf8.RuneCountInString(tc.script[:strings.Index(tc.script, "$1")]) + 1)
					}
					want := fixture.ImplicitError(tc.script, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: pos})
					fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(tc.script)), want.Diagnostic)
					run(computed)
					fixture.RunStep(t, p.Ctx, held, backend.Recorder, computed)
				}
				ctx, cancel := context.WithCancel(p.Ctx)
				cancel()
				if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				run(computed)
				if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE replacement(s SMALLINT); INSERT INTO replacement VALUES(7::smallint+2::smallint)")); err != nil {
					return err
				}
				run(fixture.Select("SELECT s FROM replacement", []string{"s"}, []string{"INT2"}, [][]any{{int64(9)}}))
				for i, typ := range computed.Types {
					if cursor.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(i) != typ || cursor.(driver.RowsColumnTypeScanType).ColumnTypeScanType(i) != reflect.TypeFor[int64]() {
						t.Fatal("retained computed width")
					}
					if _, known := cursor.(driver.RowsColumnTypeNullable).ColumnTypeNullable(i); known {
						t.Fatal("computed nullability")
					}
				}
				dst := make([]driver.Value, 3)
				for _, row := range computed.Rows {
					if err := cursor.Next(dst); err != nil {
						return err
					}
					for i, v := range row {
						if dst[i] != v {
							t.Fatal(dst, row)
						}
					}
				}
				if err := cursor.Next(dst); err != io.EOF {
					t.Fatal(err)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
