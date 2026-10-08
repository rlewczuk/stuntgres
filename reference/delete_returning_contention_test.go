//go:build reference

package reference_test

import (
	"context"
	"database/sql"
	"testing"
)

func TestReferenceDeleteReturningCurrentPredecessor(t *testing.T) {
	// Native exact pending-resource/journal witnesses live in engine tests.
	// PostgreSQL independently observes the physical transaction-ID blocker.
	for _, lane := range []string{"latest", "no_where", "predicate_false", "peer_delete", "rollback", "repeatable_read"} {
		t.Run("postgres/"+lane, func(t *testing.T) {
			p := newReferencePair(t)
			a, b := referenceLockConnections(t, p.ctx, p.postgres)
			c, err := p.postgres.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			referenceLockExec(t, p.ctx, a, "CREATE TABLE t(id INT,v TEXT)", 0)
			referenceLockExec(t, p.ctx, a, "INSERT INTO t VALUES(2,'old')", 1)
			ap, bp := referenceLockPID(t, p.ctx, a), referenceLockPID(t, p.ctx, b)
			holder, err := a.BeginTx(p.ctx, nil)
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
			referenceLockExec(t, p.ctx, holder, hold, 1)
			var worker *sql.Tx
			if lane == "repeatable_read" {
				worker, err = b.BeginTx(p.ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
				if err != nil {
					t.Fatal(err)
				}
				defer worker.Rollback()
			}
			query := "DELETE FROM t WHERE id=2 RETURNING v,old.v,new.v"
			if lane == "no_where" {
				query = "DELETE FROM t RETURNING v,old.v,new.v"
			}
			await := referenceLockOperation(t, p.ctx, func(ctx context.Context) relationOutcome {
				if worker != nil {
					rows, err := worker.QueryContext(ctx, query)
					return relationOutcome{rows: rows, err: err}
				}
				rows, err := b.QueryContext(ctx, query)
				return relationOutcome{rows: rows, err: err}
			})
			deleteReferenceWaitRow(t, p.ctx, bp, ap)
			referenceLockExec(t, p.ctx, c, "INSERT INTO t VALUES(2,'later')", 1)
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
				if o.rows != nil {
					o.rows.Close()
					t.Fatal("serialization failure cursor")
				}
				referenceAssertDiagnostic(t, o.err, &referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "40001", Message: "could not serialize access due to concurrent update"})
				if err = worker.Rollback(); err != nil {
					t.Fatal(err)
				}
				deleteReferenceRows(t, p, c, p.postgresRecord, 2, 2)
				return
			}
			if o.err != nil {
				t.Fatal(o.err)
			}
			defer o.rows.Close()
			want := "fresh"
			if lane == "rollback" {
				want = "old"
			}
			if lane == "predicate_false" || lane == "peer_delete" {
				if o.rows.Next() {
					t.Fatal("skipped delete produced output")
				}
			} else {
				if !o.rows.Next() {
					t.Fatal(o.rows.Err())
				}
				var unqualified, old string
				var newValue any
				if err = o.rows.Scan(&unqualified, &old, &newValue); err != nil || unqualified != want || old != want || newValue != nil {
					t.Fatal(unqualified, old, newValue, err)
				}
				if o.rows.Next() {
					t.Fatal("late insert entered fixed candidates")
				}
			}
			if err = o.rows.Err(); err != nil {
				t.Fatal(err)
			}
			o.rows.Close()
			if lane == "predicate_false" {
				deleteReferenceRows(t, p, c, p.postgresRecord, 2, 9)
			} else {
				deleteReferenceRows(t, p, c, p.postgresRecord, 2)
			}
		})
	}
}

func TestReferenceDeleteReturningCancellation(t *testing.T) {
	for _, kind := range []string{"admin", "lock_timeout", "statement_timeout"} {
		t.Run("postgres/"+kind, func(t *testing.T) {
			p := newReferencePair(t)
			a, b := referenceLockConnections(t, p.ctx, p.postgres)
			referenceLockExec(t, p.ctx, a, "CREATE TABLE t(id INT)", 0)
			referenceLockExec(t, p.ctx, a, "INSERT INTO t VALUES(1),(2)", 2)
			physical := deleteReferenceTupleContext(t, p.ctx, b)
			ap, bp := referenceLockPID(t, p.ctx, a), referenceLockPID(t, p.ctx, b)
			holder, err := a.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer holder.Rollback()
			referenceLockExec(t, p.ctx, holder, "DELETE FROM t WHERE id=2", 1)
			if kind != "admin" {
				referenceLockExec(t, p.ctx, b, "SET "+kind+"='500ms'", 0)
			}
			await := referenceLockOperation(t, p.ctx, func(ctx context.Context) relationOutcome {
				rows, err := b.QueryContext(ctx, "DELETE FROM t RETURNING id")
				return relationOutcome{rows: rows, err: err}
			})
			deleteReferenceWaitRow(t, p.ctx, bp, ap)
			var earlierDeletion bool
			if err = referenceAdmin.QueryRowContext(p.ctx, "SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND locktype='transactionid' AND granted AND transactionid=(SELECT xmax FROM t WHERE id=1))", bp).Scan(&earlierDeletion); err != nil || !earlierDeletion {
				t.Fatal("wait lacked independently observed earlier tuple deletion", earlierDeletion, err)
			}
			if kind == "admin" {
				var canceled bool
				if err = referenceAdmin.QueryRowContext(p.ctx, "SELECT pg_cancel_backend($1)", bp).Scan(&canceled); err != nil || !canceled {
					t.Fatal(canceled, err)
				}
			}
			o := await()
			if o.rows != nil {
				o.rows.Close()
				t.Fatal("failed RETURNING exposed cursor")
			}
			want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request", Context: physical}
			if kind == "lock_timeout" {
				want.Code, want.Message = "55P03", "canceling statement due to lock timeout"
			}
			if kind == "statement_timeout" {
				want.Message = "canceling statement due to statement timeout"
			}
			referenceAssertDiagnostic(t, o.err, &want)
			if err = holder.Rollback(); err != nil {
				t.Fatal(err)
			}
			referenceLockExec(t, p.ctx, b, "SET lock_timeout=0", 0)
			referenceLockExec(t, p.ctx, b, "SET statement_timeout=0", 0)
			deleteReferenceRows(t, p, b, p.postgresRecord, 1, 2)
		})
	}
}
