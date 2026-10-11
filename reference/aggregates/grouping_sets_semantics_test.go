package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceGroupingSetsLevels(t *testing.T) {
	fixture.GroupingSetsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		fixture.GroupingSetsFixture(run)
		for _, clause := range []string{"ROLLUP(a,b)", "GROUPING SETS((a,b),(a),())", "GROUPING SETS((a,b),GROUPING SETS((a),()))"} {
			fixture.GroupingSetsBoth(run, fixture.GroupingSetsLevel("SELECT a,b,GROUPING(a,b),count(*),sum(v) FROM gs GROUP BY "+clause, false))
		}
		fixture.GroupingSetsBoth(run, fixture.GroupingSetsLevel("SELECT a,b,GROUPING(a,b),count(*),sum(v) FROM gs GROUP BY CUBE(a,b)", true))
		for _, tc := range []struct {
			clause string
			rows   [][]any
		}{
			{"ROLLUP(a,(b,v))", [][]any{{int64(1), int64(10), int64(2), int64(0), int64(1)}, {int64(1), int64(20), int64(3), int64(0), int64(1)}, {nil, int64(10), int64(5), int64(0), int64(1)}, {int64(1), nil, nil, int64(3), int64(2)}, {nil, nil, nil, int64(3), int64(1)}, {nil, nil, nil, int64(7), int64(3)}}},
			{"CUBE((a,b),v)", [][]any{{int64(1), int64(10), int64(2), int64(0), int64(1)}, {int64(1), int64(20), int64(3), int64(0), int64(1)}, {nil, int64(10), int64(5), int64(0), int64(1)}, {int64(1), int64(10), nil, int64(1), int64(1)}, {int64(1), int64(20), nil, int64(1), int64(1)}, {nil, int64(10), nil, int64(1), int64(1)}, {nil, nil, int64(2), int64(6), int64(1)}, {nil, nil, int64(3), int64(6), int64(1)}, {nil, nil, int64(5), int64(6), int64(1)}, {nil, nil, nil, int64(7), int64(3)}}},
			{"a,GROUPING SETS((b),()),GROUPING SETS((v),())", [][]any{{int64(1), int64(10), int64(2), int64(0), int64(1)}, {int64(1), int64(20), int64(3), int64(0), int64(1)}, {nil, int64(10), int64(5), int64(0), int64(1)}, {int64(1), int64(10), nil, int64(1), int64(1)}, {int64(1), int64(20), nil, int64(1), int64(1)}, {nil, int64(10), nil, int64(1), int64(1)}, {int64(1), nil, int64(2), int64(2), int64(1)}, {int64(1), nil, int64(3), int64(2), int64(1)}, {nil, nil, int64(5), int64(2), int64(1)}, {int64(1), nil, nil, int64(3), int64(2)}, {nil, nil, nil, int64(3), int64(1)}}},
		} {
			fixture.GroupingSetsBoth(run, fixture.GroupingSetsBag("SELECT a,b,v,GROUPING(a,b,v),count(*) FROM gs GROUP BY "+tc.clause, []string{"a", "b", "v", "grouping", "count"}, []string{"INT4", "INT4", "INT4", "INT4", "INT8"}, tc.rows))
		}
		for _, clause := range []string{"a", "GROUPING SETS((a),(b))"} {
			fixture.GroupingSetsBoth(run, fixture.GroupingSetsBag("SELECT count(*),sum(v) FROM gs_empty GROUP BY "+clause, []string{"count", "sum"}, []string{"INT8", "INT8"}, nil))
		}
		fixture.GroupingSetsBoth(run, fixture.Select("SELECT 7 AS k FROM gs_empty GROUP BY ()", []string{"k"}, []string{"INT4"}, [][]any{{int64(7)}}))
	})
}

