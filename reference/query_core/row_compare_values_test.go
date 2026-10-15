package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRowCompareValues(t *testing.T) {
	subqueryReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			expr string
			want any
		}{
			{"ROW(1,2) = ROW(1,2)", true},
			{"ROW(1,2) <> ROW(1,2)", false},
			{"ROW(1,2) = ROW(1,3)", false},
			{"ROW(1,2) <> ROW(1,3)", true},
			{"(1,2) = (1,2)", true},
			{"(1,2) != (1,3)", true},
			{"ROW(NULL::int,1) = ROW(NULL::int,1)", nil},
			{"ROW(NULL::int,1) <> ROW(NULL::int,1)", nil},
			{"ROW(NULL::int,1) = ROW(2,2)", false},
			{"ROW(NULL::int,1) <> ROW(2,2)", true},
			{"ROW(1,2,NULL::int) < ROW(1,3,0)", true},
			{"ROW(1,2,NULL::int) <= ROW(1,3,0)", true},
			{"ROW(1,2,NULL::int) > ROW(1,3,0)", false},
			{"ROW(1,2,NULL::int) >= ROW(1,3,0)", false},
			{"ROW(1,NULL::int,0) < ROW(1,3,1)", nil},
			{"ROW(1,2) < ROW(1,2)", false},
			{"ROW(1,2) > ROW(1,2)", false},
			{"ROW(1,2) <= ROW(1,2)", true},
			{"ROW(1,2) >= ROW(1,2)", true},
			{"ROW(1) < ROW(2)", true},
			{"ROW(NULL::int) <= ROW(2)", nil},
			{"ROW(1::smallint,'a'::text) = ROW(1::bigint,'a'::text)", true},
			{"ROW('a',NULL) = ROW('a'::text,NULL::int)", nil},
		} {
			t.Run(tc.expr, func(t *testing.T) {
				subqueryBoth(subqueryRun(t, p, b, c), fixture.Select("SELECT "+tc.expr+" AS answer", []string{"answer"}, []string{"BOOL"}, [][]any{{tc.want}}))
			})
		}
		for _, tc := range []struct {
			left, right string
			distinct    bool
		}{
			{"ROW(NULL::int,1)", "ROW(NULL::int,1)", false},
			{"ROW(NULL::int,1)", "ROW(2,1)", true},
			{"ROW(NULL::int,1)", "ROW(NULL::int,2)", true},
			{"ROW(1,2)", "ROW(1,2)", false},
			{"ROW(1,2)", "ROW(1,3)", true},
			{"ROW()", "ROW()", false},
			{"ROW(NULL::int)", "ROW(NULL::int)", false},
			{"ROW(NULL::int)", "ROW(1)", true},
			{"ROW(1)", "ROW(1)", false},
			{"ROW(1::smallint,'a'::text)", "ROW(1::bigint,'a'::text)", false},
			{"ROW('a',NULL)", "ROW('a'::text,NULL::int)", false},
		} {
			for _, not := range []bool{false, true} {
				op, want := " IS DISTINCT FROM ", tc.distinct
				if not {
					op, want = " IS NOT DISTINCT FROM ", !want
				}
				q := "SELECT " + tc.left + op + tc.right + " AS answer"
				t.Run(q, func(t *testing.T) {
					subqueryBoth(subqueryRun(t, p, b, c), fixture.Select(q, []string{"answer"}, []string{"BOOL"}, [][]any{{want}}))
				})
			}
		}
		q := "SELECT ROW($1,$2::text) IS NOT DISTINCT FROM ROW($3::bigint,$4) AS answer"
		subqueryBoth(subqueryRun(t, p, b, c), fixture.Select(q, []string{"answer"}, []string{"BOOL"}, [][]any{{true}}, 1, "a", 1, "a"))
	})
}

func TestReferenceRowCompareSubquery(t *testing.T) {
	subqueryReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := subqueryRun(t, p, b, c)
		run(fixture.Exec("CREATE TABLE rc_rhs(a int,b int)", 0))
		run(fixture.Exec("INSERT INTO rc_rhs VALUES(NULL,1),(2,2),(2,2)", 3))
		run(fixture.Exec("CREATE TABLE rc_probe(k int,a int,b int)", 0))
		run(fixture.Exec("INSERT INTO rc_probe VALUES(1,9,9),(2,9,1),(3,2,2),(4,2,2)", 4))
		// No ORDER BY: the exact outer bag, including both duplicate probes,
		// survives duplicate RHS candidates on both hash and correlated paths.
		for _, rhs := range []string{"SELECT a,b FROM rc_rhs", "SELECT r.a,r.b FROM rc_rhs r WHERE p.k>0"} {
			q := "SELECT p.a,p.b,ROW(p.a,p.b) IN (" + rhs + ") AS answer,ROW(p.a,p.b) NOT IN (" + rhs + ") AS absent FROM rc_probe p"
			subqueryBoth(run, fixture.Select(q, []string{"a", "b", "answer", "absent"}, []string{"INT4", "INT4", "BOOL", "BOOL"}, [][]any{
				{int64(9), int64(9), false, true}, {int64(9), int64(1), nil, nil},
				{int64(2), int64(2), true, false}, {int64(2), int64(2), true, false},
			}))
		}
		for _, tc := range []struct {
			expr string
			want any
		}{
			{"ROW(NULL::int,NULL::int) IN (SELECT a,b FROM rc_rhs WHERE FALSE)", false},
			{"ROW(NULL::int,NULL::int) NOT IN (SELECT a,b FROM rc_rhs WHERE FALSE)", true},
			{"ROW(1,2,NULL::int) < ANY(SELECT 1,3,0)", true},
			{"ROW(1,2,NULL::int) > ALL(SELECT 1,3,0)", false},
			{"ROW(1,2) < ANY(SELECT a,b FROM rc_rhs)", true},
			{"ROW(1,2) < SOME(SELECT a,b FROM rc_rhs)", true},
			{"ROW(1,2) > ALL(SELECT a,b FROM rc_rhs)", false},
			{"ROW(3,2) < ANY(SELECT a,b FROM rc_rhs)", nil},
			{"ROW(3,2) > ALL(SELECT a,b FROM rc_rhs)", nil},
		} {
			t.Run(tc.expr, func(t *testing.T) {
				subqueryBoth(subqueryRun(t, p, b, c), fixture.Select("SELECT "+tc.expr+" AS answer", []string{"answer"}, []string{"BOOL"}, [][]any{{tc.want}}))
			})
		}
	})
}
