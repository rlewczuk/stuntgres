package reference_test

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

func TestReferenceSmallintMigratedSmoke(t *testing.T) {
	p := newReferenceScenario(t)
	want := [][]any{{int64(-32768), int64(-2147483648), int64(-9223372036854775808), nil}, {int64(255), int64(7), int64(8), nil}, {int64(1000), nil, nil, nil}, {int64(32767), int64(2147483647), int64(9223372036854775807), nil}}
	typed := referenceSelect("SELECT * FROM typed ORDER BY s", []string{"s", "i", "b", "n"}, []string{"INT2", "INT4", "INT8", "INT2"}, want)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
			run(referenceExec("CREATE TABLE typed(s SMALLINT NOT NULL,i INTEGER,b BIGINT,n int2)", 0))
			run(referenceExec("CREATE TABLE empty(s SMALLINT)", 0))
			run(referencePrepared(referenceExec(`CREATE TABLE alias(s "int2",n SMALLINT)`, 0)))
			for _, tc := range []struct {
				q      string
				frames [][]any
			}{
				{"INSERT INTO alias VALUES($1,$2)", [][]any{{int64(-32768), nil}, {int64(32767), nil}}},
				{"INSERT INTO typed VALUES($1,$2,$3,$4)", [][]any{{[]byte("0x_FF"), int64(7), int64(8), nil}, {int64(1000), nil, nil, nil}}},
			} {
				if strings.Contains(tc.q, "typed") {
					run(referenceExec("INSERT INTO typed VALUES(-32768,-2147483648,-9223372036854775808,NULL),(32767,2147483647,9223372036854775807,NULL)", 2))
				}
				stmt, err := backend.db.PrepareContext(p.ctx, tc.q)
				if err != nil {
					t.Fatal(err)
				}
				for _, frame := range tc.frames {
					referenceRunStep(t, p.ctx, referenceHeldStatement{backend.db, stmt, tc.q}, backend.recorder, referenceExec(tc.q, 1, frame...))
				}
				stmt.Close()
			}
			run(referenceSelect("SELECT * FROM alias ORDER BY s", []string{"s", "n"}, []string{"INT2", "INT2"}, [][]any{{int64(-32768), nil}, {int64(32767), nil}}))
			run(typed)
			run(referenceSelect("SELECT * FROM empty", []string{"s"}, []string{"INT2"}, nil))
			for _, tc := range []struct {
				query string
				types []string
			}{
				{typed.query, []string{"INT2", "INT4", "INT8", "INT2"}},
				{"SELECT * FROM empty", []string{"INT2"}},
				{"SELECT * FROM alias ORDER BY s", []string{"INT2", "INT2"}},
			} {
				rows, err := backend.db.QueryContext(p.ctx, tc.query)
				if err != nil {
					t.Fatal(err)
				}
				meta, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				rows.Close()
				if len(meta) != len(tc.types) {
					t.Fatalf("%s metadata width %d want %d", tc.query, len(meta), len(tc.types))
				}
				for i, c := range meta {
					typ := reflect.TypeFor[int64]()
					if backend.pg {
						switch tc.types[i] {
						case "INT2":
							typ = reflect.TypeFor[int16]()
						case "INT4":
							typ = reflect.TypeFor[int32]()
						}
					}
					if c.DatabaseTypeName() != tc.types[i] || c.ScanType() != typ {
						t.Fatal(c)
					}
				}
			}
			run(referenceExec("CREATE TABLE failures(i SMALLINT NOT NULL)", 0))
			run(referenceExec("INSERT INTO failures VALUES(99)", 1))
			unchanged := referenceSelect("SELECT * FROM failures", []string{"i"}, []string{"INT2"}, [][]any{{int64(99)}})
			q := "INSERT INTO failures VALUES(1),($1)"
			stmt, err := backend.db.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := referenceHeldStatement{backend.db, stmt, q}
			for _, bad := range []struct {
				arg           any
				code, message string
			}{{"oops", "22P02", `invalid input syntax for type smallint: "oops"`}, {int64(32768), "22003", `value "32768" is out of range for type smallint`}, {int64(-32769), "22003", `value "-32769" is out of range for type smallint`}} {
				s := referenceImplicitError(q, referenceDiagnostic{Code: bad.code, Message: bad.message, Context: "unnamed portal parameter $1 = '...'"}, bad.arg)
				s.errorStage = "execute"
				run(s)
				referenceRunStep(t, p.ctx, held, backend.recorder, s)
				run(unchanged)
			}
			for _, q := range []string{"INSERT INTO failures VALUES(1),(32768)", "INSERT INTO failures VALUES(1),(-32769)"} {
				s := referenceImplicitError(q, referenceDiagnostic{Code: "22003", Message: "smallint out of range"})
				s.errorStage = "execute"
				run(s)
				run(referencePrepared(s))
				run(unchanged)
			}
			for _, tc := range []struct{ q, code, message, pos string }{{"INSERT INTO failures(i) VALUES('oops')", "22P02", `invalid input syntax for type smallint: "oops"`, "32"}, {"INSERT INTO failures VALUES(1),('oops')", "22P02", `invalid input syntax for type smallint: "oops"`, "33"}, {"INSERT INTO failures VALUES()", "42601", `syntax error at or near ")"`, "29"}} {
				run(referenceImplicitError(tc.q, referenceDiagnostic{Code: tc.code, Message: tc.message, Position: tc.pos}))
			}
			ctx, cancel := context.WithCancel(p.ctx)
			cancel()
			if _, err := backend.db.ExecContext(ctx, "INSERT INTO failures VALUES(1),(2)"); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			run(unchanged)
			if !backend.pg {
				referenceSmallintMigratedControls(t, p, typed)
			}
		})
	}
}

func referenceSmallintMigratedControls(t *testing.T, p *referencePair, typed referenceStep) {
	t.Helper()
	conn, err := p.native.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	err = conn.Raw(func(raw any) error {
		held, err := raw.(driver.QueryerContext).QueryContext(p.ctx, typed.query, nil)
		if err != nil {
			return err
		}
		defer held.Close()
		var dump, redump bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
			return err
		}
		for _, name := range []string{"SMALLINT", "INTEGER", "BIGINT"} {
			if !strings.Contains(dump.String(), name) {
				t.Fatal(dump.String())
			}
		}
		if err := stuntgres.Reset(p.ctx, p.native); err != nil {
			return err
		}
		if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
			return err
		}
		if err := stuntgres.Dump(p.ctx, p.native, &redump); err != nil {
			return err
		}
		if redump.String() != dump.String() {
			t.Fatal(redump.String())
		}
		referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, typed)
		for i, name := range typed.types {
			nullable, known := held.(driver.RowsColumnTypeNullable).ColumnTypeNullable(i)
			if held.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(i) != name || held.(driver.RowsColumnTypeScanType).ColumnTypeScanType(i) != reflect.TypeFor[int64]() || !known || nullable != (i != 0) {
				t.Fatal("retained physical metadata", i)
			}
		}
		dst := make([]driver.Value, 4)
		for _, row := range typed.rows {
			if err := held.Next(dst); err != nil {
				return err
			}
			for i, v := range row {
				if dst[i] != v {
					t.Fatal(dst, row)
				}
			}
		}
		if err := held.Next(dst); err != io.EOF {
			t.Fatal(err)
		}
		err = stuntgres.Restore(p.ctx, p.native, strings.NewReader("CREATE TABLE replacement(s SMALLINT); INSERT INTO replacement VALUES(1),(32768)"))
		isolationReferenceDiagnostic(t, err, referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22003", Message: "smallint out of range"})
		referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, typed)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
