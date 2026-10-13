package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryWindowExcludeFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ex_reader"})
	queryWindowExcludeScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		queryWindowExcludeSeed(t, p, b)
		queryWindowExcludeFull(t, p, b)
		queryWindowRoutes(t, p, b, queryWindowExcludeResult(queryWindowExcludeQuery("ROWS CURRENT ROW EXCLUDE GROUP"), []int64{0, 0, 0}, []any{nil, nil, nil}))
		s := fixture.Select("SELECT count(*) OVER w AS n,sum(v) OVER w AS s FROM ex_t WHERE FALSE WINDOW w AS(ROWS CURRENT ROW EXCLUDE GROUP)", []string{"n", "s"}, []string{"INT8", "INT8"}, nil)
		s.CheckMetadata = queryWindowExcludeMetadata(b.PG, false, true)
		queryWindowRoutes(t, p, b, s)
		queryWindowExcludePrepared(t, p, b)
		queryWindowExcludeDiagnostics(t, p, b)
		queryWindowExcludeAuthorization(t, p, b)
		// Authorization denial and held-statement recovery are complete.
		// Native Dump intentionally cannot serialize explicit column ACLs.
		queryWindowRun(t, p, b, fixture.Exec("DROP TABLE ex_secret", 0))
		queryWindowExcludeViews(t, p, b)
	})
}
