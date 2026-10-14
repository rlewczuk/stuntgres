package reference_test

import (
	"database/sql"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceSeriesComposition(t *testing.T) {
	seriesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			q           string
			cols, types []string
			rows        [][]any
		}{
			{"SELECT generate_series(1,3) AS a,generate_series(10,11) AS b", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(11)}, {int64(3), nil}}},
			{"SELECT generate_series(NULL,3) AS a,generate_series(10::bigint,11::bigint) AS b", []string{"a", "b"}, []string{"INT4", "INT8"}, [][]any{{nil, int64(10)}, {nil, int64(11)}}},
			{"SELECT generate_series(NULL,3) AS a,generate_series(3,1) AS b", []string{"a", "b"}, []string{"INT4", "INT4"}, nil},
			{"SELECT generate_series(1,generate_series(1,3)) AS v", []string{"v"}, []string{"INT4"}, seriesRows(1, 1, 2, 1, 2, 3)},
			{"SELECT generate_series(1,generate_series(1,2)) AS a,generate_series(10,11) AS b", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(1), int64(11)}, {int64(2), int64(11)}}},
			{"SELECT generate_series(1,3)+10 AS v", []string{"v"}, []string{"INT4"}, seriesRows(11, 12, 13)},
			{"SELECT generate_series(1,3) AS a,string_to_table('x,y',',') AS b", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "x"}, {int64(2), "y"}, {int64(3), nil}}},
			{"SELECT generate_series(1,3) AS a,regexp_split_to_table('x,y',',') AS b", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "x"}, {int64(2), "y"}, {int64(3), nil}}},
			{`SELECT generate_series(1,3) AS a,jsonb_object_keys('{"x":1,"y":2}'::jsonb) AS b`, []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "x"}, {int64(2), "y"}, {int64(3), nil}}},
			{"SELECT id,generate_series(lo,hi) AS v FROM (VALUES(1,1,2),(1,1,2),(2,2,2),(3,NULL,2)) s(id,lo,hi)", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(1), int64(2)}, {int64(1), int64(1)}, {int64(1), int64(2)}, {int64(2), int64(2)}}},
			{"SELECT generate_series(1,3,0) AS v WHERE false", []string{"v"}, []string{"INT4"}, nil},
			{"SELECT DISTINCT generate_series(1,2) AS v FROM (VALUES(1),(1)) s(n)", []string{"v"}, []string{"INT4"}, seriesRows(1, 2)},
			{"SELECT generate_series(1,2) AS v,count(*) AS n FROM (VALUES(1),(2)) s(n) GROUP BY v", []string{"v", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(2)}, {int64(2), int64(2)}}},
			{"SELECT CASE WHEN true THEN (SELECT generate_series(1,1)) ELSE 0 END AS v", []string{"v"}, []string{"INT4"}, seriesRows(1)},
			// Literal NULL strict wrappers may erase the entire set at planning.
			{"SELECT generate_series(1,3)+NULL::integer AS v", []string{"v"}, []string{"INT4"}, [][]any{{nil}}},
		} {
			t.Run(tc.q, func(t *testing.T) {
				s := fixture.Select(tc.q, tc.cols, tc.types, tc.rows)
				s.Bag = true
				seriesBoth(t, p, b, c, s)
			})
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE SEQUENCE series_effect START 10", 0))
		for _, q := range []string{"SELECT generate_series(NULL::bigint,3::bigint,nextval('series_effect')) AS v", "SELECT generate_series(1::bigint,3::bigint,nextval('series_effect')) AS v LIMIT 0"} {
			t.Run(q, func(t *testing.T) {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT setval('series_effect',10,true) AS v", []string{"v"}, []string{"INT8"}, seriesRows(10)))
				s := fixture.Select(q, []string{"v"}, []string{"INT8"}, nil)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				want := int64(11)
				if q[len(q)-7:] == "LIMIT 0" {
					want = 10
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT currval('series_effect') AS v", []string{"v"}, []string{"INT8"}, seriesRows(want)))
			})
		}
	})
}

