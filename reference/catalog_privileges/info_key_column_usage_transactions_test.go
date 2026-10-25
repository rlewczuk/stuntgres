package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInfoKeyColumnUsageTransactions(t *testing.T) {
	kcuScenario(t, func(c *kcuCase) {
		c.exec("CREATE TABLE kcu_tx(a integer,b integer,CONSTRAINT kcu_tx_old UNIQUE(b))")
		c.run(c.conn, fixture.Exec("INSERT INTO kcu_tx VALUES(1,10),(1,20)", 2))
		peer, err := c.b.DB.Conn(c.p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		held := c.hold(peer, kcuQuery)
		check := func(e fixture.Executor, name string) {
			if name == "" {
				c.check(e, "kcu_tx")
			} else {
				c.check(e, "kcu_tx", c.pub(name, "kcu_tx", "b", 1, nil))
			}
		}
		c.exec("BEGIN", "SAVEPOINT kcu_sp", "ALTER TABLE kcu_tx RENAME CONSTRAINT kcu_tx_old TO kcu_tx_renamed")
		check(c.conn, "kcu_tx_renamed")
		check(peer, "kcu_tx_old")
		check(held, "kcu_tx_old")
		c.exec("ROLLBACK TO kcu_sp")
		check(c.conn, "kcu_tx_old")
		c.exec("ALTER TABLE kcu_tx DROP CONSTRAINT kcu_tx_old")
		check(c.conn, "")
		check(peer, "kcu_tx_old")
		c.exec("ROLLBACK")
		check(c.conn, "kcu_tx_old")
		check(held, "kcu_tx_old")
		c.exec("BEGIN", "ALTER TABLE kcu_tx RENAME CONSTRAINT kcu_tx_old TO kcu_tx_committed")
		check(peer, "kcu_tx_old")
		c.exec("COMMIT")
		check(peer, "kcu_tx_committed")
		check(held, "kcu_tx_committed")
		failure := fixture.ControlsReferenceDiagnostic("23505", `could not create unique index "kcu_tx_new"`)
		failure.Detail = "Key (a)=(1) is duplicated."
		failure.Schema = "public"
		failure.Table = "kcu_tx"
		failure.Constraint = "kcu_tx_new"
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("ALTER TABLE kcu_tx DROP CONSTRAINT kcu_tx_committed, ADD CONSTRAINT kcu_tx_new UNIQUE(a)", failure)
			s.ErrorStage = "execute"
			if prepared {
				s = fixture.Prepared(s)
			}
			c.run(c.conn, s)
			check(c.conn, "kcu_tx_committed")
			check(held, "kcu_tx_committed")
			c.run(c.conn, fixture.Select("SELECT a,b FROM kcu_tx ORDER BY b", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(1), int64(20)}}))
			c.run(c.conn, fixture.Select("SELECT relname FROM pg_class WHERE relname='kcu_tx_new'", []string{"relname"}, []string{"NAME"}, nil))
			c.run(c.conn, fixture.Select("SELECT conname FROM pg_constraint WHERE conname='kcu_tx_new' AND conrelid='kcu_tx'::regclass::oid", []string{"conname"}, []string{"NAME"}, nil))
		}
	})
}
