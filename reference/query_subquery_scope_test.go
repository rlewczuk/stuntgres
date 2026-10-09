package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceQuerySubqueryScope(t *testing.T) {
	subqueryReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := subqueryRun(t, p, b, c)
		subqueryScopeFixture(run)
		subqueryReductionFixture(run)
		for _, s := range []referenceStep{
			referenceSelect("SELECT o.id FROM outer_t o WHERE EXISTS(SELECT 1 FROM inner_t i WHERE i.k=o.id) ORDER BY o.id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}),
			referenceSelect("SELECT o.id,(SELECT i.v FROM inner_t i WHERE i.k=o.id AND i.v=10) AS s FROM outer_t o ORDER BY o.id", []string{"id", "s"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), nil}, {int64(3), nil}, {int64(3), nil}}),
			referenceSelect("SELECT o.id FROM outer_t o WHERE EXISTS(SELECT 1 FROM inner_t i WHERE EXISTS(SELECT 1 WHERE i.k=o.id)) ORDER BY o.id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}),
			referenceSelect("SELECT o.id,(SELECT d.v FROM (SELECT o.id AS v) d) AS s FROM outer_t o ORDER BY o.id", []string{"id", "s"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(3), int64(3)}, {int64(3), int64(3)}}),
			referenceSelect("SELECT o.id,(SELECT id FROM (SELECT 99 AS id) local) AS s FROM outer_t o ORDER BY o.id", []string{"id", "s"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(99)}, {int64(2), int64(99)}, {int64(3), int64(99)}, {int64(3), int64(99)}}),
			referenceSelect("SELECT d.x FROM (SELECT v AS x FROM r) d WHERE FALSE", []string{"x"}, []string{"INT4"}, nil),
			referenceSelect("SELECT d.renamed FROM (SELECT v FROM r ORDER BY v LIMIT 2) d(renamed) ORDER BY renamed DESC", []string{"renamed"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(1)}}),
			referenceSelect("SELECT x FROM (SELECT 7 AS x)", []string{"x"}, []string{"INT4"}, [][]any{{int64(7)}}),
			referenceSelect("SELECT o.id,d.x FROM outer_t o LEFT JOIN (SELECT 1 AS x) d ON o.id=d.x ORDER BY o.id", []string{"id", "x"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), nil}, {int64(3), nil}, {int64(3), nil}}),
			referenceSelect("SELECT 1 AS unit WHERE TRUE ORDER BY unit LIMIT 1", []string{"unit"}, []string{"INT4"}, [][]any{{int64(1)}}),
			referenceSelect("SELECT 1 AS unit WHERE FALSE", []string{"unit"}, []string{"INT4"}, nil),
			referenceSelect("SELECT 1 AS unit GROUP BY 1 HAVING EXISTS(SELECT 1) ORDER BY unit LIMIT 1", []string{"unit"}, []string{"INT4"}, [][]any{{int64(1)}}),
			referenceSelect("SELECT d.v,d.n FROM (SELECT v,count(*) AS n FROM r GROUP BY v HAVING count(*)>1 ORDER BY v LIMIT 1) d", []string{"v", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(2), int64(2)}}),
			referenceSelect("SELECT count(*)", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}),
			referenceSelect("SELECT count(*) WHERE FALSE", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}),
			referenceSelect("SELECT (SELECT count(o.id)) FROM outer_t o", []string{"count"}, []string{"INT8"}, [][]any{{int64(4)}}),
			referenceSelect("SELECT o.id,(SELECT o.id) AS s FROM outer_t o GROUP BY o.id ORDER BY o.id", []string{"id", "s"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(3), int64(3)}}),
			referenceSelect("SELECT o.id,(SELECT count(i.k) FROM inner_t i WHERE i.k=o.id) AS n FROM outer_t o ORDER BY o.id", []string{"id", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(2)}, {int64(2), int64(1)}, {int64(3), int64(0)}, {int64(3), int64(0)}}),
		} {
			subqueryBoth(run, s)
		}
		for _, q := range []string{"SELECT d.x FROM (SELECT v AS x FROM r) d", "SELECT x FROM (SELECT v AS x FROM r)"} {
			s := referenceSelect(q, []string{"x"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {nil}, {int64(2)}})
			s.bag = true
			subqueryBoth(run, s)
		}
		// Complete logical records observed in root's pinned18.0 tests-3/artifact22.
		for _, tc := range []struct{ q, code, message, position, detail, hint string }{
			{"SELECT * FROM outer_t o,(SELECT o.id) d", "42P01", `invalid reference to FROM-clause entry for table "o"`, "33", `There is an entry for table "o", but it cannot be referenced from this part of the query.`, `To reference that table, you must mark this subquery with LATERAL.`},
			{"SELECT (SELECT id FROM outer_t a,outer_t b) FROM outer_t o WHERE FALSE", "42702", `column reference "id" is ambiguous`, "16", "", ""},
			{"SELECT (SELECT o.id FROM inner_t o) FROM outer_t o", "42703", "column o.id does not exist", "16", `There is a column named "id" in table "o", but it cannot be referenced from this part of the query.`, ""},
			{"SELECT (SELECT o.id) FROM outer_t o GROUP BY o.id+1", "42803", `subquery uses ungrouped column "o.id" from outer query`, "16", "", ""},
			{"SELECT unnamed_subquery.x FROM (SELECT 1 AS x)", "42P01", `invalid reference to FROM-clause entry for table "unnamed_subquery"`, "8", `There is an entry for table "unnamed_subquery", but it cannot be referenced from this part of the query.`, ""},
		} {
			want := controlsReferenceDiagnostic(tc.code, tc.message)
			want.Position, want.Detail, want.Hint = tc.position, tc.detail, tc.hint
			subqueryAnalysisFailure(run, tc.q, want)
		}
		// Current locking syntax, each nested source keeps its own query level.
		subqueryBoth(run, referenceSelect("SELECT (SELECT v FROM r WHERE v=1 FOR UPDATE) AS locked", []string{"locked"}, []string{"INT4"}, [][]any{{int64(1)}}))
		subqueryBoth(run, referenceSelect("SELECT EXISTS(SELECT v FROM r WHERE v=1 FOR UPDATE) AS locked", []string{"locked"}, []string{"BOOL"}, [][]any{{true}}))
		subqueryBoth(run, referenceSelect("SELECT v FROM (SELECT v FROM r WHERE v=1 FOR UPDATE) d", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
	})
}
