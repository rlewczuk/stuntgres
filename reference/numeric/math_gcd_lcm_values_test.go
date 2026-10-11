package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceMathGcdLcmValues(t *testing.T) {
	p := fixture.NewScenario(t)
	referenceMathGcdLcmBackends(t, p, func(t *testing.T, b fixture.Backend) {
		for _, width := range []struct{ cast, typ string }{{"integer", "INT4"}, {"bigint", "INT8"}, {"numeric", "NUMERIC"}} {
			for _, pair := range [][2]int64{{1071, 462}, {-1071, 462}, {1071, -462}, {-1071, -462}, {462, 1071}, {462, -1071}, {-462, 1071}, {-462, -1071}, {0, 0}, {0, -462}, {-462, 0}} {
				t.Run(fmt.Sprint(width.cast, pair), func(t *testing.T) {
					g, l := int64(21), int64(23562)
					if pair[0] == 0 || pair[1] == 0 {
						g, l = 462, 0
						if pair[0] == 0 && pair[1] == 0 {
							g = 0
						}
					}
					var wg, wl any = g, l
					if width.typ == "NUMERIC" {
						wg, wl = fmt.Sprint(g), fmt.Sprint(l)
					}
					q := fmt.Sprintf("SELECT gcd(%d::%s,%d::%s) AS g,lcm(%d::%s,%d::%s) AS l", pair[0], width.cast, pair[1], width.cast, pair[0], width.cast, pair[1], width.cast)
					referenceMathGcdLcmStep(t, p, b, fixture.Select(q, []string{"g", "l"}, []string{width.typ, width.typ}, [][]any{{wg, wl}}))
				})
			}
			for _, args := range []string{"NULL::" + width.cast + ",1::" + width.cast, "1::" + width.cast + ",NULL::" + width.cast, "0::" + width.cast + ",NULL::" + width.cast, "NULL,1::" + width.cast} {
				referenceMathGcdLcmStep(t, p, b, fixture.Select("SELECT gcd("+args+") AS g,lcm("+args+") AS l", []string{"g", "l"}, []string{width.typ, width.typ}, [][]any{{nil, nil}}))
			}
		}
		for _, tc := range []struct{ args, g, l string }{
			{"-1.20,0.1800", "0.0600", "3.6000"}, {"0.00,0.000", "0.000", "0.000"},
			{"0.00,2.0000", "2.0000", "0.0000"}, {"2.0000,0.00", "2.0000", "0.0000"},
			{"1.2,1.2000", "1.2000", "1.2000"}, {"-0.00,-0.000", "0.000", "0.000"},
			{"4331.250,463.75000", "8.75000", "229556.25000"},
			{"4232.820,132.72000", "4.74000", "118518.96000"},
			{"9007199254740993.00,3", "3.00", "9007199254740993.00"},
			{"-9223372036854775808.0,0", "9223372036854775808.0", "0.0"},
		} {
			t.Run(tc.args, func(t *testing.T) {
				referenceMathGcdLcmStep(t, p, b, fixture.Select("SELECT gcd("+tc.args+") AS g,lcm("+tc.args+") AS l", []string{"g", "l"}, []string{"NUMERIC", "NUMERIC"}, [][]any{{tc.g, tc.l}}))
			})
		}
		for _, a := range []string{"NaN", "Infinity", "-Infinity"} {
			for _, other := range []string{"NaN", "Infinity", "-Infinity", "0.00", "1.20", "-1.20"} {
				for _, args := range []string{"'" + a + "'::numeric,'" + other + "'::numeric", "'" + other + "'::numeric,'" + a + "'::numeric"} {
					referenceMathGcdLcmStep(t, p, b, fixture.Select("SELECT gcd("+args+") AS g,lcm("+args+") AS l", []string{"g", "l"}, []string{"NUMERIC", "NUMERIC"}, [][]any{{"NaN", "NaN"}}))
				}
			}
			for _, args := range []string{"'" + a + "'::numeric,NULL::numeric", "NULL::numeric,'" + a + "'::numeric"} {
				referenceMathGcdLcmStep(t, p, b, fixture.Select("SELECT gcd("+args+") AS g,lcm("+args+") AS l", []string{"g", "l"}, []string{"NUMERIC", "NUMERIC"}, [][]any{{nil, nil}}))
			}
		}
	})
}

func TestReferenceMathGcdLcmPrecision(t *testing.T) {
	p := fixture.NewScenario(t)
	referenceMathGcdLcmBackends(t, p, func(t *testing.T, b fixture.Backend) {
		for _, tc := range []struct{ a, b, g, l string }{
			{"1e-16383", "2e-16383", "0." + strings.Repeat("0", 16382) + "1", "0." + strings.Repeat("0", 16382) + "2"},
			{"1e131071", "2.0", "2.0", "1" + strings.Repeat("0", 131071) + ".0"},
			{"1e1000", "1e-1000", "0." + strings.Repeat("0", 999) + "1", "1" + strings.Repeat("0", 1000) + "." + strings.Repeat("0", 1000)},
		} {
			t.Run(fmt.Sprintf("lengths_%d_%d", len(tc.g), len(tc.l)), func(t *testing.T) {
				q := "SELECT gcd($1::numeric,$2::numeric) AS g,lcm($1::numeric,$2::numeric) AS l"
				stmt, err := b.DB.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				s := fixture.Select(q, []string{"g", "l"}, []string{"NUMERIC", "NUMERIC"}, [][]any{{tc.g, tc.l}}, tc.a, tc.b)
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { referenceMathGcdLcmMetadata(t, rows, b.PG) }
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{b.DB, stmt, q}, b.Recorder, s)
			})
		}
		large := strings.Repeat("9", 131072)
		referenceMathGcdLcmStep(t, p, b, fixture.Select("SELECT gcd($1::numeric,2::numeric) AS g", []string{"g"}, []string{"NUMERIC"}, [][]any{{"1"}}, large))
		failure := referenceMathGcdLcmFailure("SELECT lcm($1::numeric,2::numeric)", "22003", "value overflows numeric format")
		failure.Args = []any{large}
		referenceMathGcdLcmStep(t, p, b, failure)
	})
}
