package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePgrowlocksRelationWaitAndIdentity(t *testing.T) {
	t.Run("wait-release-null-exact-relation", func(t *testing.T) {
		pgrowScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, observer *sql.Conn) {
			pgrowTable(t, p, a)
			waiter, _ := fixture.LockConnections(t, p.Ctx, b.DB)
			for _, prepared := range []bool{false, true} {
				fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
				fixture.LockExec(t, p.Ctx, a, "LOCK TABLE pgrow_t IN ACCESS EXCLUSIVE MODE", 0)
				ap := pgrowTransactionPID(t, p, a, identityLow(t, identityID(t, p, a)))
				wp := pgrowWaiterPID(t, p, waiter)
				if got := pgrowQuery(t, p, observer, "SELECT * FROM pgrowlocks(NULL::text)"); len(got) != 0 {
					t.Fatal(got)
				}
				await := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
					return fixture.RowQuery(ctx, waiter, "SELECT * FROM pgrowlocks('pgrow_t')", prepared)
				})
				pgrowWait(t, p, observer, wp, ap, true)
				fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
				out := await()
				if out.Err != nil {
					t.Fatal(out.Err)
				}
				if got := pgrowRead(t, out.Rows); len(got) != 0 {
					t.Fatal(got)
				}
				if out.Stmt != nil {
					out.Stmt.Close()
				}
			}
			fixture.LockExec(t, p.Ctx, a, "CREATE TABLE pgrow_child() INHERITS(pgrow_t)", 0)
			fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
			fixture.LockExec(t, p.Ctx, a, "LOCK TABLE ONLY pgrow_child IN ACCESS EXCLUSIVE MODE", 0)
			// No descendant AS request is allowed. A deadline bounds accidental
			// inheritance expansion without treating an elapsed delay as proof.
			pgrowEmpty(t, p, observer)
			fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
		})
	})
	t.Run("counted-access-share-release", func(t *testing.T) {
		pgrowScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, observer *sql.Conn) {
			pgrowTable(t, p, a)
			for _, prior := range []bool{false, true} {
				fixture.LockExec(t, p.Ctx, observer, "BEGIN", 0)
				if prior {
					identityBoth(t, p, b, observer, fixture.Select("SELECT v FROM pgrow_t", []string{"v"}, []string{"INT4"}, [][]any{{int64(0)}}))
				}
				for range 2 {
					pgrowEmpty(t, p, observer)
				}
				fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
				_, err := a.ExecContext(p.Ctx, "LOCK TABLE pgrow_t IN ACCESS EXCLUSIVE MODE NOWAIT")
				if prior {
					want := fixture.ControlsReferenceDiagnostic("55P03", `could not obtain lock on relation "pgrow_t"`)
					fixture.AssertDiagnostic(t, err, &want)
				} else if err != nil {
					t.Fatal("function leaked AccessShare", err)
				}
				fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
				fixture.LockExec(t, p.Ctx, observer, "ROLLBACK", 0)
				fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
				fixture.LockExec(t, p.Ctx, a, "LOCK TABLE pgrow_t IN ACCESS EXCLUSIVE MODE NOWAIT", 0)
				fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
			}
		})
	})
	t.Run("cancel-removes-request", func(t *testing.T) {
		pgrowScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, observer *sql.Conn) {
			pgrowTable(t, p, a)
			waiter, _ := fixture.LockConnections(t, p.Ctx, b.DB)
			fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
			fixture.LockExec(t, p.Ctx, a, "LOCK TABLE pgrow_t IN ACCESS EXCLUSIVE MODE", 0)
			wp := pgrowWaiterPID(t, p, waiter)
			ap := pgrowTransactionPID(t, p, a, identityLow(t, identityID(t, p, a)))
			ctx, cancel := context.WithCancel(p.Ctx)
			defer cancel()
			await := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
				return fixture.RowQuery(ctx, waiter, "SELECT * FROM pgrowlocks('pgrow_t')", false)
			})
			pgrowWait(t, p, observer, wp, ap, true)
			if b.PG {
				var canceled bool
				if err := observer.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", wp).Scan(&canceled); err != nil || !canceled {
					t.Fatal(canceled, err)
				}
			} else {
				cancel()
			}
			out := await()
			if out.Rows != nil {
				out.Rows.Close()
				t.Fatal("canceled query returned cursor")
			}
			if b.PG {
				want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
				fixture.AssertDiagnostic(t, out.Err, &want)
			} else {
				fixture.DriverOrdinaryError(t, out.Err, context.Canceled)
			}
			var queued int
			if err := observer.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_locks WHERE pid=$1 AND NOT granted", wp).Scan(&queued); err != nil || queued != 0 {
				t.Fatal("cancellation retained request", queued, err)
			}
			fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
			pgrowEmpty(t, p, waiter)
			fixture.LockExec(t, p.Ctx, observer, "BEGIN", 0)
			fixture.LockExec(t, p.Ctx, observer, "LOCK TABLE pgrow_t IN ACCESS EXCLUSIVE MODE NOWAIT", 0)
			fixture.LockExec(t, p.Ctx, observer, "ROLLBACK", 0)
		})
	})
	t.Run("queued-drop-recreate", func(t *testing.T) {
		pgrowScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, observer *sql.Conn) {
			pgrowTable(t, p, a)
			if !b.PG {
				// Explicit-transaction DROP is outside native capabilities.
				// Assert its real boundary and unchanged table independently;
				// Reset/Restore below supplies the native queued identity fence.
				fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
				_, err := a.ExecContext(p.Ctx, "DROP TABLE pgrow_t")
				want := fixture.ControlsReferenceDiagnostic("0A000", "statement is unsupported in an explicit transaction")
				fixture.AssertDiagnostic(t, err, &want)
				fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
				identityBoth(t, p, b, observer, fixture.Select("SELECT id,v FROM pgrow_t", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(0)}}))
				fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
				owner := pgrowOwner(t, p, a, "For Update")
				identityLock(t, p, a, "pgrow_t", 1, "UPDATE")
				pgrowOne(t, pgrowGet(t, p, observer), false, owner)
				fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
				return
			}
			waiter, _ := fixture.LockConnections(t, p.Ctx, b.DB)
			var oldOID, newOID int64
			if err := observer.QueryRowContext(p.Ctx, "SELECT 'pgrow_t'::regclass::oid::bigint").Scan(&oldOID); err != nil {
				t.Fatal(err)
			}
			fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
			fixture.LockExec(t, p.Ctx, a, "LOCK TABLE pgrow_t IN ACCESS EXCLUSIVE MODE", 0)
			wp := pgrowWaiterPID(t, p, waiter)
			ap := pgrowTransactionPID(t, p, a, identityLow(t, identityID(t, p, a)))
			await := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
				return fixture.RowQuery(ctx, waiter, "SELECT * FROM pgrowlocks('pgrow_t')", false)
			})
			pgrowWait(t, p, observer, wp, ap, true)
			fixture.LockExec(t, p.Ctx, a, "DROP TABLE pgrow_t", 0)
			fixture.LockExec(t, p.Ctx, a, "CREATE TABLE pgrow_t(id INT PRIMARY KEY,v INT)", 0)
			fixture.LockExec(t, p.Ctx, a, "INSERT INTO pgrow_t VALUES(7,70)", 1)
			fixture.LockExec(t, p.Ctx, a, "COMMIT", 0)
			out := await()
			// PostgreSQL RangeVar invalidation re-resolves the replacement.
			// This is not a native no-retarget parity claim.
			if out.Err != nil {
				t.Fatalf("PG queued replacement outcome: %#v", fixture.ExtractDiagnostic(t, out.Err))
			}
			if got := pgrowRead(t, out.Rows); len(got) != 0 {
				t.Fatal("replacement fabricated old owner", got)
			}
			if err := observer.QueryRowContext(p.Ctx, "SELECT 'pgrow_t'::regclass::oid::bigint").Scan(&newOID); err != nil || newOID == oldOID {
				t.Fatal("replacement identity", oldOID, newOID, err)
			}
			fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
			owner := pgrowOwner(t, p, a, "For Update")
			identityLock(t, p, a, "pgrow_t", 7, "UPDATE")
			pgrowOne(t, pgrowGet(t, p, observer), false, owner)
			fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
		})
	})
	t.Run("native-reset-restore-fence", func(t *testing.T) {
		pgrowScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, observer *sql.Conn) {
			if b.PG {
				return
			} // Actual native APIs have no PostgreSQL equivalent.
			for _, restore := range []bool{false, true} {
				pgrowTable(t, p, a)
				waiter, _ := fixture.LockConnections(t, p.Ctx, b.DB)
				fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
				fixture.LockExec(t, p.Ctx, a, "LOCK TABLE pgrow_t IN ACCESS EXCLUSIVE MODE", 0)
				wp := pgrowWaiterPID(t, p, waiter)
				ap := pgrowTransactionPID(t, p, a, identityLow(t, identityID(t, p, a)))
				await := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
					return fixture.RowQuery(ctx, waiter, "SELECT * FROM pgrowlocks('pgrow_t')", false)
				})
				pgrowWait(t, p, observer, wp, ap, true)
				var err error
				if restore {
					err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE pgrow_replacement(id INT);"))
				} else {
					err = stuntgres.Reset(p.Ctx, p.Native)
				}
				if err != nil {
					t.Fatal(err)
				}
				out := await()
				if out.Rows != nil {
					out.Rows.Close()
					t.Fatal("fenced capture returned rows")
				}
				var state interface{ SQLState() string }
				if out.Err == nil || errors.As(out.Err, &state) {
					t.Fatal("replacement must be ordinary error", out.Err)
				}
				fixture.TransactionOrdinary(t, out.Err, "stuntgres: catalog replaced before publication")
				fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
			}
		})
	})
}
