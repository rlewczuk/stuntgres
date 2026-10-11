package reference_test

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func sqlPreparedOwnership(t *testing.T, p *fixture.Pair, b fixture.Backend, a *sql.Conn) {
	peer, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	defer peer.ExecContext(p.Ctx, "DEALLOCATE ALL")
	sqlPreparedStep(t, p, b, a, fixture.Exec("PREPARE p AS SELECT 11", 0))
	sqlPreparedStep(t, p, b, peer, fixture.Exec("PREPARE p AS SELECT 22", 0))
	sqlPreparedStep(t, p, b, a, sqlPreparedScalar("EXECUTE p", "?column?", "INT4", int64(11)))
	sqlPreparedStep(t, p, b, peer, sqlPreparedScalar("EXECUTE p", "?column?", "INT4", int64(22)))
	sqlPreparedStep(t, p, b, a, fixture.ErrorDiagnostic("PREPARE p AS SELECT 99", sqlPreparedDiagnostic("42P05", `prepared statement "p" already exists`)))
	for _, command := range []string{"DEALLOCATE p", "DEALLOCATE PREPARE p", "DEALLOCATE ALL", "DEALLOCATE PREPARE ALL"} {
		sqlPreparedStep(t, p, b, a, fixture.Exec(command, 0))
		sqlPreparedStep(t, p, b, a, fixture.ErrorDiagnostic("EXECUTE p", sqlPreparedMissing("p")))
		sqlPreparedStep(t, p, b, peer, sqlPreparedScalar("EXECUTE p", "?column?", "INT4", int64(22)))
		sqlPreparedStep(t, p, b, a, fixture.Exec("PREPARE p AS SELECT 33", 0))
		sqlPreparedStep(t, p, b, a, sqlPreparedScalar("EXECUTE p", "?column?", "INT4", int64(33)))
	}
	sqlPreparedStep(t, p, b, a, fixture.Exec("DEALLOCATE ALL", 0))
	sqlPreparedStep(t, p, b, a, fixture.Exec("DEALLOCATE PREPARE ALL", 0))
}

func TestReferenceSQLPreparedOwnership(t *testing.T) { sqlPreparedScenario(t, sqlPreparedOwnership) }

func TestReferenceSQLPreparedTransactionPersistence(t *testing.T) {
	for _, savepoint := range []bool{false, true} {
		t.Run(map[bool]string{false: "rollback", true: "savepoint"}[savepoint], func(t *testing.T) {
			sqlPreparedScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				for _, action := range []string{"prepare", "deallocate"} {
					sqlPreparedStep(t, p, b, c, fixture.Exec("BEGIN", 0))
					if savepoint {
						sqlPreparedStep(t, p, b, c, fixture.Exec("SAVEPOINT s", 0))
					}
					q := "PREPARE p AS SELECT 7"
					if action == "deallocate" {
						q = "DEALLOCATE p"
					}
					sqlPreparedStep(t, p, b, c, fixture.Exec(q, 0))
					if savepoint {
						sqlPreparedStep(t, p, b, c, fixture.Exec("ROLLBACK TO s", 0))
					}
					sqlPreparedStep(t, p, b, c, fixture.Exec("ROLLBACK", 0))
					if action == "prepare" {
						sqlPreparedStep(t, p, b, c, sqlPreparedScalar("EXECUTE p", "?column?", "INT4", int64(7)))
					} else {
						sqlPreparedStep(t, p, b, c, fixture.ErrorDiagnostic("EXECUTE p", sqlPreparedMissing("p")))
					}
				}
			})
		})
	}
}

func TestReferenceSQLPreparedNames(t *testing.T) {
	sqlPreparedScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		sqlPreparedStep(t, p, b, c, fixture.Exec("PREPARE Mixed AS SELECT 7", 0))
		sqlPreparedStep(t, p, b, c, fixture.Exec("PREPARE \"Mixed\" AS SELECT 8", 0))
		sqlPreparedStep(t, p, b, c, sqlPreparedScalar("EXECUTE MIXED", "?column?", "INT4", int64(7)))
		sqlPreparedStep(t, p, b, c, sqlPreparedScalar("EXECUTE \"Mixed\"", "?column?", "INT4", int64(8)))
		sqlPreparedStep(t, p, b, c, fixture.Exec("DEALLOCATE mixed", 0))
		sqlPreparedStep(t, p, b, c, sqlPreparedScalar("EXECUTE \"Mixed\"", "?column?", "INT4", int64(8)))
	})
}

func TestReferenceSQLPreparedPhysicalLifetime(t *testing.T) {
	sqlPreparedScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		sqlPreparedStep(t, p, b, c, fixture.Exec("CREATE TABLE prep_sentinel(v int)", 0))
		sqlPreparedStep(t, p, b, c, fixture.Exec("INSERT INTO prep_sentinel VALUES(7)", 1))
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		defer peer.ExecContext(p.Ctx, "DEALLOCATE ALL")
		sqlPreparedStep(t, p, b, peer, fixture.Exec("PREPARE p AS SELECT 22", 0))
		sqlPreparedStep(t, p, b, c, fixture.Exec("PREPARE p AS SELECT 11", 0))
		b.DB.SetMaxOpenConns(2)
		b.DB.SetMaxIdleConns(1)
		identity := settingsPhysical(t, c)
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
		reused, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		if settingsPhysical(t, reused) != identity {
			t.Fatal("borrower return replaced physical session")
		}
		sqlPreparedStep(t, p, b, reused, sqlPreparedScalar("EXECUTE p", "?column?", "INT4", int64(11)))
		if err := reused.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
			t.Fatal(err)
		}
		reused.Close()
		fresh, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer fresh.Close()
		if settingsPhysical(t, fresh) == identity {
			t.Fatal("invalid physical session reused")
		}
		sqlPreparedStep(t, p, b, fresh, fixture.ErrorDiagnostic("EXECUTE p", sqlPreparedMissing("p")))
		sqlPreparedStep(t, p, b, fresh, sqlPreparedScalar("SELECT v FROM prep_sentinel", "v", "INT4", int64(7)))
		sqlPreparedStep(t, p, b, peer, sqlPreparedScalar("EXECUTE p", "?column?", "INT4", int64(22)))
	})
}

func TestReferenceSQLPreparedReadOnlyAndAbort(t *testing.T) {
	sqlPreparedScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		sqlPreparedStep(t, p, b, c, fixture.Exec("CREATE TABLE prep_ro(v int)", 0))
		sqlPreparedStep(t, p, b, c, fixture.Exec("BEGIN READ ONLY", 0))
		sqlPreparedStep(t, p, b, c, fixture.Exec("PREPARE p(int) AS INSERT INTO prep_ro VALUES($1)", 0))
		sqlPreparedStep(t, p, b, c, fixture.ErrorDiagnostic("EXECUTE p(7)", sqlPreparedDiagnostic("25006", "cannot execute INSERT in a read-only transaction")))
		sqlPreparedStep(t, p, b, c, fixture.ErrorDiagnostic("SELECT 1", sqlPreparedDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
		sqlPreparedStep(t, p, b, c, fixture.Exec("ROLLBACK", 0))
		sqlPreparedStep(t, p, b, c, fixture.Exec("EXECUTE p(7)", 1))
		sqlPreparedStep(t, p, b, c, sqlPreparedScalar("SELECT v FROM prep_ro", "v", "INT4", int64(7)))
	})
}
