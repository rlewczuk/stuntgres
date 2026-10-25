package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceEarthPointBinding(t *testing.T) {
	t.Run("coercion", func(t *testing.T) {
		fixture.SchemaReferenceCleanup(t, "earth_point_shadow")
		earthPointScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			earthInstall(t, p, b, c)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE earth_point_binding(id integer,a point,b point)", 0))
			run(fixture.Exec("INSERT INTO earth_point_binding VALUES (1,'(0,60)','(1,60)'),(2,'(0,0)','(1,0)'),(3,NULL,'(1,0)')", 3))
			earthCoordinateNumbers(t, p, b, c, "SELECT id,a <@> b AS operator_miles,geo_distance(a,b) AS function_miles FROM earth_point_binding ORDER BY id", []string{"id", "operator_miles", "function_miles"}, [][]any{{int64(1), "34.54626", "34.54626"}, {int64(2), "69.09318", "69.09318"}, {int64(3), nil, nil}}, []float64{0, 0, 0})
			earthCoordinateNumbers(t, p, b, c, "SELECT a <@> b AS operator_miles,geo_distance(a,b) AS function_miles FROM earth_point_binding WHERE false", []string{"operator_miles", "function_miles"}, [][]any{}, []float64{0, 0})
			for _, q := range []string{
				"SELECT point '(0,60)' <@> '(1,60)' AS miles",
				"SELECT '(0,60)' <@> point '(1,60)' AS miles",
				"SELECT geo_distance('(0,60)','(1,60)') AS miles",
				"SELECT public.geo_distance(point '(0,60)',point '(1,60)') AS miles",
				"SELECT point '(0,60)' OPERATOR(public.<@>) point '(1,60)' AS miles",
			} {
				earthCoordinateNumbers(t, p, b, c, q, []string{"miles"}, [][]any{{"34.54626"}}, []float64{0})
			}
			for _, q := range []string{"SELECT $1::point <@> $2::point AS miles", "SELECT geo_distance($1::point,$2::point) AS miles"} {
				for _, tc := range []struct {
					args []any
					want any
				}{
					{[]any{"(0,0)", "(1,0)"}, float64(69.09318190000541)},
					{[]any{[]byte("(0,0)"), []byte("(1,0)")}, float64(69.09318190000541)},
					{[]any{nil, "(1,0)"}, nil},
					{[]any{"(0,0)", nil}, nil},
					{[]any{nil, nil}, nil},
				} {
					earthCoordinateBoth(t, p, b, c, fixture.Select(q, []string{"miles"}, []string{"FLOAT8"}, [][]any{{tc.want}}, tc.args...))
				}
			}
			earthPointHeldRecovery(t, p, b, c)
			for _, q := range []string{
				"CREATE SCHEMA earth_point_shadow",
				"CREATE FUNCTION earth_point_shadow.geo_distance(point,point) RETURNS float8 LANGUAGE SQL RETURN 7::float8",
				"SET search_path=earth_point_shadow,public",
			} {
				run(fixture.Exec(q, 0))
			}
			earthCoordinateBoth(t, p, b, c, fixture.Select("SELECT geo_distance(point '(0,60)',point '(1,60)') AS shadow", []string{"shadow"}, []string{"FLOAT8"}, [][]any{{float64(7)}}))
			earthCoordinateNumbers(t, p, b, c, "SELECT public.geo_distance(point '(0,60)',point '(1,60)') AS miles,point '(0,60)' <@> point '(1,60)' AS operator_miles", []string{"miles", "operator_miles"}, [][]any{{"34.54626", "34.54626"}}, []float64{0, 0})
			run(fixture.Exec("SET search_path=pg_catalog", 0))
			earthCoordinateNumbers(t, p, b, c, "SELECT public.geo_distance(point '(0,60)',point '(1,60)') AS miles,point '(0,60)' OPERATOR(public.<@>) point '(1,60)' AS operator_miles", []string{"miles", "operator_miles"}, [][]any{{"34.54626", "34.54626"}}, []float64{0, 0})
			// Native pre-cancellation is an ordinary adapter/context error.
			// A fast pq command may complete before cancellation is observed.
			if b.PG {
				return
			}
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			for _, prepared := range []bool{false, true} {
				q := "SELECT public.geo_distance($1::point,$2::point)"
				var err error
				var rows *sql.Rows
				if prepared {
					h := earthHeld(t, p, c, q)
					rows, err = h.Statement.QueryContext(ctx, "(0,60)", "(1,60)")
					if rows != nil {
						rows.Close()
					}
					h.Statement.Close()
				} else {
					rows, err = c.QueryContext(ctx, q, "(0,60)", "(1,60)")
					if rows != nil {
						rows.Close()
					}
				}
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("pre-canceled error=%v", err)
				}
			}
		})
	})
	t.Run("execute_privilege", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"earth_point_reader"})
		earthPointScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			earthInstall(t, p, b, c)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			for _, q := range []string{"CREATE ROLE earth_point_reader", "GRANT USAGE ON SCHEMA public TO earth_point_reader", "REVOKE EXECUTE ON FUNCTION geo_distance(point,point) FROM PUBLIC"} {
				run(fixture.Exec(q, 0))
			}
			for _, q := range []string{"SELECT geo_distance($1::point,$2::point) AS miles", "SELECT $1::point <@> $2::point AS miles"} {
				run(fixture.Exec("SET ROLE earth_point_reader", 0))
				h := earthHeld(t, p, c, q)
				want := fixture.ControlsReferenceDiagnostic("42501", "permission denied for function geo_distance")
				// Source-grounded expectation: access checks at execution have no position.
				s := fixture.ErrorDiagnostic(q, want, "(0,60)", "(1,60)")
				s.SelectRows, s.ErrorStage = true, "execute"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
				fixture.RunStep(t, p.Ctx, h, b.Recorder, s)
				run(fixture.Exec("RESET ROLE", 0))
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION geo_distance(point,point) TO earth_point_reader", 0))
				run(fixture.Exec("SET ROLE earth_point_reader", 0))
				var got float64
				if err := h.Statement.QueryRowContext(p.Ctx, "(0,60)", "(1,60)").Scan(&got); err != nil {
					t.Fatal("held non-NULL recovery after GRANT", err)
				}
				earthCoordinateFloat(t, got, 34.54626, 0.000005)
				h.Statement.Close()
				run(fixture.Exec("RESET ROLE", 0))
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION geo_distance(point,point) FROM earth_point_reader", 0))
			}
		})
	})
}
