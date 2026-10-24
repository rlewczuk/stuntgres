package reference_test

import (
	"database/sql"
	"math"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceEarthPointValues(t *testing.T) {
	earthPointScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		earthInstall(t, p, b, c)
		// Fixed five-decimal witnesses published in REL_18_6 earthdistance.out.
		for _, tc := range []struct{ a, z, miles string }{
			{"(0,0)", "(0,0)", "0.00000"},
			{"(0,0)", "(180,0)", "12436.77274"},
			{"(0,0)", "(0,90)", "6218.38637"},
			{"(0,0)", "(90,0)", "6218.38637"},
			{"(0,0)", "(1,0)", "69.09318"},
			{"(0,0)", "(0,1)", "69.09318"},
			{"(0,30)", "(1,30)", "59.83626"},
			{"(0,30)", "(0,31)", "69.09318"},
			{"(0,60)", "(1,60)", "34.54626"},
			{"(0,60)", "(0,61)", "69.09318"},
			{"(87.6,41.8)", "(106.7,35.1)", "1129.18983"},
		} {
			earthPointMiles(t, p, b, c, "point '"+tc.a+"'", "point '"+tc.z+"'", tc.miles, 0)
		}
		// Independent high-precision radius arithmetic, not cube meter units.
		for _, tc := range []struct {
			z     string
			miles float64
		}{
			{"(1,0)", 69.09318190000541},
			{"(90,0)", 6218.386371000487},
			{"(180,0)", 12436.772742000974},
		} {
			earthPointMiles(t, p, b, c, "point '(0,0)'", "point '"+tc.z+"'", tc.miles, 4e-12)
		}
		for _, tc := range [][2]string{
			{"NULL::point", "point '(0,0)'"}, {"point '(0,0)'", "NULL::point"},
			{"NULL::point", "NULL::point"}, {"NULL::point", "point '(Infinity,NaN)'"},
			{"point '(NaN,-Infinity)'", "NULL::point"},
		} {
			earthPointMiles(t, p, b, c, tc[0], tc[1], nil, 0)
		}
		earthPointMiles(t, p, b, c, "point '(0,0)'", "point '(0,0)'", float64(0), 0)
	})
}

func TestReferenceEarthPointEdges(t *testing.T) {
	earthPointScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		earthInstall(t, p, b, c)
		for _, tc := range []struct{ a, z, miles string }{
			{"(179,0)", "(-179,0)", "138.18636"},
			{"(-179,0)", "(179,0)", "138.18636"},
			{"(0,90)", "(0,-90)", "12436.77274"},
			{"(0,60)", "(1,60)", "34.54626"},
			{"(1,60)", "(0,60)", "34.54626"},
			{"(60,0)", "(60,1)", "69.09318"},
			{"(360,0)", "(361,0)", "69.09318"},
		} {
			earthPointMiles(t, p, b, c, "point '"+tc.a+"'", "point '"+tc.z+"'", tc.miles, 0)
		}
		earthPointMiles(t, p, b, c, "point '(0,100)'", "point '(0,100)'", float64(0), 0)
		// Tiny separation must not inherit POINT predicate's 1e-6 epsilon.
		earthPointMiles(t, p, b, c, "point '(0,0)'", "point '(0,0.000000001)'", float64(6.909318190000541e-8), 3e-23)
		earthCoordinateBoth(t, p, b, c, fixture.Select("SELECT point '(0,0)' <@> point '(0,0.000000001)' > 0 AS positive", []string{"positive"}, []string{"BOOL"}, [][]any{{true}}))
		for _, nonfinite := range []string{"(NaN,0)", "(0,NaN)", "(Infinity,0)", "(-Infinity,0)", "(0,Infinity)", "(0,-Infinity)"} {
			for _, pair := range [][2]string{{"point '" + nonfinite + "'", "point '(1,2)'"}, {"point '(1,2)'", "point '" + nonfinite + "'"}} {
				earthPointMiles(t, p, b, c, pair[0], pair[1], math.NaN(), 0)
			}
		}
		for _, z := range []string{"(179.999999,0.000001)", "(180,0)"} {
			q := "SELECT point '(0,0)' <@> point '" + z + "' <= 12436.772742000978::float8 AS bounded,geo_distance(point '(0,0)',point '" + z + "') >= 12436::float8 AS near_half"
			earthCoordinateBoth(t, p, b, c, fixture.Select(q, []string{"bounded", "near_half"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
		}
	})
}
