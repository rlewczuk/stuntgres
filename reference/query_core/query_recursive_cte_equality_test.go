package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryRecursiveCTEEquality(t *testing.T) {
	recursiveCTEReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := subqueryRun(t, p, b, c)
		for i, s := range []fixture.Step{
			cteBag("WITH RECURSIVE r(v) AS (SELECT NULL::int UNION SELECT v FROM r) SELECT v FROM r", []string{"v"}, []string{"INT4"}, [][]any{{nil}}),
			cteBag("WITH RECURSIVE r(a,b) AS (SELECT * FROM (VALUES(1,NULL::int),(1,NULL::int),(NULL::int,1),(NULL::int,NULL::int)) s(a,b) UNION SELECT a,b FROM r) SELECT * FROM r", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), nil}, {nil, int64(1)}, {nil, nil}}),
			cteBag("WITH RECURSIVE r(node,depth) AS (SELECT 1,0 UNION SELECT node,depth+1 FROM r WHERE depth<2) SELECT node,depth FROM r", []string{"node", "depth"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(0)}, {int64(1), int64(1)}, {int64(1), int64(2)}}),
			cteBag("WITH RECURSIVE r(node,depth) AS (SELECT 1,0 UNION SELECT node,depth+1 FROM r WHERE depth<2) SELECT node FROM r", []string{"node"}, []string{"INT4"}, recursiveCTEInts(1, 1, 1)),
			// Release RecursiveUnion skips its hash table when numCols=0.
			// Bound this genuinely infinite DISTINCT stream by parent demand.
			cteBag("WITH RECURSIVE r AS (SELECT UNION SELECT FROM r) SELECT FROM r LIMIT 3", []string{}, []string{}, [][]any{{}, {}, {}}),
			cteBag("WITH RECURSIVE r AS (SELECT UNION ALL SELECT FROM r) SELECT FROM r LIMIT 3", []string{}, []string{}, [][]any{{}, {}, {}}),
			cteBag("WITH RECURSIVE r(v,n) AS (SELECT '{\"a\":1}'::json,1 UNION ALL SELECT v,n+1 FROM r WHERE n<2) SELECT v FROM r", []string{"v"}, []string{"JSON"}, [][]any{{"{\"a\":1}"}, {"{\"a\":1}"}}),
			cteBag("WITH RECURSIVE r(n) AS (SELECT 1 UNION SELECT n+1 FROM r WHERE n<3) SELECT FROM r", []string{}, []string{}, [][]any{{}, {}, {}}),
		} {
			t.Run([]string{"null", "null_tuples", "whole_key", "projected_key", "zero_width_union", "zero_width_all", "json_all", "zero_parent_keeps_key"}[i], func(t *testing.T) {
				recursiveCTERoutes(t, p, b, c, s)
			})
		}
		for i, tc := range []struct{ typ, values, classify string }{
			{"NUMERIC", "(1.0),(1.00),(1.000),(2),(3),(NULL),(NULL)", "CASE WHEN v IS NULL THEN 0 WHEN v=1 THEN 1 WHEN v=2 THEN 2 ELSE 3 END"},
			{"CHAR(4)", "('a'),('a '),('a  '),('b'),('c'),(NULL),(NULL)", "CASE WHEN v IS NULL THEN 0 WHEN v='a'::CHAR(4) THEN 1 WHEN v='b'::CHAR(4) THEN 2 ELSE 3 END"},
			{"REAL", "('0'),('-0'),('0'),('NaN'),('1'),(NULL),(NULL)", "CASE WHEN v IS NULL THEN 0 WHEN v=0::REAL THEN 1 WHEN v='NaN'::REAL THEN 2 ELSE 3 END"},
			{"DOUBLE PRECISION", "('0'),('-0'),('0'),('NaN'),('1'),(NULL),(NULL)", "CASE WHEN v IS NULL THEN 0 WHEN v=0::DOUBLE PRECISION THEN 1 WHEN v='NaN'::DOUBLE PRECISION THEN 2 ELSE 3 END"},
			{"INTERVAL", "('1 month'),('30 days'),('720 hours'),('1 day'),('2 days'),(NULL),(NULL)", "CASE WHEN v IS NULL THEN 0 WHEN v='1 month'::INTERVAL THEN 1 WHEN v='1 day'::INTERVAL THEN 2 ELSE 3 END"},
		} {
			t.Run(tc.typ, func(t *testing.T) {
				run := subqueryRun(t, p, b, c)
				name := fmt.Sprintf("recursive_equal_%d", i)
				run(fixture.Exec("CREATE TABLE "+name+"(v "+tc.typ+")", 0))
				run(fixture.Exec("INSERT INTO "+name+" VALUES"+tc.values, 7))
				q := "WITH RECURSIVE r(v) AS (SELECT v FROM " + name + " UNION SELECT v FROM r) SELECT " + tc.classify + " AS equality_class FROM r"
				recursiveCTERoutes(t, p, b, c, cteBag(q, []string{"equality_class"}, []string{"INT4"}, recursiveCTEInts(0, 1, 2, 3)))
				q = "WITH RECURSIVE r(v,n) AS (SELECT v,1 FROM " + name + " UNION ALL SELECT v,n+1 FROM r WHERE n<2) SELECT " + tc.classify + " AS equality_class FROM r"
				recursiveCTERoutes(t, p, b, c, cteBag(q, []string{"equality_class"}, []string{"INT4"}, recursiveCTEInts(0, 0, 0, 0, 1, 1, 1, 1, 1, 1, 2, 2, 3, 3)))
			})
		}
		t.Run("array_shape_bounds", func(t *testing.T) {
			run := subqueryRun(t, p, b, c)
			run(fixture.Exec("CREATE TABLE recursive_arrays(v INT[])", 0))
			run(fixture.Exec("INSERT INTO recursive_arrays VALUES('{1,2}'),('{1,2}'),('[0:1]={1,2}'),('{{1,2}}'),(NULL),(NULL)", 6))
			recursiveCTERoutes(t, p, b, c, cteBag("WITH RECURSIVE r(v) AS (SELECT v FROM recursive_arrays UNION SELECT v FROM r) SELECT v FROM r", []string{"v"}, []string{"_INT4"}, [][]any{{"{1,2}"}, {"[0:1]={1,2}"}, {"{{1,2}}"}, {nil}}))
		})
		run(fixture.Exec("CREATE TYPE recursive_mood AS ENUM ('a','b')", 0))
		run(fixture.Exec("CREATE DOMAIN recursive_domain AS INT", 0))
		for _, tc := range []struct {
			expr, typ string
			rows      [][]any
		}{
			{"'a'::recursive_mood", "", [][]any{{"a"}}},
			{"1::recursive_domain", "INT4", recursiveCTEInts(1)},
		} {
			t.Run(tc.expr, func(t *testing.T) {
				q := "WITH RECURSIVE r(v) AS (SELECT " + tc.expr + " UNION SELECT v FROM r) SELECT v FROM r"
				s := cteBag(q, []string{"v"}, []string{tc.typ}, tc.rows)
				if tc.typ == "" {
					s.CheckMetadata = fixture.EnumReferenceMetadata(b.PG, false)
					s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
						if b.PG {
							raw, ok := value.([]byte)
							if !ok || string(raw) != "a" {
								t.Fatalf("enum raw %T %v", value, value)
							}
						} else {
							raw, ok := value.(string)
							if !ok || raw != "a" {
								t.Fatalf("enum raw %T %v", value, value)
							}
						}
						return true
					}
				}
				recursiveCTERoutes(t, p, b, c, s)
			})
		}
	})
}
