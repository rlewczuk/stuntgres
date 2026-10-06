//go:build reference

package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
)

func referenceWaitsDiagnostic(t *testing.T, err error, code, message string, position ...string) {
	t.Helper()
	want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
	if len(position) != 0 {
		want.Position = position[0]
	}
	if got := referenceExtractDiagnostic(t, err); got != want {
		t.Fatalf("got %#v want %#v", got, want)
	}
}

func TestReferenceRelationLockWaitsNowait(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferencePair(t)
			for index, db := range []*sql.DB{p.memory, p.postgres} {
				t.Run([]string{"native", "postgres"}[index], func(t *testing.T) {
					ctx := p.ctx
					a, b := referenceLockConnections(t, ctx, db)
					referenceLockExec(t, ctx, a, "CREATE TABLE t(v INT)", 0)
					referenceLockExec(t, ctx, a, `CREATE TABLE "T"(v INT)`, 0)
					holder, err := a.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { holder.Rollback() })
					referenceLockExec(t, ctx, holder, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0)
					referenceLockExec(t, ctx, holder, `LOCK TABLE "T" IN ACCESS EXCLUSIVE MODE`, 0)
					referenceLockExec(t, ctx, holder, "INSERT INTO t VALUES(1)", 1)
					for _, name := range []string{"t", "public.t", `public."t"`, `"T"`, `public."T"`} {
						waiter, err := b.BeginTx(ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						q := "LOCK TABLE " + name + " IN ACCESS EXCLUSIVE MODE NOWAIT"
						if prepared {
							stmt, e := waiter.PrepareContext(ctx, q)
							if e != nil {
								waiter.Rollback()
								t.Fatal("metadata-only Prepare", e)
							}
							_, err = stmt.ExecContext(ctx)
							stmt.Close()
						} else {
							_, err = waiter.ExecContext(ctx, q)
						}
						decoded := name
						if name == `public."t"` {
							decoded = "public.t"
						}
						if name == `"T"` {
							decoded = "T"
						}
						if name == `public."T"` {
							decoded = "public.T"
						}
						referenceWaitsDiagnostic(t, err, "55P03", `could not obtain lock on relation "`+decoded+`"`)
						_, err = waiter.ExecContext(ctx, "SELECT v FROM t")
						referenceWaitsDiagnostic(t, err, "25P02", "current transaction is aborted, commands ignored until end of transaction block")
						if err = waiter.Rollback(); err != nil {
							t.Fatal(err)
						}
					}
					if err = holder.Commit(); err != nil {
						t.Fatal(err)
					}
					free, err := b.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer free.Rollback()
					for range 2 {
						referenceLockExec(t, ctx, free, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT", 0)
					}
					rows, err := free.QueryContext(ctx, "SELECT v FROM t")
					if err != nil {
						t.Fatal(err)
					}
					referenceTransactionValues(t, rows, 1)
				})
			}
		})
	}
}

