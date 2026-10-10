package reference_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"reflect"
	"stuntgres"
	"testing"
)

func TestReferenceCommentInquiryCapturedAndCanceled(t *testing.T) {
	for _, nonContext := range []bool{false, true} {
		t.Run(map[bool]string{false: "Recorded", true: "Source"}[nonContext], func(t *testing.T) {
			p := newReferenceScenario(t)
			nativeDSN := "reference/" + t.Name()
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					db := backend.db
					run := func(s referenceStep) { t.Helper(); s.nonContext = nonContext; commentInquiryStep(t, p, backend, db, s) }
					for _, q := range []string{"CREATE TABLE t(a INT)", "INSERT INTO t VALUES(1),(2)", "COMMENT ON TABLE t IS 'old'", "COMMENT ON ROLE postgres IS 'old shared'"} {
						count := int64(0)
						if q == "INSERT INTO t VALUES(1),(2)" {
							count = 2
						}
						run(referenceExec(q, count))
					}
					roleOID := int64(10)
					if backend.pg {
						// The oracle bootstrap role has OID10, but its configured name
						// need not be postgres. Resolve the role actually commented above.
						if err := db.QueryRowContext(p.ctx, "SELECT oid FROM pg_roles WHERE rolname='postgres'").Scan(&roleOID); err != nil {
							t.Fatal(err)
						}
						if roleOID <= 0 {
							t.Fatal("invalid postgres role identity", roleOID)
						}
					}
					q := fmt.Sprintf("SELECT obj_description('t'::regclass,'pg_class'),shobj_description(%d,'pg_authid') FROM t", roleOID)
					rows, err := db.QueryContext(p.ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer rows.Close()
					commentInquiryMetadata(t, rows, backend.pg, false, []string{"obj_description", "shobj_description"})
					var cursor driver.Rows
					if !backend.pg {
						var raw driver.Conn
						var err error
						if nonContext {
							raw, err = p.nativeConnector.Driver().Open(nativeDSN)
						} else {
							raw, err = p.nativeConnector.Connect(p.ctx)
						}
						if err != nil {
							t.Fatal(err)
						}
						defer raw.Close()
						cursor, err = raw.(driver.QueryerContext).QueryContext(p.ctx, q, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer cursor.Close()
						if !reflect.DeepEqual(cursor.Columns(), []string{"obj_description", "shobj_description"}) {
							t.Fatal(cursor.Columns())
						}
					}
					run(referenceExec("COMMENT ON TABLE t IS 'new'", 0))
					run(referenceExec("COMMENT ON ROLE postgres IS 'new shared'", 0))
					if !backend.pg {
						if err := stuntgres.Reset(p.ctx, p.native); err != nil {
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
					canceled, cancel := context.WithCancel(p.ctx)
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
					commentInquiryRows(t, fresh, err, backend.pg, false, "shobj_description", []any{"new shared"})
				})
			}
		})
	}
}

func TestReferenceCommentInquiryEmptyAndIterationCancellation(t *testing.T) {
	for _, nonContext := range []bool{false, true} {
		t.Run(map[bool]string{false: "Recorded", true: "Source"}[nonContext], func(t *testing.T) {
			p := newReferenceScenario(t)
			nativeDSN := "reference/" + t.Name()
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					db := backend.db
					run := func(s referenceStep) { t.Helper(); s.nonContext = nonContext; commentInquiryStep(t, p, backend, db, s) }
					run(referenceExec("CREATE TABLE t(a INT)", 0))
					rows, err := db.Query("SELECT col_description(NULL,1) FROM t")
					commentInquiryRows(t, rows, err, backend.pg, false, "col_description", []any{})
					for _, prepared := range []bool{false, true} {
						s := referenceSelect("SELECT col_description(NULL,1) FROM t", []string{"col_description"}, []string{"TEXT"}, [][]any{})
						if prepared {
							s = referencePrepared(s)
						}
						run(s)
					}
					run(referenceExec("INSERT INTO t VALUES(1),(2)", 2))
					run(referenceExec("COMMENT ON TABLE t IS 'whole'", 0))
					// Native raw cursors check the saved context before each Next. pq
					// transport cancellation is a distinct admitted-server witness.
					if !backend.pg {
						var raw driver.Conn
						var err error
						if nonContext {
							raw, err = p.nativeConnector.Driver().Open(nativeDSN)
						} else {
							raw, err = p.nativeConnector.Connect(p.ctx)
						}
						if err != nil {
							t.Fatal(err)
						}
						defer raw.Close()
						ctx, cancel := context.WithCancel(p.ctx)
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
						conn, err := db.Conn(p.ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer conn.Close()
						referenceSessionCancel(t, p.ctx, conn, false)
						referenceSessionCancel(t, p.ctx, conn, true)
					}
					rows, err = db.Query("SELECT obj_description('t'::regclass,'pg_class')")
					commentInquiryRows(t, rows, err, backend.pg, false, "obj_description", []any{"whole"})
				})
			}
		})
	}
}