func TestReferenceSeriesLimit(t *testing.T) {
	seriesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			q, typ string
			rows   [][]any
		}{
			{"SELECT generate_series(1::bigint,9223372036854775807::bigint) AS v LIMIT 3", "INT8", seriesRows(1, 2, 3)},
			{"SELECT generate_series(1,5) AS v ORDER BY v DESC OFFSET 1 LIMIT 2", "INT4", seriesRows(4, 3)},
			{"SELECT generate_series(1,3,0) AS v LIMIT 0", "INT4", nil},
		} {
			t.Run(tc.q, func(t *testing.T) {
				seriesBoth(t, p, b, c, fixture.Select(tc.q, []string{"v"}, []string{tc.typ}, tc.rows))
			})
		}
	})
}

func TestReferenceSeriesRestrictions(t *testing.T) {
	const lateral = "You might be able to move the set-returning function into a LATERAL FROM item."
	seriesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE series_owner(v integer)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO series_owner VALUES(99)", 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("PREPARE series_exec(integer) AS SELECT $1", 0))
		for _, tc := range []struct{ q, owner, hint string }{
			{"SELECT 1 WHERE generate_series(1,2)>0", "WHERE", ""},
			{"SELECT count(*) FROM series_owner HAVING generate_series(1,2)>0", "HAVING", ""},
			{"SELECT a.v FROM series_owner a JOIN series_owner b ON generate_series(1,2)>0", "JOIN conditions", ""},
			{"SELECT count(*) FILTER(WHERE generate_series(1,2)>0) FROM series_owner", "FILTER", ""},
			{"UPDATE series_owner SET v=generate_series(1,2)", "UPDATE", ""},
			{"SELECT 1 LIMIT generate_series(1,2)", "LIMIT", ""},
			{"SELECT 1 OFFSET generate_series(1,2)", "OFFSET", ""},
			{"CREATE TABLE series_default(v integer DEFAULT generate_series(1,2))", "DEFAULT expressions", ""},
			{"CREATE TABLE series_generated(v integer GENERATED ALWAYS AS (generate_series(1,2)) STORED)", "column generation expressions", ""},
			{"CREATE INDEX series_idx ON series_owner((generate_series(1,2)))", "index expressions", ""},
			{"CREATE INDEX series_pred ON series_owner(v) WHERE generate_series(1,2)>0", "index predicates", ""},
			{"EXECUTE series_exec(generate_series(1,2))", "EXECUTE parameters", ""},
			{"CREATE POLICY series_policy ON series_owner USING(generate_series(1,2)>0)", "policy expressions", ""},
			{"SELECT row_number() OVER(ROWS generate_series(1,2) PRECEDING)", "window definitions", ""},
			{"SELECT CASE WHEN false THEN generate_series(1,2) ELSE 0 END", "CASE", lateral},
			{"SELECT COALESCE(1,generate_series(1,2))", "COALESCE", lateral},
			{"SELECT CASE WHEN true THEN (SELECT generate_series(1,1)) ELSE generate_series(1,2) END", "CASE", lateral},
		} {
			t.Run(tc.q, func(t *testing.T) {
				q := "/*é*/ " + tc.q
				d := seriesDiagnostic(q, "generate_series", "0A000", "set-returning functions are not allowed in "+tc.owner, tc.hint)
				if tc.owner == "policy expressions" {
					d.Position = ""
				}
				if tc.owner == "CASE" || tc.owner == "COALESCE" {
					d.Message = "set-returning functions are not allowed in " + tc.owner
				}
				s := fixture.ErrorDiagnostic(q, d)
				seriesBoth(t, p, b, c, s)
			})
		}
		for _, tc := range []struct {
			q    string
			rows [][]any
		}{
			{"SELECT row_number() OVER(PARTITION BY generate_series(1,2)) AS n", seriesRows(1, 1)},
			{"SELECT row_number() OVER(ORDER BY generate_series(1,2)) AS n", seriesRows(1, 2)},
		} {
			t.Run(tc.q, func(t *testing.T) {
				s := fixture.Select(tc.q, []string{"n"}, []string{"INT8"}, tc.rows)
				s.Bag = true
				seriesBoth(t, p, b, c, s)
			})
		}
		for _, tc := range []struct{ q, message string }{
			{"SELECT sum(generate_series(1,2))", "aggregate function calls cannot contain set-returning function calls"},
			{"SELECT sum(generate_series(1,2)) OVER()", "window function calls cannot contain set-returning function calls"},
		} {
			t.Run(tc.q, func(t *testing.T) {
				s := fixture.ErrorDiagnostic(tc.q, seriesDiagnostic(tc.q, "generate_series", "0A000", tc.message, lateral))
				seriesBoth(t, p, b, c, s)
			})
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM series_owner", []string{"v"}, []string{"INT4"}, seriesRows(99)))
	})
}