func TestReferenceGroupingSetsDuplicates(t *testing.T) {
	fixture.GroupingSetsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		fixture.GroupingSetsFixture(run)
		for _, modifier := range []string{"", "ALL ", "DISTINCT "} {
			rows := [][]any{{int64(1), int64(0), int64(2)}, {nil, int64(0), int64(1)}, {nil, int64(1), int64(3)}}
			if modifier != "DISTINCT " {
				rows = append(rows, []any{int64(1), int64(0), int64(2)}, []any{nil, int64(0), int64(1)})
			}
			fixture.GroupingSetsBoth(run, fixture.GroupingSetsBag("SELECT a,GROUPING(a),count(*) FROM gs GROUP BY "+modifier+"GROUPING SETS((a),(a),())", []string{"a", "grouping", "count"}, []string{"INT4", "INT4", "INT8"}, rows))
			rows = [][]any{{int64(1), int64(0), int64(2)}, {nil, int64(0), int64(1)}}
			if modifier != "DISTINCT " {
				rows = append(rows, rows...)
			}
			fixture.GroupingSetsBoth(run, fixture.GroupingSetsBag("SELECT a,GROUPING(a),count(*) FROM gs GROUP BY "+modifier+"GROUPING SETS((a,a),(a))", []string{"a", "grouping", "count"}, []string{"INT4", "INT4", "INT8"}, rows))
			rows = [][]any{{int64(0), nil}}
			if modifier != "DISTINCT " {
				rows = append(rows, []any{int64(0), nil}, []any{int64(0), nil})
			}
			fixture.GroupingSetsBoth(run, fixture.GroupingSetsBag("SELECT count(*),sum(v) FROM gs_empty GROUP BY "+modifier+"GROUPING SETS((),(),())", []string{"count", "sum"}, []string{"INT8", "INT8"}, rows))
		}
		fixture.GroupingSetsBoth(run, fixture.GroupingSetsBag("SELECT a FROM gs WHERE a IS NULL GROUP BY DISTINCT GROUPING SETS((a),())", []string{"a"}, []string{"INT4"}, [][]any{{nil}, {nil}}))
		fixture.GroupingSetsBoth(run, fixture.Select("SELECT DISTINCT a FROM gs WHERE a IS NULL GROUP BY DISTINCT GROUPING SETS((a),())", []string{"a"}, []string{"INT4"}, [][]any{{nil}}))
		// The nine product occurrences have masks 0x1,1x2,2x2,3x3,7x1.
		for _, distinct := range []bool{false, true} {
			modifier := "ALL "
			masks := []int64{0, 1, 2, 3, 7}
			if distinct {
				modifier = "DISTINCT "
			} else {
				masks = append(masks, 1, 2, 3, 3)
			}
			fixture.GroupingSetsBoth(run, fixture.GroupingSetsBag("SELECT GROUPING(a,b,v) AS g,count(*) AS n FROM gs GROUP BY "+modifier+"ROLLUP(a,b),ROLLUP(a,v)", []string{"g", "n"}, []string{"INT4", "INT8"}, expandGroupingProductRows(masks)))
		}
	})
}

// Each independently specified set mask yields three singleton groups, except
// mask3 (a-only: counts2,1) and mask7 (global: count3).
func expandGroupingProductRows(masks []int64) [][]any {
	var rows [][]any
	for _, g := range masks {
		switch g {
		case 3:
			rows = append(rows, []any{g, int64(2)}, []any{g, int64(1)})
		case 7:
			rows = append(rows, []any{g, int64(3)})
		default:
			rows = append(rows, []any{g, int64(1)}, []any{g, int64(1)}, []any{g, int64(1)})
		}
	}
	return rows
}

func TestReferenceGroupingSetsExpressionsIndicators(t *testing.T) {
	fixture.GroupingSetsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		fixture.GroupingSetsFixture(run)
		fixture.GroupingSetsBoth(run, fixture.GroupingSetsBag("SELECT a+1 AS k,GROUPING(a+1),count(*) FROM gs GROUP BY ROLLUP(a+1)", []string{"k", "grouping", "count"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(2), int64(0), int64(2)}, {nil, int64(0), int64(1)}, {nil, int64(1), int64(3)}}))
		fixture.GroupingSetsBoth(run, fixture.GroupingSetsBag("SELECT 7 AS k,count(*) FROM gs GROUP BY GROUPING SETS((1),())", []string{"k", "count"}, []string{"INT4", "INT8"}, [][]any{{int64(7), int64(3)}, {nil, int64(3)}}))
		fixture.GroupingSetsBoth(run, fixture.GroupingSetsBag("SELECT GROUPING(a,b) AS ab,GROUPING(b,a) AS ba,GROUPING(a,a) AS aa,count(*) FROM gs GROUP BY CUBE(a,b)", []string{"ab", "ba", "aa", "count"}, []string{"INT4", "INT4", "INT4", "INT8"}, [][]any{{int64(0), int64(0), int64(0), int64(1)}, {int64(0), int64(0), int64(0), int64(1)}, {int64(0), int64(0), int64(0), int64(1)}, {int64(1), int64(2), int64(0), int64(2)}, {int64(1), int64(2), int64(0), int64(1)}, {int64(2), int64(1), int64(3), int64(2)}, {int64(2), int64(1), int64(3), int64(1)}, {int64(3), int64(3), int64(3), int64(3)}}))
		fixture.GroupingSetsBoth(run, fixture.GroupingSetsBag("SELECT GROUPING(a),count(*) FROM gs GROUP BY a", []string{"grouping", "count"}, []string{"INT4", "INT8"}, [][]any{{int64(0), int64(2)}, {int64(0), int64(1)}}))
		fixture.GroupingSetsBoth(run, fixture.GroupingSetsBag("SELECT GROUPING("+strings.TrimSuffix(strings.Repeat("a,", 31), ",")+") FROM gs GROUP BY ROLLUP(a)", []string{"grouping"}, []string{"INT4"}, [][]any{{int64(0)}, {int64(0)}, {int64(2147483647)}}))
		fixture.GroupingSetsBoth(run, fixture.GroupingSetsBag("SELECT a,GROUPING(a),count(DISTINCT b) FILTER(WHERE v>2),string_agg(b::text,',' ORDER BY b) FROM gs GROUP BY ROLLUP(a)", []string{"a", "grouping", "count", "string_agg"}, []string{"INT4", "INT4", "INT8", "TEXT"}, [][]any{{int64(1), int64(0), int64(1), "10,20"}, {nil, int64(0), int64(1), "10"}, {nil, int64(1), int64(2), "10,10,20"}}))
		run(fixture.Exec("CREATE TABLE gs_join(a INT)", 0))
		run(fixture.Exec("INSERT INTO gs_join VALUES(1)", 1))
		fixture.GroupingSetsBoth(run, fixture.GroupingSetsBag("SELECT GROUPING(gs.a),count(*) FROM gs JOIN gs_join USING(a) GROUP BY ROLLUP(a)", []string{"grouping", "count"}, []string{"INT4", "INT8"}, [][]any{{int64(0), int64(2)}, {int64(1), int64(2)}}))
		run(fixture.Exec("CREATE SEQUENCE gs_seq", 0))
		// Volatile key must be evaluated once per source row, not per level or
		// again by GROUPING. Repeated identical expression trees share a key.
		fixture.GroupingSetsBoth(run, fixture.GroupingSetsBag("SELECT GROUPING(nextval('gs_seq')),count(*) FROM gs GROUP BY GROUPING SETS((nextval('gs_seq')),(nextval('gs_seq')),())", []string{"grouping", "count"}, []string{"INT4", "INT8"}, [][]any{{int64(0), int64(1)}, {int64(0), int64(1)}, {int64(0), int64(1)}, {int64(0), int64(1)}, {int64(0), int64(1)}, {int64(0), int64(1)}, {int64(1), int64(3)}}))
		run(fixture.Select("SELECT nextval('gs_seq')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(7)}}))
	})
}

