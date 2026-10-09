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
	"testing"
	"unicode/utf8"
)

func TestReferenceSmallintArithmeticControls(t *testing.T) {
	p := newReferenceScenario(t)
	backends := p.backends()
	// Seed the oracle before native replay executes the native-generated dump there.
	for index := len(backends) - 1; index >= 0; index-- {
		backend := backends[index]
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
			run(referenceExec("CREATE TABLE t(id INT,s SMALLINT,h SMALLINT,i INT,b BIGINT)", 0))
			run(referenceExec("INSERT INTO t VALUES(1,7,2,2,2),(2,NULL,NULL,NULL,NULL)", 2))
			run(referenceExec("CREATE TABLE replay(s SMALLINT DEFAULT (7::smallint/2::smallint),b BIGINT DEFAULT (7::smallint+2147483648::bigint))", 0))
			run(referenceExec("INSERT INTO replay DEFAULT VALUES", 1))
			run(referenceExec("CREATE TABLE unused(s SMALLINT DEFAULT (32767::smallint+1::smallint))", 0))
			run(referenceExec("INSERT INTO unused VALUES(7)", 1))
			run(referenceExec("CREATE VIEW widths AS SELECT s+h AS small,s+i AS medium,s%b AS wide FROM t", 0))
			computed := referenceSelect("SELECT s+h,s+i,s%b FROM t ORDER BY id", []string{"?column?", "?column?", "?column?"}, []string{"INT2", "INT4", "INT8"}, [][]any{{int64(9), int64(9), int64(1)}, {nil, nil, nil}})
			replay := referenceSelect("SELECT * FROM replay", []string{"s", "b"}, []string{"INT2", "INT8"}, [][]any{{int64(3), int64(2147483655)}})
			view := referenceSelect("SELECT * FROM widths ORDER BY small", []string{"small", "medium", "wide"}, []string{"INT2", "INT4", "INT8"}, [][]any{{int64(9), int64(9), int64(1)}, {nil, nil, nil}})
			run(computed)
			run(referencePrepared(computed))
			run(replay)
			run(view)
			overflow := referenceImplicitError("INSERT INTO unused DEFAULT VALUES", referenceDiagnostic{Code: "22003", Message: "smallint out of range"})
			overflow.errorStage = "execute"
			run(overflow)
			run(referencePrepared(overflow))
			if backend.pg {
				return
			}
			stmt, err := backend.db.PrepareContext(p.ctx, computed.query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := referenceHeldStatement{backend.db, stmt, computed.query}
			conn, err := p.native.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			err = conn.Raw(func(raw any) error {
				cursor, err := raw.(driver.QueryerContext).QueryContext(p.ctx, computed.query, nil)
				if err != nil {
					return err
				}
				defer cursor.Close()
				var dump, again bytes.Buffer
				if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
					return err
				}
				if err := stuntgres.Reset(p.ctx, p.native); err != nil {
					return err
				}
				if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
					return err
				}
				if err := stuntgres.Dump(p.ctx, p.native, &again); err != nil {
					return err
				}
				if !bytes.Equal(dump.Bytes(), again.Bytes()) {
					t.Fatal("width replay changed canonical dump")
				}
				run(computed)
				run(replay)
				run(view)
				run(overflow)
				if p.postgres != nil {
					for _, q := range []string{"DROP VIEW widths", "DROP TABLE t,replay,unused"} {
						if _, err := p.postgres.ExecContext(p.ctx, q); err != nil {
							return err
						}
					}
					if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
						return err
					}
					for _, s := range []referenceStep{computed, replay, view, overflow} {
						referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, s)
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
					want := referenceImplicitError(tc.script, referenceDiagnostic{Code: tc.code, Message: tc.message, Position: pos})
					referenceAssertDiagnostic(t, stuntgres.Restore(p.ctx, p.native, strings.NewReader(tc.script)), want.diagnostic)
					run(computed)
					referenceRunStep(t, p.ctx, held, backend.recorder, computed)
				}
				ctx, cancel := context.WithCancel(p.ctx)
				cancel()
				if err := stuntgres.Restore(ctx, p.native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				run(computed)
				if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader("CREATE TABLE replacement(s SMALLINT); INSERT INTO replacement VALUES(7::smallint+2::smallint)")); err != nil {
					return err
				}
				run(referenceSelect("SELECT s FROM replacement", []string{"s"}, []string{"INT2"}, [][]any{{int64(9)}}))
				for i, typ := range computed.types {
					if cursor.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(i) != typ || cursor.(driver.RowsColumnTypeScanType).ColumnTypeScanType(i) != reflect.TypeFor[int64]() {
						t.Fatal("retained computed width")
					}
					if _, known := cursor.(driver.RowsColumnTypeNullable).ColumnTypeNullable(i); known {
						t.Fatal("computed nullability")
					}
				}
				dst := make([]driver.Value, 3)
				for _, row := range computed.rows {
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
