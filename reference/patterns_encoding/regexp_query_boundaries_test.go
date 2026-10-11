package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

// Placement restrictions belong to a lexical query, not to child SELECTs.
// REL_18_0 parse_expr.c transforms SubLinks with a separate ParseState; the
// parent's CASE/COALESCE checks inspect only its own p_last_srf.
func TestReferenceRegexpQueryBoundaryScalars(t *testing.T) {
	regexpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, c := range []struct {
			name, q, column, typ string
			value                any
		}{
			{"case_then", `SELECT CASE WHEN true THEN (SELECT regexp_split_to_table('a',' ')) ELSE 'x' END AS x`, "x", "TEXT", "a"},
			{"case_else", `SELECT CASE WHEN false THEN 'x' ELSE (SELECT regexp_split_to_table('a',' ')) END AS x`, "x", "TEXT", "a"},
			{"coalesce", `SELECT COALESCE((SELECT regexp_split_to_table('a',' ')),'x') AS x`, "x", "TEXT", "a"},
			{"nested_queries", `SELECT CASE WHEN true THEN (SELECT COALESCE((SELECT regexp_split_to_table('a',' ')),'x')) ELSE 'y' END AS x`, "x", "TEXT", "a"},
			{"case_zero", `SELECT CASE WHEN true THEN (SELECT regexp_split_to_table(NULL::text,' ')) ELSE 'x' END AS x`, "x", "TEXT", nil},
			{"coalesce_zero", `SELECT COALESCE((SELECT regexp_split_to_table(NULL::text,' ')),'x') AS x`, "x", "TEXT", "x"},
			{"case_no_match", `SELECT CASE WHEN true THEN (SELECT regexp_matches('x','a')) ELSE NULL::text[] END AS m`, "m", "_TEXT", nil},
			{"coalesce_no_match", `SELECT COALESCE((SELECT regexp_matches('x','a')),ARRAY['fallback']) AS m`, "m", "_TEXT", "{fallback}"},
			{"case_capture", `SELECT CASE WHEN true THEN (SELECT regexp_matches('ab','(a)(b)')) ELSE NULL::text[] END AS m`, "m", "_TEXT", "{a,b}"},
			{"correlated_child", `SELECT CASE WHEN true THEN (SELECT regexp_split_to_table(v.s,' ')) ELSE 'x' END AS x FROM (VALUES('a')) v(s)`, "x", "TEXT", "a"},
		} {
			t.Run(c.name, func(t *testing.T) {
				regexpRoutes(t, p, b, fixture.Select(c.q, []string{c.column}, []string{c.typ}, [][]any{{c.value}}))
			})
		}
		for _, q := range []string{
			`SELECT CASE WHEN true THEN (SELECT regexp_matches('aa','a','g')) ELSE NULL::text[] END AS m`,
			`SELECT COALESCE((SELECT regexp_matches('aa','a','g')),ARRAY['fallback']) AS m`,
			`SELECT CASE WHEN true THEN (SELECT regexp_split_to_table('a b',' ')) ELSE 'x' END AS x`,
			`SELECT COALESCE((SELECT regexp_split_to_table('a b',' ')),'x') AS x`,
		} {
			t.Run(q, func(t *testing.T) {
				regexpRoutes(t, p, b, regexpRuntimeError(q, "21000", "more than one row returned by a subquery used as an expression", ""))
				regexpRoutes(t, p, b, fixture.Select(`SELECT COALESCE((SELECT regexp_split_to_table('ok',' ')),'x') AS x`, []string{"x"}, []string{"TEXT"}, [][]any{{"ok"}}))
			})
		}
	})
}

