package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalViewsSavepoints(t *testing.T) {
	transactionalViewsRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c, peer *sql.Conn, mode string, held bool) {
		if mode == "autocommit" {
			return
		} // Savepoints are transaction-only; lifecycle autocommit has its own positive matrix.
		e, finish := transactionalViewsBegin(t, p, b, c, mode)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, e, b.Recorder, s) }
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE SCHEMA txv_dest", 0),
			fixture.Exec("CREATE TABLE txv_source(id INT,txt TEXT)", 0),
			fixture.Exec("INSERT INTO txv_source VALUES(1,'a'),(1,'a'),(2,NULL)", 3),
			fixture.Exec("CREATE VIEW txv_v AS SELECT id,txt FROM txv_source", 0),
			fixture.Exec("CREATE MATERIALIZED VIEW txv_m AS SELECT id,txt FROM txv_source", 3),
		} {
			transactionalViewsExec(t, p, b, e, held, s)
		}
		before := transactionalViewsIdentity(t, p, e, "txv_v")
		m := transactionalViewsIdentity(t, p, e, "txv_m")
		run(fixture.Exec("SAVEPOINT stable", 0))
		run(fixture.Exec("INSERT INTO txv_source VALUES(3,'c')", 1))
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE OR REPLACE VIEW txv_v AS SELECT id+10 AS id,txt FROM txv_source", 0),
			fixture.Exec("ALTER VIEW txv_v SET(security_barrier=true)", 0),
			fixture.Exec("ALTER VIEW txv_v RENAME TO txv_changed", 0),
			fixture.Exec("ALTER VIEW txv_changed SET SCHEMA txv_dest", 0),
			fixture.Exec("CREATE MATERIALIZED VIEW txv_late AS SELECT id,txt FROM txv_source", 4),
			fixture.Exec("DROP MATERIALIZED VIEW txv_m", 0),
			fixture.Exec("CREATE MATERIALIZED VIEW txv_m AS SELECT 99 AS id", 1),
		} {
			transactionalViewsExec(t, p, b, e, held, s)
		}
		for range 2 {
			run(fixture.Exec("ROLLBACK TO stable", 0))
			run(transactionalViewsBag("txv_source", false))
			run(transactionalViewsBag("txv_v", false))
			run(transactionalViewsBag("txv_m", false))
			fixture.QueryTableCreateAbsent(t, run, "txv_late")
			if got := transactionalViewsIdentity(t, p, e, "txv_v"); !reflect.DeepEqual(got, before) {
				t.Fatal(before, got)
			}
			if got := transactionalViewsIdentity(t, p, e, "txv_m"); !reflect.DeepEqual(got, m) {
				t.Fatal(m, got)
			}
		}
		transactionalViewsExec(t, p, b, e, held, fixture.Exec("DROP VIEW txv_v", 0))
		transactionalViewsExec(t, p, b, e, held, fixture.Exec("CREATE VIEW txv_v AS SELECT 77 AS id", 0))
		if got := transactionalViewsIdentity(t, p, e, "txv_v"); reflect.DeepEqual(got, before) {
			t.Fatal("same-name replacement retained deleted lifetime")
		}
		run(fixture.Exec("ROLLBACK TO stable", 0))
		if got := transactionalViewsIdentity(t, p, e, "txv_v"); !reflect.DeepEqual(got, before) {
			t.Fatal(before, got)
		}
		run(fixture.Exec("SAVEPOINT released", 0))
		transactionalViewsExec(t, p, b, e, held, fixture.Exec("CREATE MATERIALIZED VIEW txv_released AS SELECT id,txt FROM txv_source WITH NO DATA", 0))
		run(fixture.Exec("RELEASE released", 0))
		fixture.MaterializedViewPopulation(run, "txv_released", false)
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, transactionalViewsCount("txv_released", 0))
		finish(false)
		base := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, name := range []string{"txv_source", "txv_v", "txv_m", "txv_released"} {
			fixture.QueryTableCreateAbsent(t, base, name)
		}
		base(fixture.Exec("CREATE MATERIALIZED VIEW txv_released AS SELECT 8 AS id", 1))
		base(fixture.Select("SELECT id FROM txv_released", []string{"id"}, []string{"INT4"}, [][]any{{int64(8)}}))
	})
}
