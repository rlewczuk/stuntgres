package reference_test

import (
	"database/sql"
	"math"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStatisticalAggregateCoercion(t *testing.T) {
	statisticalScenario(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE TABLE t(k INTEGER,x REAL,y REAL,b BIGINT,n NUMERIC)", 0))
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("INSERT INTO t VALUES (1,0.1,0.2,9007199254740992,9007199254740992),(2,0.3,0.6,9007199254740993,9007199254740994)", 2))
		// Binary32 inputs widen exactly. The two-point centered sum is delta²/2,
		// population variance delta²/4, and sample variance delta²/2.
		delta := float64(float32(0.3)) - float64(float32(0.1))
		pop, sample := delta*delta/4, delta*delta/2
		s := fixture.Select("SELECT "+statisticalProjection("x ORDER BY k")+" FROM t", statisticalNames, []string{"FLOAT8", "FLOAT8", "FLOAT8", "FLOAT8", "FLOAT8", "FLOAT8"}, [][]any{{pop, sample, sample, math.Sqrt(pop), math.Sqrt(sample), math.Sqrt(sample)}})
		s.CheckMetadata = statisticalMetadata(pg, s.Types)
		statisticalBoth(t, p, c, r, s)
		for _, tc := range []struct {
			x, y string
			want []any
		}{
			{"x", "y", []any{float64(2), sample, 2 * sample, 4 * sample}},
			{"b", "b", []any{nil, float64(0), float64(0), float64(0)}},
			// Released FLOAT8 transition rounds the second X residual to 4
			// after summing the large offsets (pinned PG18.0 witness).
			{"n", "2*(k+0)", []any{float64(0.5), float64(8), float64(4), float64(2)}},
		} {
			q := "SELECT regr_slope(" + tc.y + "," + tc.x + " ORDER BY k),regr_sxx(" + tc.y + "," + tc.x + " ORDER BY k),regr_sxy(" + tc.y + "," + tc.x + " ORDER BY k),regr_syy(" + tc.y + "," + tc.x + " ORDER BY k) FROM t"
			statisticalBoth(t, p, c, r, regressionStep(q, tc.want))
		}
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("UPDATE t SET n=1e309 WHERE k=2", 1))
		for _, q := range []string{"SELECT regr_sxx(k,n) FROM t", "SELECT regr_sxx(n,k) FROM t"} {
			s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("22003", "\"1"+strings.Repeat("0", 309)+"\" is out of range for type double precision"))
			s.SelectRows, s.ErrorStage = true, "execute"
			statisticalBoth(t, p, c, r, s)
		}
	})
}

func TestReferenceStatisticalAggregateArgumentEvaluation(t *testing.T) {
	statisticalScenario(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE TABLE t(i INTEGER)", 0))
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("INSERT INTO t VALUES (1)", 1))
		for _, args := range []string{"NULL::double precision,1/(i-1)", "1/(i-1),NULL::double precision"} {
			q := "SELECT regr_sxx(" + args + ")"
			s := fixture.ErrorDiagnostic(q+" FROM t", fixture.ControlsReferenceDiagnostic("22012", "division by zero"))
			s.SelectRows, s.ErrorStage = true, "execute"
			statisticalBoth(t, p, c, r, s)
			for _, filter := range []string{"FALSE", "NULL::boolean", "i>1"} {
				statisticalBoth(t, p, c, r, fixture.Select(q+" FILTER (WHERE "+filter+") FROM t", []string{"regr_sxx"}, []string{"FLOAT8"}, [][]any{{nil}}))
			}
		}
	})
}

func TestReferenceStatisticalAggregateParserWindow(t *testing.T) {
	statisticalScenario(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE TABLE t(i INTEGER)", 0))
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("INSERT INTO t VALUES (1),(2),(3)", 3))
		for _, name := range append(append([]string{}, statisticalNames...), regressionNames...) {
			args := "i"
			want := any("1.00000000000000000000")
			typ := "NUMERIC"
			if strings.HasPrefix(name, "regr_") {
				args, typ = "2*i,i", "FLOAT8"
				want = map[string]float64{"regr_slope": 2, "regr_sxx": 2, "regr_sxy": 4, "regr_syy": 8}[name]
			} else if name == "var_pop" {
				want = "0.66666666666666666667"
			} else if name == "stddev_pop" {
				want = "0.81649658092772603273"
			}
			q := "SELECT pg_catalog.\"" + name + "\"(" + args + ") OVER () FROM t"
			if pg {
				statisticalBoth(t, p, c, r, fixture.Select(q, []string{name}, []string{typ}, [][]any{{want}, {want}, {want}}))
			} else {
				d := fixture.ControlsReferenceDiagnostic("0A000", "window function provider is unsupported")
				d.Position = "8"
				s := fixture.ErrorDiagnostic(q, d)
				s.SelectRows, s.ErrorStage = true, "execute"
				for _, prepared := range []bool{false, true} {
					s.Prepared = prepared
					if prepared {
						s.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, c, r, s)
				}
			}
			badArgs, badTypes := "TRUE", "boolean"
			if strings.HasPrefix(name, "regr_") {
				badArgs, badTypes = "TRUE,i", "boolean, integer"
			}
			bad := "SELECT pg_catalog.\"" + name + "\"(" + badArgs + ") OVER () FROM t WHERE FALSE"
			d := fixture.ControlsReferenceDiagnostic("42883", "function pg_catalog."+name+"("+badTypes+") does not exist")
			d.Position, d.Hint = "8", "No function matches the given name and argument types. You might need to add explicit type casts."
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(bad, d)
				s.Prepared, s.SelectRows, s.ErrorStage = prepared, true, "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, c, r, s)
			}
			for _, callee := range []string{name, "\"" + name + "\"", "pg_catalog.\"" + name + "\""} {
				ddl := "CREATE TABLE bad_stats(i INTEGER,g DOUBLE PRECISION GENERATED ALWAYS AS (" + callee + "(" + args + ")) STORED)"
				d := fixture.ControlsReferenceDiagnostic("42803", "aggregate functions are not allowed in column generation expressions")
				d.Position = strconv.Itoa(strings.Index(ddl, callee) + 1)
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic(ddl, d)
					s.Prepared, s.ErrorStage = prepared, "execute"
					fixture.RunStep(t, p.Ctx, c, r, s)
				}
			}
		}
	})
}
