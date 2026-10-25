package reference_test

import (
	"database/sql/driver"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGRulesTemporary(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			owner, peer := fixture.LockConnections(t, p.Ctx, b.DB)
			run := func(db fixture.Executor, q string) { fixture.RunStep(t, p.Ctx, db, b.Recorder, fixture.Exec(q, 0)) }
			run(peer, "CREATE TABLE pgr_persistent(id INT)")
			run(peer, "CREATE RULE r AS ON INSERT TO pgr_persistent DO NOTHING")
			query := "SELECT * FROM pg_catalog.pg_rules WHERE tablename=$1 ORDER BY schemaname,rulename"
			stmt, err := peer.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: peer, Statement: stmt, SQL: query}
			checkPeer := func(want [][]any) {
				pgRulesCheck(t, p, b, peer, query, want, "pgr_temp")
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.PGTableViewSelect(b, query, pgRulesColumns, pgRulesTypes, want, "pgr_temp"))
				pgRulesCheck(t, p, b, peer, query, [][]any{{"public", "pgr_persistent", "r", "CREATE RULE r AS\n    ON INSERT TO public.pgr_persistent DO NOTHING;"}}, "pgr_persistent")
			}
			checkPeer(nil)
			if b.PG {
				fixture.RunStep(t, p.Ctx, owner, b.Recorder, fixture.Select("SELECT pg_advisory_lock(31842001)", []string{"pg_advisory_lock"}, []string{"VOID"}, [][]any{{""}}))
			}
			for phase := range 2 {
				run(owner, "CREATE TEMP TABLE pgr_temp(id INT)")
				run(owner, "CREATE RULE r AS ON INSERT TO pgr_temp DO INSTEAD NOTHING")
				var schema string
				if err := peer.QueryRowContext(p.Ctx, "SELECT n.nspname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.relname='pgr_temp' AND c.relpersistence='t'").Scan(&schema); err != nil {
					t.Fatal(err)
				}
				pgRulesCheck(t, p, b, owner, query, [][]any{{schema, "pgr_temp", "r", "CREATE RULE r AS\n    ON INSERT TO pg_temp.pgr_temp DO INSTEAD NOTHING;"}}, "pgr_temp")
				checkPeer([][]any{{schema, "pgr_temp", "r", "CREATE RULE r AS\n    ON INSERT TO " + schema + ".pgr_temp DO INSTEAD NOTHING;"}})
				if phase == 0 {
					run(owner, "DROP TABLE pgr_temp")
					checkPeer(nil)
				}
			}
			// ErrBadConn retires the physical connection rather than returning a lease.
			if err := owner.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
				t.Fatal("physical retirement", err)
			}
			if b.PG {
				fixture.ExtensionTempExitBarrier(t, p, peer, b.Recorder, 31842001)
			}
			checkPeer(nil)
		})
	}
}
