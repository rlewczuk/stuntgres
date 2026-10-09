package reference_test

import (
	"context"
	"database/sql"
	"testing"
)

func TestReferenceRowLockMigratedSmoke(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {

			transactionAffected := queryLockMigratedAffected
			transactionSelectValues := func(ctx context.Context, x referenceExecutor, q string, prepared bool, want ...int64) error {
				return queryLockMigratedTyped(ctx, x, q, prepared, backend.pg, "v", "INT4", want...)
			}
			rowLockSmokeCheck := func(t *testing.T, err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			rowLockSmokeBlocked := func(t *testing.T, x referenceExecutor, q string, prepared bool) {
				queryLockMigratedBlocked(t, p.ctx, x, backend.pg, q, prepared)
			}
			rowLockSmokeFixture := func(t *testing.T) (*sql.DB, *sql.Conn, *sql.Conn) {
				a, b := referenceLockConnections(t, p.ctx, backend.db)
				referenceLockExec(t, p.ctx, a, "CREATE TABLE t(id INT,v INT)", 0)
				referenceLockExec(t, p.ctx, a, "INSERT INTO t VALUES(1,10),(2,20)", 2)
				return backend.db, a, b
			}

			ctx := p.ctx
			_, a, b := rowLockSmokeFixture(t)
			for _, prepared := range []bool{false, true} {
				for _, clause := range []string{"FOR KEY SHARE", "FOR SHARE", "FOR NO KEY UPDATE", "FOR UPDATE"} {
					tx, err := a.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					queryLockRollbackCleanup(t, tx)
					func() {
						defer tx.Rollback()
						rowLockSmokeCheck(t, transactionSelectValues(ctx, tx, "SELECT v FROM t WHERE id=1 "+clause, prepared, 10))
						rowLockSmokeCheck(t, transactionSelectValues(ctx, b, "SELECT v FROM t WHERE id=2 FOR UPDATE", prepared, 20))
						rowLockSmokeCheck(t, transactionSelectValues(ctx, b, "SELECT v FROM t WHERE id=1", prepared, 10))
						if clause == "FOR KEY SHARE" {
							rowLockSmokeCheck(t, transactionAffected(ctx, b, "UPDATE t SET v=10 WHERE id=1", prepared, 1))
						} else {
							rowLockSmokeBlocked(t, b, "UPDATE t SET v=11 WHERE id=1", prepared)
						}
						rowLockSmokeBlocked(t, b, "DELETE FROM t WHERE id=1", prepared)
					}()
					rowLockSmokeCheck(t, transactionAffected(ctx, b, "UPDATE t SET v=10 WHERE id=1", prepared, 1))
				}
			}
			tx, err := a.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			queryLockRollbackCleanup(t, tx)
			defer tx.Rollback()
			stmt, err := tx.PrepareContext(ctx, "SELECT v FROM t WHERE id=1 FOR UPDATE")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			rows, err := stmt.QueryContext(ctx)
			if rows != nil {
				defer rows.Close()
			}
			if err != nil {
				t.Fatal(err)
			}
			rows.Close()
			stmt.Close()
			rowLockSmokeBlocked(t, b, "UPDATE t SET v=11 WHERE id=1", false)
			rowLockSmokeCheck(t, tx.Commit())
			rowLockSmokeCheck(t, transactionAffected(ctx, b, "UPDATE t SET v=11 WHERE id=1", true, 1))

		})
	}
}

func TestReferenceRowLockMigratedJoinSmoke(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {

			transactionAffected := queryLockMigratedAffected
			transactionSelectValues := func(ctx context.Context, x referenceExecutor, q string, prepared bool, want ...int64) error {
				return queryLockMigratedTyped(ctx, x, q, prepared, backend.pg, "v", "INT4", want...)
			}
			rowLockSmokeCheck := func(t *testing.T, err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			rowLockSmokeBlocked := func(t *testing.T, x referenceExecutor, q string, prepared bool) {
				queryLockMigratedBlocked(t, p.ctx, x, backend.pg, q, prepared)
			}
			rowLockSmokeFixture := func(t *testing.T) (*sql.DB, *sql.Conn, *sql.Conn) {
				a, b := referenceLockConnections(t, p.ctx, backend.db)
				referenceLockExec(t, p.ctx, a, "CREATE TABLE t(id INT,v INT)", 0)
				referenceLockExec(t, p.ctx, a, "INSERT INTO t VALUES(1,10),(2,20)", 2)
				return backend.db, a, b
			}

			ctx := p.ctx
			_, a, b := rowLockSmokeFixture(t)
			rowLockSmokeCheck(t, transactionAffected(ctx, a, "CREATE TABLE u(id INT)", false, 0))
			rowLockSmokeCheck(t, transactionAffected(ctx, a, "INSERT INTO u VALUES(1),(3)", false, 2))
			for _, prepared := range []bool{false, true} {
				for _, from := range []string{"t JOIN u ON t.id=u.id", "t JOIN u USING(id)", "t a JOIN t b ON a.id=b.id"} {
					tx, err := a.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					queryLockRollbackCleanup(t, tx)
					func() {
						defer tx.Rollback()
						query := "SELECT t.v FROM " + from + " WHERE t.id=1 FOR SHARE"
						if from == "t a JOIN t b ON a.id=b.id" {
							query = "SELECT a.v FROM " + from + " WHERE a.id=1 FOR SHARE"
						}
						rowLockSmokeCheck(t, transactionSelectValues(ctx, tx, query, prepared, 10))
						rowLockSmokeCheck(t, transactionAffected(ctx, b, "UPDATE t SET v=20 WHERE id=2", prepared, 1))
						rowLockSmokeCheck(t, transactionAffected(ctx, b, "UPDATE u SET id=3 WHERE id=3", prepared, 1))
						rowLockSmokeBlocked(t, b, "UPDATE t SET v=11 WHERE id=1", prepared)
						if from != "t a JOIN t b ON a.id=b.id" {
							rowLockSmokeBlocked(t, b, "DELETE FROM u WHERE id=1", prepared)
						}
					}()
				}
			}

		})
	}
}

