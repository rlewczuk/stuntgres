package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceRoutineProceduresAtomicity(t *testing.T) {
	for _, language := range []string{"sql", "plpgsql"} {
		t.Run(language, func(t *testing.T) {
			proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				resultsExec(t, p, b, c, "CREATE TABLE pc_atomic(v integer)", "CREATE TABLE pc_unique(v integer CONSTRAINT pc_unique_key UNIQUE)")
				body := "INSERT INTO pc_atomic VALUES(x);SELECT 10/x"
				constraintBody := "INSERT INTO pc_atomic VALUES(x);INSERT INTO pc_unique VALUES(x),(x);SELECT x"
				failureContext := `SQL function "pc_fail" statement 2`
				constraintContext := `SQL function "pc_constraint" statement 2`
				if language == "plpgsql" {
					body = "BEGIN INSERT INTO pc_atomic VALUES(x) RETURNING v INTO answer;answer:=10/x;END"
					constraintBody = "BEGIN INSERT INTO pc_atomic VALUES(x) RETURNING v INTO answer;INSERT INTO pc_unique VALUES(x),(x) RETURNING v INTO answer;answer:=x;END"
					failureContext = "PL/pgSQL function pc_fail(integer) line 1 at assignment"
					constraintContext = "SQL statement \"INSERT INTO pc_unique VALUES(x),(x) RETURNING v\"\nPL/pgSQL function pc_constraint(integer) line 1 at SQL statement"
				}
				resultsExec(t, p, b, c, "CREATE PROCEDURE pc_fail(IN x integer,OUT answer integer) LANGUAGE "+language+" AS $$"+body+"$$", "CREATE PROCEDURE pc_constraint(IN x integer,OUT answer integer) LANGUAGE "+language+" AS $$"+constraintBody+"$$")
				q := "CALL pc_fail($1::integer,NULL)"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				proceduresCount(t, p, b, c, "pc_atomic", 0)
				want := proceduresDiagnostic("22012", "division by zero", 0)
				want.Context = failureContext
				resultsError(t, p, c, q, true, "execute", want, 0)
				proceduresCount(t, p, b, c, "pc_atomic", 0)
				rows, err := stmt.QueryContext(p.Ctx, 0)
				if rows != nil {
					rows.Close()
					t.Fatal("failed CALL exposed rows")
				}
				fixture.AssertDiagnostic(t, err, &want)
				proceduresCount(t, p, b, c, "pc_atomic", 0)
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, proceduresStep(b, q, []string{"answer"}, []string{"INT4"}, [][]any{{int64(5)}}, 2))
				proceduresBoth(t, p, b, c, "CALL pc_fail(2,NULL)", []string{"answer"}, []string{"INT4"}, [][]any{{int64(5)}})
				resultsStoredBoth(t, p, b, c, "SELECT v FROM pc_atomic ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(2)}, {int64(2)}})
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM pc_atomic", 3))
				want = proceduresDiagnostic("23505", `duplicate key value violates unique constraint "pc_unique_key"`, 0)
				want.Detail = "Key (v)=(4) already exists."
				want.Schema = "public"
				want.Table = "pc_unique"
				want.Constraint = "pc_unique_key"
				want.Context = constraintContext
				resultsError(t, p, c, "CALL pc_constraint($1::integer,NULL)", true, "execute", want, 4)
				proceduresCount(t, p, b, c, "pc_atomic", 0)
				proceduresCount(t, p, b, c, "pc_unique", 0)
				proceduresBoth(t, p, b, c, "CALL pc_fail(5,NULL)", []string{"answer"}, []string{"INT4"}, [][]any{{int64(2)}})
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM pc_atomic", 2))
				observer, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer observer.Close()
				tx, err := c.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, proceduresStep(b, "CALL pc_fail(2,NULL)", []string{"answer"}, []string{"INT4"}, [][]any{{int64(5)}}))
				proceduresCount(t, p, b, tx, "pc_atomic", 1) // same owner sees its uncommitted write
				proceduresCount(t, p, b, observer, "pc_atomic", 0)
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				proceduresCount(t, p, b, observer, "pc_atomic", 0)
				tx, err = c.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, proceduresStep(b, "CALL pc_fail(2,NULL)", []string{"answer"}, []string{"INT4"}, [][]any{{int64(5)}}))
				proceduresCount(t, p, b, observer, "pc_atomic", 0)
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
				proceduresCount(t, p, b, observer, "pc_atomic", 1)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM pc_atomic", 1))
				tx, err = c.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				want = proceduresDiagnostic("22012", "division by zero", 0)
				want.Context = failureContext
				s := fixture.ErrorDiagnostic("CALL pc_fail(0,NULL)", want)
				s.SelectRows = true
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, s)
				aborted := proceduresDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block", 0)
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.ErrorDiagnostic("SELECT 1", aborted))
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				proceduresCount(t, p, b, c, "pc_atomic", 0)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, proceduresStep(b, q, []string{"answer"}, []string{"INT4"}, [][]any{{int64(5)}}, 2))
				proceduresCount(t, p, b, c, "pc_atomic", 1)
			})
		})
	}
}

