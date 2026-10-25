package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoConstraintColumnUsageTransactions(t *testing.T) {
	ccuScenario(t, func(c *ccuCase) {
		c.exec("CREATE TABLE ccu_tx(a integer,b integer,CONSTRAINT ccu_tx_old UNIQUE(b))")
		c.run(c.conn, fixture.Exec("INSERT INTO ccu_tx VALUES(1,10),(1,20)", 2))
		peer, err := c.b.DB.Conn(c.p.Ctx)
		if err != nil {
			c.t.Fatal(err)
		}
		defer peer.Close()
		held := c.hold(peer, ccuQuery)
		check := func(e fixture.Executor, name string) {
			if name == "" {
				c.check(e, "ccu_tx")
			} else {
				c.check(e, "ccu_tx", c.pub("ccu_tx", "b", name))
			}
		}
		c.exec("BEGIN", "SAVEPOINT ccu_sp", "ALTER TABLE ccu_tx RENAME CONSTRAINT ccu_tx_old TO ccu_tx_renamed")
		check(c.conn, "ccu_tx_renamed")
		check(peer, "ccu_tx_old")
		check(held, "ccu_tx_old")
		c.exec("ROLLBACK TO ccu_sp")
		check(c.conn, "ccu_tx_old")
		c.exec("ALTER TABLE ccu_tx DROP CONSTRAINT ccu_tx_old")
		check(c.conn, "")
		check(peer, "ccu_tx_old")
		check(held, "ccu_tx_old")
		c.exec("ROLLBACK")
		check(c.conn, "ccu_tx_old")
		check(held, "ccu_tx_old")
		c.exec("BEGIN", "ALTER TABLE ccu_tx RENAME CONSTRAINT ccu_tx_old TO ccu_tx_committed")
		check(peer, "ccu_tx_old")
		c.exec("COMMIT")
		check(peer, "ccu_tx_committed")
		check(held, "ccu_tx_committed")
		failure := fixture.ControlsReferenceDiagnostic("23505", `could not create unique index "ccu_tx_new"`)
		failure.Detail = "Key (a)=(1) is duplicated."
		failure.Schema = "public"
		failure.Table = "ccu_tx"
		failure.Constraint = "ccu_tx_new"
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("ALTER TABLE ccu_tx DROP CONSTRAINT ccu_tx_committed, ADD CONSTRAINT ccu_tx_new UNIQUE(a)", failure)
			s.ErrorStage = "execute"
			if prepared {
				s = fixture.Prepared(s)
			}
			c.run(c.conn, s)
			check(c.conn, "ccu_tx_committed")
			check(held, "ccu_tx_committed")
			c.run(c.conn, fixture.Select("SELECT a,b FROM ccu_tx ORDER BY b", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(1), int64(20)}}))
			c.run(c.conn, fixture.Select("SELECT relname FROM pg_class WHERE relname='ccu_tx_new'", []string{"relname"}, []string{"NAME"}, nil))
			c.run(c.conn, fixture.Select("SELECT conname FROM pg_constraint WHERE conname='ccu_tx_new' AND conrelid='ccu_tx'::regclass::oid", []string{"conname"}, []string{"NAME"}, nil))
		}
	})
}
