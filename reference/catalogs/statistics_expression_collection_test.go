package reference_test

import (
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceStatisticsExpressionCollection(t *testing.T) {
	withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
		expressionPopulation(s)
		for _, q := range []string{
			"CREATE STATISTICS expr_single ON (a+1) FROM expr_t",
			"CREATE STATISTICS expr_joint(ndistinct) ON (a+1),b FROM expr_t",
			"CREATE STATISTICS expr_pair(ndistinct) ON (a+1),(b+2) FROM expr_t",
			"CREATE STATISTICS expr_text ON (a::text) FROM expr_t",
			"CREATE STATISTICS expr_default ON (a+1),b FROM expr_t",
		} {
			s.run(fixture.Exec(q, 0))
		}
		s.run(expressionMissingData())
		s.run(fixture.Exec("ANALYZE expr_t", 0))
		for _, name := range []string{"expr_single", "expr_joint", "expr_pair", "expr_default"} {
			s.run(expressionSkew(name, 1, "{2,3}"))
		}
		s.run(expressionSkew("expr_pair", 2, "{12,22}"))
		s.run(expressionSkew("expr_text", 1, "{1,2}"))
		s.run(expressionJoint(s.pg, "expr_joint", `{"2, -1": 3}`, 1))
		s.run(expressionJoint(s.pg, "expr_pair", `{"-1, -2": 3}`, 2))
		s.run(fixture.Select("SELECT stxdndistinct IS NULL AS no_joint,stxddependencies IS NULL AS no_dependencies,stxdmcv IS NULL AS no_joint_mcv"+expressionDataFrom, []string{"no_joint", "no_dependencies", "no_joint_mcv"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}, "expr_single"))
		expressionRawDatum(t, s, "expr_single")
		// Mutations retain old facts until the ordinary ANALYZE publication.
		s.run(fixture.Exec("UPDATE expr_t SET a=9,b=90", 20))
		s.run(expressionSkew("expr_single", 1, "{2,3}"))
		// Only simple statistics keys gate selected ANALYZE eligibility.
		// Expressions read a from full rows even though only b is selected.
		s.run(fixture.Exec("ANALYZE expr_t(b)", 0))
		s.run(expressionUniform("expr_single", "{10}"))
		s.run(expressionJoint(s.pg, "expr_joint", `{"2, -1": 1}`, 1))
		s.run(expressionUniform("expr_pair", "{10}"))
		s.run(expressionJoint(s.pg, "expr_pair", `{"-1, -2": 1}`, 2))
		s.run(expressionUniform("expr_default", "{10}"))
	})
}

func expressionUniform(name, values string) fixture.Step {
	return fixture.Select("SELECT (stxdexpr[1]).stanullfrac,(stxdexpr[1]).stadistinct,(stxdexpr[1]).stakind1,(stxdexpr[1]).stavalues1::text AS values,(stxdexpr[1]).stanumbers1::text AS frequencies"+expressionDataFrom, []string{"stanullfrac", "stadistinct", "stakind1", "values", "frequencies"}, []string{"FLOAT4", "FLOAT4", "INT2", "TEXT", "TEXT"}, [][]any{{float64(0), float64(1), int64(1), values, "{1}"}}, name)
}

func TestReferenceStatisticsExpressionNullAndHistogram(t *testing.T) {
	withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
		for _, q := range []string{"CREATE TABLE expr_t(a INT)", "INSERT INTO expr_t VALUES(NULL),(NULL),(NULL),(NULL),(NULL)", "CREATE STATISTICS expr_null ON (a+1) FROM expr_t", "CREATE STATISTICS expr_coalesce ON (coalesce(a,0)) FROM expr_t"} {
			count := int64(0)
			if strings.HasPrefix(q, "INSERT") {
				count = 5
			}
			s.run(fixture.Exec(q, count))
		}
		s.run(fixture.Exec("ANALYZE expr_t", 0))
		s.run(fixture.Select("SELECT (stxdexpr[1]).stanullfrac,(stxdexpr[1]).stadistinct,(stxdexpr[1]).stawidth,(stxdexpr[1]).stakind1,(stxdexpr[1]).stavalues1::text AS values,(stxdexpr[1]).stanumbers1::text AS numbers"+expressionDataFrom, []string{"stanullfrac", "stadistinct", "stawidth", "stakind1", "values", "numbers"}, []string{"FLOAT4", "FLOAT4", "INT4", "INT2", "TEXT", "TEXT"}, [][]any{{float64(1), float64(0), int64(4), int64(0), nil, nil}}, "expr_null"))
		// One value among five rows is encoded as -1/5 (the >10% rule).
		s.run(fixture.Select("SELECT (stxdexpr[1]).stanullfrac,abs((stxdexpr[1]).stadistinct+0.2)<0.000001 AS distinct_fraction,(stxdexpr[1]).stakind1,(stxdexpr[1]).stavalues1::text AS values,(stxdexpr[1]).stanumbers1::text AS frequencies"+expressionDataFrom, []string{"stanullfrac", "distinct_fraction", "stakind1", "values", "frequencies"}, []string{"FLOAT4", "BOOL", "INT2", "TEXT", "TEXT"}, [][]any{{float64(0), true, int64(1), "{0}", "{1}"}}, "expr_coalesce"))
		s.run(fixture.Exec("CREATE TABLE expr_hist(a INT)", 0))
		values := make([]string, 200)
		for i := range values {
			values[i] = fmt.Sprintf("(%d)", i+1)
		}
		s.run(fixture.Exec("INSERT INTO expr_hist VALUES"+strings.Join(values, ","), 200))
		s.run(fixture.Exec("CREATE STATISTICS expr_hist_s ON (a+1) FROM expr_hist", 0))
		s.run(fixture.Exec("ANALYZE expr_hist", 0))
		s.run(fixture.Select("SELECT (stxdexpr[1]).stanullfrac,(stxdexpr[1]).stadistinct,(stxdexpr[1]).stakind1,(stxdexpr[1]).stanumbers1::text AS numbers,NOT (1 IN ("+expressionSlotKinds("(stxdexpr[1])")+")) AS no_mcv"+expressionDataFrom, []string{"stanullfrac", "stadistinct", "stakind1", "numbers", "no_mcv"}, []string{"FLOAT4", "FLOAT4", "INT2", "TEXT", "BOOL"}, [][]any{{float64(0), float64(-1), int64(2), nil, true}}, "expr_hist_s"))
		var histogram string
		if err := s.conn.QueryRowContext(s.pair.Ctx, "SELECT (stxdexpr[1]).stavalues1::text"+expressionDataFrom, "expr_hist_s").Scan(&histogram); err != nil {
			t.Fatal(err)
		}
		parts := strings.Split(strings.Trim(histogram, "{}"), ",")
		if len(parts) != 101 || parts[0] != "2" || parts[len(parts)-1] != "201" {
			t.Fatal("histogram bounds/count", histogram)
		}
		last := 1
		for _, part := range parts {
			var value int
			if _, err := fmt.Sscan(part, &value); err != nil || value <= last {
				t.Fatal("histogram not ordered", histogram, err)
			}
			last = value
		}
	})
}
