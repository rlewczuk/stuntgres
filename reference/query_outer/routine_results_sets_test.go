package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoutineResultsTable(t *testing.T) {
	resultsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		resultsSetSetup(t, p, b, c)
		values := [][][]any{{}, {{int64(1), int64(10)}}, {{int64(1), int64(10)}, {int64(2), int64(20)}, {int64(2), int64(20)}}}
		for n := range 3 {
			resultsBoth(t, p, b, c, "SELECT * FROM rr_table($1::integer) ORDER BY key,val", []string{"key", "val"}, []string{"INT4", "INT4"}, values[n], n)
			one := [][]any{}
			for _, row := range values[n] {
				one = append(one, []any{row[1]})
			}
			resultsBoth(t, p, b, c, "SELECT * FROM rr_one_table($1::integer) ORDER BY item", []string{"item"}, []string{"INT4"}, one, n)
		}
		resultsHeld(t, p, b, c, "SELECT * FROM rr_table($1::integer) ORDER BY key,val", []string{"key", "val"}, []string{"INT4", "INT4"}, [][]any{{0}, {1}, {2}, {0}, {2}}, [][][]any{values[0], values[1], values[2], values[0], values[2]})
		resultsBothMetadata(t, p, b, c, "SELECT proargnames::text AS names,proargmodes::text AS modes,pronargs::integer AS inputs,proretset AS sets FROM pg_proc WHERE proname='rr_table'", []string{"names", "modes", "inputs", "sets"}, []string{"TEXT", "TEXT", "INT4", "BOOL"}, [][]any{{"{\"\",key,val}", "{i,t,t}", int64(1), true}}, []resultsNativeColumn{resultsTextMetadata, resultsTextMetadata, {}, resultsNonNullMetadata})
		resultsBothMetadata(t, p, b, c, "SELECT proargnames::text AS names,proargmodes::text AS modes,proretset AS sets FROM pg_proc WHERE proname='rr_one_table'", []string{"names", "modes", "sets"}, []string{"TEXT", "TEXT", "BOOL"}, [][]any{{"{\"\",item}", "{i,t}", true}}, []resultsNativeColumn{resultsTextMetadata, resultsTextMetadata, resultsNonNullMetadata})
	})
}

func TestReferenceRoutineResultsSets(t *testing.T) {
	resultsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		resultsSetSetup(t, p, b, c)
		resultsSetCheck(t, p, b, c)
		values := [][][]any{{}, {{int64(10)}}, {{int64(10)}, {int64(20)}, {int64(20)}}}
		for _, name := range []string{"rr_set", "rr_wide_set", "rr_one_table"} {
			typ, col := "INT4", name
			if name == "rr_wide_set" {
				typ = "INT8"
			}
			if name == "rr_one_table" {
				col = "item"
			}
			q := "SELECT * FROM " + name + "($1::integer) ORDER BY 1"
			for n := range 3 {
				resultsBoth(t, p, b, c, q, []string{col}, []string{typ}, values[n], n)
			}
			resultsHeld(t, p, b, c, q, []string{col}, []string{typ}, [][]any{{0}, {1}, {2}, {0}, {2}}, [][][]any{values[0], values[1], values[2], values[0], values[2]})
		}
		// No ORDER BY is needed for a bag; duplicate 20 must remain twice.
		s := resultsStep(b, "SELECT rr_set(2) AS v", []string{"v"}, []string{"INT4"}, values[2])
		s.Bag = true
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		for _, shape := range []struct{ name, typ string }{{"rr_record_set", "RECORD"}, {"rr_named_set", ""}, {"rr_table", "RECORD"}} {
			for n := range 3 {
				rows := [][]any{}
				if n >= 1 {
					rows = append(rows, []any{"(1,10)"})
				}
				if n >= 2 {
					rows = append(rows, []any{"(2,20)"}, []any{"(2,20)"})
				}
				s := resultsStep(b, "SELECT "+shape.name+"($1::integer) AS v", []string{"v"}, []string{shape.typ}, rows, n)
				s.Bag = true
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
			}
		}
	})
}

func TestReferenceRoutineResultsFrom(t *testing.T) {
	resultsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		resultsSetSetup(t, p, b, c)
		resultsMultiSetup(t, p, b, c)
		resultsBoth(t, p, b, c, "SELECT a,b FROM rr_table(2) AS f(a,b) ORDER BY a,b", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}, {int64(2), int64(20)}})
		resultsBoth(t, p, b, c, "SELECT v,n FROM rr_set($1::integer) WITH ORDINALITY AS f(v,n) ORDER BY n", []string{"v", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(10), int64(1)}, {int64(20), int64(2)}, {int64(20), int64(3)}}, 2)
		resultsBoth(t, p, b, c, "SELECT key,val,n FROM rr_table(2) WITH ORDINALITY AS f(key,val,n) ORDER BY n", []string{"key", "val", "n"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(1), int64(10), int64(1)}, {int64(2), int64(20), int64(2)}, {int64(2), int64(20), int64(3)}})
		resultsBoth(t, p, b, c, "SELECT l.n,f.key,f.val FROM (VALUES(0),(1),(2)) AS l(n),rr_table(l.n) AS f ORDER BY l.n,f.key,f.val", []string{"n", "key", "val"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(1), int64(10)}, {int64(2), int64(1), int64(10)}, {int64(2), int64(2), int64(20)}, {int64(2), int64(2), int64(20)}})
		resultsBothMetadata(t, p, b, c, "SELECT l.n,f.key,f.val FROM (VALUES(0),(1),(2)) AS l(n) LEFT JOIN LATERAL rr_table(l.n) AS f ON true ORDER BY l.n,f.key,f.val", []string{"n", "key", "val"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(0), nil, nil}, {int64(1), int64(1), int64(10)}, {int64(2), int64(1), int64(10)}, {int64(2), int64(2), int64(20)}, {int64(2), int64(2), int64(20)}}, []resultsNativeColumn{{}, resultsNullableMetadata, resultsNullableMetadata})
		resultsBoth(t, p, b, c, "SELECT f.inc,f.twice,s.v FROM rr_multi(4) AS f CROSS JOIN rr_set(2) AS s(v) ORDER BY s.v", []string{"inc", "twice", "v"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(5), int64(8), int64(10)}, {int64(5), int64(8), int64(20)}, {int64(5), int64(8), int64(20)}})
	})
}
