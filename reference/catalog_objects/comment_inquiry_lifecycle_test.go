package reference_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"reflect"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCommentInquiryCapturedAndCanceled(t *testing.T) {
	for _, nonContext := range []bool{false, true} {
		t.Run(map[bool]string{false: "Recorded", true: "Source"}[nonContext], func(t *testing.T) {
			p := fixture.NewScenario(t)
			nativeDSN := "reference/" + t.Name()
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					db := backend.DB
					run := func(s fixture.Step) { t.Helper(); s.NonContext = nonContext; commentInquiryStep(t, p, backend, db, s) }
					for _, q := range []string{"CREATE TABLE t(a INT)", "INSERT INTO t VALUES(1),(2)", "COMMENT ON TABLE t IS 'old'", "COMMENT ON ROLE postgres IS 'old shared'"} {
						count := int64(0)
						if q == "INSERT INTO t VALUES(1),(2)" {
							count = 2
						}
						run(fixture.Exec(q, count))
					}
					roleOID := int64(10)
					if backend.PG {
						// The oracle bootstrap role has OID10, but its configured name
						// need not be postgres. Resolve the role actually commented above.
						if err := db.QueryRowContext(p.Ctx, "SELECT oid FROM pg_roles WHERE rolname='postgres'").Scan(&roleOID); err != nil {
							t.Fatal(err)
						}
						if roleOID <= 0 {
							t.Fatal("invalid postgres role identity", roleOID)
						}
					}
					q := fmt.Sprintf("SELECT obj_description('t'::regclass,'pg_class'),shobj_description(%d,'pg_authid') FROM t", roleOID)
					rows, err := db.QueryContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer rows.Close()
					commentInquiryMetadata(t, rows, backend.PG, false, []string{"obj_description", "shobj_description"})
					var cursor driver.Rows
					if !backend.PG {
						var raw driver.Conn
						var err error
						if nonContext {
							raw, err = p.NativeConnector.Driver().Open(nativeDSN)
						} else {
							raw, err = p.NativeConnector.Connect(p.Ctx)
						}
						if err != nil {
							t.Fatal(err)
						}
						defer raw.Close()
						cursor, err = raw.(driver.QueryerContext).QueryContext(p.Ctx, q, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer cursor.Close()
						if !reflect.DeepEqual(cursor.Columns(), []string{"obj_description", "shobj_description"}) {
							t.Fatal(cursor.Columns())
						}
					}
					run(fixture.Exec("COMMENT ON TABLE t IS 'new'", 0))
					run(fixture.Exec("COMMENT ON ROLE postgres IS 'new shared'", 0))
					if !backend.PG {
						if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
							t.Fatal(err)
						}
					}
					for range 2 {
						if !rows.Next() {
							t.Fatal("missing captured row", rows.Err())
						}
						var a, b any
						if err := rows.Scan(&a, &b); err != nil || a != "old" || b != "old shared" {
							t.Fatal(a, b, err)
						}
						if cursor != nil {
							v := make([]driver.Value, 2)
							if err := cursor.Next(v); err != nil || !reflect.DeepEqual(v, []driver.Value{"old", "old shared"}) {
								t.Fatal(v, err)
							}
						}
					}
					if rows.Next() || rows.Err() != nil {
						t.Fatal(rows.Err())
					}
					if err := rows.Close(); err != nil {
						t.Fatal(err)
					}
					if cursor != nil {
						for range 2 {
							if err := cursor.Next(make([]driver.Value, 2)); err != io.EOF {
								t.Fatal(err)
							}
						}
						if err := cursor.Close(); err != nil {
							t.Fatal(err)
						}
					}
					canceled, cancel := context.WithCancel(p.Ctx)
					cancel()
					for _, q := range []string{"SELECT obj_description(11,'pg_namespace')", "SELECT obj_description(NULL)"} {
						canceledRows, err := db.QueryContext(canceled, q)
						if canceledRows != nil {
							canceledRows.Close()
							t.Fatal("canceled cursor")
						}
						if !errors.Is(err, context.Canceled) {
							t.Fatal(err)
						}
					}
					fresh, err := db.Query("SELECT shobj_description($1,'pg_authid')", roleOID)
					commentInquiryRows(t, fresh, err, backend.PG, false, "shobj_description", []any{"new shared"})
				})
			}
		})
	}
}

func TestReferenceCommentInquiryEmptyAndIterationCancellation(t *testing.T) {
	for _, nonContext := range []bool{false, true} {
		t.Run(map[bool]string{false: "Recorded", true: "Source"}[nonContext], func(t *testing.T) {
			p := fixture.NewScenario(t)
			nativeDSN := "reference/" + t.Name()
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					db := backend.DB
					run := func(s fixture.Step) { t.Helper(); s.NonContext = nonContext; commentInquiryStep(t, p, backend, db, s) }
					run(fixture.Exec("CREATE TABLE t(a INT)", 0))
					rows, err := db.Query("SELECT col_description(NULL,1) FROM t")
					commentInquiryRows(t, rows, err, backend.PG, false, "col_description", []any{})
					for _, prepared := range []bool{false, true} {
						s := fixture.Select("SELECT col_description(NULL,1) FROM t", []string{"col_description"}, []string{"TEXT"}, [][]any{})
						if prepared {
							s = fixture.Prepared(s)
						}
						run(s)
					}
					run(fixture.Exec("INSERT INTO t VALUES(1),(2)", 2))
					run(fixture.Exec("COMMENT ON TABLE t IS 'whole'", 0))
					// Native raw cursors check the saved context before each Next. pq
					// transport cancellation is a distinct admitted-server witness.
					if !backend.PG {
						var raw driver.Conn
						var err error
						if nonContext {
							raw, err = p.NativeConnector.Driver().Open(nativeDSN)
						} else {
							raw, err = p.NativeConnector.Connect(p.Ctx)
						}
						if err != nil {
							t.Fatal(err)
						}
						defer raw.Close()
						ctx, cancel := context.WithCancel(p.Ctx)
						defer cancel()
						cursor, err := raw.(driver.QueryerContext).QueryContext(ctx, "SELECT obj_description('t'::regclass,'pg_class') FROM t", nil)
						if err != nil {
							t.Fatal(err)
						}
						defer cursor.Close()
						v := make([]driver.Value, 1)
						if err := cursor.Next(v); err != nil || v[0] != "whole" {
							t.Fatal(v, err)
						}
						cancel()
						for range 2 {
							if err := cursor.Next(v); !errors.Is(err, context.Canceled) {
								t.Fatal("iteration cancellation", err)
							}
						}
						if err := cursor.Close(); err != nil {
							t.Fatal(err)
						}
						if err := cursor.Next(v); err != io.EOF {
							t.Fatal("closed cursor", err)
						}
					} else {
						conn, err := db.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer conn.Close()
						fixture.SessionCancel(t, p.Ctx, conn, false)
						fixture.SessionCancel(t, p.Ctx, conn, true)
					}
					rows, err = db.Query("SELECT obj_description('t'::regclass,'pg_class')")
					commentInquiryRows(t, rows, err, backend.PG, false, "obj_description", []any{"whole"})
				})
			}
		})
	}
}
