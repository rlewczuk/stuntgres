package reference_test

import (
	"bytes"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceStatisticsExpressionEqualityOnly(t *testing.T) {
	withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
		for _, q := range []string{
			"CREATE TABLE expr_xid(w xid)",
			"CREATE STATISTICS expr_xid_s ON (coalesce(w,w)) FROM expr_xid",
			"CREATE TABLE expr_other(a INT)",
			"CREATE STATISTICS expr_other_s ON (a+1) FROM expr_other",
		} {
			s.run(fixture.Exec(q, 0))
		}
		s.run(fixture.Exec("INSERT INTO expr_xid VALUES('1'),('1'),('2'),('2')", 4))
		s.run(fixture.Exec("INSERT INTO expr_other VALUES"+strings.TrimSuffix(strings.Repeat("(1),", 20), ","), 20))
		s.run(fixture.Exec("ANALYZE expr_other", 0))
		s.run(expressionUniform("expr_other_s", "{2}"))
		s.run(fixture.Exec("ANALYZE expr_xid", 0))
		// xid has hash equality but no default btree operator class. The
		// genuine MCV equality operator must not be replaced by zero or an
		// unrelated ordered provider merely to make the row projectable.
		q := "SELECT (stxdexpr[1]).stanullfrac,(stxdexpr[1]).stadistinct,(stxdexpr[1]).stakind1,(stxdexpr[1]).staop1='=(xid,xid)'::regoperator AS equality_operator,(stxdexpr[1]).stacoll1::bigint AS collation,(stxdexpr[1]).stavalues1::text AS values,(stxdexpr[1]).stanumbers1::text AS frequencies,NOT (2 IN (" + expressionSlotKinds("(stxdexpr[1])") + ")) AS no_histogram" + expressionDataFrom
		s.run(fixture.Select(q, []string{"stanullfrac", "stadistinct", "stakind1", "equality_operator", "collation", "values", "frequencies", "no_histogram"}, []string{"FLOAT4", "FLOAT4", "INT2", "BOOL", "INT8", "TEXT", "TEXT", "BOOL"}, [][]any{{float64(0), float64(-0.5), int64(1), true, int64(0), "{1,2}", "{0.5,0.5}", true}}, "expr_xid_s"))
		// Runtime projection visits all facts: this independent control must
		// remain readable after an equality-only object acquires data.
		s.run(expressionUniform("expr_other_s", "{2}"))
	})
}

func expressionNullWidths() fixture.Step {
	q := "SELECT s.stxname,(stxdexpr[1]).stanullfrac,(stxdexpr[1]).stadistinct,(stxdexpr[1]).stawidth,(stxdexpr[1]).stakind1,(stxdexpr[1]).stavalues1::text AS values FROM pg_statistic_ext s JOIN pg_statistic_ext_data d ON d.stxoid=s.oid WHERE s.stxname IN ('expr_width_int','expr_width_text') AND NOT d.stxdinherit ORDER BY s.stxname"
	return fixture.Select(q, []string{"stxname", "stanullfrac", "stadistinct", "stawidth", "stakind1", "values"}, []string{"NAME", "FLOAT4", "FLOAT4", "INT4", "INT2", "TEXT"}, [][]any{{"expr_width_int", float64(1), float64(0), int64(4), int64(0), nil}, {"expr_width_text", float64(1), float64(0), int64(0), int64(0), nil}})
}

func TestReferenceStatisticsExpressionAllNullWidthControls(t *testing.T) {
	withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
		for _, q := range []string{"CREATE TABLE expr_width(a INT)", "CREATE STATISTICS expr_width_int ON (a+1) FROM expr_width", "CREATE STATISTICS expr_width_text ON (a::text) FROM expr_width"} {
			s.run(fixture.Exec(q, 0))
		}
		s.run(fixture.Exec("INSERT INTO expr_width VALUES(NULL),(NULL),(NULL),(NULL),(NULL)", 5))
		s.run(fixture.Exec("ANALYZE expr_width", 0))
		s.run(expressionNullWidths())
		s.run(fixture.Exec("UPDATE expr_width SET a=9", 5))
		s.run(expressionNullWidths())
		if !s.pg {
			var saved bytes.Buffer
			if err := stuntgres.Dump(s.pair.Ctx, s.pair.Native, &saved); err != nil {
				t.Fatal(err)
			}
			if saved.Len() == 0 {
				t.Fatal("empty all-NULL width dump")
			}
			if err := stuntgres.Reset(s.pair.Ctx, s.pair.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(s.pair.Ctx, s.pair.Native, bytes.NewReader(saved.Bytes())); err != nil {
				t.Fatal(err)
			}
		}
		s.run(expressionNullWidths())
		s.run(fixture.Select("SELECT min(a),max(a),count(*) FROM expr_width", []string{"min", "max", "count"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(9), int64(9), int64(5)}}))
		s.run(fixture.Exec("ANALYZE expr_width", 0))
		s.run(fixture.Select("SELECT (stxdexpr[1]).stanullfrac,(stxdexpr[1]).stawidth,(stxdexpr[1]).stavalues1::text AS values"+expressionDataFrom, []string{"stanullfrac", "stawidth", "values"}, []string{"FLOAT4", "INT4", "TEXT"}, [][]any{{float64(0), int64(4), "{10}"}}, "expr_width_int"))
	})
}
