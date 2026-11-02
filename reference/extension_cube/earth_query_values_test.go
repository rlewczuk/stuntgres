package reference_test

import (
	"database/sql"
	"math"
	"strconv"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceEarthQueryValues(t *testing.T) {
	t.Run("distance", func(t *testing.T) {
		earthCoordinateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			earthInstall(t, p, b, c)
			// Released five-decimal witnesses, not output copied from native.
			for _, tc := range []struct {
				a, z string
				want any
			}{
				{"'(6378168)'::earth", "'(6378168)'::earth", float64(0)},
				{"'(6378168)'::earth", "'(6378168,0,0)'::earth", float64(0)},
				{"'(6378168)'::earth", "'(-6378168)'::earth", "20037605.73216"},
				{"ll_to_earth(0,0)", "ll_to_earth(0,90)", "10018802.86608"},
				{"ll_to_earth(0,0)", "ll_to_earth(90,0)", "10018802.86608"},
				{"ll_to_earth(0,0)", "ll_to_earth(0,1)", "111320.03185"},
				{"ll_to_earth(41.8,87.6)", "ll_to_earth(35.1,106.7)", "1819303.21265"},
				{"ll_to_earth(0,180)", "ll_to_earth(0,-180)", "0.00000"},
				{"ll_to_earth(90,0)", "ll_to_earth(90,180)", "0.00000"},
				{"'(6378174)'::earth", "'(-6378174)'::earth", "20037605.73216"},
				{"'(6378168,0,NaN)'::earth", "'(6378168)'::earth", float64(0)},
				{"'(6378168,NaN)'::earth", "'(6378168)'::earth", float64(0)},
			} {
				q := "SELECT earth_distance(" + tc.a + "," + tc.z + ") AS d"
				earthCoordinateNumbers(t, p, b, c, q, []string{"d"}, [][]any{{tc.want}}, []float64{0})
			}
			q := "SELECT earth_distance($1::earth,$2::earth) AS d"
			zero := fixture.Select(q, []string{"d"}, []string{"FLOAT8"}, [][]any{{float64(0)}}, "(6378168)", "(6378168,0,0)")
			zero.CheckRaw = cubeCoordinateScalar(b, q, 0).CheckRaw
			pi := float64(math.Pi)
			opposite := fixture.Select(q, []string{"d"}, []string{"FLOAT8"}, [][]any{{pi * 6378168}}, "(6378168)", "(-6378168)")
			earthQueryRoutes(t, p, b, c, zero,
				opposite,
				fixture.Select(q, []string{"d"}, []string{"FLOAT8"}, [][]any{{nil}}, nil, "(6378168)"),
				fixture.Select(q, []string{"d"}, []string{"FLOAT8"}, [][]any{{nil}}, "(6378168)", nil),
				fixture.Select(q, []string{"d"}, []string{"FLOAT8"}, [][]any{{nil}}, nil, nil),
			)
			for _, where := range []string{"", " WHERE false"} {
				want := [][]any{{nil}}
				if where != "" {
					want = [][]any{}
				}
				earthCoordinateNumbers(t, p, b, c, "SELECT earth_distance(NULL,NULL) AS d"+where, []string{"d"}, want, []float64{0})
			}
		})
	})
	t.Run("box", func(t *testing.T) {
		earthCoordinateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			earthInstall(t, p, b, c)
			box := "earth_box(ll_to_earth(0,0),112000)"
			q := "SELECT cube_ll_coord(" + box + ",1) AS xlo,cube_ll_coord(" + box + ",2) AS ylo,cube_ll_coord(" + box + ",3) AS zlo,cube_ur_coord(" + box + ",1) AS xhi,cube_ur_coord(" + box + ",2) AS yhi,cube_ur_coord(" + box + ",3) AS zhi"
			earthCoordinateNumbers(t, p, b, c, q, []string{"xlo", "ylo", "zlo", "xhi", "yhi", "zhi"}, [][]any{{"6266169.43896", "-111998.56104", "-111998.56104", "6490166.56104", "111998.56104", "111998.56104"}}, []float64{0, 0, 0, 0, 0, 0})

			q = "SELECT earth_box($1::earth,$2::float8) AS b,cube_dim(earth_box($1::earth,$2::float8)) AS n"
			var steps []fixture.Step
			for _, radius := range []any{float64(0), math.Copysign(0, -1), float64(-1), "-Infinity"} {
				steps = append(steps, cubeConstructorSelect(b, q, []string{"b", "n"}, []string{"CUBE", "INT4"}, [][]any{{"(6378168)", int64(1)}}, "(6378168)", radius))
			}
			pi := float64(math.Pi)
			maximum := pi * 6378168
			for _, radius := range []any{math.Nextafter(maximum, 0), maximum, math.Nextafter(maximum, math.Inf(1)), "Infinity", "NaN"} {
				// At these adjacent arc inputs sin(arc/diameter) rounds to 1;
				// above the half circumference the released SQL clamps to D.
				steps = append(steps, cubeConstructorSelect(b, q, []string{"b", "n"}, []string{"CUBE", "INT4"}, [][]any{{"(-6378168, -12756336, -12756336),(19134504, 12756336, 12756336)", int64(3)}}, "(6378168)", radius))
			}
			for _, args := range [][]any{{nil, float64(0)}, {"(6378168)", nil}, {nil, nil}} {
				steps = append(steps, cubeConstructorSelect(b, q, []string{"b", "n"}, []string{"CUBE", "INT4"}, [][]any{{nil, nil}}, args...))
			}
			earthQueryRoutes(t, p, b, c, steps...)
			for _, radius := range []string{"0", "-1", "'-Infinity'::float8", "'Infinity'::float8", "'NaN'::float8", strconv.FormatFloat(maximum, 'g', -1, 64)} {
				want, n := "(6378168)", int64(1)
				if radius != "0" && radius != "-1" && radius != "'-Infinity'::float8" {
					want, n = "(-6378168, -12756336, -12756336),(19134504, 12756336, 12756336)", 3
				}
				q := "SELECT earth_box('(6378168)'::earth," + radius + ") AS b,cube_dim(earth_box('(6378168)'::earth," + radius + ")) AS n"
				earthQueryRoutes(t, p, b, c, cubeConstructorSelect(b, q, []string{"b", "n"}, []string{"CUBE", "INT4"}, [][]any{{want, n}}))
			}
			// Positive enlargement fills missing axes; its CUBE result is not
			// rechecked against the earth point/surface domain constraints.
			earthQueryRoutes(t, p, b, c, fixture.Select("SELECT cube_dim(earth_box('(6378168)'::earth,1)) AS n,cube_is_point(earth_box('(6378168)'::earth,1)) AS point", []string{"n", "point"}, []string{"INT4", "BOOL"}, [][]any{{int64(3), false}}))
			earthQueryRoutes(t, p, b, c, cubeConstructorSelect(b, "SELECT earth_box('(6378168,0,NaN)'::earth,0) AS b", []string{"b"}, []string{"CUBE"}, [][]any{{"(6378168, 0, NaN),(6378168, 0, NaN)"}}))
			earthQueryRoutes(t, p, b, c, cubeConstructorSelect(b, "SELECT earth_box('(6378168,0,NaN)'::earth,1) AS b", []string{"b"}, []string{"CUBE"}, [][]any{{"(6378167, -0.9999999999999991, NaN),(6378169, 0.9999999999999991, NaN)"}}))
			for _, where := range []string{"", " WHERE false"} {
				want := [][]any{{nil}}
				if where != "" {
					want = [][]any{}
				}
				earthQueryRoutes(t, p, b, c, cubeConstructorSelect(b, "SELECT earth_box(NULL,NULL) AS b"+where, []string{"b"}, []string{"CUBE"}, want))
			}
		})
	})
}
