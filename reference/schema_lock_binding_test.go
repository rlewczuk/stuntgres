//go:build reference

package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"runtime"
	"testing"
	"time"
)

func schemaShadowValues(t *testing.T, rows *sql.Rows, want ...int64) {
	t.Helper()
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil || len(columns) != 1 || columns[0] != "id" {
		t.Fatal("column metadata", columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "INT4" {
		t.Fatal("type metadata", types, err)
	}
	transactionRowValues(t, rows, want...)
}

// Observe the exact ungranted relation request, not elapsed time or merely a
// busy backend. These expectations are independent pinned PostgreSQL outcomes.
func schemaShadowWait(t *testing.T, ctx context.Context, pid int, relation string) {
	t.Helper()
	for {
		var waiting bool
		err := referenceAdmin.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND locktype='relation' AND relation=$2::regclass AND NOT granted) AND cardinality(pg_blocking_pids($1)) > 0`, pid, relation).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		runtime.Gosched()
	}
}

func TestReferenceSchemaLockedBindingShadowCreation(t *testing.T) {
	for _, phase := range []string{"direct-select", "direct-insert", "prepare-select", "held-select"} {
		t.Run(phase, func(t *testing.T) {
			pair := newReferencePair(t)
			ctx, cancel := context.WithCancel(pair.ctx)
			defer cancel()
			a, waiter := referenceLockConnections(t, ctx, pair.postgres)
			shadow, err := pair.postgres.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer shadow.Close()
			defer func() {
				cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
				defer stop()
				if _, err := pair.postgres.ExecContext(cleanup, "DROP SCHEMA IF EXISTS early, late CASCADE"); err != nil {
					t.Error("schema cleanup", err)
				}
			}()
			referenceLockExec(t, ctx, a, "CREATE SCHEMA early", 0)
			referenceLockExec(t, ctx, a, "CREATE SCHEMA late CREATE TABLE t(id INT)", 0)
			referenceLockExec(t, ctx, a, "INSERT INTO late.t VALUES(7)", 1)
			referenceLockExec(t, ctx, waiter, "SET search_path TO early, late", 0)
			var held *sql.Stmt
			if phase == "held-select" {
				held, err = waiter.PrepareContext(ctx, "SELECT id FROM t")
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				rows, err := held.QueryContext(ctx)
				if err != nil {
					t.Fatal(err)
				}
				schemaShadowValues(t, rows, 7)
			}
			var pid int
			if err := waiter.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
				t.Fatal(err)
			}
			lateTx, err := a.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer lateTx.Rollback()
			referenceLockExec(t, ctx, lateTx, "LOCK TABLE late.t IN ACCESS EXCLUSIVE MODE", 0)
			done := make(chan relationOutcome, 1)
			go func() {
				var out relationOutcome
				switch phase {
				case "direct-select":
					out.rows, out.err = waiter.QueryContext(ctx, "SELECT id FROM t")
				case "direct-insert":
					out.result, out.err = waiter.ExecContext(ctx, "INSERT INTO t VALUES(11)")
				case "prepare-select":
					out.stmt, out.err = waiter.PrepareContext(ctx, "SELECT id FROM t")
				case "held-select":
					out.rows, out.err = held.QueryContext(ctx)
				}
				done <- out
			}()
			joined := false
			defer func() {
				cancel()
				if !joined {
					out := referenceLockResult(t, done)
					if out.rows != nil {
						out.rows.Close()
					}
					if out.stmt != nil {
						out.stmt.Close()
					}
				}
			}()
			schemaShadowWait(t, ctx, pid, "late.t")
			referenceLockExec(t, ctx, shadow, "CREATE TABLE early.t(id INT)", 0)
			referenceLockExec(t, ctx, shadow, "INSERT INTO early.t VALUES(9)", 1)
			earlyTx, err := shadow.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer earlyTx.Rollback()
			referenceLockExec(t, ctx, earlyTx, "LOCK TABLE early.t IN ACCESS EXCLUSIVE MODE", 0)
			if err := lateTx.Rollback(); err != nil {
				t.Fatal(err)
			}
			if phase != "held-select" {
				schemaShadowWait(t, ctx, pid, "early.t")
				if err := earlyTx.Rollback(); err != nil {
					t.Fatal(err)
				}
			}
			out := referenceLockResult(t, done)
			joined = true
			if out.err != nil {
				t.Fatal(out.err)
			}
			switch phase {
			case "held-select":
				schemaShadowValues(t, out.rows, 7)
				t.Log("pinned warmed statement selected locked late.t=7 while early AX remained held")
				if err := earlyTx.Rollback(); err != nil {
					t.Fatal(err)
				}
			case "direct-select":
				schemaShadowValues(t, out.rows, 9)
				t.Log("pinned direct SELECT reacquired early.t and returned9")
			case "direct-insert":
				referenceLockAffected(t, out.result, 1)
				t.Log("pinned direct INSERT reacquired early.t and affected1")
			case "prepare-select":
				defer out.stmt.Close()
				rows, err := out.stmt.QueryContext(ctx)
				if err != nil {
					t.Fatal(err)
				}
				schemaShadowValues(t, rows, 9)
				t.Log("pinned semantic Prepare reacquired early.t; execution returned9")
			}
			for _, table := range []string{"early.t", "late.t"} {
				rows, err := waiter.QueryContext(ctx, "SELECT id FROM "+table+" ORDER BY id")
				if err != nil {
					t.Fatal(err)
				}
				if table == "late.t" {
					schemaShadowValues(t, rows, 7)
				} else if phase == "direct-insert" {
					schemaShadowValues(t, rows, 9, 11)
				} else {
					schemaShadowValues(t, rows, 9)
				}
			}
		})
	}
}

// Public native APIs intentionally expose no queue introspection. This is a
// steady-state AX exclusion witness, not a claim to synchronize shadow capture.
func TestReferenceSchemaShadowAXNative(t *testing.T) {
	pair := newReferencePair(t)
	ctx := pair.ctx
	a, waiter := referenceLockConnections(t, ctx, pair.memory)
	for _, q := range []string{"CREATE SCHEMA early CREATE TABLE t(id INT)", "CREATE SCHEMA late CREATE TABLE t(id INT)", "SET search_path TO early, late"} {
		referenceLockExec(t, ctx, waiter, q, 0)
	}
	referenceLockExec(t, ctx, waiter, "INSERT INTO early.t VALUES(9)", 1)
	referenceLockExec(t, ctx, waiter, "INSERT INTO late.t VALUES(7)", 1)
	held, err := waiter.PrepareContext(ctx, "SELECT id FROM t")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	tx, err := a.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	referenceLockExec(t, ctx, tx, "LOCK TABLE early.t IN ACCESS EXCLUSIVE MODE", 0)
	for _, phase := range []string{"select", "insert", "prepare", "held"} {
		bounded, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
		var err error
		switch phase {
		case "select":
			var rows *sql.Rows
			rows, err = waiter.QueryContext(bounded, "SELECT id FROM t")
			if rows != nil {
				rows.Close()
			}
		case "insert":
			_, err = waiter.ExecContext(bounded, "INSERT INTO t VALUES(11)")
		case "prepare":
			var stmt *sql.Stmt
			stmt, err = waiter.PrepareContext(bounded, "SELECT id FROM t")
			if stmt != nil {
				stmt.Close()
			}
		case "held":
			var rows *sql.Rows
			rows, err = held.QueryContext(bounded)
			if rows != nil {
				rows.Close()
			}
		}
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(phase, "held AX did not exclude native operation", err)
		}
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		q    string
		want int64
	}{{"SELECT id FROM early.t", 9}, {"SELECT id FROM late.t", 7}} {
		rows, err := waiter.QueryContext(ctx, item.q)
		if err != nil {
			t.Fatal(err)
		}
		schemaShadowValues(t, rows, item.want)
	}
}