func TestReferenceGroupingSetsCorrelationPresence(t *testing.T) {
	fixture.GroupingSetsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		fixture.GroupingSetsFixture(run)
		fixture.GroupingSetsBoth(run, fixture.GroupingSetsBag("SELECT o.a,(SELECT GROUPING(o.a)) AS g,count(*) FROM gs o GROUP BY ROLLUP(o.a)", []string{"a", "g", "count"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(1), int64(0), int64(2)}, {nil, int64(0), int64(1)}, {nil, int64(1), int64(3)}}))
		fixture.GroupingSetsBoth(run, fixture.GroupingSetsBag("SELECT o.a,(SELECT (SELECT GROUPING(o.a))) AS g,count(*) FROM gs o GROUP BY ROLLUP(o.a)", []string{"a", "g", "count"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(1), int64(0), int64(2)}, {nil, int64(0), int64(1)}, {nil, int64(1), int64(3)}}))
		fixture.GroupingSetsBoth(run, fixture.GroupingSetsBag("SELECT o.a,(SELECT GROUPING(i.a) FROM gs i GROUP BY GROUPING SETS((i.a),()) HAVING GROUPING(i.a)=1) AS g FROM gs o GROUP BY ROLLUP(o.a)", []string{"a", "g"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {nil, int64(1)}, {nil, int64(1)}}))
		q := "SELECT (SELECT GROUPING(o.a+1)) FROM gs o GROUP BY ROLLUP(o.a+1)"
		fixture.GroupingSetsAnalysisError(run, fixture.GroupingSetsError(q, "42803", "arguments to GROUPING must be grouping expressions of the associated query level", "o.a+1"))
		q = "SELECT (SELECT GROUPING(i.a,o.a) FROM gs i GROUP BY i.a) FROM gs o GROUP BY o.a"
		fixture.GroupingSetsAnalysisError(run, fixture.GroupingSetsError(q, "42803", "arguments to GROUPING must be grouping expressions of the associated query level", "o.a)"))
		for _, tc := range []struct {
			clause string
			want   bool
		}{
			{"()", true}, {"GROUPING SETS((a),())", true}, {"GROUPING SETS((a))", false}, {"GROUPING SETS((),(),())", true}, {"() HAVING FALSE", false},
		} {
			fixture.GroupingSetsBoth(run, fixture.Select("SELECT EXISTS(SELECT 1 FROM gs_empty GROUP BY "+tc.clause+") AS present", []string{"present"}, []string{"BOOL"}, [][]any{{tc.want}}))
		}
		q = "SELECT EXISTS(SELECT GROUPING(a+1) FROM gs_empty GROUP BY a HAVING FALSE)"
		fixture.GroupingSetsAnalysisError(run, fixture.GroupingSetsError(q, "42803", "arguments to GROUPING must be grouping expressions of the associated query level", "a+1"))
	})
}
