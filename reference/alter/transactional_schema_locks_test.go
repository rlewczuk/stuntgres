package reference_test

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func transactionalSchemaPID(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) int {
	t.Helper()
	if b.PG {
		return fixture.LockPID(t, p.Ctx, c)
	}
	fixture.LockExec(t, p.Ctx, c, "CREATE TEMP SEQUENCE tx_schema_pid", 0)
	var name string
	if err := c.QueryRowContext(p.Ctx, "SELECT nspname FROM pg_namespace WHERE oid=pg_my_temp_schema()").Scan(&name); err != nil {
		t.Fatal(err)
	}
	id, err := strconv.Atoi(strings.TrimPrefix(name, "pg_temp_"))
	if err != nil || id <= 0 {
		t.Fatal(name, err)
	}
	return id
}

// Observe the actual pending request on either public backend, never elapsed time.
func transactionalSchemaPending(t *testing.T, p *fixture.Pair, b fixture.Backend, waiter int, relation int64, finished ...<-chan struct{}) {
	t.Helper()
	ctx, cancel := context.WithTimeout(p.Ctx, 3*time.Second)
	defer cancel()
	for {
		if len(finished) > 0 {
			select {
			case <-finished[0]:
				t.Fatal("operation completed without the required pending request")
			default:
			}
		}
		var pending bool
		if err := b.DB.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND relation=$2 AND NOT granted)", waiter, relation).Scan(&pending); err != nil {
			t.Fatal(err)
		}
		if pending {
			return
		}
	}
}

func TestReferenceTransactionalSchemaLocks(t *testing.T) {
	transactionalSchemaNoRetarget(t)
	for _, q := range []string{"DROP TABLE tx_lock", "ALTER TABLE tx_lock RENAME TO tx_lock_new", "ALTER TABLE tx_lock ALTER COLUMN v SET DEFAULT 8", "ALTER TABLE tx_lock SET SCHEMA tx_lock_dest"} {
		t.Run(q, func(t *testing.T) {
			fixture.SchemaReferenceCleanup(t, "tx_lock_dest")
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					a, c := fixture.LockConnections(t, p.Ctx, b.DB)
					fixture.LockExec(t, p.Ctx, a, "CREATE SCHEMA tx_lock_dest", 0)
					fixture.LockExec(t, p.Ctx, a, "CREATE TABLE tx_lock(v INT DEFAULT 2)", 0)
					fixture.LockExec(t, p.Ctx, a, "INSERT INTO tx_lock DEFAULT VALUES", 1)
					fixture.LockExec(t, p.Ctx, a, "CREATE TABLE tx_lock_early(v INT DEFAULT 1)", 0)
					fixture.LockExec(t, p.Ctx, a, "INSERT INTO tx_lock_early DEFAULT VALUES", 1)
					var oid int64
					if err := a.QueryRowContext(p.Ctx, "SELECT 'tx_lock'::regclass::oid").Scan(&oid); err != nil {
						t.Fatal(err)
					}
					ap, cp := transactionalSchemaPID(t, p, b, a), transactionalSchemaPID(t, p, b, c)
					fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
					defer a.ExecContext(p.Ctx, "ROLLBACK")
					rows, err := a.QueryContext(p.Ctx, "SELECT v FROM tx_lock")
					if err != nil {
						t.Fatal(err)
					}
					rows.Close()
					fixture.LockExec(t, p.Ctx, c, "BEGIN", 0)
					defer c.ExecContext(p.Ctx, "ROLLBACK")
					fixture.LockExec(t, p.Ctx, c, "ALTER TABLE tx_lock_early ALTER COLUMN v SET DEFAULT 5", 0)
					fixture.LockExec(t, p.Ctx, c, "SAVEPOINT before_ddl", 0)
					finished := make(chan struct{})
					await := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
						defer close(finished)
						result, err := c.ExecContext(ctx, q)
						return fixture.RelationOutcome{Result: result, Err: err}
					})
					transactionalSchemaPending(t, p, b, cp, oid, finished)
					fixture.LockExec(t, p.Ctx, a, "COMMIT", 0)
					o := await()
					if o.Err != nil {
						t.Fatal(o.Err)
					}
					n, err := o.Result.RowsAffected()
					if err != nil || n != 0 {
						t.Fatal(n, err)
					}
					// Successful DDL keeps AX after its statement, including after result close.
					followDone := make(chan struct{})
					follow := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
						defer close(followDone)
						rows, err := a.QueryContext(ctx, "SELECT v FROM tx_lock")
						return fixture.RelationOutcome{Rows: rows, Err: err}
					})
					transactionalSchemaPending(t, p, b, ap, oid, followDone)
					fixture.LockExec(t, p.Ctx, c, "ROLLBACK TO before_ddl", 0)
					fixture.LimitLockValues(t, follow(), b.PG, 2)
					var earlyOID int64
					if err := c.QueryRowContext(p.Ctx, "SELECT 'tx_lock_early'::regclass::oid").Scan(&earlyOID); err != nil {
						t.Fatal(err)
					}
					earlyDone := make(chan struct{})
					early := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
						defer close(earlyDone)
						rows, err := a.QueryContext(ctx, "SELECT v FROM tx_lock_early")
						return fixture.RelationOutcome{Rows: rows, Err: err}
					})
					transactionalSchemaPending(t, p, b, ap, earlyOID, earlyDone)
					fixture.LockExec(t, p.Ctx, c, "COMMIT", 0)
					fixture.LimitLockValues(t, early(), b.PG, 1)
					// Active cancellation observes a real queued operation, joins it, then
					// checks owner recovery. The fixture distinguishes native context errors.
					fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
					fixture.LockExec(t, p.Ctx, a, "LOCK TABLE tx_lock IN ACCESS SHARE MODE", 0)
					fixture.RelationLockActiveCancel(t, p, b, c, q, func(ctx context.Context) fixture.RelationOutcome {
						result, err := c.ExecContext(ctx, q)
						return fixture.RelationOutcome{Result: result, Err: err}
					})
					fixture.LockExec(t, p.Ctx, c, "BEGIN", 0)
					fixture.LockExec(t, p.Ctx, c, "SET LOCAL lock_timeout='100ms'", 0)
					fixture.LockExec(t, p.Ctx, c, "SAVEPOINT timeout_recovery", 0)
					_, err = c.ExecContext(p.Ctx, q)
					d := fixture.ControlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout")
					fixture.AssertDiagnostic(t, err, &d)
					_, err = c.ExecContext(p.Ctx, "SELECT 1")
					d = fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")
					fixture.AssertDiagnostic(t, err, &d)
					fixture.LockExec(t, p.Ctx, c, "ROLLBACK TO timeout_recovery", 0)
					fixture.LockExec(t, p.Ctx, c, "COMMIT", 0)
					fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
					fixture.LockExec(t, p.Ctx, c, "INSERT INTO tx_lock VALUES(3)", 1)
				})
			}
		})
	}
}
