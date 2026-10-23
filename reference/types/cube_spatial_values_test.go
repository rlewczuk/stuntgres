package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCubeSpatialValues(t *testing.T) {
	cubeSpatialScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		cubeInstall(t, p, b, c)
		cases := append([]cubeSpatialCase(nil), cubeSpatialFinite...)
		cases = append(cases, cubeSpatialCase{"dimensions100", "(3,4," + strings.Repeat("0,", 97) + "0)", "()", false, true, false, 5, 7, 4})
		for i, tc := range cases {
			t.Run(cubeSpatialCaseName(i, tc.name), func(t *testing.T) {
				for _, reverse := range []bool{false, true} {
					a, d, contains, contained := tc.a, tc.b, tc.contains, tc.contained
					if reverse {
						a, d, contains, contained = d, a, contained, contains
					}
					spellings := [][2]string{{a, d}}
					flip := func(value string) string {
						if at := strings.Index(value, "),("); at >= 0 {
							return value[at+2:] + "," + value[:at+1]
						}
						return value
					}
					if flip(a) != a || flip(d) != d {
						spellings = append(spellings, [2]string{flip(a), flip(d)})
					}
					for _, pair := range spellings {
						q := "SELECT " + cubeSpatialProjection(cubeLiteral(pair[0])+"::cube", cubeLiteral(pair[1])+"::cube")
						want := cubeSpatialResult(tc.overlap, contains, contained, tc.euclidean, tc.taxicab, tc.chebyshev)
						cubeSpatialBoth(t, p, b, c, cubeSpatialNamedSelect(b, q, [][]any{want}))
					}
				}
			})
		}
		// Root's pinned PG18.6 oracle confirmed these independently specified
		// orientation, equal-infinity and sequential binary64 arithmetic records.
		special := []struct {
			name, a, b                    string
			overlap, contains, contained  bool
			euclidean, taxicab, chebyshev string
		}{
			{"nan_point", "(NaN)", "(0)", true, true, true, "0", "0", "0"},
			{"nan_first_corner", "(NaN),(1)", "(2)", false, false, false, "0", "0", "0"},
			{"nan_second_corner", "(1),(NaN)", "(2)", true, true, true, "0", "0", "0"},
			{"nan_multi_first", "(NaN,0),(1,1)", "(2,4)", false, false, false, "3", "3", "3"},
			{"nan_multi_second", "(1,0),(NaN,1)", "(2,4)", false, false, false, "3", "3", "3"},
			{"nan_axis", "(NaN,3)", "(0,0)", false, false, false, "3", "3", "3"},
			{"positive_infinity_equal", "(Infinity)", "(Infinity)", true, true, true, "NaN", "NaN", "0"},
			{"negative_infinity_equal", "(-Infinity)", "(-Infinity)", true, true, true, "NaN", "NaN", "0"},
			{"opposite_infinity", "(-Infinity)", "(Infinity)", false, false, false, "Infinity", "Infinity", "Infinity"},
			{"finite_infinity", "(0)", "(Infinity)", false, false, false, "Infinity", "Infinity", "Infinity"},
			{"nan_infinity_mixture", "(NaN,Infinity,3)", "(0,Infinity,0)", false, false, false, "NaN", "NaN", "3"},
			{"square_overflow", "(1e200)", "(0)", false, false, false, "Infinity", "1e+200", "1e+200"},
			{"square_underflow", "(1e-200)", "(0)", false, false, false, "0", "1e-200", "1e-200"},
			{"subtraction_overflow", "(1e308)", "(-1e308)", false, false, false, "Infinity", "Infinity", "Infinity"},
			{"sum_large_first", "(1e16,1,1)", "()", false, true, false, "1e+16", "1e+16", "1e+16"},
			{"sum_large_last", "(1,1,1e16)", "()", false, true, false, "1e+16", "1.0000000000000002e+16", "1e+16"},
			{"signed_zero", "(-0)", "(0)", true, true, true, "0", "0", "0"},
		}
		for _, tc := range special {
			t.Run(tc.name, func(t *testing.T) {
				for _, reverse := range []bool{false, true} {
					a, d, contains, contained := tc.a, tc.b, tc.contains, tc.contained
					if reverse {
						a, d, contains, contained = d, a, contained, contains
					}
					parts := make([]string, len(cubeSpatialSurfaces))
					types := append([]string(nil), cubeSpatialTypes...)
					for i, surface := range cubeSpatialSurfaces {
						parts[i] = "(" + cubeSpatialExpression(surface, cubeLiteral(a)+"::cube", cubeLiteral(d)+"::cube") + ")"
						if i >= 3 {
							parts[i] += "::text"
							types[i] = "TEXT"
						}
						parts[i] += " AS " + cubeSpatialNames[i]
					}
					want := [][]any{{tc.overlap, contains, contained, tc.euclidean, tc.euclidean, tc.taxicab, tc.chebyshev}}
					cubeSpatialBoth(t, p, b, c, cubeSpatialSelect(b, "SELECT "+strings.Join(parts, ","), cubeSpatialNames, types, want))
				}
			})
		}
	})
}