func TestReferenceRegexpQueryBoundaryFilterValues(t *testing.T) {
	regexpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, c := range []struct {
			name, q string
			n       int64
		}{
			{"filter_match", `SELECT count(*) FILTER(WHERE EXISTS(SELECT regexp_matches('a','a'))) AS n`, 1},
			{"filter_no_match", `SELECT count(*) FILTER(WHERE EXISTS(SELECT regexp_matches('x','a'))) AS n`, 0},
			{"filter_split", `SELECT count(*) FILTER(WHERE EXISTS(SELECT regexp_split_to_table('a b',' '))) AS n`, 1},
			{"filter_null", `SELECT count(*) FILTER(WHERE EXISTS(SELECT regexp_split_to_table(NULL::text,' '))) AS n`, 0},
		} {
			t.Run(c.name, func(t *testing.T) {
				s := fixture.Select(c.q, []string{"n"}, []string{"INT8"}, [][]any{{c.n}})
				s.CheckMetadata = regexpMetadata(b.PG, "n")
				regexpRoutes(t, p, b, s)
			})
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(`CREATE TABLE boundary_values(s TEXT)`, 0))
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("prepared=%t", prepared), func(t *testing.T) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
				q := `INSERT INTO boundary_values VALUES((SELECT regexp_split_to_table('a',' '))),((SELECT regexp_split_to_table('b',' ')))`
				s := fixture.Exec(q, 2)
				s.Prepared = prepared
				run(s)
				run(fixture.Select(`SELECT s FROM boundary_values ORDER BY s`, []string{"s"}, []string{"TEXT"}, [][]any{{"a"}, {"b"}}))
				run(fixture.Exec(`DELETE FROM boundary_values`, 2))

				// Zero child rows mean one SQLNULL scalar cell, not loss of an
				// outer VALUES row. Each outer row still inserts exactly once.
				q = `INSERT INTO boundary_values VALUES((SELECT regexp_split_to_table(NULL::text,' '))),((SELECT regexp_split_to_table('c',' ')))`
				s = fixture.Exec(q, 2)
				s.Prepared = prepared
				run(s)
				run(fixture.Select(`SELECT s FROM boundary_values ORDER BY s NULLS LAST`, []string{"s"}, []string{"TEXT"}, [][]any{{"c"}, {nil}}))
				run(fixture.Exec(`DELETE FROM boundary_values`, 2))

				// A later multirow scalar child fails atomically: the earlier
				// legal scalar cell must not leave a partially inserted row.
				q = `INSERT INTO boundary_values VALUES((SELECT regexp_split_to_table('a',' '))),((SELECT regexp_split_to_table('b c',' ')))`
				s = regexpRuntimeError(q, "21000", "more than one row returned by a subquery used as an expression", "")
				s.SelectRows = false
				s.Prepared = prepared
				run(s)
				run(fixture.Select(`SELECT s FROM boundary_values`, []string{"s"}, []string{"TEXT"}, nil))
			})
		}
	})
}

func TestReferenceRegexpQueryBoundaryRestrictions(t *testing.T) {
	regexpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(`CREATE TABLE boundary_restrictions(s TEXT)`, 0))
		const hint = "You might be able to move the set-returning function into a LATERAL FROM item."
		for _, c := range []struct {
			name, q, context, hint, token string
			insert                        bool
		}{
			{"case_restore", `SELECT /* é🙂 */ CASE WHEN true THEN (SELECT regexp_split_to_table('a',' ')) ELSE regexp_split_to_table('b',' ') END AS x`, "CASE", hint, "regexp_split_to_table('b'", false},
			{"coalesce_restore", `SELECT /* é🙂 */ COALESCE((SELECT regexp_split_to_table('a',' ')),regexp_split_to_table('b',' ')) AS x`, "COALESCE", hint, "regexp_split_to_table('b'", false},
			{"filter_restore", `SELECT /* é🙂 */ count(*) FILTER(WHERE EXISTS(SELECT regexp_matches('a','a')) OR regexp_matches('b','b') IS NOT NULL) AS n`, "FILTER", "", "regexp_matches('b'", false},
			{"values_restore", `INSERT /* é🙂 */ INTO boundary_restrictions VALUES((SELECT regexp_split_to_table('a',' '))),(regexp_split_to_table('b',' '))`, "VALUES", "", "regexp_split_to_table('b'", true},
		} {
			t.Run(c.name, func(t *testing.T) {
				s := regexpBindError(c.q, "0A000", "set-returning functions are not allowed in "+c.context, c.hint, c.token)
				s.SelectRows = !c.insert
				regexpRoutes(t, p, b, s)
				regexpRoutes(t, p, b, fixture.Select(`SELECT CASE WHEN true THEN (SELECT regexp_split_to_table('ok',' ')) ELSE 'x' END AS x`, []string{"x"}, []string{"TEXT"}, [][]any{{"ok"}}))
			})
		}
		regexpRoutes(t, p, b, fixture.Select(`SELECT s FROM boundary_restrictions`, []string{"s"}, []string{"TEXT"}, nil))
	})
}
