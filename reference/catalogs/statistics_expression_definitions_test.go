package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceStatisticsExpressionDefinitions(t *testing.T) {
	withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
		expressionPopulation(s)
		for _, q := range []string{
			"CREATE STATISTICS expr_single ON (a+1) FROM expr_t",
			"CREATE STATISTICS expr_joint(ndistinct) ON (a+1),b FROM expr_t",
			"CREATE STATISTICS expr_default ON b,(a+1) FROM expr_t",
			"CREATE STATISTICS expr_pair(ndistinct) ON (a+1),(b+2) FROM expr_t",
			"CREATE STATISTICS expr_volatile ON (random()) FROM expr_t",
			"CREATE STATISTICS expr_eight ON (a+1),(a+2),(a+3),(a+4),(a+5),(a+6),(a+7),(a+8) FROM expr_t",
		} {
			s.run(fixture.Exec(q, 0))
		}
		s.run(expressionDefinition("expr_single", "", "{e}"))
		s.run(expressionDefinition("expr_joint", "2", "{d,e}"))
		s.run(expressionDefinition("expr_default", "2", "{d,f,m,e}"))
		s.run(expressionDefinition("expr_pair", "", "{d,e}"))
		s.run(expressionDefinition("expr_volatile", "", "{e}"))
		s.run(expressionDefinition("expr_eight", "", "{d,f,m,e}"))
		s.run(expressionMissingData())
		for _, q := range []string{"CREATE STATISTICS ON (a+1),b FROM expr_t", "CREATE STATISTICS ON (a+1),b FROM expr_t", "CREATE STATISTICS ON b,(a+1) FROM expr_t"} {
			s.run(fixture.Exec(q, 0))
		}
		s.run(fixture.Select("SELECT stxname FROM pg_statistic_ext WHERE stxname LIKE 'expr_t_%' ORDER BY stxname", []string{"stxname"}, []string{"NAME"}, [][]any{{"expr_t_b_expr_stat"}, {"expr_t_expr_b_stat"}, {"expr_t_expr_b_stat1"}}))
		s.run(fixture.Exec("ANALYZE expr_t", 0))
		s.run(expressionSkew("expr_single", 1, "{2,3}"))
		oid := fixture.DeparseDefinitionID(t, s.conn, "SELECT oid FROM pg_statistic_ext WHERE stxname='expr_single'")
		// A valid conditional proposal leaves the retained definition untouched.
		s.run(fixture.Exec("CREATE STATISTICS IF NOT EXISTS expr_single ON (a+2) FROM expr_t", 0))
		if got := fixture.DeparseDefinitionID(t, s.conn, "SELECT oid FROM pg_statistic_ext WHERE stxname='expr_single'"); got != oid {
			t.Fatal("conditional create changed identity", oid, got)
		}
		s.run(expressionSkew("expr_single", 1, "{2,3}"))
	})
}
