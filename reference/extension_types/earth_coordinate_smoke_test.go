package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceEarthCoordinateFeatureSmoke(t *testing.T) {
	earthCoordinateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		earthInstall(t, p, b, c)
		earthCoordinateBoth(t, p, b, c, fixture.Select("SELECT earth() AS r,sec_to_gc(-1) AS a,gc_to_sec(-1) AS b,gc_to_sec('Infinity'::float8) AS diameter", []string{"r", "a", "b", "diameter"}, []string{"FLOAT8", "FLOAT8", "FLOAT8", "FLOAT8"}, [][]any{{float64(6378168), float64(0), float64(0), float64(12756336)}}))
		q := "SELECT latitude(ll_to_earth($1::float8,$2::float8)) AS lat,longitude(ll_to_earth($1::float8,$2::float8)) AS lon"
		h := earthHeld(t, p, c, q)
		defer h.Statement.Close()
		for _, xy := range [][]any{{float64(0), float64(0)}, {float64(45), float64(90)}, {nil, nil}} {
			var lat, lon sql.NullFloat64
			if err := h.Statement.QueryRowContext(p.Ctx, xy...).Scan(&lat, &lon); err != nil {
				t.Fatal(err)
			}
			if xy[0] == nil {
				if lat.Valid || lon.Valid {
					t.Fatal("held NULL lost strictness")
				}
			} else {
				if !lat.Valid || !lon.Valid {
					t.Fatal("unexpected NULL")
				}
				earthCoordinateFloat(t, lat.Float64, xy[0].(float64), 1e-10)
				earthCoordinateFloat(t, lon.Float64, xy[1].(float64), 1e-10)
			}
		}
		earthCoordinateStoredSetup(t, p, b, c)
		earthCoordinateStoredVerify(t, p, b, c)
		earthCoordinateError(t, p, b, c, "INSERT INTO earth_coordinate_defaults(id,p) VALUES (2,ll_to_earth(0,0)),(3,ll_to_earth($1::float8,0)) RETURNING id", "held", []any{"Infinity"}, earthCoordinateBodyError("ll_to_earth", "22003", "input is out of range"), false, nil)
		earthCoordinateStoredVerify(t, p, b, c)
	})
}
