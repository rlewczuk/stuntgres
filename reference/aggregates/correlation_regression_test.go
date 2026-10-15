package reference_test

import (
	"database/sql"
	"fmt"
	"math"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

var correlationNames = []string{"corr", "covar_pop", "covar_samp", "regr_avgx", "regr_avgy", "regr_count", "regr_intercept", "regr_r2"}
var correlationTypes = []string{"FLOAT8", "FLOAT8", "FLOAT8", "FLOAT8", "FLOAT8", "INT8", "FLOAT8", "FLOAT8"}
var correlationKnown = []any{float64(1), float64(4.0 / 3), float64(2), float64(2), float64(5), int64(3), float64(1), float64(1)}
var correlationEmpty = []any{nil, nil, nil, nil, nil, int64(0), nil, nil}

func correlationScenario(t *testing.T, body func(*testing.T, *fixture.Pair, *sql.Conn, *fixture.Recorder, bool)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := conn.Close(); err != nil {
					t.Error(err)
				}
			})
			body(t, p, conn, backend.Recorder, backend.PG)
		})
	}
}

func correlationProjection(args string) string {
	calls := make([]string, len(correlationNames))
	for i, name := range correlationNames {
		calls[i] = name + "(" + args + ")"
	}
	return strings.Join(calls, ",")
}

func correlationStep(q string, values []any, pg bool) fixture.Step {
	s := fixture.Select(q, correlationNames, correlationTypes, [][]any{values})
	s.CheckMetadata = statisticalMetadata(pg, correlationTypes)
	return s
}

// Only the named nonexact mathematical cells use abs(error) <=
// 1e-12*max(1,abs(center)). NULL, INT8 count and all other cells remain exact.
// SQL intervals preserve raw BOOL/INT8 recorder checks, not a global fuzzy comparator.
func correlationIntervals(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, args, source string, values []any, approximate ...int) {
	t.Helper()
	calls, types, want := make([]string, 8), append([]string{}, correlationTypes...), append([]any{}, values...)
	for i, name := range correlationNames {
		calls[i] = name + "(" + args + ")"
	}
	for _, i := range approximate {
		center := values[i].(float64)
		delta := 1e-12 * math.Max(1, math.Abs(center))
		calls[i] = fmt.Sprintf("(%s BETWEEN (%.17g::double precision-%.17g::double precision) AND (%.17g::double precision+%.17g::double precision))", calls[i], center, delta, center, delta)
		types[i], want[i] = "BOOL", true
	}
	for i := range calls {
		calls[i] += " AS " + correlationNames[i]
	}
	statisticalBoth(t, p, c, r, fixture.Select("SELECT "+strings.Join(calls, ",")+" FROM "+source, correlationNames, types, [][]any{want}))
}

func correlationAcceptance(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
	run(fixture.Exec("CREATE TABLE paired(k INTEGER,y DOUBLE PRECISION,x DOUBLE PRECISION)", 0))
	run(fixture.Exec("INSERT INTO paired VALUES (1,3,1),(2,5,2),(3,7,3),(4,NULL,1000),(5,1000,NULL),(6,NULL,NULL)", 6))
	q := "SELECT " + correlationProjection("y,x ORDER BY k") + " FROM paired"
	statisticalBoth(t, p, c, r, correlationStep(q, correlationKnown, pg))
	for _, suffix := range []string{" WHERE FALSE", " WHERE y IS NULL OR x IS NULL"} {
		statisticalBoth(t, p, c, r, correlationStep(q+suffix, correlationEmpty, pg))
	}
	empty := fixture.Select(q+" WHERE FALSE GROUP BY k", correlationNames, correlationTypes, nil)
	empty.CheckMetadata = statisticalMetadata(pg, correlationTypes)
	statisticalBoth(t, p, c, r, empty)
	for _, tc := range []struct {
		args string
		want []any
	}{
		{"2*x,2", []any{nil, float64(0), float64(0), float64(2), float64(4), int64(3), nil, nil}},
		{"2,x", []any{nil, float64(0), float64(0), float64(2), float64(2), int64(3), float64(2), float64(1)}},
		{"2,2", []any{nil, float64(0), float64(0), float64(2), float64(2), int64(3), nil, nil}},
		{"7,2", []any{nil, float64(0), nil, float64(2), float64(7), int64(1), nil, nil}},
	} {
		suffix := " WHERE k<=3"
		if tc.args == "7,2" {
			suffix = " WHERE k=1"
		}
		statisticalBoth(t, p, c, r, correlationStep("SELECT "+correlationProjection(tc.args+" ORDER BY k")+" FROM paired"+suffix, tc.want, pg))
	}
}