func TestReferenceRelationLockWaitsTimeout(t *testing.T) {
	for _, phase := range []string{"direct", "prepare", "cached"} {
		for _, tc := range []struct{ lock, statement, code, message string }{
			{"200ms", "0", "55P03", "canceling statement due to lock timeout"},
			{"200ms", "2s", "55P03", "canceling statement due to lock timeout"},
			{"2s", "200ms", "57014", "canceling statement due to statement timeout"},
			{"200ms", "200ms", "57014", "canceling statement due to statement timeout"},
		} {
			t.Run(phase+"/"+tc.lock+"/"+tc.statement, func(t *testing.T) {
				p := newReferencePair(t)
				for index, db := range []*sql.DB{p.memory, p.postgres} {
					t.Run([]string{"native", "postgres"}[index], func(t *testing.T) {
						ctx := p.ctx
						a, b := referenceLockConnections(t, ctx, db)
						referenceLockExec(t, ctx, a, "CREATE TABLE t(v INT)", 0)
						referenceLockExec(t, ctx, a, "INSERT INTO t VALUES(7)", 1)
						ap, bp := 0, 0
						if index == 1 {
							ap = referenceLockPID(t, ctx, a)
							bp = referenceLockPID(t, ctx, b)
						}
						var cached *sql.Stmt
						if phase == "cached" {
							var err error
							cached, err = b.PrepareContext(ctx, "SELECT v FROM t")
							if err != nil {
								t.Fatal(err)
							}
							defer cached.Close()
						}
						referenceLockExec(t, ctx, b, "SET lock_timeout='"+tc.lock+"'", 0)
						referenceLockExec(t, ctx, b, "SET statement_timeout='"+tc.statement+"'", 0)
						holder, err := a.BeginTx(ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer holder.Rollback()
						referenceLockExec(t, ctx, holder, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0)
						referenceLockExec(t, ctx, holder, "INSERT INTO t VALUES(1)", 1)
						await := referenceLockOperation(t, ctx, func(ctx context.Context) relationOutcome {
							switch phase {
							case "prepare":
								stmt, err := b.PrepareContext(ctx, "SELECT v FROM t")
								return relationOutcome{stmt: stmt, err: err}
							case "cached":
								rows, err := cached.QueryContext(ctx)
								return relationOutcome{rows: rows, err: err}
							default:
								rows, err := b.QueryContext(ctx, "SELECT v FROM t")
								return relationOutcome{rows: rows, err: err}
							}
						})
						if index == 1 {
							referenceLockWaitBehind(t, ctx, bp, ap, "AccessShareLock")
						}
						o := await()
						if o.rows != nil {
							o.rows.Close()
							t.Fatal("failed wait returned rows")
						}
						if o.stmt != nil {
							o.stmt.Close()
							t.Fatal("failed Prepare returned handle")
						}
						position := ""
						if tc.code == "55P03" && phase != "cached" {
							position = "15"
						}
						referenceWaitsDiagnostic(t, o.err, tc.code, tc.message, position)
						if err = holder.Rollback(); err != nil {
							t.Fatal(err)
						}
						rows, err := b.QueryContext(ctx, "SELECT v FROM t")
						if err != nil {
							t.Fatal(err)
						}
						referenceTransactionValues(t, rows, 7)
						referenceLockExec(t, ctx, b, "INSERT INTO t VALUES(2)", 1)
					})
				}
			})
		}
	}
}

func TestReferenceRelationLockWaitsSettings(t *testing.T) {
	p := newReferencePair(t)
	for index, db := range []*sql.DB{p.memory, p.postgres} {
		t.Run([]string{"native", "postgres"}[index], func(t *testing.T) {
			ctx := p.ctx
			conn, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			check := func(want string) {
				t.Helper()
				var got string
				if err := conn.QueryRowContext(ctx, "SHOW lock_timeout").Scan(&got); err != nil || got != want {
					t.Fatalf("SHOW=%q (%v), want %q", got, err, want)
				}
			}
			check("0")
			for _, tc := range []struct{ input, want string }{{"1000", "1s"}, {"1500ms", "1500ms"}, {"1.5s", "1500ms"}, {"1000us", "1ms"}, {"0.5ms", "0"}} {
				referenceLockExec(t, ctx, conn, "SET lock_timeout='"+tc.input+"'", 0)
				check(tc.want)
			}
			for _, tc := range []struct{ input, message, hint string }{
				{"-1", `-1 ms is outside the valid range for parameter "lock_timeout" (0 ms .. 2147483647 ms)`, ""},
				{"1 MS", `invalid value for parameter "lock_timeout": "1 MS"`, `Valid units for this parameter are "us", "ms", "s", "min", "h", and "d".`},
				{"2147483648", `invalid value for parameter "lock_timeout": "2147483648"`, "Value exceeds integer range."},
			} {
				_, err = conn.ExecContext(ctx, "SET lock_timeout='"+tc.input+"'")
				want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: tc.message, Hint: tc.hint}
				if got := referenceExtractDiagnostic(t, err); got != want {
					t.Fatal(got, want)
				}
				check("0")
			}
			for _, commit := range []bool{false, true} {
				referenceLockExec(t, ctx, conn, "SET lock_timeout='1s'", 0)
				tx, err := conn.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				referenceLockExec(t, ctx, tx, "SET LOCAL lock_timeout='2s'", 0)
				var got string
				if err = tx.QueryRowContext(ctx, "SHOW lock_timeout").Scan(&got); err != nil || got != "2s" {
					tx.Rollback()
					t.Fatal(got, err)
				}
				if commit {
					err = tx.Commit()
				} else {
					err = tx.Rollback()
				}
				if err != nil {
					t.Fatal(err)
				}
				check("1s")
			}
			referenceLockExec(t, ctx, conn, "RESET lock_timeout", 0)
			check("0")
			var value string
			if err = conn.QueryRowContext(ctx, "SELECT set_config('lock_timeout','1500ms',false)").Scan(&value); err != nil || value != "1500ms" {
				t.Fatal(value, err)
			}
			if err = conn.QueryRowContext(ctx, "SELECT current_setting('lock_timeout')").Scan(&value); err != nil || value != "1500ms" {
				t.Fatal(value, err)
			}
			referenceLockExec(t, ctx, conn, "SET SESSION lock_timeout=DEFAULT", 0)
			check("0")
			referenceLockExec(t, ctx, conn, "SET lock_timeout='1s'", 0)
			referenceLockExec(t, ctx, conn, "RESET ALL", 0)
			check("0")
		})
	}
}
