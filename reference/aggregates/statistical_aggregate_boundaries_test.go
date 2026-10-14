package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func statisticalExecutionError(t *testing.T, p *fixture.Pair, conn *sql.Conn, r *fixture.Recorder, q, message string, selectRows bool) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22003", Message: message})
		s.Prepared, s.SelectRows, s.ErrorStage = prepared, selectRows, "execute"
		fixture.RunStep(t, p.Ctx, conn, r, s)
	}
}

func TestReferenceStatisticalAggregateBoundaries(t *testing.T) {
	for _, c := range []struct {
		name, kind, values, pop, sample, root string
		count                                 int64
	}{
		{"singleton", "NUMERIC", "(7)", "0", "", "0", 1},
		{"equal_scale", "NUMERIC", "(1.0000000000000000000000000),(1)", "0", "0", "0", 2},
		{"scale", "NUMERIC", "(1.0000000000000000000000000),(3)", "1." + strings.Repeat("0", 50), "2." + strings.Repeat("0", 50), "1." + strings.Repeat("0", 50), 2},
		{"clamp", "NUMERIC", "(1." + strings.Repeat("0", 501) + "),(3)", "1." + strings.Repeat("0", 1000), "2." + strings.Repeat("0", 1000), "1." + strings.Repeat("0", 1000), 2},
		{"beyond_float", "BIGINT", "(9007199254740992),(9007199254740994)", "1.00000000000000000000", "2.0000000000000000", "1.00000000000000000000", 2},
		{"huge_equal", "NUMERIC", "('9e131071'),('9e131071')", "0", "0", "0", 2},
	} {
		t.Run(c.name, func(t *testing.T) {
			statisticalScenario(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, r *fixture.Recorder, pg bool) {
				fixture.RunStep(t, p.Ctx, conn, r, fixture.Exec("CREATE TABLE t(v "+c.kind+")", 0))
				fixture.RunStep(t, p.Ctx, conn, r, fixture.Exec("INSERT INTO t VALUES "+c.values, c.count))
				var sample any = c.sample
				if c.count == 1 {
					sample = nil
				}
				statisticalBoth(t, p, conn, r, fixture.Select("SELECT var_pop(v),var_samp(v),stddev_pop(v) FROM t", []string{"var_pop", "var_samp", "stddev_pop"}, []string{"NUMERIC", "NUMERIC", "NUMERIC"}, [][]any{{c.pop, sample, c.root}}))
			})
		})
	}
	for _, kind := range []string{"NUMERIC", "DOUBLE PRECISION"} {
		for _, special := range []string{"NaN", "Infinity", "-Infinity"} {
			for _, count := range []int64{1, 2} {
				t.Run(kind+special+strings.Repeat("x", int(count)), func(t *testing.T) {
					statisticalScenario(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, r *fixture.Recorder, pg bool) {
						fixture.RunStep(t, p.Ctx, conn, r, fixture.Exec("CREATE TABLE t(v "+kind+")", 0))
						values := "('" + special + "')"
						if count == 2 {
							values += ",(1)"
						}
						fixture.RunStep(t, p.Ctx, conn, r, fixture.Exec("INSERT INTO t VALUES "+values, count))
						var sample any
						if count == 2 {
							sample = "NaN"
						}
						query := "SELECT var_pop(v)::text,var_samp(v)::text,variance(v)::text,stddev_pop(v)::text,stddev_samp(v)::text,stddev(v)::text FROM t"
						types := []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}
						if kind == "NUMERIC" {
							// NUMERIC's public codec already returns exact strings, including
							// NaN. An incidental explicit text cast adds no moment contract.
							query = "SELECT " + statisticalProjection("v") + " FROM t"
							types = []string{"NUMERIC", "NUMERIC", "NUMERIC", "NUMERIC", "NUMERIC", "NUMERIC"}
						}
						step := fixture.Select(query, statisticalNames, types, [][]any{{"NaN", sample, sample, "NaN", sample, sample}})
						if kind == "NUMERIC" {
							step.CheckMetadata = statisticalMetadata(pg, types)
						}
						statisticalBoth(t, p, conn, r, step)
					})
				})
			}
		}
	}
	for _, c := range []struct {
		name, values string
		want         []any
	}{
		{"singleton", "(1,2,1)", []any{nil, float64(0), float64(0), float64(0)}},
		{"vertical", "(1,2,2),(2,4,2),(3,6,2)", []any{nil, float64(0), float64(0), float64(8)}},
		{"horizontal", "(1,2,1),(2,2,2),(3,2,3)", []any{float64(0), float64(2), float64(0), float64(0)}},
		// The mathematical zero is a discovery expectation; REL_18_0 may expose
		// roundoff here, unlike the devel constant-axis correction.
		{"nonbinary_constant", "(1,0.09,0.1),(2,0.09,0.1),(3,0.09,0.1)", []any{nil, float64(0), float64(0), float64(0)}},
	} {
		t.Run(c.name, func(t *testing.T) {
			statisticalScenario(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, r *fixture.Recorder, pg bool) {
				fixture.RunStep(t, p.Ctx, conn, r, fixture.Exec("CREATE TABLE t(k INTEGER,y DOUBLE PRECISION,x DOUBLE PRECISION)", 0))
				fixture.RunStep(t, p.Ctx, conn, r, fixture.Exec("INSERT INTO t VALUES "+c.values, int64(strings.Count(c.values, "),(")+1)))
				statisticalBoth(t, p, conn, r, regressionStep("SELECT regr_slope(y,x ORDER BY k),regr_sxx(y,x ORDER BY k),regr_sxy(y,x ORDER BY k),regr_syy(y,x ORDER BY k) FROM t", c.want))
			})
		})
	}
	for _, c := range []struct {
		name, values string
		want         []any
	}{
		{"special_y", "('Infinity',1)", []any{nil, "0", "NaN", "NaN"}},
		{"special_x", "(1,'Infinity')", []any{"NaN", "NaN", "NaN", "0"}},
		{"both_special", "('NaN','NaN')", []any{"NaN", "NaN", "NaN", "NaN"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			statisticalScenario(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, r *fixture.Recorder, pg bool) {
				fixture.RunStep(t, p.Ctx, conn, r, fixture.Exec("CREATE TABLE t(y DOUBLE PRECISION,x DOUBLE PRECISION)", 0))
				fixture.RunStep(t, p.Ctx, conn, r, fixture.Exec("INSERT INTO t VALUES "+c.values, 1))
				statisticalBoth(t, p, conn, r, fixture.Select("SELECT regr_slope(y,x)::text,regr_sxx(y,x)::text,regr_sxy(y,x)::text,regr_syy(y,x)::text FROM t", regressionNames, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{c.want}))
			})
		})
	}
	for _, c := range []struct {
		name, values string
		want         float64
		count        int64
	}{
		{"stable", "(100000003),(100000004),(100000006),(100000007)", 2.5, 4},
		{"offset", "(7000000000005),(7000000000007)", 1, 2},
	} {
		t.Run(c.name, func(t *testing.T) {
			statisticalScenario(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, r *fixture.Recorder, pg bool) {
				fixture.RunStep(t, p.Ctx, conn, r, fixture.Exec("CREATE TABLE t(v DOUBLE PRECISION)", 0))
				fixture.RunStep(t, p.Ctx, conn, r, fixture.Exec("INSERT INTO t VALUES "+c.values, c.count))
				statisticalBoth(t, p, conn, r, fixture.Select("SELECT var_pop(v ORDER BY v) FROM t", []string{"var_pop"}, []string{"FLOAT8"}, [][]any{{c.want}}))
			})
		})
	}
	for _, c := range []struct{ name, kind, values, q, message string }{
		{"float_square", "DOUBLE PRECISION", "(1e200),(0)", "SELECT var_pop(v) FROM t", "value out of range: overflow"},
		{"float_sum", "DOUBLE PRECISION", "(1e308),(1e308)", "SELECT stddev(v) FROM t", "value out of range: overflow"},
		{"numeric_range", "NUMERIC", "('9e131071'),('-9e131071')", "SELECT var_pop(v) FROM t", "value overflows numeric format"},
		{"unused_axis", "DOUBLE PRECISION", "(1e200),(0)", "SELECT regr_sxx(v,1) FROM t", "value out of range: overflow"},
	} {
		t.Run(c.name, func(t *testing.T) {
			statisticalScenario(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, r *fixture.Recorder, pg bool) {
				fixture.RunStep(t, p.Ctx, conn, r, fixture.Exec("CREATE TABLE t(v "+c.kind+")", 0))
				fixture.RunStep(t, p.Ctx, conn, r, fixture.Exec("INSERT INTO t VALUES "+c.values, 2))
				statisticalExecutionError(t, p, conn, r, c.q, c.message, true)
			})
		})
	}
}
