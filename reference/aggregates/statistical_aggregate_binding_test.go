package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStatisticalAggregateBinding(t *testing.T) {
	statisticalScenario(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, r *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, r, s) }
		run(fixture.Exec("CREATE TABLE t(g INTEGER,i INTEGER,y DOUBLE PRECISION,x DOUBLE PRECISION)", 0))
		run(fixture.Exec("INSERT INTO t VALUES (1,1,2,1),(1,2,4,2),(1,3,6,3),(2,NULL,NULL,NULL)", 4))
		statisticalBoth(t, p, conn, r, fixture.Select(`SELECT pg_catalog.var_samp(i),"stddev"(i),pg_catalog.regr_slope(y,x) FROM t`, []string{"var_samp", "stddev", "regr_slope"}, []string{"NUMERIC", "NUMERIC", "FLOAT8"}, [][]any{{"1.00000000000000000000", "1.00000000000000000000", float64(2)}}))
		statisticalBoth(t, p, conn, r, fixture.Select("SELECT var_pop(NULL),stddev('1'),variance(NULL) FROM t", []string{"var_pop", "stddev", "variance"}, []string{"FLOAT8", "FLOAT8", "FLOAT8"}, [][]any{{nil, float64(0), nil}}))
		statisticalBoth(t, p, conn, r, fixture.Select("SELECT g,var_samp(i),stddev_samp(i),var_samp(i),regr_slope(y,x),count(i),sum(i) FROM t GROUP BY g ORDER BY g", []string{"g", "var_samp", "stddev_samp", "var_samp", "regr_slope", "count", "sum"}, []string{"INT4", "NUMERIC", "NUMERIC", "NUMERIC", "FLOAT8", "INT8", "INT8"}, [][]any{{int64(1), "1.00000000000000000000", "1.00000000000000000000", "1.00000000000000000000", float64(2), int64(3), int64(6)}, {int64(2), nil, nil, nil, nil, int64(0), nil}}))
		statisticalBoth(t, p, conn, r, fixture.Select("SELECT g FROM t GROUP BY g HAVING var_samp(i)>0 ORDER BY regr_slope(y,x)", []string{"g"}, []string{"INT4"}, [][]any{{int64(1)}}))
		statisticalBoth(t, p, conn, r, fixture.Select("SELECT var_samp(ALL i ORDER BY i DESC),var_samp(DISTINCT i),regr_slope(y,x) FILTER (WHERE i>1) FROM t", []string{"var_samp", "var_samp", "regr_slope"}, []string{"NUMERIC", "NUMERIC", "FLOAT8"}, [][]any{{"1.00000000000000000000", "1.00000000000000000000", float64(2)}}))
		statisticalBoth(t, p, conn, r, fixture.Select("SELECT var_pop(1/(i-1)) FILTER (WHERE i>1),regr_sxx(1/(i-1),1/(i-1)) FILTER (WHERE i>1) FROM t", []string{"var_pop", "regr_sxx"}, []string{"NUMERIC", "FLOAT8"}, [][]any{{"0.25000000000000000000", float64(0.5)}}))
		statisticalBoth(t, p, conn, r, fixture.Select("SELECT var_pop($1),$1::text FROM t", []string{"var_pop", "text"}, []string{"FLOAT8", "TEXT"}, [][]any{{float64(0), "1"}}, "1"))
		for _, q := range []string{"SELECT var_pop($1),stddev_samp($2) FROM t", "SELECT regr_slope($1,$2),regr_sxx($1,$2) FROM t"} {
			stmt, err := conn.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
				for _, args := range [][]any{{float64(3), float64(7)}, {nil, float64(2)}, {float64(4), nil}} {
					columns := []string{"var_pop", "stddev_samp"}
					values := []any{float64(0), float64(0)}
					if args[0] == nil {
						values[0] = nil
					}
					if args[1] == nil {
						values[1] = nil
					}
					if q == "SELECT regr_slope($1,$2),regr_sxx($1,$2) FROM t" {
						columns = []string{"regr_slope", "regr_sxx"}
						values = []any{nil, float64(0)}
						if args[0] == nil || args[1] == nil {
							values[1] = nil
						}
					}
					s := fixture.Select(q, columns, []string{"FLOAT8", "FLOAT8"}, [][]any{values}, args...)
					s.CheckMetadata = statisticalMetadata(pg, s.Types)
					fixture.RunStep(t, p.Ctx, held, r, s)
				}
			}()
		}
		run(fixture.Exec("CREATE VIEW moments AS SELECT g,var_samp(i) AS v,regr_slope(y,x) AS s FROM t GROUP BY g", 0))
		want := fixture.Select("SELECT g,v,s FROM moments ORDER BY g", []string{"g", "v", "s"}, []string{"INT4", "NUMERIC", "FLOAT8"}, [][]any{{int64(1), "1.00000000000000000000", float64(2)}, {int64(2), nil, nil}})
		statisticalBoth(t, p, conn, r, want)
		run(fixture.Exec("CREATE TABLE target(g INTEGER,v NUMERIC,s DOUBLE PRECISION)", 0))
		for _, prepared := range []bool{false, true} {
			s := fixture.Exec("INSERT INTO target SELECT g,v,s FROM moments", 2)
			s.Prepared = prepared
			run(s)
			run(fixture.Exec("DELETE FROM target", 2))
		}
		run(fixture.Exec("CREATE TABLE materialized AS SELECT g,v,s FROM moments", 2))
		want.Query = "SELECT g,v,s FROM materialized ORDER BY g"
		statisticalBoth(t, p, conn, r, want)
		want.Query = "SELECT g,v,s FROM (SELECT g,var_samp(i) AS v,regr_slope(y,x) AS s FROM t GROUP BY g) AS derived ORDER BY g"
		statisticalBoth(t, p, conn, r, want)
	})
}
