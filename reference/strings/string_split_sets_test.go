package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStringSplitTableSources(t *testing.T) {
	stringReplaceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range stringSplitVectors() {
			t.Run(tc.args, func(t *testing.T) {
				rows := make([][]any, 0, len(tc.fields))
				for _, v := range tc.fields {
					rows = append(rows, []any{v, v == nil})
				}
				s := fixture.Select("SELECT v,v IS NULL AS n FROM string_to_table("+tc.args+") AS t(v)", []string{"v", "n"}, []string{"TEXT", "BOOL"}, rows)
				s.Bag = true
				stringReplaceBoth(t, p, b, c, s)
			})
		}
		for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE ss_source(id int,s text)", 0), fixture.Exec("INSERT INTO ss_source VALUES(1,'x,x'),(2,''),(3,NULL),(4,'y')", 4)} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		for _, from := range []string{"ss_source s CROSS JOIN string_to_table(s.s,',') AS t(v)", "ss_source s, string_to_table(s.s,',') AS t(v)", "ss_source s CROSS JOIN LATERAL string_to_table(s.s,',') AS t(v)"} {
			s := fixture.Select("SELECT s.id,t.v FROM "+from, []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "x"}, {int64(1), "x"}, {int64(4), "y"}})
			s.Bag = true
			stringReplaceBoth(t, p, b, c, s)
		}
		for _, tc := range []struct {
			q            string
			names, types []string
			rows         [][]any
			ordered      bool
		}{
			{"SELECT s.id,t.v,t.n FROM ss_source s LEFT JOIN LATERAL string_to_table(s.s,',') WITH ORDINALITY AS t(v,n) ON TRUE ORDER BY s.id,t.n", []string{"id", "v", "n"}, []string{"INT4", "TEXT", "INT8"}, [][]any{{int64(1), "x", int64(1)}, {int64(1), "x", int64(2)}, {int64(2), nil, nil}, {int64(3), nil, nil}, {int64(4), "y", int64(1)}}, true},
			{"SELECT v FROM string_to_table('x,x,y',',') AS t(v) WHERE v='x'", []string{"v"}, []string{"TEXT"}, [][]any{{"x"}, {"x"}}, false},
			{"SELECT v,n FROM string_to_table('x,x,,',',','') WITH ORDINALITY AS t(v,n) ORDER BY n", []string{"v", "n"}, []string{"TEXT", "INT8"}, [][]any{{"x", int64(1)}, {"x", int64(2)}, {nil, int64(3)}, {nil, int64(4)}}, true},
			{"SELECT t.t,t.ordinality FROM string_to_table('a,b',',') WITH ORDINALITY AS t ORDER BY t.ordinality", []string{"t", "ordinality"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(1)}, {"b", int64(2)}}, true},
			{"SELECT t.t FROM ROWS FROM(string_to_table('a,b',',')) AS t", []string{"t"}, []string{"TEXT"}, [][]any{{"a"}, {"b"}}, false},
			{"SELECT t.t,t.ordinality FROM ROWS FROM(string_to_table('a,b',',')) WITH ORDINALITY AS t ORDER BY t.ordinality", []string{"t", "ordinality"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(1)}, {"b", int64(2)}}, true},
			{"SELECT v,ordinality FROM string_to_table('a,b',',') WITH ORDINALITY AS t(v) ORDER BY ordinality", []string{"v", "ordinality"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(1)}, {"b", int64(2)}}, true},
			{"SELECT a,b,n FROM ROWS FROM(string_to_table('a,b',','),string_to_table('x',',')) WITH ORDINALITY AS t(a,b,n) ORDER BY n", []string{"a", "b", "n"}, []string{"TEXT", "TEXT", "INT8"}, [][]any{{"a", "x", int64(1)}, {"b", nil, int64(2)}}, true},
			{"SELECT * FROM ROWS FROM(string_to_table('',','),string_to_table(NULL,',')) WITH ORDINALITY AS t(a,b,n)", []string{"a", "b", "n"}, []string{"TEXT", "TEXT", "INT8"}, nil, false},
			{"SELECT a.v AS a,b.v AS b FROM string_to_table('a,b',',') AS a(v),string_to_table('x,y',',') AS b(v)", []string{"a", "b"}, []string{"TEXT", "TEXT"}, [][]any{{"a", "x"}, {"a", "y"}, {"b", "x"}, {"b", "y"}}, false},
			{"SELECT a.v AS a,b.v AS b FROM pg_catalog.string_to_table('a:b,c:d',',') AS a(v) JOIN string_to_table(a.v,':') AS b(v) ON TRUE", []string{"a", "b"}, []string{"TEXT", "TEXT"}, [][]any{{"a:b", "a"}, {"a:b", "b"}, {"c:d", "c"}, {"c:d", "d"}}, false},
			{"SELECT s.v,t.x FROM (VALUES ('q')) AS s(v) FULL JOIN string_to_table('a,b',',') AS t(x) ON s.v=t.x", []string{"v", "x"}, []string{"TEXT", "TEXT"}, [][]any{{"q", nil}, {nil, "a"}, {nil, "b"}}, false},
			{"SELECT s.v,t.x FROM (VALUES ('q')) AS s(v) RIGHT JOIN string_to_table('a,b',',') AS t(x) ON s.v=t.x", []string{"v", "x"}, []string{"TEXT", "TEXT"}, [][]any{{nil, "a"}, {nil, "b"}}, false},
			{"SELECT s.id,t.a,t.b,t.n FROM ss_source s CROSS JOIN LATERAL ROWS FROM(string_to_table(s.s,','),string_to_table('z',',')) WITH ORDINALITY AS t(a,b,n) ORDER BY s.id,t.n", []string{"id", "a", "b", "n"}, []string{"INT4", "TEXT", "TEXT", "INT8"}, [][]any{{int64(1), "x", "z", int64(1)}, {int64(1), "x", nil, int64(2)}, {int64(2), nil, "z", int64(1)}, {int64(3), nil, "z", int64(1)}, {int64(4), "y", "z", int64(1)}}, true},
		} {
			t.Run(tc.q, func(t *testing.T) {
				s := fixture.Select(tc.q, tc.names, tc.types, tc.rows)
				s.Bag = !tc.ordered
				stringReplaceBoth(t, p, b, c, s)
			})
		}
	})
}

