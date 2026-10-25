package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceAmcheckHeapTemporary(t *testing.T) {
	for _, kind := range []string{"table", "sequence"} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s-%t", kind, prepared), func(t *testing.T) {
				fixture.RoleReferenceCleanup(t, []string{"heap_temp_reader"})
				heapScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, owner *sql.Conn) {
					heapInstall(t, p, b, owner)
					caller, err := b.DB.Conn(p.Ctx)
					if err != nil { t.Fatal(err) }
					defer caller.Close()
					defer func() {
						ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
						defer cancel()
						for _, c := range []*sql.Conn{caller, owner} {
							for _, query := range []string{"ROLLBACK", "RESET ROLE"} {
								if _, err := c.ExecContext(ctx, query); err != nil { t.Error("temporary scenario cleanup", query, err) }
							}
						}
					}()
					run := func(t *testing.T, c *sql.Conn, step fixture.Step) { t.Helper(); fixture.RunStep(t, p.Ctx, c, b.Recorder, step) }
					run(t, owner, fixture.Exec("CREATE ROLE heap_temp_reader", 0))
					run(t, owner, fixture.Exec("GRANT USAGE ON SCHEMA public TO heap_temp_reader", 0))
					if kind == "table" {
						run(t, owner, fixture.Exec("CREATE TEMP TABLE heap_checked_tmp(v INT)", 0))
						run(t, owner, fixture.Exec("INSERT INTO heap_checked_tmp VALUES(7),(9)", 2))
					} else {
						run(t, owner, fixture.Exec("CREATE TEMP SEQUENCE heap_checked_tmp", 0))
						run(t, owner, fixture.Select("SELECT nextval('pg_temp.heap_checked_tmp') AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(1)}}))
					}
					// Both objects predate every tested transaction. Merely owning
					// an unused TEMP object must not prohibit transaction preparation.
					run(t, caller, fixture.Exec("CREATE TEMP TABLE heap_unused_tmp(v INT)", 0))
					var oid int64
					if err := owner.QueryRowContext(p.Ctx, "SELECT 'pg_temp.heap_checked_tmp'::regclass::oid::bigint").Scan(&oid); err != nil || oid <= 0 { t.Fatal("temporary numeric identity", oid, err) }
					var persistentOID int64
					if err := owner.QueryRowContext(p.Ctx, "SELECT 'public.heap_t'::regclass::oid::bigint").Scan(&persistentOID); err != nil || persistentOID <= 0 { t.Fatal("persistent numeric identity", persistentOID, err) }
					query := "SELECT * FROM public.verify_heapam($1::oid::regclass)"
					statements := make(map[*sql.Conn]*sql.Stmt)
					if prepared {
						for _, c := range []*sql.Conn{caller, owner} {
							stmt, err := c.PrepareContext(p.Ctx, query)
							if err != nil { t.Fatal(err) }
							defer stmt.Close()
							statements[c] = stmt
						}
					}
					invoke := func(t *testing.T, c *sql.Conn, step fixture.Step) {
						t.Helper()
						var executor fixture.Executor = c
						if prepared { executor = fixture.HeldStatement{Executor: c, Statement: statements[c], SQL: query} }
						fixture.RunStep(t, p.Ctx, executor, b.Recorder, step)
					}
					lockFree := func(t *testing.T) {
						t.Helper()
						run(t, owner, fixture.Select("SELECT count(*) AS n FROM pg_locks WHERE locktype='relation' AND relation=$1::oid AND mode='AccessShareLock' AND granted", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}, oid))
					}
					foreignAttempt := func(t *testing.T, phase string, diagnostic fixture.Diagnostic) {
						t.Helper()
						gid := fmt.Sprintf("heap_foreign_%s_%t_%s", kind, prepared, phase)
						fixture.PreparedReferenceCleanup(t, b, gid)
						run(t, caller, fixture.Exec("BEGIN", 0))
						run(t, caller, fixture.Exec("SAVEPOINT heap_attempt", 0))
						invoke(t, caller, heapError(query, diagnostic, oid))
						lockFree(t)
						run(t, caller, fixture.Exec("ROLLBACK TO heap_attempt", 0))
						if phase == "foreign" {
							// PostgreSQL 18.6 records the TEMP attempt before storage
							// rejection; ROLLBACK TO does not erase that footprint.
							run(t, caller, fixture.ErrorDiagnostic("PREPARE TRANSACTION '"+gid+"'", heapDiagnostic("0A000", "cannot PREPARE a transaction that has operated on temporary objects")))
							run(t, caller, fixture.Exec("ROLLBACK", 0))
						} else {
							// EXECUTE denial precedes target admission and leaves no
							// TEMP footprint, even though the caller owns unused TEMP.
							run(t, caller, fixture.Exec("PREPARE TRANSACTION '"+gid+"'", 0))
							fixture.PreparedReferenceOne(t, p, owner, gid)
							run(t, owner, fixture.Exec("ROLLBACK PREPARED '"+gid+"'", 0))
						}
						fixture.PreparedReferenceAbsent(t, p, owner, gid)
					}
					run(t, caller, fixture.Exec("SET ROLE heap_temp_reader", 0))
					t.Run("execute-before-target", func(t *testing.T) { foreignAttempt(t, "denied", heapDiagnostic("42501", "permission denied for function verify_heapam")) })
					run(t, caller, fixture.Exec("ROLLBACK", 0))
					run(t, owner, fixture.Exec("GRANT EXECUTE ON FUNCTION public.verify_heapam"+heapSignature+" TO heap_temp_reader", 0))
					t.Run("foreign-storage", func(t *testing.T) { foreignAttempt(t, "foreign", heapDiagnostic("0A000", "cannot access temporary tables of other sessions")) })
					// A failed expectation must not poison the following independent
					// own-TEMP witness, nor leave the borrower in an aborted savepoint.
					run(t, caller, fixture.Exec("ROLLBACK", 0))
					run(t, owner, fixture.Exec("REVOKE EXECUTE ON FUNCTION public.verify_heapam"+heapSignature+" FROM heap_temp_reader", 0))
					t.Run("revoke-held-before-target", func(t *testing.T) { foreignAttempt(t, "revoked", heapDiagnostic("42501", "permission denied for function verify_heapam")) })
					run(t, caller, fixture.Exec("ROLLBACK", 0))
					run(t, caller, fixture.Exec("RESET ROLE", 0))
					// Only a persistent numeric target is accessed in this transaction.
					unused := fmt.Sprintf("heap_unused_%s_%t", kind, prepared)
					fixture.PreparedReferenceCleanup(t, b, unused)
					run(t, owner, fixture.Exec("BEGIN", 0))
					invoke(t, owner, heapEmpty(query, persistentOID))
					run(t, owner, fixture.Exec("PREPARE TRANSACTION '"+unused+"'", 0))
					run(t, caller, fixture.Exec("ROLLBACK PREPARED '"+unused+"'", 0))
					t.Run("own-storage-and-footprint", func(t *testing.T) {
						gid := fmt.Sprintf("heap_own_%s_%t", kind, prepared)
						fixture.PreparedReferenceCleanup(t, b, gid)
						run(t, owner, fixture.Exec("BEGIN", 0))
						invoke(t, owner, heapEmpty(query, oid))
						run(t, owner, fixture.ErrorDiagnostic("PREPARE TRANSACTION '"+gid+"'", heapDiagnostic("0A000", "cannot PREPARE a transaction that has operated on temporary objects")))
						fixture.PreparedReferenceAbsent(t, p, caller, gid)
						run(t, owner, fixture.Exec("ROLLBACK", 0))
						lockFree(t)
					})
					run(t, owner, fixture.Exec("ROLLBACK", 0))
					if kind == "table" {
						run(t, owner, fixture.Select("SELECT v FROM pg_temp.heap_checked_tmp ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(9)}}))
					} else {
						run(t, owner, fixture.Select("SELECT last_value,is_called FROM pg_temp.heap_checked_tmp", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), true}}))
					}
				})
			})
		}
	}
}