func TestReferenceSeriesBoundaries(t *testing.T) {
	// Numeric/temporal providers retain explicit native capability boundaries.
	seriesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			q           string
			cols, types []string
			rows        [][]any
		}{
			{"SELECT v FROM generate_series(1,3) s(v) ORDER BY v", []string{"v"}, []string{"INT4"}, seriesRows(1, 2, 3)},
			{"SELECT v,n FROM generate_series(1,2) WITH ORDINALITY s(v,n) ORDER BY n", []string{"v", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}}},
			{"SELECT v FROM ROWS FROM(generate_series(1,2)) s(v) ORDER BY v", []string{"v"}, []string{"INT4"}, seriesRows(1, 2)},
			{"SELECT v FROM (VALUES(2)) s(n) CROSS JOIN LATERAL generate_series(1,s.n) g(v) ORDER BY v", []string{"v"}, []string{"INT4"}, seriesRows(1, 2)},
			{"SELECT generate_series(1.0,3) AS v ORDER BY v", []string{"v"}, []string{"NUMERIC"}, [][]any{{"1.0"}, {"2.0"}, {"3.0"}}},
			{"SELECT generate_series('2020-01-01'::timestamp,'2020-01-02'::timestamp,'1 day'::interval)::text AS v ORDER BY v", []string{"v"}, []string{"TEXT"}, [][]any{{"2020-01-01 00:00:00"}, {"2020-01-02 00:00:00"}}},
			{"SELECT generate_series('2020-01-01 UTC'::timestamptz,'2020-01-02 UTC'::timestamptz,'1 day'::interval,'UTC')::text AS v ORDER BY v", []string{"v"}, []string{"TEXT"}, [][]any{{"2020-01-01 00:00:00+00"}, {"2020-01-02 00:00:00+00"}}},
			{"SELECT generate_series(DATE '2020-01-01',DATE '2020-01-02',INTERVAL '1 day') AS v ORDER BY v", []string{"v"}, []string{"TIMESTAMPTZ"}, [][]any{{time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)}, {time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC)}}},
		} {
			t.Run(tc.q, func(t *testing.T) {
				if tc.types[0] == "TIMESTAMPTZ" {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET TIME ZONE 'UTC'", 0))
				}
				s := fixture.Select(tc.q, tc.cols, tc.types, tc.rows)
				if !b.PG && tc.types[0] != "INT4" {
					message := ""
					if tc.types[0] == "NUMERIC" {
						message = "generate_series with numeric arguments is unsupported"
					} else if tc.types[0] == "TEXT" || tc.types[0] == "TIMESTAMPTZ" {
						message = "generate_series with temporal arguments is unsupported"
					}
					s = fixture.ErrorDiagnostic(tc.q, seriesDiagnostic(tc.q, "generate_series", "0A000", message, ""))
					s.SelectRows = true
				}
				seriesBoth(t, p, b, c, s)
			})
		}
	})
}
