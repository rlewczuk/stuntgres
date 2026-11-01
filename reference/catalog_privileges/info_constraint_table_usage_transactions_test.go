package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoConstraintTableUsageTransactions(t *testing.T) {
	ctuScenario(t, func(c *ctuCase) {
		c.exec("CREATE TABLE ctu_tx(a integer,b integer,CONSTRAINT ctu_tx_old UNIQUE(b))")
		c.run(c.conn, fixture.Exec("INSERT INTO ctu_tx VALUES(1,10),(1,20)", 2))
		peer, err := c.b.DB.Conn(c.p.Ctx)
		if err != nil {
			c.t.Fatal(err)
		}
		defer peer.Close()
		held := c.hold(peer, ctuQuery)
		check := func(e fixture.Executor, names ...string) {
			var want [][]any
			for _, name := range names {
				want = append(want, c.pub("ctu_tx", name))
			}
			c.check(e, "ctu_tx", want...)
		}
		c.exec("BEGIN", "SAVEPOINT ctu_sp", "ALTER TABLE ctu_tx RENAME CONSTRAINT ctu_tx_old TO ctu_tx_renamed")
		check(c.conn, "ctu_tx_renamed")
		check(peer, "ctu_tx_old")
		check(held, "ctu_tx_old")
		c.exec("ROLLBACK TO ctu_sp")
		check(c.conn, "ctu_tx_old")
		c.exec("ALTER TABLE ctu_tx DROP CONSTRAINT ctu_tx_old")
		check(c.conn)
		check(peer, "ctu_tx_old")
		check(held, "ctu_tx_old")
		c.exec("ALTER TABLE ctu_tx ADD CONSTRAINT ctu_tx_private UNIQUE(b)")
		check(c.conn, "ctu_tx_private")
		check(peer, "ctu_tx_old")
		check(held, "ctu_tx_old")
		c.exec("ROLLBACK")
		check(c.conn, "ctu_tx_old")
		check(peer, "ctu_tx_old")
		check(held, "ctu_tx_old")
		c.exec("BEGIN", "ALTER TABLE ctu_tx RENAME CONSTRAINT ctu_tx_old TO ctu_tx_committed")
		check(peer, "ctu_tx_old")
		check(held, "ctu_tx_old")
		c.exec("COMMIT")
		check(c.conn, "ctu_tx_committed")
		check(peer, "ctu_tx_committed")
		check(held, "ctu_tx_committed")
		failure := fixture.ControlsReferenceDiagnostic("23505", `could not create unique index "ctu_tx_new"`)
		failure.Detail, failure.Schema, failure.Table, failure.Constraint = "Key (a)=(1) is duplicated.", "public", "ctu_tx", "ctu_tx_new"
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("ALTER TABLE ctu_tx DROP CONSTRAINT ctu_tx_committed, ADD CONSTRAINT ctu_tx_new UNIQUE(a)", failure)
			s.ErrorStage = "execute"
			if prepared {
				s = fixture.Prepared(s)
			}
			c.run(c.conn, s)
			check(c.conn, "ctu_tx_committed")
			check(peer, "ctu_tx_committed")
			check(held, "ctu_tx_committed")
			c.run(c.conn, fixture.Select("SELECT a,b FROM ctu_tx ORDER BY b", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(1), int64(20)}}))
			c.run(c.conn, fixture.Select("SELECT relname FROM pg_class WHERE relname='ctu_tx_new'", []string{"relname"}, []string{"NAME"}, nil))
			c.run(c.conn, fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='ctu_tx'::regclass::oid AND contype='u' ORDER BY conname", []string{"conname"}, []string{"NAME"}, [][]any{{"ctu_tx_committed"}}))
		}
	})
}
