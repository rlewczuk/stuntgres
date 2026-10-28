package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAlterClusterSmoke(t *testing.T) {
	persistenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		clusterSetup(t, p, b, c)
		catalogQuery := clusterBits(false, false).Query
		catalog, err := c.PrepareContext(p.Ctx, catalogQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer catalog.Close()
		data, err := c.PrepareContext(p.Ctx, clusterRows().Query)
		if err != nil {
			t.Fatal(err)
		}
		defer data.Close()
		alter, err := c.PrepareContext(p.Ctx, "ALTER TABLE t CLUSTER ON b")
		if err != nil {
			t.Fatal(err)
		}
		defer alter.Close()
		persistenceExec(t, p, b, c, "ALTER TABLE t CLUSTER ON a")
		persistenceRun(t, p, b, fixture.HeldStatement{Executor: c, Statement: catalog, SQL: catalogQuery}, clusterBits(true, false))
		persistenceRun(t, p, b, fixture.HeldStatement{Executor: c, Statement: alter, SQL: "ALTER TABLE t CLUSTER ON b"}, fixture.Exec("ALTER TABLE t CLUSTER ON b", 0))
		persistenceRun(t, p, b, fixture.HeldStatement{Executor: c, Statement: catalog, SQL: catalogQuery}, clusterBits(false, true))
		persistenceRun(t, p, b, fixture.HeldStatement{Executor: c, Statement: data, SQL: clusterRows().Query}, clusterRows())
		persistenceFailure(t, p, b, c, "ALTER TABLE t CLUSTER ON a, CLUSTER ON missing_idx", false, persistenceDiagnostic("42704", `index "missing_idx" for table "t" does not exist`))
		clusterCheck(t, p, b, c, false, true)
		persistenceExec(t, p, b, c, "ALTER TABLE t SET WITHOUT OIDS")
		want := persistenceDiagnostic("42601", `syntax error at or near "WITH"`)
		want.Position = fixture.Position(19)
		persistenceRun(t, p, b, c, fixture.ErrorDiagnostic("ALTER TABLE t SET WITH OIDS", want))
		clusterCheck(t, p, b, c, false, true)
		if !b.PG {
			clusterControlRoundtrip(t, p, func(run func(fixture.Step)) { run(clusterBits(false, true)); run(clusterRows()) })
		}
		persistenceExec(t, p, b, c, "ALTER TABLE t SET WITHOUT CLUSTER")
		clusterCheck(t, p, b, c, false, false)
	})
}
