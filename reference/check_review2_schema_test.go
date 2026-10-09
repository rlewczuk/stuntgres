package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"
)

const referenceCheckInlineSchema = "CREATE SCHEMA checked CREATE TABLE earlier(a INT CHECK(a=1)) CREATE TABLE t(a INT CONSTRAINT c CHECK(nextval('public.s')>0))"

func TestReferenceCheckReview2SchemaPrivateDropCancellation(t *testing.T) {
	for _, commit := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("commit=%t/prepared=%t", commit, prepared), func(t *testing.T) {
				p := newReferenceScenario(t)
				ctx, cancel := context.WithTimeout(p.ctx, 5*time.Second)
				defer cancel()
				a, err := p.memory.Conn(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer a.Close()
				b, err := p.memory.Conn(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer b.Close()
				run := func(s referenceStep) { referenceCheckRun(t, ctx, b, p.memoryRecord, s) }
				referenceCheckRun(t, ctx, a, p.memoryRecord, referenceExec("CREATE SEQUENCE public.s", 0))
				before := referenceCheckDump(t, p)
				tx, err := a.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				referenceCheckRun(t, ctx, tx, p.memoryRecord, referenceExec("DROP SEQUENCE public.s CASCADE", 0))
				var stmt *sql.Stmt
				if prepared {
					stmt, err = b.PrepareContext(ctx, referenceCheckInlineSchema)
					if err != nil {
						t.Fatal("effect-free Prepare waited/failed", err)
					}
					defer stmt.Close()
				}
				x := referenceExecutor(b)
				if stmt != nil {
					x = referenceHeldStatement{referenceExecutor: b, statement: stmt, query: referenceCheckInlineSchema}
				}
				blocked, stop := context.WithTimeout(context.WithValue(ctx, referenceRecorderKey{}, p.memoryRecord), 50*time.Millisecond)
				result, createErr := x.ExecContext(blocked, referenceCheckInlineSchema)
				stop()
				if result != nil || !errors.Is(createErr, context.DeadlineExceeded) {
					t.Fatal("inline CREATE must wait", result, createErr)
				}
				var state interface{ SQLState() string }
				if errors.As(createErr, &state) {
					t.Fatal("fabricated cancellation SQLSTATE", createErr)
				}
				if !bytes.Equal(before, referenceCheckDump(t, p)) {
					t.Fatal("canceled schema/table/provider publication")
				}
				run(referenceImplicitError("ALTER SCHEMA checked RENAME TO leaked", referenceDiagnostic{Code: "3F000", Message: `schema "checked" does not exist`}))
				if commit {
					err = tx.Commit()
				} else {
					err = tx.Rollback()
				}
				if err != nil {
					t.Fatal(err)
				}
				if commit {
					result, err = x.ExecContext(ctx, referenceCheckInlineSchema)
					if result != nil || err == nil {
						t.Fatal("missing committed-away provider accepted", result, err)
					}
					run(referenceExec("CREATE SEQUENCE public.s", 0))
				}
				referenceCheckRun(t, ctx, x, p.memoryRecord, referenceExec(referenceCheckInlineSchema, 0))
				run(referenceExec("INSERT INTO checked.earlier VALUES(1)", 1))
				run(referenceExec("INSERT INTO checked.t VALUES(7)", 1))
				run(referenceSelect("SELECT a FROM checked.earlier", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}))
				run(referenceSelect("SELECT a FROM checked.t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
				run(referenceSelect("SELECT currval('public.s')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(1)}}))
				referenceCheckDump(t, p)
			})
		}
	}
}

func TestReferenceCheckReview2SchemaPrivateDropOutcomes(t *testing.T) {
	for _, commit := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("commit=%t/prepared=%t", commit, prepared), func(t *testing.T) {
				p := newReferenceScenario(t)
				for _, backend := range p.backends() {
					t.Run(backend.name, func(t *testing.T) {
						ctx, cancel := context.WithTimeout(p.ctx, 5*time.Second)
						defer cancel()
						a, err := backend.db.Conn(ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer a.Close()
						b, err := backend.db.Conn(ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer b.Close()
						run := func(s referenceStep) { referenceCheckRun(t, ctx, b, backend.recorder, s) }
						defer b.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS checked CASCADE")
						referenceCheckRun(t, ctx, a, backend.recorder, referenceExec("CREATE SEQUENCE public.s", 0))
						var oid int64
						if backend.pg {
							if err := a.QueryRowContext(ctx, "SELECT 'public.s'::regclass::oid").Scan(&oid); err != nil || oid <= 0 || oid > 1<<32-1 {
								t.Fatal(oid, err)
							}
						}
						tx, err := a.BeginTx(ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer tx.Rollback()
						referenceCheckRun(t, ctx, tx, backend.recorder, referenceExec("DROP SEQUENCE public.s CASCADE", 0))
						// Native's waiting safety boundary is independently exercised above.
						if !backend.pg {
							if commit {
								err = tx.Commit()
							} else {
								err = tx.Rollback()
							}
							if err != nil {
								t.Fatal(err)
							}
							if commit {
								run(referenceExec("CREATE SEQUENCE public.s", 0))
							}
						}
						s := referenceExec(referenceCheckInlineSchema, 0)
						s.prepared = prepared
						run(s)
						run(referenceExec("INSERT INTO checked.earlier VALUES(1)", 1))
						run(referenceSelect("SELECT a FROM checked.t", []string{"a"}, []string{"INT4"}, nil))
						if backend.pg {
							if commit {
								err = tx.Commit()
							} else {
								err = tx.Rollback()
							}
							if err != nil {
								t.Fatal(err)
							}
						}
						run(referenceSelect("SELECT a FROM checked.earlier", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}))
						run(referenceSelect("SELECT a FROM checked.t", []string{"a"}, []string{"INT4"}, nil))
						if backend.pg && commit {
							run(referenceImplicitError("INSERT INTO checked.t VALUES(7)", referenceDiagnostic{Code: "XX000", Message: fmt.Sprintf("could not open relation with OID %d", oid)}))
							run(referenceSelect("SELECT a FROM checked.t", []string{"a"}, []string{"INT4"}, nil))
						} else {
							run(referenceExec("INSERT INTO checked.t VALUES(7)", 1))
							run(referenceSelect("SELECT a FROM checked.t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
							run(referenceSelect("SELECT currval('public.s')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(1)}}))
						}
					})
				}
			})
		}
	}
}