func TestReferenceStringSplitTargetExpansion(t *testing.T) {
	stringReplaceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE ss_wrapper_null(n text)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ss_wrapper_null VALUES(NULL)", 1))
		for _, tc := range []struct {
			q            string
			names, types []string
			rows         [][]any
		}{
			{"SELECT 7 AS n,string_to_table('x,x,,',',') AS v", []string{"n", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "x"}, {int64(7), "x"}, {int64(7), ""}, {int64(7), ""}}},
			{"SELECT 7 AS n,string_to_table('',',') AS v", []string{"n", "v"}, []string{"INT4", "TEXT"}, nil},
			{"SELECT string_to_table('a,b',',') AS a,string_to_table('x',',') AS b", []string{"a", "b"}, []string{"TEXT", "TEXT"}, [][]any{{"a", "x"}, {"b", nil}}},
			{"SELECT string_to_table('',',') AS a,string_to_table('x,x',',') AS b", []string{"a", "b"}, []string{"TEXT", "TEXT"}, [][]any{{nil, "x"}, {nil, "x"}}},
			{"SELECT upper(string_to_table('a,b',',')) AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"A"}, {"B"}}},
			// Pinned PostgreSQL folds a literal-NULL strict wrapper to one NULL.
			{"SELECT replace(string_to_table('a,b',','), NULL, 'x') AS v", []string{"v"}, []string{"TEXT"}, [][]any{{nil}}},
			// A physical nullable argument is not a constant-folding shortcut.
			{"SELECT replace(string_to_table('a,b',','), n, 'x') AS v FROM ss_wrapper_null", []string{"v"}, []string{"TEXT"}, [][]any{{nil}, {nil}}},
			{"SELECT string_to_table(string_to_table('a:b,c:d',','),':') AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"a"}, {"b"}, {"c"}, {"d"}}},
			{"SELECT string_to_table(string_to_table('a:b,c:d',','),':') AS a,string_to_table('x,y,z',',') AS b", []string{"a", "b"}, []string{"TEXT", "TEXT"}, [][]any{{"a", "x"}, {"b", "x"}, {"c", "y"}, {"d", "y"}}},
			{"SELECT s.id,string_to_table(s.s,',') AS v FROM (VALUES(1,'x,x'),(2,''),(3,NULL),(4,'y')) AS s(id,s)", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "x"}, {int64(1), "x"}, {int64(4), "y"}}},
		} {
			t.Run(tc.q, func(t *testing.T) {
				s := fixture.Select(tc.q, tc.names, tc.types, tc.rows)
				s.Bag = true
				stringReplaceBoth(t, p, b, c, s)
			})
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE SEQUENCE ss_attempt START 1", 0))
		for _, tc := range []struct {
			q, typ string
			rows   [][]any
		}{
			{"SELECT string_to_array(NULL,',',nextval('ss_attempt')::text) AS v", "_TEXT", [][]any{{nil}}},
			{"SELECT string_to_table(NULL,',',nextval('ss_attempt')::text) AS v", "TEXT", nil},
		} {
			for _, prepared := range []bool{false, true} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT setval('ss_attempt',10,true) AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(10)}}))
				s := fixture.Select(tc.q, []string{"v"}, []string{tc.typ}, tc.rows)
				if prepared {
					s = fixture.Prepared(s)
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT currval('ss_attempt') AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(11)}}))
			}
		}
		for _, tc := range []struct {
			q           string
			rows        [][]any
			reset, curr int64
		}{
			{"SELECT nextval('ss_attempt') AS n,string_to_table('a,b',',') AS v", [][]any{{int64(1), "a"}, {int64(2), "b"}}, 1, 3},
			{"SELECT nextval('ss_attempt') AS n,upper(string_to_table('a,b',',')) AS v", [][]any{{int64(1), "A"}, {int64(2), "B"}}, 1, 2},
			{"SELECT nextval('ss_attempt') AS n,string_to_table('a,b',',') AS v LIMIT 1", [][]any{{int64(1), "a"}}, 1, 1},
			{"SELECT nextval('ss_attempt') AS n,string_to_table('a,b',',') AS v FROM (VALUES(1),(2)) t(i) ORDER BY n", [][]any{{int64(1), "a"}, {int64(1), "b"}, {int64(2), "a"}, {int64(2), "b"}}, 1, 2},
			{"SELECT nextval('ss_attempt') AS n,string_to_table('',',') AS v", nil, 10, 11},
			{"SELECT nextval('ss_attempt') AS n,upper(string_to_table('',',')) AS v", nil, 10, 10},
			{"SELECT nextval('ss_attempt') AS n,string_to_table('a,b',',') AS v WHERE false", nil, 10, 10},
		} {
			t.Run(tc.q, func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT setval('ss_attempt',$1,$2) AS v", []string{"v"}, []string{"INT8"}, [][]any{{tc.reset}}, tc.reset, tc.reset == 10))
					s := fixture.Select(tc.q, []string{"n", "v"}, []string{"INT8", "TEXT"}, tc.rows)
					s.Bag = true
					if prepared {
						s = fixture.Prepared(s)
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT currval('ss_attempt') AS v", []string{"v"}, []string{"INT8"}, [][]any{{tc.curr}}))
				}
			})
		}
		for _, tc := range []struct {
			name, query string
			names       []string
			types       []string
			rows        [][]any
			curr        int64
		}{
			{"mixed_depth_effects", "SELECT nextval('ss_attempt') AS n,upper(string_to_table('x,y',',')) AS shallow,string_to_table(string_to_table('a:b,c:d',','),':') AS deep", []string{"n", "shallow", "deep"}, []string{"INT8", "TEXT", "TEXT"}, [][]any{{int64(1), "X", "a"}, {int64(2), "X", "b"}, {int64(4), "Y", "c"}, {int64(5), "Y", "d"}}, 6},
			{"erased_wrapper_effects", "SELECT nextval('ss_attempt') AS n,replace(string_to_table('a,b',','),NULL,'x') AS erased,string_to_table('c,d',',') AS live", []string{"n", "erased", "live"}, []string{"INT8", "TEXT", "TEXT"}, [][]any{{int64(1), nil, "c"}, {int64(2), nil, "d"}}, 3},
			{"decreased_depth_peer_zip", "SELECT nextval('ss_attempt') AS n,string_to_table(replace(string_to_table('a,b',','),NULL,'x'),',') AS gone,string_to_table('c,d',',') AS live", []string{"n", "gone", "live"}, []string{"INT8", "TEXT", "TEXT"}, [][]any{{int64(1), nil, "c"}, {int64(2), nil, "d"}}, 3},
			{"scalar_first_argument_effects", "SELECT nextval('ss_attempt') AS n,string_to_table(nextval('ss_attempt')::text,',') AS v", []string{"n", "v"}, []string{"INT8", "TEXT"}, [][]any{{int64(1), "2"}}, 3},
			{"srf_first_argument_effects", "SELECT string_to_table(nextval('ss_attempt')::text,',') AS v,nextval('ss_attempt') AS n", []string{"v", "n"}, []string{"TEXT", "INT8"}, [][]any{{"1", int64(2)}}, 3},
			{"scalar_first_multirow_arguments", "SELECT nextval('ss_attempt') AS n,string_to_table(nextval('ss_attempt')::text||',x',',') AS v", []string{"n", "v"}, []string{"INT8", "TEXT"}, [][]any{{int64(1), "2"}, {int64(3), "x"}}, 4},
			{"srf_first_multirow_arguments", "SELECT string_to_table(nextval('ss_attempt')::text||',x',',') AS v,nextval('ss_attempt') AS n", []string{"v", "n"}, []string{"TEXT", "INT8"}, [][]any{{"1", int64(2)}, {"x", int64(3)}}, 4},
			{"empty_argument_attempt", "SELECT nextval('ss_attempt') AS n,string_to_table(split_part(nextval('ss_attempt')::text,',',2),',') AS v", []string{"n", "v"}, []string{"INT8", "TEXT"}, nil, 2},
			{"wrapped_argument_upper_owner", "SELECT nextval('ss_attempt') AS n,upper(string_to_table(nextval('ss_attempt')::text,',')) AS v", []string{"n", "v"}, []string{"INT8", "TEXT"}, [][]any{{int64(2), "1"}}, 2},
			{"effective_shallow_scalar_effects", "SELECT nextval('ss_attempt')::text||string_to_table('x,y',',') AS shallow,string_to_table(string_to_table('a:b,c:d',','),':') AS deep", []string{"shallow", "deep"}, []string{"TEXT", "TEXT"}, [][]any{{"1x", "a"}, {"2x", "b"}, {"4y", "c"}, {"5y", "d"}}, 6},
			{"effective_shallow_upper_control", "SELECT nextval('ss_attempt')::text||string_to_table('x,y',',') AS shallow,upper(string_to_table(string_to_table('a:b,c:d',','),':')) AS deep", []string{"shallow", "deep"}, []string{"TEXT", "TEXT"}, [][]any{{"1x", "A"}, {"2x", "B"}, {"3y", "C"}, {"4y", "D"}}, 4},
		} {
			t.Run(tc.name, func(t *testing.T) {
				statement, err := c.PrepareContext(p.Ctx, tc.query)
				if err != nil {
					t.Fatal(err)
				}
				defer statement.Close()
				for reuse := range 2 {
					t.Run([]string{"initial", "reuse"}[reuse], func(t *testing.T) {
						for _, mode := range []struct {
							name     string
							executor fixture.Executor
						}{{"direct", c}, {"prepared", fixture.HeldStatement{c, statement, tc.query}}} {
							t.Run(mode.name, func(t *testing.T) {
								fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT setval('ss_attempt',1,false) AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(1)}}))
								result := fixture.Select(tc.query, tc.names, tc.types, tc.rows)
								result.Bag = true
								fixture.RunStep(t, p.Ctx, mode.executor, b.Recorder, result)
								fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT currval('ss_attempt') AS v", []string{"v"}, []string{"INT8"}, [][]any{{tc.curr}}))
							})
						}
					})
				}
			})
		}
		const repeated = "SELECT replace(string_to_table($1,','),NULL,'x') AS v,string_to_table($2,',') AS w ORDER BY w"
		st, err := c.PrepareContext(p.Ctx, repeated)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		for _, tc := range []struct {
			source, peer string
			rows         [][]any
		}{
			{"a,b", "b,a", [][]any{{nil, "a"}, {nil, "b"}}},
			{"c,d,e", "", nil},
			{"", "z", [][]any{{nil, "z"}}},
			{"a,b", "b,a", [][]any{{nil, "a"}, {nil, "b"}}},
		} {
			s := fixture.Select(repeated, []string{"v", "w"}, []string{"TEXT", "TEXT"}, tc.rows, tc.source, tc.peer)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{c, st, repeated}, b.Recorder, s)
		}
	})
}

