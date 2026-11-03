package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFTSRankValues(t *testing.T) {
	// PG18 Table 9.43 and REL_18_6 tstypes.out (six-digit regression
	// rendering). The latter's tolerance is only the quoted decimal precision.
	cases := []struct {
		name, vector, query string
		rank, cd, tolerance float64
	}{
		{"single", "cat:1", "cat", .06079271, .1, 5e-9},
		{"repeated", "cat:1,3", "cat", .07599088549613953, .2, 5e-9},
		{"released_or_c", "a:1 s:2C d g", "a|s", .0911891, .3, 5e-8},
		{"released_and_c", "a:1 s:2C d g", "a&s", .140153, .133333, 5e-7},
		{"released_or_b", "a:1 s:2B d g", "a|s", .151982, .5, 5e-7},
		{"released_and_b", "a:1 s:2B d g", "a&s", .198206, .16, 5e-7},
		{"released_or_d", "a:1 s:2 d g", "a|s", .0607927, .2, 5e-8},
		{"released_and_d", "a:1 s:2 d g", "a&s", .0991032, .1, 5e-8},
		{"released_missing", "a:1 sa:2C d g", "a|s", .0303964, .1, 5e-8},
		{"released_prefix", "a:1 sa:2C d g", "a|s:*", .0911891, .3, 5e-8},
		{"stripped", "cat", "cat", .06079271, 0, 5e-9},
		{"empty_vector", "", "cat", 0, 0, 0},
		{"missing", "cat:1", "rat", 0, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				for n, name := range ftsRankNames {
					value := tc.rank
					if n == 1 {
						value = tc.cd
					}
					want := ftsRankWant{value: value, tolerance: tc.tolerance}
					// Source float stores give 0x3d9ba11c; the pinned oracle's
					// shortest decimal is 0.075990885, not an exact widening.
					if tc.name == "repeated" && n == 0 {
						want = ftsRankBits(0x3d9ba11c)
					}
					for form, types := range ftsRankForms {
						t.Run(fmt.Sprintf("%s_%d", name, form), func(t *testing.T) {
							ftsRankRoutes(t, p, b, c, "SELECT "+name+"("+ftsRankParameters(types)+")", name, "FLOAT4", want, ftsRankArguments(types, tc.vector, tc.query)...)
						})
					}
				}
			})
		})
	}
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer ftsConvertSetting(t, p, c)()
		ftsRankRoutes(t, p, b, c, "SELECT ts_rank(to_tsvector('raining cats and dogs'),'cat'::tsquery)", "ts_rank", "FLOAT4", ftsRankWant{value: .06079271, tolerance: 5e-9})
		// An empty constructor is evaluated once here; ranking an already empty
		// query must not manufacture another constructor notice.
		for _, name := range ftsRankNames {
			ftsRankRoutes(t, p, b, c, "SELECT "+name+"('cat:1'::tsvector,query) FROM (SELECT ''::tsquery AS query) AS q", name, "FLOAT4", ftsRankBits(0))
			ftsRankEmptyQueryNotices(t, p, b, name)
		}
	})
}

