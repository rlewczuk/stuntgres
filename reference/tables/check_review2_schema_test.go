package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

const referenceCheckInlineSchema = "CREATE SCHEMA checked CREATE TABLE earlier(a INT CHECK(a=1)) CREATE TABLE t(a INT CONSTRAINT c CHECK(nextval('public.s')>0))"

func TestReferenceCheckReview2SchemaPrivateDropCancellation(t *testing.T) {
	for _, commit := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("commit=%t/prepared=%t", commit, prepared), func(t *testing.T) {
				p := fixture.NewScenario(t)
				ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
				defer cancel()
				a, err := p.Memory.Conn(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer a.Close()
				b, err := p.Memory.Conn(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer b.Close()
				run := func(s fixture.Step) { fixture.CheckRun(t, ctx, b, p.MemoryRecord, s) }
				fixture.CheckRun(t, ctx, a, p.MemoryRecord, fixture.Exec("CREATE SEQUENCE public.s", 0))
				before := fixture.CheckDump(t, p)
				tx, err := a.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				fixture.CheckRun(t, ctx, tx, p.MemoryRecord, fixture.Exec("DROP SEQUENCE public.s CASCADE", 0))
				var stmt *sql.Stmt
				if prepared {
					stmt, err = b.PrepareContext(ctx, referenceCheckInlineSchema)
					if err != nil {
						t.Fatal("effect-free Prepare waited/failed", err)
					}
					defer stmt.Close()
				}
				x := fixture.Executor(b)
				if stmt != nil {
					x = fixture.HeldStatement{Executor: b, Statement: stmt, SQL: referenceCheckInlineSchema}
				}
				blocked, stop := context.WithTimeout(context.WithValue(ctx, fixture.RecorderKey{}, p.MemoryRecord), 50*time.Millisecond)
				result, createErr := x.ExecContext(blocked, referenceCheckInlineSchema)
				stop()
				if result != nil || !errors.Is(createErr, context.DeadlineExceeded) {
					t.Fatal("inline CREATE must wait", result, createErr)
				}
				var state interface{ SQLState() string }
				if errors.As(createErr, &state) {
					t.Fatal("fabricated cancellation SQLSTATE", createErr)
				}
				if !bytes.Equal(before, fixture.CheckDump(t, p)) {
					t.Fatal("canceled schema/table/provider publication")
				}
				run(fixture.ImplicitError("ALTER SCHEMA checked RENAME TO leaked", fixture.Diagnostic{Code: "3F000", Message: `schema "checked" does not exist`}))
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
					run(fixture.Exec("CREATE SEQUENCE public.s", 0))
				}
				fixture.CheckRun(t, ctx, x, p.MemoryRecord, fixture.Exec(referenceCheckInlineSchema, 0))
				run(fixture.Exec("INSERT INTO checked.earlier VALUES(1)", 1))
				run(fixture.Exec("INSERT INTO checked.t VALUES(7)", 1))
				run(fixture.Select("SELECT a FROM checked.earlier", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}))
				run(fixture.Select("SELECT a FROM checked.t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
				run(fixture.Select("SELECT currval('public.s')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(1)}}))
				fixture.CheckDump(t, p)
			})
		}
	}
}

func TestReferenceCheckReview2SchemaPrivateDropOutcomes(t *testing.T) {
	for _, commit := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("commit=%t/prepared=%t", commit, prepared), func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, backend := range p.Backends() {
					t.Run(backend.Name, func(t *testing.T) {
						ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
						defer cancel()
						a, err := backend.DB.Conn(ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer a.Close()
						b, err := backend.DB.Conn(ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer b.Close()
						run := func(s fixture.Step) { fixture.CheckRun(t, ctx, b, backend.Recorder, s) }
						defer b.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS checked CASCADE")
						fixture.CheckRun(t, ctx, a, backend.Recorder, fixture.Exec("CREATE SEQUENCE public.s", 0))
						var oid int64
						if backend.PG {
							if err := a.QueryRowContext(ctx, "SELECT 'public.s'::regclass::oid").Scan(&oid); err != nil || oid <= 0 || oid > 1<<32-1 {
								t.Fatal(oid, err)
							}
						}
						tx, err := a.BeginTx(ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer tx.Rollback()
						fixture.CheckRun(t, ctx, tx, backend.Recorder, fixture.Exec("DROP SEQUENCE public.s CASCADE", 0))
						// Native's waiting safety boundary is independently exercised above.
						if !backend.PG {
							if commit {
								err = tx.Commit()
							} else {
								err = tx.Rollback()
							}
							if err != nil {
								t.Fatal(err)
							}
							if commit {
								run(fixture.Exec("CREATE SEQUENCE public.s", 0))
							}
						}
						s := fixture.Exec(referenceCheckInlineSchema, 0)
						s.Prepared = prepared
						run(s)
						run(fixture.Exec("INSERT INTO checked.earlier VALUES(1)", 1))
						run(fixture.Select("SELECT a FROM checked.t", []string{"a"}, []string{"INT4"}, nil))
						if backend.PG {
							if commit {
								err = tx.Commit()
							} else {
								err = tx.Rollback()
							}
							if err != nil {
								t.Fatal(err)
							}
						}
						run(fixture.Select("SELECT a FROM checked.earlier", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}))
						run(fixture.Select("SELECT a FROM checked.t", []string{"a"}, []string{"INT4"}, nil))
						if backend.PG && commit {
							run(fixture.ImplicitError("INSERT INTO checked.t VALUES(7)", fixture.Diagnostic{Code: "XX000", Message: fmt.Sprintf("could not open relation with OID %d", oid)}))
							run(fixture.Select("SELECT a FROM checked.t", []string{"a"}, []string{"INT4"}, nil))
						} else {
							run(fixture.Exec("INSERT INTO checked.t VALUES(7)", 1))
							run(fixture.Select("SELECT a FROM checked.t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
							run(fixture.Select("SELECT currval('public.s')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(1)}}))
						}
					})
				}
			})
		}
	}
}
