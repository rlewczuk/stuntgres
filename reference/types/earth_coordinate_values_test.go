package reference_test

import (
	"database/sql"
	"fmt"
	"math"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceEarthCoordinateValues(t *testing.T) {
	t.Run("scalars", func(t *testing.T) {
		earthCoordinateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			earthInstall(t, p, b, c)
			earthCoordinateBoth(t, p, b, c, fixture.Select("SELECT earth() AS r,earth()*2 AS diameter", []string{"r", "diameter"}, []string{"FLOAT8", "FLOAT8"}, [][]any{{float64(6378168), float64(12756336)}}))
			for _, tc := range []struct{ input, arc, chord string }{
				{"10000", "10000.00102", "9999.99898"}, {"100000", "100001.02426", "99998.97577"}, {"1000000", "1001027.07131", "998976.08618"},
			} {
				q := "SELECT sec_to_gc(" + tc.input + ") AS arc,gc_to_sec(" + tc.input + ") AS chord"
				earthCoordinateNumbers(t, p, b, c, q, []string{"arc", "chord"}, [][]any{{tc.arc, tc.chord}}, []float64{0, 0})
			}
			pi := float64(math.Pi)
			maximum := pi * 6378168 // Round pi before the released SQL multiplication.
			for _, fn := range []string{"sec_to_gc", "gc_to_sec"} {
				q := "SELECT " + fn + "($1::float8) AS v"
				h := earthHeld(t, p, c, q)
				for _, x := range []float64{-1, math.Inf(-1), -math.SmallestNonzeroFloat64, 0, math.Copysign(0, -1), math.Inf(1), math.NaN(), 12756336, math.Nextafter(12756336, 0), math.Nextafter(12756336, math.Inf(1)), maximum, math.Nextafter(maximum, 0), math.Nextafter(maximum, math.Inf(1))} {
					// Independent released SQL formula: the quotient, not input alone, controls the strict upper branch.
					want := float64(0)
					if !(x < 0) {
						if fn == "sec_to_gc" {
							if math.IsNaN(x) || x/12756336 > 1 {
								want = maximum
							} else {
								want = 12756336 * math.Asin(x/12756336)
							}
						} else {
							if math.IsNaN(x) || x/6378168 > pi {
								want = 12756336
							} else {
								want = 12756336 * math.Sin(x/12756336)
							}
						}
					}
					for _, route := range []string{"direct", "fresh", "held"} {
						var got float64
						var err error
						switch route {
						case "direct":
							err = c.QueryRowContext(p.Ctx, q, x).Scan(&got)
						case "fresh":
							s, e := c.PrepareContext(p.Ctx, q)
							if e != nil {
								t.Fatal(e)
							}
							err = s.QueryRowContext(p.Ctx, x).Scan(&got)
							s.Close()
						case "held":
							err = h.Statement.QueryRowContext(p.Ctx, x).Scan(&got)
						}
						if err != nil {
							t.Fatalf("%s %s(%g): %v", route, fn, x, err)
						}
						tol := 2 * math.Abs(math.Nextafter(want, math.Inf(1))-want)
						earthCoordinateFloat(t, got, want, tol)
					}
				}
				h.Statement.Close()
			}
		})
	})
	t.Run("coordinates", func(t *testing.T) {
		earthCoordinateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			earthInstall(t, p, b, c)
			q := "SELECT latitude(ll_to_earth($1::float8,$2::float8)),longitude(ll_to_earth($1::float8,$2::float8))"
			h := earthHeld(t, p, c, q)
			defer h.Statement.Close()
			for _, xy := range [][2]float64{{0, 0}, {0, 90}, {0, -90}, {0, 180}, {0, -180}, {45, 90}, {-45, -90}, {90, 0}, {-90, 0}, {90, 180}, {-90, 180}, {41.8, 87.6}, {360, 360}, {180, 180}, {180, 360}, {-180, -360}, {1e300, 1e300}} {
				for _, route := range []string{"literal", "direct", "fresh", "held"} {
					var lat, lon float64
					var err error
					switch route {
					case "literal":
						err = c.QueryRowContext(p.Ctx, fmt.Sprintf("SELECT latitude(ll_to_earth(%.17g,%.17g)),longitude(ll_to_earth(%.17g,%.17g))", xy[0], xy[1], xy[0], xy[1])).Scan(&lat, &lon)
					case "direct":
						err = c.QueryRowContext(p.Ctx, q, xy[0], xy[1]).Scan(&lat, &lon)
					case "fresh":
						s, e := c.PrepareContext(p.Ctx, q)
						if e != nil {
							t.Fatal(e)
						}
						err = s.QueryRowContext(p.Ctx, xy[0], xy[1]).Scan(&lat, &lon)
						s.Close()
					case "held":
						err = h.Statement.QueryRowContext(p.Ctx, xy[0], xy[1]).Scan(&lat, &lon)
					}
					if err != nil {
						t.Fatalf("%s %v: %v", route, xy, err)
					}
					if math.IsNaN(lat) || math.IsNaN(lon) || math.IsInf(lat, 0) || math.IsInf(lon, 0) {
						t.Errorf("%s %v returned nonfinite coordinates %g,%g", route, xy, lat, lon)
						continue
					}
					wantLat, wantLon := xy[0], xy[1]
					if math.Abs(xy[0]) > 90 {
						if xy[0] == 1e300 {
							if math.IsNaN(lat) || math.IsNaN(lon) || math.Abs(lat) > 90 || math.Abs(lon) > 180 {
								t.Errorf("large finite angle: %g %g", lat, lon)
							}
							continue
						}
						wantLat = 0
						wantLon = 0
						if xy == [2]float64{180, 360} || xy == [2]float64{-180, -360} {
							wantLon = 180
						}
					}
					if math.Abs(lat-wantLat) > 1e-10 || math.Abs(math.Remainder(lon-wantLon, 360)) > 1e-10 {
						t.Errorf("%s %v -> %g,%g want %g,%g", route, xy, lat, lon, wantLat, wantLon)
					}
					if xy[0] == 0 && math.Abs(xy[1]) == 180 && lon != xy[1] {
						t.Errorf("signed dateline got %g want %g", lon, xy[1])
					}
				}
			}
			for _, tc := range []struct {
				q    string
				want float64
			}{{"latitude('(0,0,6378174)'::earth)", 90}, {"latitude('(0,0,-6378174)'::earth)", -90}, {"latitude('(6378168)'::earth)", 0}, {"longitude('(-6378168)'::earth)", 180}, {"longitude('(-6378168,-0)'::earth)", -180}} {
				earthCoordinateBoth(t, p, b, c, fixture.Select("SELECT "+tc.q+" AS v", []string{"v"}, []string{"FLOAT8"}, [][]any{{tc.want}}))
			}
			for _, where := range []string{"", " WHERE false"} {
				values := [][]any{{nil}}
				if where != "" {
					values = [][]any{}
				}
				s := fixture.EarthSelect(b, "SELECT ll_to_earth(NULL,0) AS p"+where, []string{"p"}, values)
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { cubeMetadata(t, b.PG, rows, []string{"p"}, nil) }
				earthCoordinateBoth(t, p, b, c, s)
			}
			earthCoordinateBoth(t, p, b, c, fixture.EarthSelect(b, "SELECT ll_to_earth(0,0) AS p", []string{"p"}, [][]any{{"(6378168, 0, 0)"}}))
			earthCoordinateBoth(t, p, b, c, fixture.Select("SELECT sec_to_gc(NULL) AS a,gc_to_sec(NULL) AS b,latitude(NULL) AS c,longitude(NULL) AS d", []string{"a", "b", "c", "d"}, []string{"FLOAT8", "FLOAT8", "FLOAT8", "FLOAT8"}, [][]any{{nil, nil, nil, nil}}))
			earthCoordinateBoth(t, p, b, c, fixture.EarthSelect(b, "SELECT ll_to_earth(0,NULL) AS p", []string{"p"}, [][]any{{nil}}))
			var a, z sql.NullFloat64
			if err := h.Statement.QueryRowContext(p.Ctx, nil, nil).Scan(&a, &z); err != nil || a.Valid || z.Valid {
				t.Fatalf("held NULL %v %v: %v", a, z, err)
			}
		})
	})
}
