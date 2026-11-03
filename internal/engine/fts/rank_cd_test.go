package fts

import (
	"slices"
	"testing"
)

func TestFTSRankCoverDensity(t *testing.T) {
	// Minimal-cover streams and their independent harmonic/noise contributions:
	// singletons [1],[3]; alternating conjunction [1,2],[2,3],[3,4];
	// equal-position distinct entries have negative noise rounded to floor(n/2).
	for _, tc := range []struct {
		name, vector, query string
		flags               int32
		want                float32
	}{
		{"singleton", "cat:1", "cat", 0, .1},
		{"two_singletons", "cat:1,3", "cat", 0, .2},
		{"adjacent", "cat:1 rat:2", "cat & rat", 0, .1},
		{"one_noise", "cat:1 rat:3", "cat & rat", 0, .05},
		{"harmonic_da", "cat:1 rat:2A", "cat & rat", 0, 2.0 / 11},
		{"alternating_covers", "cat:1,3 rat:2,4", "cat & rat", 0, .3},
		{"three_midpoints", "cat:1,3,7", "cat", 4, .075},
		{"one_midpoint_no_distance", "cat:1", "cat", 4, .1},
		{"equal_position", "cat:1 rat:1", "cat & rat", 0, .1},
		{"saturation", "cat:16383 rat:16384", "cat & rat", 0, .1},
		{"fully_stripped", "cat rat", "cat & rat", 0, 0},
		{"required_stripped", "cat:1 rat", "cat & rat", 0, 0},
		{"weight_mask", "cat:1", "cat:A", 0, 0},
		{"mask_a", "cat:1A", "cat:A", 0, 1},
		{"prefix_overlap", "cat:1 cats:2", "cat:* & cats", 0, .1},
		{"not_cover_not_global", "cat:1 rat:2", "cat & !rat", 0, .1},
		{"phrase_no_cover", "cat:1 rat:3", "cat <-> rat", 0, 0},
		{"phrase_cover", "cat:1 rat:3", "cat <2> rat", 0, .05},
		{"repeated_phrase", "cat:1,2,3", "cat <-> cat", 0, .2},
	} {
		t.Run(tc.name, func(t *testing.T) { rankUnit(t, tc.vector, tc.query, tc.flags, true, tc.want, 1e-7) })
	}
	for _, tc := range []struct {
		name, vector, query string
		want                [][4]int
	}{
		{"singletons", "cat:1,3", "cat", [][4]int{{0, 0, 1, 1}, {1, 1, 3, 3}}},
		{"alternating", "cat:1,3 rat:2,4", "cat & rat", [][4]int{{0, 1, 1, 2}, {1, 2, 2, 3}, {2, 3, 3, 4}}},
		{"equal_position_entries", "cat:1 rat:1", "cat & rat", [][4]int{{0, 1, 1, 1}}},
		{"prefix_same_entry", "cat:1 cats:2", "cat:* & cats", [][4]int{{1, 1, 2, 2}}},
		{"local_not", "cat:1 rat:2", "cat & !rat", [][4]int{{0, 0, 1, 1}}},
		{"phrase_width", "cat:1 rat:3", "cat <2> rat", [][4]int{{0, 1, 1, 3}}},
		{"repeated_phrase_entries", "cat:1,2,3", "cat <-> cat", [][4]int{{0, 1, 1, 2}, {1, 2, 2, 3}}},
		{"required_stripped", "cat:1 rat", "cat & rat", nil},
		{"mask_filters_document", "cat:1", "cat:A", nil},
	} {
		t.Run("stream_"+tc.name, func(t *testing.T) {
			v, q := rankOperands(t, tc.vector, tc.query)
			document, occurrences, err := v.rankDocument(t.Context(), q)
			if err != nil {
				t.Fatal(err)
			}
			var covers [][4]int
			err = rankCovers(t.Context(), q, document, occurrences, func(begin, end int) error {
				covers = append(covers, [4]int{begin, end, int(document[begin].position.number), int(document[end].position.number)})
				return nil
			})
			if err != nil || !slices.Equal(covers, tc.want) {
				t.Fatal("minimal cover stream [begin,end,p,r]", covers, tc.want, err)
			}
		})
	}
}
