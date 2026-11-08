package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryLateralFeatureSmoke(t *testing.T) {
	subqueryReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := subqueryRun(t, p, b, c)
		lateralRepro(run)
		lateralFixture(run)
		subqueryBoth(run, lateralLatestStep())
		subqueryBoth(run, lateralChainStep())
		stmt, err := c.PrepareContext(p.Ctx, lateralHeldSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: lateralHeldSQL}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(lateralHeldSQL, []string{"id", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(103)}}, int64(100), int64(2)))
		const later = "SELECT * FROM LATERAL (SELECT u.w) s, lat_u u"
		subqueryAnalysisFailure(run, later, lateralDiagnostic(later, "u.w", "42P01", `missing FROM-clause entry for table "u"`, "", ""))
		run(fixture.Exec("CREATE VIEW lat_v3 AS SELECT t.id,s.n FROM lat_t t,LATERAL (SELECT 42 AS n) s", 0))
		subqueryBoth(run, fixture.Select("SELECT pg_get_viewdef('lat_v3'::regclass,true)", []string{"pg_get_viewdef"}, []string{"TEXT"}, [][]any{{lateralUncorrelatedDefinition}}))
		subqueryBoth(run, fixture.Select("SELECT * FROM lat_v3 ORDER BY id", []string{"id", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(42)}, {int64(2), int64(42)}, {int64(3), int64(42)}}))
	})
}
