package reference_test

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"
)

func sqlPreparedOwnership(t *testing.T, p *referencePair, b referenceBackend, a *sql.Conn) {
	peer, err := b.db.Conn(p.ctx)
	if err != nil { t.Fatal(err) }
	defer peer.Close()
	defer peer.ExecContext(p.ctx, "DEALLOCATE ALL")
	sqlPreparedStep(t, p, b, a, referenceExec("PREPARE p AS SELECT 11", 0))
	sqlPreparedStep(t, p, b, peer, referenceExec("PREPARE p AS SELECT 22", 0))
	sqlPreparedStep(t, p, b, a, sqlPreparedScalar("EXECUTE p", "?column?", "INT4", int64(11)))
	sqlPreparedStep(t, p, b, peer, sqlPreparedScalar("EXECUTE p", "?column?", "INT4", int64(22)))
	sqlPreparedStep(t, p, b, a, referenceErrorDiagnostic("PREPARE p AS SELECT 99", sqlPreparedDiagnostic("42P05", `prepared statement "p" already exists`)))
	for _, command := range []string{"DEALLOCATE p", "DEALLOCATE PREPARE p", "DEALLOCATE ALL", "DEALLOCATE PREPARE ALL"} {
		sqlPreparedStep(t, p, b, a, referenceExec(command, 0))
		sqlPreparedStep(t, p, b, a, referenceErrorDiagnostic("EXECUTE p", sqlPreparedMissing("p")))
		sqlPreparedStep(t, p, b, peer, sqlPreparedScalar("EXECUTE p", "?column?", "INT4", int64(22)))
		sqlPreparedStep(t, p, b, a, referenceExec("PREPARE p AS SELECT 33", 0))
		sqlPreparedStep(t, p, b, a, sqlPreparedScalar("EXECUTE p", "?column?", "INT4", int64(33)))
	}
	sqlPreparedStep(t, p, b, a, referenceExec("DEALLOCATE ALL", 0))
	sqlPreparedStep(t, p, b, a, referenceExec("DEALLOCATE PREPARE ALL", 0))
}

func TestReferenceSQLPreparedOwnership(t *testing.T) { sqlPreparedScenario(t, sqlPreparedOwnership) }

func TestReferenceSQLPreparedTransactionPersistence(t *testing.T) {
	for _, savepoint := range []bool{false,true} {
		t.Run(map[bool]string{false:"rollback",true:"savepoint"}[savepoint], func(t *testing.T) {
			sqlPreparedScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
				for _, action := range []string{"prepare","deallocate"} {
					sqlPreparedStep(t, p, b, c, referenceExec("BEGIN", 0))
					if savepoint { sqlPreparedStep(t, p, b, c, referenceExec("SAVEPOINT s", 0)) }
					q := "PREPARE p AS SELECT 7"
					if action == "deallocate" { q = "DEALLOCATE p" }
					sqlPreparedStep(t, p, b, c, referenceExec(q, 0))
					if savepoint { sqlPreparedStep(t, p, b, c, referenceExec("ROLLBACK TO s", 0)) }
					sqlPreparedStep(t, p, b, c, referenceExec("ROLLBACK", 0))
					if action == "prepare" {
						sqlPreparedStep(t, p, b, c, sqlPreparedScalar("EXECUTE p", "?column?", "INT4", int64(7)))
					} else {
						sqlPreparedStep(t, p, b, c, referenceErrorDiagnostic("EXECUTE p", sqlPreparedMissing("p")))
					}
				}
			})
		})
	}
}

func TestReferenceSQLPreparedNames(t *testing.T) {
	sqlPreparedScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		sqlPreparedStep(t, p, b, c, referenceExec("PREPARE Mixed AS SELECT 7", 0))
		sqlPreparedStep(t, p, b, c, referenceExec("PREPARE \"Mixed\" AS SELECT 8", 0))
		sqlPreparedStep(t, p, b, c, sqlPreparedScalar("EXECUTE MIXED", "?column?", "INT4", int64(7)))
		sqlPreparedStep(t, p, b, c, sqlPreparedScalar("EXECUTE \"Mixed\"", "?column?", "INT4", int64(8)))
		sqlPreparedStep(t, p, b, c, referenceExec("DEALLOCATE mixed", 0))
		sqlPreparedStep(t, p, b, c, sqlPreparedScalar("EXECUTE \"Mixed\"", "?column?", "INT4", int64(8)))
	})
}

func TestReferenceSQLPreparedPhysicalLifetime(t *testing.T) {
	sqlPreparedScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		sqlPreparedStep(t, p, b, c, referenceExec("CREATE TABLE prep_sentinel(v int)", 0))
		sqlPreparedStep(t, p, b, c, referenceExec("INSERT INTO prep_sentinel VALUES(7)", 1))
		peer, err := b.db.Conn(p.ctx)
		if err != nil { t.Fatal(err) }
		defer peer.Close()
		defer peer.ExecContext(p.ctx, "DEALLOCATE ALL")
		sqlPreparedStep(t, p, b, peer, referenceExec("PREPARE p AS SELECT 22", 0))
		sqlPreparedStep(t, p, b, c, referenceExec("PREPARE p AS SELECT 11", 0))
		b.db.SetMaxOpenConns(2)
		b.db.SetMaxIdleConns(1)
		identity := settingsPhysical(t, c)
		if err := c.Close(); err != nil { t.Fatal(err) }
		reused, err := b.db.Conn(p.ctx)
		if err != nil { t.Fatal(err) }
		if settingsPhysical(t, reused) != identity { t.Fatal("borrower return replaced physical session") }
		sqlPreparedStep(t, p, b, reused, sqlPreparedScalar("EXECUTE p", "?column?", "INT4", int64(11)))
		if err := reused.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) { t.Fatal(err) }
		reused.Close()
		fresh, err := b.db.Conn(p.ctx)
		if err != nil { t.Fatal(err) }
		defer fresh.Close()
		if settingsPhysical(t, fresh) == identity { t.Fatal("invalid physical session reused") }
		sqlPreparedStep(t, p, b, fresh, referenceErrorDiagnostic("EXECUTE p", sqlPreparedMissing("p")))
		sqlPreparedStep(t, p, b, fresh, sqlPreparedScalar("SELECT v FROM prep_sentinel", "v", "INT4", int64(7)))
		sqlPreparedStep(t, p, b, peer, sqlPreparedScalar("EXECUTE p", "?column?", "INT4", int64(22)))
	})
}

func TestReferenceSQLPreparedReadOnlyAndAbort(t *testing.T) {
	sqlPreparedScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		sqlPreparedStep(t, p, b, c, referenceExec("CREATE TABLE prep_ro(v int)", 0))
		sqlPreparedStep(t, p, b, c, referenceExec("BEGIN READ ONLY", 0))
		sqlPreparedStep(t, p, b, c, referenceExec("PREPARE p(int) AS INSERT INTO prep_ro VALUES($1)", 0))
		sqlPreparedStep(t, p, b, c, referenceErrorDiagnostic("EXECUTE p(7)", sqlPreparedDiagnostic("25006", "cannot execute INSERT in a read-only transaction")))
		sqlPreparedStep(t, p, b, c, referenceErrorDiagnostic("SELECT 1", sqlPreparedDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
		sqlPreparedStep(t, p, b, c, referenceExec("ROLLBACK", 0))
		sqlPreparedStep(t, p, b, c, referenceExec("EXECUTE p(7)", 1))
		sqlPreparedStep(t, p, b, c, sqlPreparedScalar("SELECT v FROM prep_ro", "v", "INT4", int64(7)))
	})
}
