package reference_test

import (
	"database/sql"
	"strings"
	"testing"
)

func TestReferenceGroupingSetsLevels(t *testing.T) {
	groupingSetsScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, run func(referenceStep)) {
		groupingSetsFixture(run)
		for _, clause := range []string{"ROLLUP(a,b)", "GROUPING SETS((a,b),(a),())", "GROUPING SETS((a,b),GROUPING SETS((a),()))"} {
			groupingSetsBoth(run, groupingSetsLevel("SELECT a,b,GROUPING(a,b),count(*),sum(v) FROM gs GROUP BY "+clause, false))
		}
		groupingSetsBoth(run, groupingSetsLevel("SELECT a,b,GROUPING(a,b),count(*),sum(v) FROM gs GROUP BY CUBE(a,b)", true))
		for _, tc := range []struct {
			clause string
			rows   [][]any
		}{
			{"ROLLUP(a,(b,v))", [][]any{{int64(1), int64(10), int64(2), int64(0), int64(1)}, {int64(1), int64(20), int64(3), int64(0), int64(1)}, {nil, int64(10), int64(5), int64(0), int64(1)}, {int64(1), nil, nil, int64(3), int64(2)}, {nil, nil, nil, int64(3), int64(1)}, {nil, nil, nil, int64(7), int64(3)}}},
			{"CUBE((a,b),v)", [][]any{{int64(1), int64(10), int64(2), int64(0), int64(1)}, {int64(1), int64(20), int64(3), int64(0), int64(1)}, {nil, int64(10), int64(5), int64(0), int64(1)}, {int64(1), int64(10), nil, int64(1), int64(1)}, {int64(1), int64(20), nil, int64(1), int64(1)}, {nil, int64(10), nil, int64(1), int64(1)}, {nil, nil, int64(2), int64(6), int64(1)}, {nil, nil, int64(3), int64(6), int64(1)}, {nil, nil, int64(5), int64(6), int64(1)}, {nil, nil, nil, int64(7), int64(3)}}},
			{"a,GROUPING SETS((b),()),GROUPING SETS((v),())", [][]any{{int64(1), int64(10), int64(2), int64(0), int64(1)}, {int64(1), int64(20), int64(3), int64(0), int64(1)}, {nil, int64(10), int64(5), int64(0), int64(1)}, {int64(1), int64(10), nil, int64(1), int64(1)}, {int64(1), int64(20), nil, int64(1), int64(1)}, {nil, int64(10), nil, int64(1), int64(1)}, {int64(1), nil, int64(2), int64(2), int64(1)}, {int64(1), nil, int64(3), int64(2), int64(1)}, {nil, nil, int64(5), int64(2), int64(1)}, {int64(1), nil, nil, int64(3), int64(2)}, {nil, nil, nil, int64(3), int64(1)}}},
		} {
			groupingSetsBoth(run, groupingSetsBag("SELECT a,b,v,GROUPING(a,b,v),count(*) FROM gs GROUP BY "+tc.clause, []string{"a", "b", "v", "grouping", "count"}, []string{"INT4", "INT4", "INT4", "INT4", "INT8"}, tc.rows))
		}
		for _, clause := range []string{"a", "GROUPING SETS((a),(b))"} {
			groupingSetsBoth(run, groupingSetsBag("SELECT count(*),sum(v) FROM gs_empty GROUP BY "+clause, []string{"count", "sum"}, []string{"INT8", "INT8"}, nil))
		}
		groupingSetsBoth(run, referenceSelect("SELECT 7 AS k FROM gs_empty GROUP BY ()", []string{"k"}, []string{"INT4"}, [][]any{{int64(7)}}))
	})
}

