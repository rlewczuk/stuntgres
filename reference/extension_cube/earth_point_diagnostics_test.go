package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func earthPointMissingOperator(signature string) fixture.Diagnostic {
	want := fixture.ControlsReferenceDiagnostic("42883", "operator does not exist: "+signature)
	want.Position = "22"
	want.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
	return want
}

func TestReferenceEarthPointDiagnostics(t *testing.T) {
	t.Run("absent", func(t *testing.T) {
		earthPointScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, tc := range []struct {
				q    string
				want fixture.Diagnostic
			}{
				{"SELECT point '(0,0)' <@> point '(1,0)'", earthPointMissingOperator("point <@> point")},
				{"SELECT geo_distance(point '(0,0)',point '(1,0)')", earthCoordinateMissing("geo_distance(point, point)", "8")},
			} {
				for _, route := range []string{"direct", "fresh", "held"} {
					earthCoordinateError(t, p, b, c, tc.q, route, nil, tc.want, true, nil)
				}
			}
		})
	})
	t.Run("installed", func(t *testing.T) {
		earthPointScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			earthInstall(t, p, b, c)
			bad := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type point: "bad"`)
			bad.Position = "14"
			for _, tc := range []struct {
				q    string
				want fixture.Diagnostic
			}{
				{"SELECT point '(0,0)' <@> 1", earthPointMissingOperator("point <@> integer")},
				{"SELECT geo_distance(1,2)", earthCoordinateMissing("geo_distance(integer, integer)", "8")},
				{"SELECT geo_distance(point '(0,0)')", earthCoordinateMissing("geo_distance(point)", "8")},
				{"SELECT point 'bad' <@> point '(0,0)'", bad},
			} {
				for _, route := range []string{"direct", "fresh", "held"} {
					earthCoordinateError(t, p, b, c, tc.q, route, nil, tc.want, true, nil)
				}
			}
			earthPointHeldRecovery(t, p, b, c)
		})
	})
	t.Run("statement_atomicity", func(t *testing.T) {
		earthPointScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			earthInstall(t, p, b, c)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE earth_point_atomic(id integer,a point,b point)", 0))
			run(fixture.Exec("INSERT INTO earth_point_atomic VALUES (1,'(0,60)','(1,60)')", 1))
			q := "INSERT INTO earth_point_atomic VALUES (2,'(0,0)','(1,0)'),(3,$1::point,'(1,0)') RETURNING a <@> b AS miles"
			want := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type point: "bad"`)
			want.Context = "unnamed portal parameter $1 = '...'"
			for _, route := range []string{"direct", "fresh", "held"} {
				earthCoordinateError(t, p, b, c, q, route, []any{"bad"}, want, false, nil)
				earthCoordinateNumbers(t, p, b, c, "SELECT id,a <@> b AS operator_miles,geo_distance(a,b) AS function_miles FROM earth_point_atomic ORDER BY id", []string{"id", "operator_miles", "function_miles"}, [][]any{{int64(1), "34.54626", "34.54626"}}, []float64{0, 0, 0})
			}
		})
	})
}