func TestReferenceStringSplitClauseComposition(t *testing.T) {
	stringReplaceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			q            string
			names, types []string
			rows         [][]any
			bag          bool
		}{
			{"SELECT count(*) AS c,string_to_table('a,a,b',',') AS v FROM (VALUES(1),(2)) t(n)", []string{"c", "v"}, []string{"INT8", "TEXT"}, [][]any{{int64(2), "a"}, {int64(2), "a"}, {int64(2), "b"}}, true},
			{"SELECT string_to_table('a,a,b',',') AS v,count(*) AS c FROM (VALUES(1),(2)) t(n) GROUP BY v ORDER BY v", []string{"v", "c"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(4)}, {"b", int64(2)}}, false},
			{"SELECT count(*) AS c FROM (VALUES(1),(2)) t(n) GROUP BY string_to_table('a,a,b',',') ORDER BY c", []string{"c"}, []string{"INT8"}, [][]any{{int64(2)}, {int64(4)}}, false},
			{"SELECT string_to_table('b,a,a',',') AS v ORDER BY v", []string{"v"}, []string{"TEXT"}, [][]any{{"a"}, {"a"}, {"b"}}, false},
			{"SELECT string_to_table('b,a',',') AS a,string_to_table('x',',') AS b ORDER BY a", []string{"a", "b"}, []string{"TEXT", "TEXT"}, [][]any{{"a", nil}, {"b", "x"}}, false},
			{"SELECT string_to_table('a,b',',') AS a,string_to_table('x',',') AS b ORDER BY b NULLS LAST", []string{"a", "b"}, []string{"TEXT", "TEXT"}, [][]any{{"a", "x"}, {"b", nil}}, false},
			{"SELECT 9 AS n ORDER BY string_to_table('b,a,a',',')", []string{"n"}, []string{"INT4"}, [][]any{{int64(9)}, {int64(9)}, {int64(9)}}, false},
			{"SELECT DISTINCT string_to_table('b,a,a',',') AS v ORDER BY v", []string{"v"}, []string{"TEXT"}, [][]any{{"a"}, {"b"}}, false},
			{"SELECT string_to_table('b,a,c',',') AS v ORDER BY 1 OFFSET 1 LIMIT 1", []string{"v"}, []string{"TEXT"}, [][]any{{"b"}}, false},
			{"SELECT n,string_to_table('b,a',',') AS v FROM (VALUES(2),(1)) t(n) ORDER BY n LIMIT 3", []string{"n", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "b"}, {int64(1), "a"}, {int64(2), "b"}}, false},
			{"SELECT DISTINCT ON(n) n,string_to_table('a,a,b',',') AS v FROM (VALUES(1),(1)) t(n) ORDER BY n", []string{"n", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(1), "a"}, {int64(1), "b"}}, true},
			{"SELECT DISTINCT ON(n) n,string_to_table('a,a,b',',') AS v FROM (VALUES(1),(1)) t(n) ORDER BY n,v", []string{"n", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}}, false},
			{"SELECT string_to_table('b,a,a',',') AS v,row_number() OVER(ORDER BY string_to_table('b,a,a',',')) AS rn ORDER BY rn", []string{"v", "rn"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(1)}, {"a", int64(2)}, {"b", int64(3)}}, false},
			{"WITH q AS (SELECT string_to_table('a,a,b',',') AS v) SELECT v FROM q UNION ALL SELECT string_to_table('c,c',',') AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"a"}, {"a"}, {"b"}, {"c"}, {"c"}}, true},
			{"SELECT count(v) AS c FROM string_to_table('x,NULL,x',',','NULL') t(v)", []string{"c"}, []string{"INT8"}, [][]any{{int64(2)}}, false},
			{"SELECT count(*) AS c FROM (SELECT string_to_table('a,a,b',',') AS v) q", []string{"c"}, []string{"INT8"}, [][]any{{int64(3)}}, false},
			{"SELECT string_to_table('a,a,b',',') AS v,row_number() OVER(PARTITION BY string_to_table('a,a,b',',') ORDER BY 1) AS n ORDER BY v,n", []string{"v", "n"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(1)}, {"a", int64(2)}, {"b", int64(1)}}, false},
			{"SELECT string_to_table('a,a,b',',') AS v,count(*) AS c FROM (VALUES(1),(2)) t(n) GROUP BY v ORDER BY v,c", []string{"v", "c"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(4)}, {"b", int64(2)}}, false},
			{"SELECT string_to_table('b,a,c',',') AS v ORDER BY v OFFSET 1 LIMIT 1", []string{"v"}, []string{"TEXT"}, [][]any{{"b"}}, false},
			{"SELECT n,string_to_table('a,b',',') AS v,row_number() OVER(ORDER BY n) AS rn FROM (VALUES(2),(1)) t(n) ORDER BY n,v", []string{"n", "v", "rn"}, []string{"INT4", "TEXT", "INT8"}, [][]any{{int64(1), "a", int64(1)}, {int64(1), "b", int64(1)}, {int64(2), "a", int64(2)}, {int64(2), "b", int64(2)}}, false},
		} {
			t.Run(tc.q, func(t *testing.T) {
				s := fixture.Select(tc.q, tc.names, tc.types, tc.rows)
				s.Bag = tc.bag
				stringReplaceBoth(t, p, b, c, s)
			})
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE dst(v text)", 0))
		for _, prepared := range []bool{false, true} {
			for _, s := range []fixture.Step{fixture.Exec("DELETE FROM dst", 0), fixture.Exec("INSERT INTO dst(v) VALUES(string_to_table('a,a,b',','))", 3), fixture.Exec("INSERT INTO dst(v) VALUES(string_to_table('',','))", 0), fixture.Exec("INSERT INTO dst(v) SELECT string_to_table('x,x',',')", 2)} {
				if prepared {
					s = fixture.Prepared(s)
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
			s := fixture.Select("SELECT v FROM dst", []string{"v"}, []string{"TEXT"}, [][]any{{"a"}, {"a"}, {"b"}, {"x"}, {"x"}})
			s.Bag = true
			stringReplaceBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM dst", 5))
		}
		for _, prepared := range []bool{false, true} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO dst VALUES('a'),('b'),('c')", 3))
			for _, s := range []fixture.Step{
				fixture.Exec("UPDATE dst SET v=upper(t.x) FROM string_to_table('a,b',',') AS t(x) WHERE dst.v=t.x", 2),
				fixture.Exec("DELETE FROM dst USING string_to_table('',',') AS t(x) WHERE dst.v=t.x", 0),
				fixture.Exec("DELETE FROM dst USING string_to_table('A,B',',') AS t(x) WHERE dst.v=t.x", 2),
			} {
				if prepared {
					s = fixture.Prepared(s)
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
			stringReplaceBoth(t, p, b, c, fixture.Select("SELECT v FROM dst ORDER BY v", []string{"v"}, []string{"TEXT"}, [][]any{{"c"}}))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM dst", 1))
		}
	})
}
