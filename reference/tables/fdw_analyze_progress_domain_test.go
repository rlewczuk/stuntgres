package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func fdwAnalyzeProgressDomainFailure(t *testing.T, prepared bool) {
	t.Helper()
	fdwAnalyzeProgressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
		fdwAnalyzeDirectSetup(t, p, b, r, run)
		run(fixture.Exec("DROP FOREIGN TABLE fdw_direct CASCADE", 0))
		run(fixture.Exec("CREATE DOMAIN fdw_direct_nonnegative AS integer CONSTRAINT fdw_direct_nonnegative_check CHECK(VALUE>=0)", 0))
		run(fixture.Exec("CREATE FOREIGN TABLE fdw_direct(a fdw_direct_nonnegative,b integer,tag text) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'direct')", 0))
		run(fixture.Exec("CREATE STATISTICS fdw_direct_joint(ndistinct,dependencies,mcv) ON a,b FROM fdw_direct", 0))
		run(fixture.Exec("CREATE STATISTICS fdw_direct_expression ON (a::integer+1) FROM fdw_direct", 0))
		fdwAnalyzeProgressDirectRun(t, p, b, c, r, prepared, "")
		fdwAnalyzeProgressDirectProducts(run)
		joint := fdwAnalyzeDirectPayload(t, p, c, "fdw_direct_joint")
		expression := fdwAnalyzeDirectPayload(t, p, c, "fdw_direct_expression")
		id := fdwAnalyzeProgressIdentify(t, p, c, "fdw_direct")
		fdwSourceExec(t, p, b, r, run, "UPDATE fdw_source.direct SET a=-1", 1000)
		run(fixture.Exec("SET search_path=pg_catalog", 0))
		want := fdwDiagnostic("23514", `value for domain public.fdw_direct_nonnegative violates check constraint "fdw_direct_nonnegative_check"`)
		want.Schema, want.DataType, want.Constraint = "public", "fdw_direct_nonnegative", "fdw_direct_nonnegative_check"
		if b.PG {
			want.Context = `column "a" of foreign table "fdw_direct"`
		}
		failure := fixture.ErrorDiagnostic("ANALYZE public.fdw_direct", want)
		failure.Prepared, failure.ErrorStage = prepared, "execute"
		run(failure)
		run(fixture.Exec("SET search_path=public", 0))
		fdwAnalyzeProgressAbsent(t, p, b, id)
		fdwAnalyzeRelationPopulation(run, "fdw_direct", 1000)
		if fdwAnalyzeDirectPayload(t, p, c, "fdw_direct_joint") != joint || fdwAnalyzeDirectPayload(t, p, c, "fdw_direct_expression") != expression {
			t.Fatal("domain failure published partial typed products")
		}
		if !b.PG {
			fdwAnalyzeProgressResources(t, r)
		}
		fdwSourceExec(t, p, b, r, run, "UPDATE fdw_source.direct SET a=9", 1000)
		fdwAnalyzeProgressDirectRun(t, p, b, c, r, prepared, "")
		fdwAnalyzeDirectJoint(run, 3)
		fdwAnalyzeDirectExpression(run, 1, "{10}")
	})
}