func TestReferenceFTSRankSemantics(t *testing.T) {
	// Source-derived independent literals; the difficult CD cover cases below
	// are deliberately visible assertions for the root's pinned-oracle gate.
	cases := []struct {
		name, vector, query string
		rank, cd            float64
	}{
		{"or_absent_divisor", "cat:1", "cat|rat", .030396355, .1},
		{"not_leaf_collected", "cat:1", "!cat", .06079271, 0},
		{"not_no_global_match_gate", "cat:1 rat:2", "cat & !rat", .0991032, .1},
		{"or_not_root", "cat:1 rat:2", "cat | !rat", .06079271, .1},
		{"phrase_root", "cat:1 rat:3", "cat <-> rat", .09850086, 0},
		{"phrase_gap_matches", "cat:1 rat:3", "cat <2> rat", .09850086, .05},
		{"one_missing_and", "cat:1", "cat & rat", 1e-20, 0},
		{"third_missing_and", "cat:1 rat:2", "cat & rat & zebra", .0991032, 0},
		{"equal_positions", "cat:1 rat:1", "cat & rat", 1e-20, .1},
		{"stripped_synthetic_equal", "cat rat", "cat & rat", 1e-16, 0},
		{"one_stripped_required", "cat:1 rat", "cat & rat", 1e-16, 0},
		{"one_synthetic_equal_at_limit", "cat:16383 rat", "cat & rat", 1e-16, 0},
		{"last_stored_position", "cat:16383", "cat", .06079271, .1},
		{"clamped_input_position", "cat:16384", "cat", .06079271, .1},
		{"weight_mask_ignored_frequency", "cat:1", "cat:A", .06079271, 0},
		{"weight_mask_allowed", "cat:1A", "cat:A", .6079271, 1},
		{"duplicate_operand", "cat:1", "cat|cat", .06079271, .1},
		{"duplicate_masks", "cat:1A", "cat:A|cat:D", .6079271, 1},
		// SortAndUniqItems compares lexeme bytes only, preserving the chosen
		// operand's prefix flag. This query retains cat:* and calc_rank_or
		// separately adds both matching entries before dividing by one item.
		{"duplicate_prefix", "cat:1 cats:2", "cat|cat:*", .12158542, .2},
		{"prefix_multiple_candidates", "cat:1 cats:2", "ca:*", .12158542, .2},
		{"position_order_da", "cat:1D,2A", "cat", .6687198281288147, 1.1},
		{"position_order_ad", "cat:1A,2D", "cat", .6231253, 1.1},
		{"first_maximum_tie", "cat:1A,2D,3A", "cat", .6906728148460388, 2.1},
		{"adjacent_cover", "cat:1 rat:2", "cat & rat", .0991032, .1},
		{"gap_cover", "cat:1 rat:3", "cat & rat", .09850086, .05},
		{"harmonic_cover", "cat:1 rat:2A", "cat & rat", .3133917, .18181818},
		{"repeated_covers", "cat:1,3 rat:2,4", "cat & rat", .3400053381919861, .3},
		{"repeated_phrase", "cat:1,2,3", "cat <-> cat", .08274563401937485, .2},
		// calc_rank_and remembers only the last prefix candidate: cats:2.
		// The later exact cats:2 has distance zero from it, so no pair is
		// initialized and calc_rank converts -1 to 1e-20 (CD is independent).
		{"overlapping_prefix", "cat:1 cats:2", "cat:* & cats", 1e-20, .1},
		{"prefix_prior_last_candidate", "aa:1 ab:10 b:11", "a:* & b", .09910321980714798, .1},
		{"distance_100", "cat:1 rat:101", "cat & rat", 4.058137391394195e-15, .001},
		{"distance_101", "cat:1 rat:102", "cat & rat", 1e-16, .1 / 101},
		{"saturated_positions", "cat:16383 rat:16384", "cat & rat", 1e-20, .1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				for n, name := range ftsRankNames {
					want := tc.rank
					if n == 1 {
						want = tc.cd
					}
					// Several literals above quote six-digit released/source arithmetic.
					ftsRankRoutes(t, p, b, c, "SELECT "+name+"($1::tsvector,$2::tsquery)", name, "FLOAT4", ftsRankWant{value: want, tolerance: absRank(want) * 6e-6}, tc.vector, tc.query)
				}
			})
		})
	}
	// REL_18_6 qsort_arg is not stable: insertion (<7), exact middle (7),
	// median3 (>7) and ninther (>40) preserve different first-equal prefixes.
	// The right-first arena begins z,cat,cat,cat:*,...; z is unmatched.
	// Prefix survivor sums two D entries and divides by two unique operands;
	// exact survivor sums only cat then divides by two. CD sees all leaves.
	for _, tc := range []struct {
		count int
		bits  uint32
	}{
		{6, 0x3cf901c7}, {7, 0x3d7901c7}, {8, 0x3d7901c7}, {40, 0x3d7901c7}, {41, 0x3cf901c7},
	} {
		t.Run(fmt.Sprintf("operand_ties_%02d", tc.count), func(t *testing.T) {
			parts := make([]string, tc.count)
			for i := range parts {
				word := "cat"
				if i == 0 {
					word = "z"
				} else if i >= 3 && i%2 == 1 {
					word = "cat:*"
				}
				parts[len(parts)-1-i] = word
			}
			query := strings.Join(parts, " | ")
			ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				for i, name := range ftsRankNames {
					bits := tc.bits
					if i == 1 {
						bits = 0x3e4ccccd
					}
					ftsRankRoutes(t, p, b, c, "SELECT "+name+"($1::tsvector,$2::tsquery)", name, "FLOAT4", ftsRankWant{bits: &bits}, "cat:1 cats:2", query)
				}
			})
		})
	}
}

func absRank(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}
