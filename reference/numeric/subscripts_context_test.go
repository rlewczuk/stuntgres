package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSubscriptsComposition(t *testing.T) {
	subscriptsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			q           string
			cols, types []string
			rows        [][]any
			bag         bool
		}{
			{"SELECT generate_subscripts(" + subscriptsMatrix + ",1) AS a,generate_subscripts(" + subscriptsMatrix + ",2) AS b", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(-1), int64(3)}, {int64(0), int64(4)}, {nil, int64(5)}}, true},
			{"SELECT generate_subscripts(NULL::integer[],1) AS a,generate_series(10,11) AS b", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{nil, int64(10)}, {nil, int64(11)}}, true},
			{"SELECT generate_subscripts(" + subscriptsMatrix + ",generate_series(1,2)) AS s", []string{"s"}, []string{"INT4"}, seriesRows(-1, 0, 3, 4, 5), true},
			{"SELECT generate_subscripts('{1,2}'::integer[],1)+10 AS s", []string{"s"}, []string{"INT4"}, seriesRows(11, 12), true},
			{"SELECT generate_subscripts(a,1) AS s FROM (VALUES('{1,2}'::integer[]),('{1,2}'::integer[])) v(a)", []string{"s"}, []string{"INT4"}, seriesRows(1, 2, 1, 2), true},
			{"SELECT generate_subscripts('{1,2}'::integer[],1) AS s WHERE false", []string{"s"}, []string{"INT4"}, nil, true},
			{"SELECT DISTINCT generate_subscripts('{1,2}'::integer[],1) AS s FROM (VALUES(1),(1)) v(n)", []string{"s"}, []string{"INT4"}, seriesRows(1, 2), true},
			{"SELECT generate_subscripts('{1,2}'::integer[],1) AS s,count(*) AS n FROM (VALUES(1),(2)) v(n) GROUP BY s", []string{"s", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(2)}, {int64(2), int64(2)}}, true},
			{"SELECT DISTINCT ON(s) generate_subscripts('{1,2}'::integer[],1) AS s FROM (VALUES(1),(1)) v(n) ORDER BY s", []string{"s"}, []string{"INT4"}, seriesRows(1, 2), false},
			{"SELECT 7 AS n ORDER BY generate_subscripts('{1,2}'::integer[],1)", []string{"n"}, []string{"INT4"}, seriesRows(7, 7), false},
			{"SELECT row_number() OVER(PARTITION BY generate_subscripts('{1,2}'::integer[],1)) AS n", []string{"n"}, []string{"INT8"}, seriesRows(1, 1), true},
			{"SELECT row_number() OVER(ORDER BY generate_subscripts('{1,2}'::integer[],1)) AS n", []string{"n"}, []string{"INT8"}, seriesRows(1, 2), true},
			{"SELECT CASE WHEN true THEN (SELECT generate_subscripts('{1}'::integer[],1)) ELSE 0 END AS s", []string{"s"}, []string{"INT4"}, seriesRows(1), true},
			{"SELECT generate_subscripts('{1,2}'::integer[],1) AS s LIMIT 0", []string{"s"}, []string{"INT4"}, nil, true},
			{"SELECT generate_subscripts(" + subscriptsMatrix + ",2,true) AS s ORDER BY s DESC OFFSET 1 LIMIT 2", []string{"s"}, []string{"INT4"}, seriesRows(4, 3), false},
		} {
			t.Run(tc.q, func(t *testing.T) {
				s := fixture.Select(tc.q, tc.cols, tc.types, tc.rows)
				s.Bag = tc.bag
				seriesBoth(t, p, b, c, s)
			})
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE SEQUENCE subscripts_effect START 10", 0))
		for _, tc := range []struct {
			q     string
			value int64
		}{
			{"SELECT generate_subscripts(a,nextval('subscripts_effect')::integer) AS s FROM (VALUES(NULL::integer[])) v(a)", 11},
			{"SELECT generate_subscripts('{1}'::integer[],nextval('subscripts_effect')::integer) AS s LIMIT 0", 10},
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT setval('subscripts_effect',10,true) AS v", []string{"v"}, []string{"INT8"}, seriesRows(10)))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(tc.q, []string{"s"}, []string{"INT4"}, nil))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT currval('subscripts_effect') AS v", []string{"v"}, []string{"INT8"}, seriesRows(tc.value)))
		}
	})
}