func TestReferenceGroupingSetsDuplicates(t *testing.T) {
	groupingSetsScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, run func(referenceStep)) {
		groupingSetsFixture(run)
		for _, modifier := range []string{"", "ALL ", "DISTINCT "} {
			rows := [][]any{{int64(1), int64(0), int64(2)}, {nil, int64(0), int64(1)}, {nil, int64(1), int64(3)}}
			if modifier != "DISTINCT " {
				rows = append(rows, []any{int64(1), int64(0), int64(2)}, []any{nil, int64(0), int64(1)})
			}
			groupingSetsBoth(run, groupingSetsBag("SELECT a,GROUPING(a),count(*) FROM gs GROUP BY "+modifier+"GROUPING SETS((a),(a),())", []string{"a", "grouping", "count"}, []string{"INT4", "INT4", "INT8"}, rows))
			rows = [][]any{{int64(1), int64(0), int64(2)}, {nil, int64(0), int64(1)}}
			if modifier != "DISTINCT " {
				rows = append(rows, rows...)
			}
			groupingSetsBoth(run, groupingSetsBag("SELECT a,GROUPING(a),count(*) FROM gs GROUP BY "+modifier+"GROUPING SETS((a,a),(a))", []string{"a", "grouping", "count"}, []string{"INT4", "INT4", "INT8"}, rows))
			rows = [][]any{{int64(0), nil}}
			if modifier != "DISTINCT " {
				rows = append(rows, []any{int64(0), nil}, []any{int64(0), nil})
			}
			groupingSetsBoth(run, groupingSetsBag("SELECT count(*),sum(v) FROM gs_empty GROUP BY "+modifier+"GROUPING SETS((),(),())", []string{"count", "sum"}, []string{"INT8", "INT8"}, rows))
		}
		groupingSetsBoth(run, groupingSetsBag("SELECT a FROM gs WHERE a IS NULL GROUP BY DISTINCT GROUPING SETS((a),())", []string{"a"}, []string{"INT4"}, [][]any{{nil}, {nil}}))
		groupingSetsBoth(run, referenceSelect("SELECT DISTINCT a FROM gs WHERE a IS NULL GROUP BY DISTINCT GROUPING SETS((a),())", []string{"a"}, []string{"INT4"}, [][]any{{nil}}))
		// The nine product occurrences have masks 0x1,1x2,2x2,3x3,7x1.
		for _, distinct := range []bool{false, true} {
			modifier := "ALL "
			masks := []int64{0, 1, 2, 3, 7}
			if distinct {
				modifier = "DISTINCT "
			} else {
				masks = append(masks, 1, 2, 3, 3)
			}
			groupingSetsBoth(run, groupingSetsBag("SELECT GROUPING(a,b,v) AS g,count(*) AS n FROM gs GROUP BY "+modifier+"ROLLUP(a,b),ROLLUP(a,v)", []string{"g", "n"}, []string{"INT4", "INT8"}, expandGroupingProductRows(masks)))
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
	groupingSetsScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, run func(referenceStep)) {
		groupingSetsFixture(run)
		groupingSetsBoth(run, groupingSetsBag("SELECT a+1 AS k,GROUPING(a+1),count(*) FROM gs GROUP BY ROLLUP(a+1)", []string{"k", "grouping", "count"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(2), int64(0), int64(2)}, {nil, int64(0), int64(1)}, {nil, int64(1), int64(3)}}))
		groupingSetsBoth(run, groupingSetsBag("SELECT 7 AS k,count(*) FROM gs GROUP BY GROUPING SETS((1),())", []string{"k", "count"}, []string{"INT4", "INT8"}, [][]any{{int64(7), int64(3)}, {nil, int64(3)}}))
		groupingSetsBoth(run, groupingSetsBag("SELECT GROUPING(a,b) AS ab,GROUPING(b,a) AS ba,GROUPING(a,a) AS aa,count(*) FROM gs GROUP BY CUBE(a,b)", []string{"ab", "ba", "aa", "count"}, []string{"INT4", "INT4", "INT4", "INT8"}, [][]any{{int64(0), int64(0), int64(0), int64(1)}, {int64(0), int64(0), int64(0), int64(1)}, {int64(0), int64(0), int64(0), int64(1)}, {int64(1), int64(2), int64(0), int64(2)}, {int64(1), int64(2), int64(0), int64(1)}, {int64(2), int64(1), int64(3), int64(2)}, {int64(2), int64(1), int64(3), int64(1)}, {int64(3), int64(3), int64(3), int64(3)}}))
		groupingSetsBoth(run, groupingSetsBag("SELECT GROUPING(a),count(*) FROM gs GROUP BY a", []string{"grouping", "count"}, []string{"INT4", "INT8"}, [][]any{{int64(0), int64(2)}, {int64(0), int64(1)}}))
		groupingSetsBoth(run, groupingSetsBag("SELECT GROUPING("+strings.TrimSuffix(strings.Repeat("a,", 31), ",")+") FROM gs GROUP BY ROLLUP(a)", []string{"grouping"}, []string{"INT4"}, [][]any{{int64(0)}, {int64(0)}, {int64(2147483647)}}))
		groupingSetsBoth(run, groupingSetsBag("SELECT a,GROUPING(a),count(DISTINCT b) FILTER(WHERE v>2),string_agg(b::text,',' ORDER BY b) FROM gs GROUP BY ROLLUP(a)", []string{"a", "grouping", "count", "string_agg"}, []string{"INT4", "INT4", "INT8", "TEXT"}, [][]any{{int64(1), int64(0), int64(1), "10,20"}, {nil, int64(0), int64(1), "10"}, {nil, int64(1), int64(2), "10,10,20"}}))
		run(referenceExec("CREATE TABLE gs_join(a INT)", 0))
		run(referenceExec("INSERT INTO gs_join VALUES(1)", 1))
		groupingSetsBoth(run, groupingSetsBag("SELECT GROUPING(gs.a),count(*) FROM gs JOIN gs_join USING(a) GROUP BY ROLLUP(a)", []string{"grouping", "count"}, []string{"INT4", "INT8"}, [][]any{{int64(0), int64(2)}, {int64(1), int64(2)}}))
		run(referenceExec("CREATE SEQUENCE gs_seq", 0))
		// Volatile key must be evaluated once per source row, not per level or
		// again by GROUPING. Repeated identical expression trees share a key.
		groupingSetsBoth(run, groupingSetsBag("SELECT GROUPING(nextval('gs_seq')),count(*) FROM gs GROUP BY GROUPING SETS((nextval('gs_seq')),(nextval('gs_seq')),())", []string{"grouping", "count"}, []string{"INT4", "INT8"}, [][]any{{int64(0), int64(1)}, {int64(0), int64(1)}, {int64(0), int64(1)}, {int64(0), int64(1)}, {int64(0), int64(1)}, {int64(0), int64(1)}, {int64(1), int64(3)}}))
		run(referenceSelect("SELECT nextval('gs_seq')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(7)}}))
	})
}