func TestReferenceRoutineProceduresCancellation(t *testing.T) {
	for _, language := range []string{"sql", "plpgsql"} {
		t.Run(language, func(t *testing.T) {
			proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				resultsExec(t, p, b, c, "CREATE TABLE pc_cancel(v integer)")
				body := "INSERT INTO pc_cancel VALUES(30);SELECT 5 FROM pg_sleep(seconds)"
				contextText := `SQL function "pc_pause" statement 2`
				if language == "plpgsql" {
					body = "BEGIN INSERT INTO pc_cancel VALUES(30) RETURNING v INTO answer;SELECT 5 INTO answer FROM pg_sleep(seconds);END"
					contextText = "SQL statement \"SELECT 5 " + strings.Repeat(" ", len("INTO answer")) + " FROM pg_sleep(seconds)\"\nPL/pgSQL function pc_pause(double precision) line 1 at SQL statement"
				}
				resultsExec(t, p, b, c, "CREATE PROCEDURE pc_pause(IN seconds double precision,OUT answer integer) LANGUAGE "+language+" AS $$"+body+"$$")
				q := "CALL pc_pause($1::double precision,NULL)"
				for _, prepared := range []bool{false, true} {
					func() {
						var stmt *sql.Stmt
						if prepared {
							var err error
							stmt, err = c.PrepareContext(p.Ctx, q)
							if err != nil {
								t.Fatal(err)
							}
							defer stmt.Close()
						}
						proceduresCount(t, p, b, c, "pc_cancel", 0)
						fixture.SleepActiveCancelWithContext(t, p, b, c, q, contextText, nil, func(ctx context.Context) fixture.RelationOutcome {
							var rows *sql.Rows
							var err error
							if stmt != nil {
								rows, err = stmt.QueryContext(ctx, 600)
							} else {
								rows, err = c.QueryContext(ctx, q, 600)
							}
							return fixture.RelationOutcome{Rows: rows, Err: err}
						})
						proceduresCount(t, p, b, c, "pc_cancel", 0)
						if !b.PG {
							for _, expired := range []bool{false, true} {
								ctx, cancel := context.WithCancel(p.Ctx)
								if expired {
									cancel()
									ctx, cancel = context.WithDeadline(p.Ctx, time.Now().Add(-time.Second))
								} else {
									cancel()
								}
								rows, err := c.QueryContext(ctx, q, 0)
								want := context.Canceled
								if expired {
									want = context.DeadlineExceeded
								}
								if rows != nil {
									rows.Close()
									t.Fatal("interrupted CALL exposed rows")
								}
								if !errors.Is(err, want) {
									t.Fatal(err, want)
								}
								cancel()
							}
							proceduresCount(t, p, b, c, "pc_cancel", 0)
						}
						target := fixture.Executor(c)
						if stmt != nil {
							target = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
						}
						fixture.RunStep(t, p.Ctx, target, b.Recorder, proceduresStep(b, q, []string{"answer"}, []string{"INT4"}, [][]any{{int64(5)}}, 0))
						resultsStoredBoth(t, p, b, c, "SELECT v FROM pc_cancel", []string{"v"}, []string{"INT4"}, [][]any{{int64(30)}})
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM pc_cancel", 1))
					}()
				}
			})
		})
	}
}
