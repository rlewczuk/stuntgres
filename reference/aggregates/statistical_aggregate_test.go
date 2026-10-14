package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

// Mathematical expectations and REL_18_0 finalization rules are specified before
// consulting the pinned oracle. Discovery never learns from the native engine.
func statisticalScenario(t *testing.T, body func(*testing.T, *fixture.Pair, *sql.Conn, *fixture.Recorder, bool)) {
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

func statisticalBoth(t *testing.T, p *fixture.Pair, conn *sql.Conn, recorder *fixture.Recorder, s fixture.Step) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s.Prepared = prepared
		fixture.RunStep(t, p.Ctx, conn, recorder, s)
	}
}

func statisticalMetadata(pg bool, types []string) func(*testing.T, *sql.Rows) {
	return func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if len(columns) != len(types) {
			t.Fatalf("metadata columns: %d", len(columns))
		}
		for i, c := range columns {
			want := reflect.TypeOf(float64(0))
			if types[i] == "NUMERIC" {
				want = reflect.TypeOf("")
				if pg {
					want = reflect.TypeOf((*any)(nil)).Elem()
				}
			}
			if c.ScanType() != want {
				t.Errorf("column %d ScanType %v want %v", i, c.ScanType(), want)
			}
			if n, ok := c.Length(); n != 0 || ok {
				t.Errorf("column %d Length %d/%v", i, n, ok)
			}
			precision, scale, ok := c.DecimalSize()
			if pg && types[i] == "NUMERIC" {
				if precision != 65535 || scale != 65531 || !ok {
					t.Errorf("numeric DecimalSize %d/%d/%v", precision, scale, ok)
				}
			} else if precision != 0 || scale != 0 || ok {
				t.Errorf("DecimalSize %d/%d/%v", precision, scale, ok)
			}
			if nullable, known := c.Nullable(); nullable != !pg || known != !pg {
				t.Errorf("Nullable %v/%v", nullable, known)
			}
		}
	}
}

var statisticalNames = []string{"var_pop", "var_samp", "variance", "stddev_pop", "stddev_samp", "stddev"}
var regressionNames = []string{"regr_slope", "regr_sxx", "regr_sxy", "regr_syy"}

func statisticalProjection(argument string) string {
	calls := make([]string, len(statisticalNames))
	for i, name := range statisticalNames {
		calls[i] = name + "(" + argument + ")"
	}
	return strings.Join(calls, ",")
}

func regressionStep(q string, values []any) fixture.Step {
	return fixture.Select(q, regressionNames, []string{"FLOAT8", "FLOAT8", "FLOAT8", "FLOAT8"}, [][]any{values})
}

func TestReferenceStatisticalAggregateRegression(t *testing.T) {
	statisticalScenario(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, r *fixture.Recorder, pg bool) {
		fixture.RunStep(t, p.Ctx, conn, r, fixture.Exec("CREATE TABLE t(k INTEGER,y DOUBLE PRECISION,x DOUBLE PRECISION)", 0))
		fixture.RunStep(t, p.Ctx, conn, r, fixture.Exec("INSERT INTO t VALUES (1,2,1),(2,4,2),(3,6,3),(4,NULL,1000),(5,1000,NULL)", 5))
		q := "SELECT regr_slope(y,x ORDER BY k),regr_sxx(y,x ORDER BY k),regr_sxy(y,x ORDER BY k),regr_syy(y,x ORDER BY k) FROM t"
		s := regressionStep(q, []any{float64(2), float64(2), float64(4), float64(8)})
		s.CheckMetadata = statisticalMetadata(pg, s.Types)
		statisticalBoth(t, p, conn, r, s)
		statisticalBoth(t, p, conn, r, regressionStep(strings.ReplaceAll(q, "(y,x", "(-y,x"), []any{float64(-2), float64(2), float64(-4), float64(8)}))
		for _, suffix := range []string{" WHERE FALSE", " WHERE x IS NULL OR y IS NULL"} {
			statisticalBoth(t, p, conn, r, regressionStep(q+suffix, []any{nil, nil, nil, nil}))
		}
		fixture.RunStep(t, p.Ctx, conn, r, fixture.Exec("DELETE FROM t", 5))
		fixture.RunStep(t, p.Ctx, conn, r, fixture.Exec("INSERT INTO t VALUES (1,2,1),(2,2,2),(3,2,2)", 3))
		statisticalBoth(t, p, conn, r, fixture.Select("SELECT regr_sxx(DISTINCT y,x),regr_slope(DISTINCT y,x) FROM t", []string{"regr_sxx", "regr_slope"}, []string{"FLOAT8", "FLOAT8"}, [][]any{{float64(0.5), float64(0)}}))
	})
}

func TestReferenceStatisticalAggregateOverloads(t *testing.T) {
	for _, kind := range []string{"SMALLINT", "INTEGER", "BIGINT", "NUMERIC", "REAL", "DOUBLE PRECISION"} {
		t.Run(kind, func(t *testing.T) {
			statisticalScenario(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, r *fixture.Recorder, pg bool) {
				fixture.RunStep(t, p.Ctx, conn, r, fixture.Exec("CREATE TABLE t(g INTEGER,v "+kind+")", 0))
				fixture.RunStep(t, p.Ctx, conn, r, fixture.Exec("INSERT INTO t VALUES (1,1),(1,2),(1,3),(1,NULL)", 4))
				typ := "NUMERIC"
				values := []any{"0.66666666666666666667", "1.00000000000000000000", "1.00000000000000000000", "0.81649658092772603273", "1.00000000000000000000", "1.00000000000000000000"}
				if kind == "REAL" || kind == "DOUBLE PRECISION" {
					typ = "FLOAT8"
					values = []any{float64(2.0 / 3), float64(1), float64(1), math.Sqrt(2.0 / 3), float64(1), float64(1)}
				}
				types := []string{typ, typ, typ, typ, typ, typ}
				for _, tc := range []struct {
					suffix string
					rows   [][]any
				}{{"", [][]any{values}}, {" WHERE FALSE", [][]any{{nil, nil, nil, nil, nil, nil}}}, {" WHERE v IS NULL", [][]any{{nil, nil, nil, nil, nil, nil}}}, {" WHERE FALSE GROUP BY g", nil}} {
					s := fixture.Select("SELECT "+statisticalProjection("v")+" FROM t"+tc.suffix, statisticalNames, types, tc.rows)
					s.CheckMetadata = statisticalMetadata(pg, types)
					statisticalBoth(t, p, conn, r, s)
				}
			})
		})
	}
}