func TestReferenceGroupingSetsCorrelationPresence(t *testing.T) {
	groupingSetsScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, run func(referenceStep)) {
		groupingSetsFixture(run)
		groupingSetsBoth(run, groupingSetsBag("SELECT o.a,(SELECT GROUPING(o.a)) AS g,count(*) FROM gs o GROUP BY ROLLUP(o.a)", []string{"a", "g", "count"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(1), int64(0), int64(2)}, {nil, int64(0), int64(1)}, {nil, int64(1), int64(3)}}))
		groupingSetsBoth(run, groupingSetsBag("SELECT o.a,(SELECT (SELECT GROUPING(o.a))) AS g,count(*) FROM gs o GROUP BY ROLLUP(o.a)", []string{"a", "g", "count"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(1), int64(0), int64(2)}, {nil, int64(0), int64(1)}, {nil, int64(1), int64(3)}}))
		groupingSetsBoth(run, groupingSetsBag("SELECT o.a,(SELECT GROUPING(i.a) FROM gs i GROUP BY GROUPING SETS((i.a),()) HAVING GROUPING(i.a)=1) AS g FROM gs o GROUP BY ROLLUP(o.a)", []string{"a", "g"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {nil, int64(1)}, {nil, int64(1)}}))
		q := "SELECT (SELECT GROUPING(o.a+1)) FROM gs o GROUP BY ROLLUP(o.a+1)"
		groupingSetsAnalysisError(run, groupingSetsError(q, "42803", "arguments to GROUPING must be grouping expressions of the associated query level", "o.a+1"))
		q = "SELECT (SELECT GROUPING(i.a,o.a) FROM gs i GROUP BY i.a) FROM gs o GROUP BY o.a"
		groupingSetsAnalysisError(run, groupingSetsError(q, "42803", "arguments to GROUPING must be grouping expressions of the associated query level", "o.a)"))
		for _, tc := range []struct {
			clause string
			want   bool
		}{
			{"()", true}, {"GROUPING SETS((a),())", true}, {"GROUPING SETS((a))", false}, {"GROUPING SETS((),(),())", true}, {"() HAVING FALSE", false},
		} {
			groupingSetsBoth(run, referenceSelect("SELECT EXISTS(SELECT 1 FROM gs_empty GROUP BY "+tc.clause+") AS present", []string{"present"}, []string{"BOOL"}, [][]any{{tc.want}}))
		}
		q = "SELECT EXISTS(SELECT GROUPING(a+1) FROM gs_empty GROUP BY a HAVING FALSE)"
		groupingSetsAnalysisError(run, groupingSetsError(q, "42803", "arguments to GROUPING must be grouping expressions of the associated query level", "a+1"))
	})
}
