package reference_test

import (
	"database/sql"
	"fmt"
	"math"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFTSRankWeights(t *testing.T) {
	for _, tc := range []struct {
		name, weights, vector string
		rank, cd              float64
	}{
		{"default", "{0.1,0.2,0.4,1}", "cat:1", .06079271, .1},
		{"negative_defaults", "{-1,-2,-3,-4}", "cat:1C", .12158542, .2},
		{"nan_defaults", "{NaN,NaN,NaN,NaN}", "cat:1B", .24317084, .4},
		{"negative_infinity_defaults", "{-Infinity,-Infinity,-Infinity,-Infinity}", "cat:1A", .6079271, 1},
		{"storage_lower_bound", "[0:3]={0.3,0.4,0.5,0.6}", "cat:1", .18237813, .3},
		{"extra_ignored", "{0.1,0.2,0.4,1,Infinity,8}", "cat:1", .06079271, .1},
		{"category_d", "{0.4,0.3,0.2,0.1}", "cat:1D", .24317084, .4},
		{"category_c", "{0.4,0.3,0.2,0.1}", "cat:1C", .18237813, .3},
		{"category_b", "{0.4,0.3,0.2,0.1}", "cat:1B", .12158542, .2},
		{"category_a", "{0.4,0.3,0.2,0.1}", "cat:1A", .06079271, .1},
		{"zero", "{0,0,0,0}", "cat:1", 0, 0},
		{"signed_zero", "{-0,-0,-0,-0}", "cat:1", 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				for n, name := range ftsRankNames {
					value := tc.rank
					if n == 1 {
						value = tc.cd
					}
					for _, normalization := range []string{"", ",0"} {
						ftsRankRoutes(t, p, b, c, "SELECT "+name+"($1::real[],$2::tsvector,'cat'::tsquery"+normalization+")", name, "FLOAT4", ftsRankWant{value: value, tolerance: math.Abs(value) * 2e-7}, tc.weights, tc.vector)
					}
				}
			})
		})
	}
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		// Source float stores, rather than one final float64->float32 cast.
		// Two differently ordered occurrence weights expose the intermediate
		// binary32 sum and the first maximum index. Decimal input is not rounded
		// through TEXT or a NUMERIC accumulator by the test.
		for _, tc := range []struct {
			vector string
			bits   uint32
		}{
			{"cat:1D,2A", 0x3f2b3139},
			{"cat:1A,2D", 0x3f1f8524},
			{"cat:1A,2D,3A", 0x3f30cfef},
		} {
			t.Run(fmt.Sprintf("binary32_%08x", tc.bits), func(t *testing.T) {
				ftsRankRoutes(t, p, b, c, "SELECT ts_rank($1::tsvector,'cat'::tsquery)", "ts_rank", "FLOAT4", ftsRankBits(tc.bits), tc.vector)
			})
		}
		for _, name := range ftsRankNames {
			// getWeights preserves -0; calc_rank starts +0, whereas CD's
			// reciprocal accumulator makes the sign a pinned-oracle question.
			// The assertion intentionally retains the predicted +0 bit witness.
			ftsRankRoutes(t, p, b, c, "SELECT "+name+"('{-0,-0,-0,-0}'::real[],'cat:1'::tsvector,'cat'::tsquery)", name, "FLOAT4", ftsRankBits(0))
		}
	})
}