func TestReferenceRowLockMigratedVersionSmoke(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {

			transactionAffected := queryLockMigratedAffected
			transactionSelectValues := func(ctx context.Context, x referenceExecutor, q string, prepared bool, want ...int64) error {
				return queryLockMigratedTyped(ctx, x, q, prepared, backend.pg, "v", "INT4", want...)
			}
			rowLockSmokeCheck := func(t *testing.T, err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			rowLockSmokeBlocked := func(t *testing.T, x referenceExecutor, q string, prepared bool) {
				queryLockMigratedBlocked(t, p.ctx, x, backend.pg, q, prepared)
			}
			rowLockSmokeFixture := func(t *testing.T) (*sql.DB, *sql.Conn, *sql.Conn) {
				a, b := referenceLockConnections(t, p.ctx, backend.db)
				referenceLockExec(t, p.ctx, a, "CREATE TABLE t(id INT,v INT)", 0)
				referenceLockExec(t, p.ctx, a, "INSERT INTO t VALUES(1,10),(2,20)", 2)
				return backend.db, a, b
			}

			ctx := p.ctx
			_, a, b := rowLockSmokeFixture(t)
			// Public boundaries and current-head outcomes accompany deterministic native
			// journal and PostgreSQL wait-observer tests; deadlines are not queue proof.
			for _, change := range []string{"UPDATE t SET v=30 WHERE id=1", "DELETE FROM t WHERE id=1"} {
				tx, err := a.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				queryLockRollbackCleanup(t, tx)
				defer tx.Rollback()
				rowLockSmokeCheck(t, transactionAffected(ctx, tx, change, false, 1))
				rowLockSmokeCheck(t, transactionSelectValues(ctx, b, "SELECT v FROM t WHERE id=1", true, 10))
				rowLockSmokeBlocked(t, b, "SELECT v FROM t WHERE id=1 FOR SHARE", false)
				rowLockSmokeCheck(t, tx.Commit())
				if change[0] == 'D' {
					rowLockSmokeCheck(t, transactionSelectValues(ctx, b, "SELECT v FROM t WHERE id=1 FOR SHARE", true))
				} else {
					rowLockSmokeCheck(t, transactionSelectValues(ctx, b, "SELECT v FROM t WHERE id=1 FOR SHARE", true, 30))
					rowLockSmokeCheck(t, transactionSelectValues(ctx, b, "SELECT v FROM t WHERE v=10 FOR UPDATE", false))
					rowLockSmokeCheck(t, transactionAffected(ctx, b, "UPDATE t SET v=10 WHERE id=1", false, 1))
				}
			}
			for _, change := range []string{"UPDATE t SET v=30 WHERE id=1", "DELETE FROM t WHERE id=1"} {
				rowLockSmokeCheck(t, transactionAffected(ctx, a, "INSERT INTO t VALUES(1,10)", false, 1))
				reader, err := b.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
				if err != nil {
					t.Fatal(err)
				}
				queryLockRollbackCleanup(t, reader)
				defer reader.Rollback()
				rowLockSmokeCheck(t, transactionSelectValues(ctx, reader, "SELECT v FROM t WHERE id=1", false, 10))
				rowLockSmokeCheck(t, transactionAffected(ctx, a, change, false, 1))
				if change[0] == 'U' {
					for _, prepared := range []bool{false, true} {
						rowLockSmokeCheck(t, transactionSelectValues(ctx, reader, "SELECT v FROM t WHERE id=1 FOR KEY SHARE", prepared, 10))
						rowLockSmokeCheck(t, transactionSelectValues(ctx, reader, "SELECT v FROM t WHERE v=10 FOR KEY SHARE", prepared, 10))
					}
					rowLockSmokeCheck(t, reader.Rollback())
					rowLockSmokeCheck(t, transactionAffected(ctx, a, "DELETE FROM t WHERE id=1", false, 1))
					continue
				}
				rows, err := reader.QueryContext(ctx, "SELECT v FROM t WHERE id=1 FOR KEY SHARE")
				terminal := queryLockErrorOutcome(t, relationOutcome{rows: rows, err: err}, backend.pg, "v", nil)
				referenceAssertDiagnostic(t, terminal, ptrQueryLockDiagnostic(controlsReferenceDiagnostic("40001", "could not serialize access due to concurrent update")))
				rowLockSmokeCheck(t, reader.Rollback())
				rowLockSmokeCheck(t, transactionAffected(ctx, a, "DELETE FROM t WHERE id=1", false, map[bool]int64{false: 1, true: 0}[change[0] == 'D']))
			}

		})
	}
}

func ptrQueryLockDiagnostic(d referenceDiagnostic) *referenceDiagnostic { return &d }
