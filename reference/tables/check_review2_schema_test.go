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

// As in PostgreSQL 18.6, schema elements that reference a sequence another
// transaction is dropping wait for it: the whole CREATE SCHEMA succeeds after a
// rollback and fails 42704, creating nothing, after a commit (18.0 created an
// orphaned dependency).
func TestReferenceCheckReview2SchemaPrivateDropWaits(t *testing.T) {
	for _, commit := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("commit=%t/prepared=%t", commit, prepared), func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, backend := range p.Backends() {
					t.Run(backend.Name, func(t *testing.T) {
						ctx, cancel := context.WithTimeout(p.Ctx, 10*time.Second)
						defer cancel()
						a, b := fixture.LockConnections(t, ctx, backend.DB)
						observer, _ := fixture.LockConnections(t, ctx, backend.DB)
						holder, waiter := checkReviewSessionID(t, ctx, backend, a), checkReviewSessionID(t, ctx, backend, b)
						run := func(s fixture.Step) { fixture.CheckRun(t, ctx, b, backend.Recorder, s) }
						defer b.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS checked CASCADE")
						fixture.CheckRun(t, ctx, a, backend.Recorder, fixture.Exec("CREATE SEQUENCE public.s", 0))
						tx, err := a.BeginTx(ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer tx.Rollback()
						fixture.CheckRun(t, ctx, tx, backend.Recorder, fixture.Exec("DROP SEQUENCE public.s CASCADE", 0))
						done := fixture.RowAsync(t, ctx, b, referenceCheckInlineSchema, prepared, false)
						checkReviewWaitBehind(t, ctx, observer, waiter, holder, done)
						if commit {
							err = tx.Commit()
						} else {
							err = tx.Rollback()
						}
						if err != nil {
							t.Fatal(err)
						}
						o := fixture.LockResult(t, done)
						if commit {
							if o.Result != nil {
								t.Fatal("failed CREATE SCHEMA exposed result")
							}
							fixture.WaitsDiagnostic(t, o.Err, "42704", "referenced relation was concurrently dropped")
							run(fixture.Select("SELECT count(*) AS n FROM pg_namespace WHERE nspname='checked'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
							return
						}
						if o.Err != nil {
							t.Fatal(o.Err)
						}
						run(fixture.Exec("INSERT INTO checked.earlier VALUES(1)", 1))
						run(fixture.Select("SELECT a FROM checked.earlier", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}))
						run(fixture.Exec("INSERT INTO checked.t VALUES(7)", 1))
						run(fixture.Select("SELECT a FROM checked.t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
						run(fixture.Select("SELECT currval('public.s')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(1)}}))
					})
				}
			})
		}
	}
}
