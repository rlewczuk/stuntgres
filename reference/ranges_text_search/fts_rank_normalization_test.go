package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFTSRankNormalization(t *testing.T) {
	// REL_18_6 calc_rank/calc_rank_cd, evaluated independently: L=5 and
	// U=4, NOT maximum position=10000. Two cat extents have centers 1,3:
	// NExtent=2, SumDist=1/2, so CD bit4 divides by4; rank ignores it.
	for _, tc := range []struct {
		flag     int
		rank, cd float64
	}{
		{0, .07599088549613953, .20000000298023224},
		{1, .029397286474704742, .11162212491035461},
		{2, .015198176726698875, .03999999910593033},
		{4, .07599088549613953, .05000000074505806},
		{8, .01899772137403488, .05000000074505806},
		{16, .03272749483585358, .08613531291484833},
		{32, .07062409818172455, .1666666716337204},
		{63, .0006326356087811291, .0006005520117469132},
		{64, .07599088549613953, .20000000298023224},
		{-1, .0006326356087811291, .0006005520117469132},
	} {
		t.Run(fmt.Sprint(tc.flag), func(t *testing.T) {
			ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				for n, name := range ftsRankNames {
					want := tc.rank
					if n == 1 {
						want = tc.cd
					}
					for _, weighted := range []bool{false, true} {
						prefix := ""
						if weighted {
							prefix = "'{0.1,0.2,0.4,1}'::real[],"
						}
						query := "SELECT " + name + "(" + prefix + "$1::tsvector,$2::tsquery,$3::integer)"
						ftsRankRoutes(t, p, b, c, query, name, "FLOAT4", ftsRankExact(want), "cat:1,3 rat:10000 stripped zebra", "cat", tc.flag)
						ftsRankRoutes(t, p, b, c, query, name, "FLOAT4", ftsRankBits(0), "cat:1,3 rat:10000 stripped zebra", "absent", tc.flag)
					}
				}
			})
		})
	}
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, vector := range []string{"cat:1", "cat:1 rat:2", "cat:1 rat:1"} {
			// One extent has SumDist=0: bit4 must leave the score unchanged.
			ftsRankRoutes(t, p, b, c, "SELECT ts_rank_cd($1::tsvector,'cat'::tsquery,4)", "ts_rank_cd", "FLOAT4", ftsRankExact(.1), vector)
		}
		// Three strictly increasing centers 1,3,7: SumDist=.5+.25,
		// NExtent=3; a .3 raw score therefore becomes .075.
		ftsRankRoutes(t, p, b, c, "SELECT ts_rank_cd('cat:1,3,7'::tsvector,'cat'::tsquery,4)", "ts_rank_cd", "FLOAT4", ftsRankExact(.075))
	})
}