func TestReferenceCorrelationRegressionValues(t *testing.T) {
	correlationScenario(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		correlationAcceptance(t, p, c, r, pg)
		statisticalBoth(t, p, c, r, correlationStep("SELECT "+correlationProjection("-y,x ORDER BY k")+" FROM paired", []any{float64(-1), float64(-4.0 / 3), float64(-2), float64(2), float64(-5), int64(3), float64(-1), float64(1)}, pg))
		statisticalBoth(t, p, c, r, correlationStep("SELECT "+correlationProjection("y,x+1000000000 ORDER BY k")+" FROM paired", []any{float64(1), float64(4.0 / 3), float64(2), float64(1000000002), float64(5), int64(3), float64(-1999999999), float64(1)}, pg))
		statisticalBoth(t, p, c, r, fixture.Select("SELECT regr_count(DISTINCT 2,x),regr_avgx(DISTINCT 2,x),regr_count(ALL y,x) FILTER (WHERE k<=2) FROM paired WHERE k<=2 OR k=3", []string{"regr_count", "regr_avgx", "regr_count"}, []string{"INT8", "FLOAT8", "INT8"}, [][]any{{int64(3), float64(2), int64(2)}}))
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("INSERT INTO paired VALUES (7,2,2),(8,2,2),(9,2,1)", 3))
		statisticalBoth(t, p, c, r, fixture.Select("SELECT regr_count(DISTINCT y,x),regr_avgx(DISTINCT y,x) FROM paired WHERE k>=7", []string{"regr_count", "regr_avgx"}, []string{"INT8", "FLOAT8"}, [][]any{{int64(2), float64(1.5)}}))
		statisticalBoth(t, p, c, r, fixture.Select("SELECT regr_count(y,x),regr_avgy(y,x) FROM paired WHERE k<=3 GROUP BY y HAVING regr_count(y,x)=1 ORDER BY y", []string{"regr_count", "regr_avgy"}, []string{"INT8", "FLOAT8"}, [][]any{{int64(1), float64(3)}, {int64(1), float64(5)}, {int64(1), float64(7)}}))
		for i, name := range correlationNames {
			for _, callee := range []string{"\"" + name + "\"", "pg_catalog.\"" + name + "\""} {
				statisticalBoth(t, p, c, r, fixture.Select("SELECT "+callee+"(y,x) FROM paired WHERE k<=3", []string{name}, []string{correlationTypes[i]}, [][]any{{correlationKnown[i]}}))
			}
		}
		q := "SELECT " + correlationProjection("$1,$2") + " FROM paired WHERE k<=3"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, pair := range [][]any{{float64(7), float64(2)}, {float64(9), float64(4)}} {
			s := correlationStep(q, []any{nil, float64(0), float64(0), pair[1], pair[0], int64(3), nil, nil}, pg)
			s.Args = pair
			fixture.RunStep(t, p.Ctx, held, r, s)
		}
	})
	for _, kind := range []string{"SMALLINT", "INTEGER", "BIGINT", "NUMERIC", "REAL", "DOUBLE PRECISION"} {
		t.Run(kind, func(t *testing.T) {
			correlationScenario(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
				fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE TABLE typed(k INTEGER,y "+kind+",x "+kind+")", 0))
				statisticalBoth(t, p, c, r, correlationStep("SELECT "+correlationProjection("y,x")+" FROM typed", correlationEmpty, pg))
				fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("INSERT INTO typed VALUES (1,3,1),(2,5,2),(3,7,3)", 3))
				statisticalBoth(t, p, c, r, correlationStep("SELECT "+correlationProjection("y,x ORDER BY k")+" FROM typed", correlationKnown, pg))
			})
		})
	}
}

func TestReferenceCorrelationRegressionBoundaries(t *testing.T) {
	correlationScenario(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		run(fixture.Exec("CREATE TABLE t(k INTEGER,y DOUBLE PRECISION,x DOUBLE PRECISION)", 0))
		run(fixture.Exec("INSERT INTO t VALUES (1,1,1),(2,2,2),(3,2,3)", 3))
		correlationIntervals(t, p, c, r, "y,x ORDER BY k", "t", []any{float64(0.8660254037844386), float64(1.0 / 3), float64(0.5), float64(2), float64(5.0 / 3), int64(3), float64(2.0 / 3), float64(0.75)}, 0, 1, 4, 6)
		for _, tc := range []struct {
			values string
			want   []any
		}{
			{"(1,1,'Infinity')", []any{nil, "NaN", nil, "Infinity", "1", int64(1), "NaN", "1"}},
			{"(1,'Infinity',1)", []any{nil, "NaN", nil, "1", "Infinity", int64(1), nil, nil}},
		} {
			run(fixture.Exec("DELETE FROM t", 3))
			run(fixture.Exec("INSERT INTO t VALUES "+tc.values, 1))
			calls, types := make([]string, 8), make([]string, 8)
			for i, name := range correlationNames {
				calls[i], types[i] = name+"(y,x)::text AS "+name, "TEXT"
				if i == 5 {
					calls[i], types[i] = "regr_count(y,x)", "INT8"
				}
			}
			statisticalBoth(t, p, c, r, fixture.Select("SELECT "+strings.Join(calls, ",")+" FROM t", correlationNames, types, [][]any{tc.want}))
			run(fixture.Exec("DELETE FROM t", 1))
			run(fixture.Exec("INSERT INTO t VALUES (1,1,1),(2,2,2),(3,2,3)", 3))
		}
		run(fixture.Exec("DELETE FROM t", 3))
		run(fixture.Exec("INSERT INTO t VALUES (1,1e308,1e308),(2,1e308,1e308)", 2))
		for _, args := range []string{"y,1", "1,x"} {
			name := "regr_avgx"
			if args == "1,x" {
				name = "regr_avgy"
			}
			statisticalExecutionError(t, p, c, r, "SELECT "+name+"("+args+") FROM t", "value out of range: overflow", true)
		}
		statisticalBoth(t, p, c, r, fixture.Select("SELECT regr_count(y,x) FROM t", []string{"regr_count"}, []string{"INT8"}, [][]any{{int64(2)}}))
		run(fixture.Exec("INSERT INTO t VALUES (3,'NaN','Infinity'),(4,'-Infinity','NaN'),(5,NULL,'Infinity')", 3))
		statisticalBoth(t, p, c, r, fixture.Select("SELECT regr_count(y,x) FROM t", []string{"regr_count"}, []string{"INT8"}, [][]any{{int64(4)}}))
	})
}
