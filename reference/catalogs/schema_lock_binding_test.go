//go:build reference

package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres/reference/fixture"
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
	fixture.TransactionRowValues(t, rows, want...)
}

func TestReferenceSchemaLockedBindingShadowCreation(t *testing.T) {
	for _, phase := range []string{"direct-select", "direct-insert", "prepare-select", "held-select"} {
		t.Run(phase, func(t *testing.T) {
			pair := fixture.NewPair(t)
			ctx, cancel := context.WithCancel(pair.Ctx)
			defer cancel()
			a, waiter := fixture.LockConnections(t, ctx, pair.Postgres)
			shadow, err := pair.Postgres.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer shadow.Close()
			defer func() {
				cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
				defer stop()
				if _, err := pair.Postgres.ExecContext(cleanup, "DROP SCHEMA IF EXISTS early, late CASCADE"); err != nil {
					t.Error("schema cleanup", err)
				}
			}()
			fixture.LockExec(t, ctx, a, "CREATE SCHEMA early", 0)
			fixture.LockExec(t, ctx, a, "CREATE SCHEMA late CREATE TABLE t(id INT)", 0)
			fixture.LockExec(t, ctx, a, "INSERT INTO late.t VALUES(7)", 1)
			fixture.LockExec(t, ctx, waiter, "SET search_path TO early, late", 0)
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
			fixture.LockExec(t, ctx, lateTx, "LOCK TABLE late.t IN ACCESS EXCLUSIVE MODE", 0)
			done := make(chan fixture.RelationOutcome, 1)
			go func() {
				var out fixture.RelationOutcome
				switch phase {
				case "direct-select":
					out.Rows, out.Err = waiter.QueryContext(ctx, "SELECT id FROM t")
				case "direct-insert":
					out.Result, out.Err = waiter.ExecContext(ctx, "INSERT INTO t VALUES(11)")
				case "prepare-select":
					out.Stmt, out.Err = waiter.PrepareContext(ctx, "SELECT id FROM t")
				case "held-select":
					out.Rows, out.Err = held.QueryContext(ctx)
				}
				done <- out
			}()
			joined := false
			defer func() {
				cancel()
				if !joined {
					out := fixture.LockResult(t, done)
					if out.Rows != nil {
						out.Rows.Close()
					}
					if out.Stmt != nil {
						out.Stmt.Close()
					}
				}
			}()
			fixture.SchemaShadowWait(t, ctx, pid, "late.t")
			fixture.LockExec(t, ctx, shadow, "CREATE TABLE early.t(id INT)", 0)
			fixture.LockExec(t, ctx, shadow, "INSERT INTO early.t VALUES(9)", 1)
			earlyTx, err := shadow.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer earlyTx.Rollback()
			fixture.LockExec(t, ctx, earlyTx, "LOCK TABLE early.t IN ACCESS EXCLUSIVE MODE", 0)
			if err := lateTx.Rollback(); err != nil {
				t.Fatal(err)
			}
			if phase != "held-select" {
				fixture.SchemaShadowWait(t, ctx, pid, "early.t")
				if err := earlyTx.Rollback(); err != nil {
					t.Fatal(err)
				}
			}
			out := fixture.LockResult(t, done)
			joined = true
			if out.Err != nil {
				t.Fatal(out.Err)
			}
			switch phase {
			case "held-select":
				schemaShadowValues(t, out.Rows, 7)
				t.Log("pinned warmed statement selected locked late.t=7 while early AX remained held")
				if err := earlyTx.Rollback(); err != nil {
					t.Fatal(err)
				}
			case "direct-select":
				schemaShadowValues(t, out.Rows, 9)
				t.Log("pinned direct SELECT reacquired early.t and returned9")
			case "direct-insert":
				fixture.LockAffected(t, out.Result, 1)
				t.Log("pinned direct INSERT reacquired early.t and affected1")
			case "prepare-select":
				defer out.Stmt.Close()
				rows, err := out.Stmt.QueryContext(ctx)
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
	pair := fixture.NewPair(t)
	ctx := pair.Ctx
	a, waiter := fixture.LockConnections(t, ctx, pair.Memory)
	for _, q := range []string{"CREATE SCHEMA early CREATE TABLE t(id INT)", "CREATE SCHEMA late CREATE TABLE t(id INT)", "SET search_path TO early, late"} {
		fixture.LockExec(t, ctx, waiter, q, 0)
	}
	fixture.LockExec(t, ctx, waiter, "INSERT INTO early.t VALUES(9)", 1)
	fixture.LockExec(t, ctx, waiter, "INSERT INTO late.t VALUES(7)", 1)
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
	fixture.LockExec(t, ctx, tx, "LOCK TABLE early.t IN ACCESS EXCLUSIVE MODE", 0)
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