func TestReferenceSubscriptsRestrictions(t *testing.T) {
	const call = "generate_subscripts('{1,2}'::integer[],1)"
	const lateral = "You might be able to move the set-returning function into a LATERAL FROM item."
	subscriptsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE subscripts_owner(v integer)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO subscripts_owner VALUES(99)", 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("PREPARE subscripts_exec(integer) AS SELECT $1", 0))
		for _, tc := range []struct{ q, owner, hint string }{
			{"SELECT 1 WHERE " + call + ">0", "WHERE", ""},
			{"SELECT count(*) FROM subscripts_owner HAVING " + call + ">0", "HAVING", ""},
			{"SELECT a.v FROM subscripts_owner a JOIN subscripts_owner b ON " + call + ">0", "JOIN conditions", ""},
			{"SELECT count(*) FILTER(WHERE " + call + ">0) FROM subscripts_owner", "FILTER", ""},
			{"UPDATE subscripts_owner SET v=" + call, "UPDATE", ""},
			{"SELECT 1 LIMIT " + call, "LIMIT", ""}, {"SELECT 1 OFFSET " + call, "OFFSET", ""},
			{"CREATE TABLE subscripts_default(v integer DEFAULT " + call + ")", "DEFAULT expressions", ""},
			{"CREATE TABLE subscripts_check(v integer CHECK(" + call + ">0))", "check constraints", ""},
			{"CREATE TABLE subscripts_generated(v integer GENERATED ALWAYS AS (" + call + ") STORED)", "column generation expressions", ""},
			{"CREATE INDEX subscripts_idx ON subscripts_owner((" + call + "))", "index expressions", ""},
			{"CREATE INDEX subscripts_pred ON subscripts_owner(v) WHERE " + call + ">0", "index predicates", ""},
			{"EXECUTE subscripts_exec(" + call + ")", "EXECUTE parameters", ""},
			{"CREATE POLICY subscripts_policy ON subscripts_owner USING(" + call + ">0)", "policy expressions", ""},
			{"SELECT row_number() OVER(ROWS " + call + " PRECEDING)", "window definitions", ""},
			{"SELECT CASE WHEN false THEN " + call + " ELSE 0 END", "CASE", lateral},
			{"SELECT COALESCE(1," + call + ")", "COALESCE", lateral},
		} {
			t.Run(tc.q, func(t *testing.T) {
				q := "/*é*/ " + tc.q
				d := seriesDiagnostic(q, "generate_subscripts", "0A000", "set-returning functions are not allowed in "+tc.owner, tc.hint)
				if tc.owner == "policy expressions" {
					d.Position = ""
				}
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic(q, d)
					s.Prepared = prepared
					s.SelectRows = strings.HasPrefix(tc.q, "SELECT")
					s.ErrorStage = "execute"
					if prepared && (s.SelectRows || strings.HasPrefix(tc.q, "UPDATE")) {
						s.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
			})
		}
		for _, tc := range []struct{ q, msg string }{
			{"SELECT sum(" + call + ")", "aggregate function calls cannot contain set-returning function calls"},
			{"SELECT sum(" + call + ") OVER()", "window function calls cannot contain set-returning function calls"},
		} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(tc.q, seriesDiagnostic(tc.q, "generate_subscripts", "0A000", tc.msg, lateral))
				s.SelectRows, s.Prepared = true, prepared
				s.ErrorStage = map[bool]string{false: "execute", true: "prepare"}[prepared]
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
		}
		const bad = "SELECT 1 WHERE generate_subscripts(1,1)>0"
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic(bad, seriesDiagnostic(bad, "generate_subscripts", "42883", "function generate_subscripts(integer, integer) does not exist", "No function matches the given name and argument types. You might need to add explicit type casts."))
			s.SelectRows, s.Prepared = true, prepared
			s.ErrorStage = map[bool]string{false: "execute", true: "prepare"}[prepared]
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM subscripts_owner", []string{"v"}, []string{"INT4"}, seriesRows(99)))
	})
}

func TestReferenceSubscriptsBoundaries(t *testing.T) {
	subscriptsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			q           string
			cols, types []string
			rows        [][]any
		}{
			{"SELECT s FROM generate_subscripts('{1,2}'::integer[],1) g(s) ORDER BY s", []string{"s"}, []string{"INT4"}, seriesRows(1, 2)},
			{"SELECT s,n FROM generate_subscripts('{1,2}'::integer[],1,true) WITH ORDINALITY g(s,n) ORDER BY n", []string{"s", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(2), int64(1)}, {int64(1), int64(2)}}},
			{"SELECT s FROM ROWS FROM(generate_subscripts('{1,2}'::integer[],1)) g(s) ORDER BY s", []string{"s"}, []string{"INT4"}, seriesRows(1, 2)},
			{"SELECT s FROM (VALUES('{1,2}'::integer[])) v(a) CROSS JOIN LATERAL generate_subscripts(a,1) g(s) ORDER BY s", []string{"s"}, []string{"INT4"}, seriesRows(1, 2)},
		} {
			t.Run(tc.q, func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					s := fixture.Select(tc.q, tc.cols, tc.types, tc.rows)
					if !b.PG {
						s = fixture.ErrorDiagnostic(tc.q, seriesDiagnostic(tc.q, "generate_subscripts", "0A000", "functions in FROM are unsupported", ""))
						s.SelectRows = true
						s.ErrorStage = map[bool]string{false: "execute", true: "prepare"}[prepared]
					}
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
			})
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE subscripts_values_boundary(s integer)", 0))
		const q = "INSERT INTO subscripts_values_boundary VALUES(generate_subscripts('{1,2}'::integer[],1))"
		for _, prepared := range []bool{false, true} {
			s := fixture.Exec(q, 2)
			if !b.PG {
				s = fixture.ErrorDiagnostic(q, seriesDiagnostic(q, "generate_subscripts", "0A000", "generate_subscripts is unsupported in INSERT VALUES", ""))
				s.ErrorStage = map[bool]string{false: "execute", true: "prepare"}[prepared]
			}
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		want := seriesRows(1, 1, 2, 2)
		if !b.PG {
			want = nil
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT s FROM subscripts_values_boundary ORDER BY s", []string{"s"}, []string{"INT4"}, want))
	})
}
