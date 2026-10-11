//go:build reference

package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDeleteReturningCurrentPredecessor(t *testing.T) {
	// Native exact pending-resource/journal witnesses live in engine tests.
	// PostgreSQL independently observes the physical transaction-ID blocker.
	for _, lane := range []string{"latest", "no_where", "predicate_false", "peer_delete", "rollback", "repeatable_read"} {
		t.Run("postgres/"+lane, func(t *testing.T) {
			p := fixture.NewPair(t)
			a, b := fixture.LockConnections(t, p.Ctx, p.Postgres)
			c, err := p.Postgres.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			fixture.LockExec(t, p.Ctx, a, "CREATE TABLE t(id INT,v TEXT)", 0)
			fixture.LockExec(t, p.Ctx, a, "INSERT INTO t VALUES(2,'old')", 1)
			ap, bp := fixture.LockPID(t, p.Ctx, a), fixture.LockPID(t, p.Ctx, b)
			holder, err := a.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer holder.Rollback()
			hold := "UPDATE t SET v='fresh' WHERE id=2"
			if lane == "predicate_false" {
				hold = "UPDATE t SET id=9 WHERE id=2"
			}
			if lane == "peer_delete" {
				hold = "DELETE FROM t WHERE id=2"
			}
			fixture.LockExec(t, p.Ctx, holder, hold, 1)
			var worker *sql.Tx
			if lane == "repeatable_read" {
				worker, err = b.BeginTx(p.Ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
				if err != nil {
					t.Fatal(err)
				}
				defer worker.Rollback()
			}
			query := "DELETE FROM t WHERE id=2 RETURNING v,old.v,new.v"
			if lane == "no_where" {
				query = "DELETE FROM t RETURNING v,old.v,new.v"
			}
			await := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
				if worker != nil {
					rows, err := worker.QueryContext(ctx, query)
					return fixture.RelationOutcome{Rows: rows, Err: err}
				}
				rows, err := b.QueryContext(ctx, query)
				return fixture.RelationOutcome{Rows: rows, Err: err}
			})
			deleteReferenceWaitRow(t, p.Ctx, bp, ap)
			fixture.LockExec(t, p.Ctx, c, "INSERT INTO t VALUES(2,'later')", 1)
			if lane == "rollback" {
				err = holder.Rollback()
			} else {
				err = holder.Commit()
			}
			if err != nil {
				t.Fatal(err)
			}
			o := await()
			if lane == "repeatable_read" {
				if o.Rows != nil {
					o.Rows.Close()
					t.Fatal("serialization failure cursor")
				}
				fixture.AssertDiagnostic(t, o.Err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "40001", Message: "could not serialize access due to concurrent update"})
				if err = worker.Rollback(); err != nil {
					t.Fatal(err)
				}
				fixture.DeleteReferenceRows(t, p, c, p.PostgresRecord, 2, 2)
				return
			}
			if o.Err != nil {
				t.Fatal(o.Err)
			}
			defer o.Rows.Close()
			want := "fresh"
			if lane == "rollback" {
				want = "old"
			}
			if lane == "predicate_false" || lane == "peer_delete" {
				if o.Rows.Next() {
					t.Fatal("skipped delete produced output")
				}
			} else {
				if !o.Rows.Next() {
					t.Fatal(o.Rows.Err())
				}
				var unqualified, old string
				var newValue any
				if err = o.Rows.Scan(&unqualified, &old, &newValue); err != nil || unqualified != want || old != want || newValue != nil {
					t.Fatal(unqualified, old, newValue, err)
				}
				if o.Rows.Next() {
					t.Fatal("late insert entered fixed candidates")
				}
			}
			if err = o.Rows.Err(); err != nil {
				t.Fatal(err)
			}
			o.Rows.Close()
			if lane == "predicate_false" {
				fixture.DeleteReferenceRows(t, p, c, p.PostgresRecord, 2, 9)
			} else {
				fixture.DeleteReferenceRows(t, p, c, p.PostgresRecord, 2)
			}
		})
	}
}

func TestReferenceDeleteReturningServerCancellation(t *testing.T) {
	for _, kind := range []string{"admin", "lock_timeout", "statement_timeout"} {
		t.Run("postgres/"+kind, func(t *testing.T) {
			p := fixture.NewPair(t)
			a, b := fixture.LockConnections(t, p.Ctx, p.Postgres)
			fixture.LockExec(t, p.Ctx, a, "CREATE TABLE t(id INT)", 0)
			fixture.LockExec(t, p.Ctx, a, "INSERT INTO t VALUES(1),(2)", 2)
			physical := deleteReferenceTupleContext(t, p.Ctx, b)
			ap, bp := fixture.LockPID(t, p.Ctx, a), fixture.LockPID(t, p.Ctx, b)
			holder, err := a.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer holder.Rollback()
			fixture.LockExec(t, p.Ctx, holder, "DELETE FROM t WHERE id=2", 1)
			if kind != "admin" {
				fixture.LockExec(t, p.Ctx, b, "SET "+kind+"='500ms'", 0)
			}
			await := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
				rows, err := b.QueryContext(ctx, "DELETE FROM t RETURNING id")
				return fixture.RelationOutcome{Rows: rows, Err: err}
			})
			deleteReferenceWaitRow(t, p.Ctx, bp, ap)
			var earlierDeletion bool
			if err = fixture.Admin.QueryRowContext(p.Ctx, "SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND locktype='transactionid' AND granted AND transactionid=(SELECT xmax FROM t WHERE id=1))", bp).Scan(&earlierDeletion); err != nil || !earlierDeletion {
				t.Fatal("wait lacked independently observed earlier tuple deletion", earlierDeletion, err)
			}
			if kind == "admin" {
				var canceled bool
				if err = fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", bp).Scan(&canceled); err != nil || !canceled {
					t.Fatal(canceled, err)
				}
			}
			o := await()
			if o.Rows != nil {
				o.Rows.Close()
				t.Fatal("failed RETURNING exposed cursor")
			}
			want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request", Context: physical}
			if kind == "lock_timeout" {
				want.Code, want.Message = "55P03", "canceling statement due to lock timeout"
			}
			if kind == "statement_timeout" {
				want.Message = "canceling statement due to statement timeout"
			}
			fixture.AssertDiagnostic(t, o.Err, &want)
			if err = holder.Rollback(); err != nil {
				t.Fatal(err)
			}
			fixture.LockExec(t, p.Ctx, b, "SET lock_timeout=0", 0)
			fixture.LockExec(t, p.Ctx, b, "SET statement_timeout=0", 0)
			fixture.DeleteReferenceRows(t, p, b, p.PostgresRecord, 1, 2)
		})
	}
}
