package reference_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"runtime"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

// Observe real relation-manager projections on both backends, not elapsed time
// or goroutine launch order. The selected holder and waiter must share the exact
// relation OID and conflicting modes, with an ungranted waiter.
func truncateRestartWait(t *testing.T, p *fixture.Pair, b fixture.Backend, holder, waiter int, sequence, heldMode, waitingMode string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
	defer cancel()
	for {
		var observed bool
		err := b.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND relation=$3::regclass::oid AND mode=$4 AND granted) AND EXISTS(SELECT 1 FROM pg_locks WHERE pid=$2 AND relation=$3::regclass::oid AND mode=$5 AND NOT granted)`, holder, waiter, sequence, heldMode, waitingMode).Scan(&observed)
		if err != nil {
			t.Fatal(err)
		}
		if observed {
			return
		}
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
		runtime.Gosched()
	}
}

func TestReferenceTruncateRestartLocks(t *testing.T) {
	for _, holderKind := range []string{"SequenceRowAS", "FunctionRX"} {
		for _, terminal := range []string{"LockTimeout", "StatementTimeout", "Cancel"} {
			t.Run(holderKind+"/"+terminal, func(t *testing.T) {
				truncateRestartScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture, held bool) {
					truncateRestartSeed(t, f)
					truncateExec(t, f, f.A, "CREATE TABLE second(id SERIAL)", 0)
					truncateExec(t, f, f.A, "INSERT INTO second DEFAULT VALUES", 1)
					q := "TRUNCATE t,second RESTART IDENTITY"
					x := truncateRestartRoute(t, p, f.B, q, held)
					holder := truncateRestartPID(t, p, b, f.A)
					waiter := truncateRestartPID(t, p, b, f.B)
					tx := truncateBegin(t, p, f.A, nil)
					mode := "AccessShareLock"
					if holderKind == "SequenceRowAS" {
						truncateRestartState(t, f, tx, "second_id_seq", 1, true)
					} else {
						mode = "RowExclusiveLock"
						truncateRestartValue(t, f, tx, "SELECT nextval('second_id_seq')", "nextval", 2)
					}
					// A lock timer counts only the lock wait; a statement timer also
					// counts parse and plan time of the direct route.
					setting, timer := "lock_timeout", "10ms"
					if terminal == "StatementTimeout" {
						setting, timer = "statement_timeout", "100ms"
					}
					if terminal != "Cancel" {
						truncateExec(t, f, f.B, "SET "+setting+"='"+timer+"'", 0)
					}
					ctx, cancel := context.WithCancel(p.Ctx)
					defer cancel()
					join := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
						r, err := x.ExecContext(ctx, q)
						return fixture.RelationOutcome{Result: r, Err: err}
					})
					// A timer's exact outcome proves the wait; never race an observer
					// against the short timer. Cancel observes the parked waiter.
					if terminal == "Cancel" {
						truncateRestartWait(t, p, b, holder, waiter, "second_id_seq", mode, "AccessExclusiveLock")
						// Both earlier table and owned sequence acquisitions must be real.
						var prefix bool
						if err := b.DB.QueryRowContext(p.Ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND relation='t'::regclass::oid AND mode='AccessExclusiveLock' AND granted) AND EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND relation='t_id_seq'::regclass::oid AND mode='AccessExclusiveLock' AND granted)`, waiter).Scan(&prefix); err != nil || !prefix {
							t.Fatalf("missing table/sequence prefix: %v %v", prefix, err)
						}
						if b.PG {
							var canceled bool
							if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", waiter).Scan(&canceled); err != nil || !canceled {
								t.Fatalf("cancel: %v %v", canceled, err)
							}
						} else {
							cancel()
						}
					}
					o := join()
					if o.Result != nil {
						t.Fatal("failed reset returned result")
					}
					if terminal == "Cancel" && !b.PG {
						if !errors.Is(o.Err, context.Canceled) {
							t.Fatalf("native cancellation: %v", o.Err)
						}
					} else {
						d := truncateWant("55P03", "canceling statement due to lock timeout")
						if terminal == "StatementTimeout" {
							d = truncateWant("57014", "canceling statement due to statement timeout")
						}
						if terminal == "Cancel" {
							d = truncateWant("57014", "canceling statement due to user request")
						}
						fixture.AssertDiagnostic(t, o.Err, &d)
					}
					// The same physical owner and held utility recover after a lock
					// timeout (resetting it takes no lock) or a cancellation. Once a
					// statement timer is armed even SET can time out, so that owner is
					// disposed and the independent holder session recovers instead.
					survivor := x
					switch terminal {
					case "LockTimeout":
						truncateExec(t, f, f.B, "SET lock_timeout=0", 0)
					case "StatementTimeout":
						if err := f.B.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
							t.Fatal("disposing timer-armed owner", err)
						}
						survivor = f.A
					}
					if err := tx.Rollback(); err != nil {
						t.Fatal(err)
					}
					truncateRows(t, f, f.A, "t", 1, 2)
					truncateRows(t, f, f.A, "second", 1)
					truncateRestartState(t, f, f.A, "t_id_seq", 2, true)
					last := int64(1)
					if holderKind == "FunctionRX" {
						last = 2
					}
					truncateRestartState(t, f, f.A, "second_id_seq", last, true)
					// Recovery leaves no leaked locks.
					truncateExec(t, f, survivor, q, 0)
					truncateRestartValue(t, f, f.A, "SELECT nextval('t_id_seq')", "nextval", 1)
					truncateRestartValue(t, f, f.A, "SELECT nextval('second_id_seq')", "nextval", 1)
				})
			})
		}
	}
	for _, function := range []string{"nextval('t_id_seq')", "currval('t_id_seq')", "lastval()", "setval('t_id_seq',80)"} {
		t.Run("ResetBlocks/"+function, func(t *testing.T) {
			truncateRestartScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture, held bool) {
				truncateExec(t, f, f.A, "CREATE TABLE t(id SERIAL)", 0)
				truncateExec(t, f, f.A, "ALTER SEQUENCE t_id_seq CACHE 4", 0)
				truncateRestartValue(t, f, f.B, "SELECT nextval('t_id_seq')", "nextval", 1)
				holder := truncateRestartPID(t, p, b, f.A)
				waiter := truncateRestartPID(t, p, b, f.B)
				q := "TRUNCATE t RESTART IDENTITY"
				x := truncateRestartRoute(t, p, f.A, q, held)
				defer f.A.ExecContext(p.Ctx, "ROLLBACK")
				truncateExec(t, f, f.A, "BEGIN", 0)
				truncateExec(t, f, x, q, 0) // AX must survive without any function demand.
				query := "SELECT " + function
				consumer := truncateRestartRoute(t, p, f.B, query, held)
				join := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
					r, err := consumer.QueryContext(ctx, query)
					return fixture.RelationOutcome{Rows: r, Err: err}
				})
				truncateRestartWait(t, p, b, holder, waiter, "t_id_seq", "AccessExclusiveLock", "RowExclusiveLock")
				truncateExec(t, f, f.A, "COMMIT", 0)
				o := join()
				if o.Rows != nil {
					defer o.Rows.Close()
				}
				if o.Err != nil || o.Rows == nil {
					t.Fatalf("blocked function: %v", o.Err)
				}
				columns, err := o.Rows.Columns()
				label := "nextval"
				switch function {
				case "currval('t_id_seq')":
					label = "currval"
				case "lastval()":
					label = "lastval"
				case "setval('t_id_seq',80)":
					label = "setval"
				}
				if err != nil || len(columns) != 1 || columns[0] != label {
					t.Fatalf("columns: %v want %s: %v", columns, label, err)
				}
				types, err := o.Rows.ColumnTypes()
				if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "INT8" {
					t.Fatalf("types: %v %v", types, err)
				}
				if !o.Rows.Next() {
					t.Fatalf("missing function value: %v", o.Rows.Err())
				}
				var got int64
				want := int64(1)
				if function == "setval('t_id_seq',80)" {
					want = 80
				}
				if err := o.Rows.Scan(&got); err != nil || got != want {
					t.Fatalf("function value %d want %d: %v", got, want, err)
				}
				if o.Rows.Next() || o.Rows.Err() != nil {
					t.Fatalf("function tail: %v", o.Rows.Err())
				}
				o.Rows.Close()
			})
		})
	}
}
