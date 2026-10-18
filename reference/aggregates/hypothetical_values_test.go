package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceHypotheticalAggregateValues(t *testing.T) {
	hypotheticalScenario(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, b fixture.Backend) {
		hypotheticalSetup(t, p, c, b)
		for _, tc := range []struct {
			direct, order string
			row           []any
		}{
			{"5", "v", hypotheticalRow(1, 1, 0, .2)},
			{"20", "v", hypotheticalRow(2, 2, .25, .8)},
			{"30", "v", hypotheticalRow(4, 3, .75, .8)},
			{"50", "v", hypotheticalRow(5, 4, 1, 1)},
			{"20", "v DESC", hypotheticalRow(2, 2, .25, .8)},
			{"30", "v DESC", hypotheticalRow(2, 2, .25, .4)},
		} {
			orderedSetBoth(t, p, c, b, hypotheticalResult("SELECT "+hypotheticalProjection(tc.direct, tc.order, "")+" FROM hs_input", tc.row))
		}
		for _, tail := range []string{" WHERE FALSE", ""} {
			filter := ""
			if tail == "" {
				filter = " FILTER(WHERE FALSE)"
			}
			orderedSetBoth(t, p, c, b, hypotheticalResult("SELECT "+hypotheticalProjection("20", "v", filter)+" FROM hs_input"+tail, hypotheticalRow(1, 1, 0, 1)))
		}
		orderedSetBoth(t, p, c, b, hypotheticalResult("SELECT "+hypotheticalProjection("20", "v", " FILTER(WHERE NULL)")+" FROM hs_input", hypotheticalRow(1, 1, 0, 1)))
		orderedSetBoth(t, p, c, b, hypotheticalResult("SELECT "+hypotheticalProjection("20", "v", "")+" FROM hs_input WHERE FALSE GROUP BY v"))
		orderedSetBoth(t, p, c, b, hypotheticalResult("SELECT "+hypotheticalProjection("20", "v", "")+" FROM hs_input HAVING FALSE"))
		orderedSetBoth(t, p, c, b, hypotheticalResult("SELECT "+hypotheticalProjection("20", "10", ""), hypotheticalRow(2, 2, 1, 1)))
		orderedSetBoth(t, p, c, b, fixture.Select("SELECT rank(20) WITHIN GROUP(ORDER BY v) AS a,rank(30) WITHIN GROUP(ORDER BY v) AS b,rank(30) WITHIN GROUP(ORDER BY v DESC) AS c,dense_rank(30) WITHIN GROUP(ORDER BY v) AS d FROM hs_input", []string{"a", "b", "c", "d"}, []string{"INT8", "INT8", "INT8", "INT8"}, [][]any{{int64(2), int64(4), int64(2), int64(3)}}))
	})
}

func TestReferenceHypotheticalAggregateNulls(t *testing.T) {
	hypotheticalScenario(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, b fixture.Backend) {
		for _, tc := range []struct {
			values, direct, order string
			row                   []any
		}{
			{"(1),(2),(2),(NULL)", "2", "v ASC NULLS LAST", hypotheticalRow(2, 2, .25, .8)},
			{"(1),(2),(2),(NULL)", "NULL::int", "v", hypotheticalRow(4, 3, .75, 1)},
			{"(1),(2),(2),(NULL)", "NULL::int", "v ASC NULLS FIRST", hypotheticalRow(1, 1, 0, .4)},
			{"(1),(2),(2),(NULL)", "NULL::int", "v DESC", hypotheticalRow(1, 1, 0, .4)},
			{"(NULL::int),(NULL),(NULL)", "NULL::int", "v", hypotheticalRow(1, 1, 0, 1)},
			{"(NULL::int),(NULL),(NULL)", "2", "v NULLS LAST", hypotheticalRow(1, 1, 0, .25)},
			{"(NULL::int),(NULL),(NULL)", "2", "v NULLS FIRST", hypotheticalRow(4, 2, 1, 1)},
		} {
			q := "SELECT " + hypotheticalProjection(tc.direct, tc.order, "") + " FROM (VALUES " + tc.values + ") AS a(v)"
			orderedSetBoth(t, p, c, b, hypotheticalResult(q, tc.row))
		}
	})
}

func TestReferenceHypotheticalAggregateMultiKey(t *testing.T) {
	hypotheticalScenario(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, b fixture.Backend) {
		const source = " FROM (VALUES (1,'a'::text),(1,'b'),(1,'b'),(2,'a'),(NULL,'z')) AS a(x,y)"
		for _, tc := range []struct {
			direct, order string
			row           []any
		}{
			{"1,'b'", "x,y", hypotheticalRow(2, 2, .2, 4.0/6)},
			{"1,'c'", "x,y", hypotheticalRow(4, 3, .6, 4.0/6)},
			{"1,'b'", "x,y DESC", hypotheticalRow(1, 1, 0, .5)},
			{"NULL::int,'z'", "x,y", hypotheticalRow(5, 4, .8, 1)},
		} {
			orderedSetBoth(t, p, c, b, hypotheticalResult("SELECT "+hypotheticalProjection(tc.direct, tc.order, "")+source, tc.row))
		}
		const triples = " FROM (VALUES (1,2,3),(1,2,3),(1,2,NULL),(1,1,9),(NULL,2,3),(NULL,NULL,NULL)) AS a(x,y,z)"
		orderedSetBoth(t, p, c, b, hypotheticalResult("SELECT "+hypotheticalProjection("1,2,3", "x NULLS LAST,y DESC NULLS FIRST,z NULLS FIRST", "")+triples, hypotheticalRow(2, 2, 1.0/6, 4.0/7)))
		orderedSetBoth(t, p, c, b, hypotheticalResult("SELECT "+hypotheticalProjection("1,2,1", "x,y DESC,x", "")+triples, hypotheticalRow(1, 1, 0, 4.0/7)))
		orderedSetBoth(t, p, c, b, hypotheticalResult("SELECT "+hypotheticalProjection("NULL::int,NULL::int", "x NULLS FIRST,y NULLS FIRST", "")+" FROM (VALUES (NULL::int,NULL::int),(NULL,1),(1,NULL)) AS a(x,y)", hypotheticalRow(1, 1, 0, .5)))
		// Aggregates take at most 99 arguments. PostgreSQL 18.6 rejects 50
		// pairs at analysis (18.0 accepted them and crashed on execution).
		parts := make([]string, 49)
		for i := range parts {
			parts[i] = strconv.Itoa(i + 1)
		}
		keys := strings.Join(parts, ",")
		orderedSetBoth(t, p, c, b, hypotheticalResult("SELECT "+hypotheticalProjection(keys, keys, ""), hypotheticalRow(1, 1, 0, 1)))
		keys += ",50"
		for _, name := range []string{"rank", "dense_rank", "percent_rank", "cume_dist"} {
			q := "SELECT " + name + "(" + keys + ") WITHIN GROUP(ORDER BY " + keys + ")"
			orderedSetBoth(t, p, c, b, orderedSetDiagnostic(q, "54023", "aggregates cannot have more than 99 arguments", name, ""))
		}
		tooMany := strings.TrimSuffix(strings.Repeat("1,", 51), ",")
		q := "SELECT rank(" + tooMany + ") WITHIN GROUP(ORDER BY " + tooMany + ")"
		orderedSetBoth(t, p, c, b, orderedSetDiagnostic(q, "54023", "cannot pass more than 100 arguments to a function", "rank", ""))
	})
}
