package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"stuntgres"
	"testing"
)

func visibilityBoolMetadata(t *testing.T, rows *sql.Rows) {
	t.Helper()
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for _, typ := range types {
		if typ.DatabaseTypeName() == "BOOL" && typ.ScanType() != reflect.TypeFor[bool]() {
			t.Fatal(typ.Name(), typ.ScanType())
		}
	}
}

func visibilityInquiryRows(t *testing.T, rows *sql.Rows, err error, label string, want []any) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(cols, []string{label}) {
		t.Fatal(cols, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "BOOL" {
		t.Fatal(types, err)
	}
	visibilityBoolMetadata(t, rows)
	got := []any{}
	for rows.Next() {
		var v any
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		got = append(got, v)
	}
	if rows.Err() != nil || !reflect.DeepEqual(got, want) {
		t.Fatal(got, want, rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestReferenceVisibilityInquiryControlsAndViews(t *testing.T) {
	for _, source := range []bool{false, true} {
		t.Run(strconv.FormatBool(source), func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					run := func(s referenceStep) {
						s.nonContext = source
						if s.selectRows {
							s.checkMetadata = visibilityBoolMetadata
						}
						referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
					}
					t.Cleanup(func() {
						if _, err := backend.db.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS a CASCADE"); err != nil {
							t.Error("visibility controls schema cleanup", err)
						}
					})
					queries := []referenceStep{referenceExec("CREATE SCHEMA a", 0), referenceExec("CREATE TABLE a.t(id OID)", 0), referenceExec("INSERT INTO a.t VALUES(23),(0),(NULL)", 3), referenceExec("CREATE SEQUENCE a.s", 0), referenceExec("CREATE VIEW a.v AS SELECT pg_type_is_visible(id) AS visible FROM a.t", 0), referenceExec("CREATE VIEW a.null_v AS SELECT pg_table_is_visible(NULL) AS visible FROM a.t", 0), referenceExec("CREATE TABLE a.checked(id OID CHECK(pg_type_is_visible(id) IS NOT NULL))", 0), referenceExec("INSERT INTO a.checked VALUES(23)", 1)}
					for _, s := range queries {
						run(s)
					}
					check := func() {
						run(referenceSelect("SELECT visible FROM a.v ORDER BY visible", []string{"visible"}, []string{"BOOL"}, [][]any{{true}, {nil}, {nil}}))
						s := referenceSelect("SELECT visible FROM a.null_v", []string{"visible"}, []string{"BOOL"}, [][]any{{nil}, {nil}, {nil}})
						s.bag = true
						run(s)
						for _, expr := range []string{"pg_table_is_visible('a.t'::regclass)", "pg_table_is_visible('a.s'::regclass)", "pg_table_is_visible('a.v'::regclass)"} {
							run(referenceSelect("SELECT "+expr, []string{"pg_table_is_visible"}, []string{"BOOL"}, [][]any{{false}}))
						}
						run(referenceSelect("SELECT pg_collation_is_visible(811),pg_conversion_is_visible(4402)", []string{"pg_collation_is_visible", "pg_conversion_is_visible"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
					}
					check()
					if !backend.pg {
						var rows *sql.Rows
						var err error
						if source {
							rows, err = backend.db.Query("SELECT pg_type_is_visible(id) FROM a.t ORDER BY id")
						} else {
							rows, err = backend.db.QueryContext(p.ctx, "SELECT pg_type_is_visible(id) FROM a.t ORDER BY id")
						}
						if err != nil {
							t.Fatal(err)
						}
						defer rows.Close()
						var dump bytes.Buffer
						if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
							t.Fatal(err)
						}
						if err := stuntgres.Reset(p.ctx, p.native); err != nil {
							t.Fatal(err)
						}
						if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
							t.Fatal(err)
						}
						visibilityInquiryRows(t, rows, nil, "pg_type_is_visible", []any{nil, true, nil})
						check()
						d := stuntgres.NewDriver()
						if source {
							d = p.nativeConnector.Driver().(*stuntgres.Driver)
						}
						c, err := d.OpenConnector(t.Name() + "-foreign")
						if err != nil {
							t.Fatal(err)
						}
						other := sql.OpenDB(c)
						defer other.Close()
						if err := stuntgres.Restore(p.ctx, other, bytes.NewReader(dump.Bytes())); err != nil {
							t.Fatal(err)
						}
						var old *sql.Rows
						if source {
							old, err = other.Query("SELECT visible FROM a.v ORDER BY visible")
						} else {
							old, err = other.QueryContext(p.ctx, "SELECT visible FROM a.v ORDER BY visible")
						}
						visibilityInquiryRows(t, old, err, "visible", []any{true, nil, nil})
						if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader("CREATE TABLE broken(id OID); INSERT INTO broken VALUES('bad');")); err == nil {
							t.Fatal("invalid Restore succeeded")
						}
						check()
					}
				})
			}
		})
	}
}

func TestReferenceVisibilityInquiryPreparedInputsAndCancellation(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			for _, fn := range []string{"pg_table_is_visible", "pg_type_is_visible", "pg_collation_is_visible", "pg_conversion_is_visible"} {
				q := "SELECT " + fn + "($1)"
				stmt, err := backend.db.PrepareContext(p.ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				s := referenceImplicitError(q, referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type oid: "bad"`, Context: "unnamed portal parameter $1 = '...'"}, "bad")
				s.selectRows = true
				s.errorStage = "execute"
				referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: backend.db, statement: stmt, query: q}, backend.recorder, s)
				if err := stmt.Close(); err != nil {
					t.Fatal(err)
				}
			}
			for _, s := range []referenceStep{referenceExec("CREATE TABLE ids(id OID)", 0), referenceExec("INSERT INTO ids VALUES(23),(23)", 2)} {
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
			}
			if !backend.pg {
				conn, err := p.native.Conn(p.ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				ctx, cancel := context.WithCancel(p.ctx)
				defer cancel()
				var cursor driver.Rows
				if err := conn.Raw(func(raw any) error {
					var e error
					cursor, e = raw.(driver.QueryerContext).QueryContext(ctx, "SELECT pg_type_is_visible(id) FROM ids", nil)
					return e
				}); err != nil {
					t.Fatal(err)
				}
				defer cursor.Close()
				values := make([]driver.Value, 1)
				if err := cursor.Next(values); err != nil || values[0] != true {
					t.Fatal(values, err)
				}
				cancel()
				if err := cursor.Next(values); !errors.Is(err, context.Canceled) {
					t.Fatal("iteration cancellation", err)
				}
				rows, err := backend.db.QueryContext(ctx, "SELECT pg_type_is_visible(23)")
				if rows != nil || !errors.Is(err, context.Canceled) {
					if rows != nil {
						rows.Close()
					}
					t.Fatal(rows, err)
				}
			} else {
				conn, err := backend.db.Conn(p.ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				referenceSessionCancel(t, p.ctx, conn, false)
				referenceSessionCancel(t, p.ctx, conn, true)
			}
		})
	}
}
