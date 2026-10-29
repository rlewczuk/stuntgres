package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceFTSStatCounts(t *testing.T) {
	ftsParserScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c := ftsInspectionConnection(t, p, b)
		ftsStatSetup(t, p, b, c)
		inner := "SELECT vector FROM fts_stat_documents"
		ftsInspectionBoth(t, p, b, c, fixture.Select("SELECT * FROM ts_stat("+ftsTypesQuote(inner)+") ORDER BY word", ftsStatColumns, ftsStatTypes, ftsStatBase()))
		for _, tc := range []struct {
			weights string
			want    [][]any
		}{
			{"ab", [][]any{{"cat", int64(1), int64(2)}, {"dog", int64(1), int64(1)}}},
			{"AaBb", [][]any{{"cat", int64(1), int64(2)}, {"dog", int64(1), int64(1)}}},
			{"d", [][]any{{"dog", int64(2), int64(2)}}}, {"c", [][]any{{"cat", int64(1), int64(1)}}},
			{"", ftsStatBase()}, {"xyz", ftsStatBase()}, {"xAa!Bb", [][]any{{"cat", int64(1), int64(2)}, {"dog", int64(1), int64(1)}}},
		} {
			ftsInspectionBoth(t, p, b, c, fixture.Select("SELECT * FROM ts_stat($1::text,$2::text) ORDER BY word", ftsStatColumns, ftsStatTypes, tc.want, inner, tc.weights))
		}
		for _, tc := range []struct {
			inner, weights string
			want           [][]any
		}{
			{"SELECT 'x:1A,1B,2D'::tsvector", "", [][]any{{"x", int64(1), int64(2)}}},
			{"SELECT 'x:1A,1B,2D'::tsvector", "a", [][]any{{"x", int64(1), int64(1)}}},
			{"SELECT strip('x:1,2'::tsvector)", "", [][]any{{"x", int64(1), int64(1)}}},
			{"SELECT strip('x:1,2'::tsvector)", "abcd", nil},
			{"SELECT 'x:16383A,16384B,999999D'::tsvector", "", [][]any{{"x", int64(1), int64(1)}}},
			{"SELECT NULL::tsvector", "", nil}, {"SELECT ''::tsvector", "", nil},
		} {
			ftsInspectionBoth(t, p, b, c, fixture.Select("SELECT * FROM ts_stat($1::text,$2::text) ORDER BY word", ftsStatColumns, ftsStatTypes, tc.want, tc.inner, tc.weights))
		}
		for _, query := range []string{"SELECT * FROM ts_stat(NULL::text)", "SELECT * FROM ts_stat('malformed SQL',NULL::text)", "SELECT * FROM ts_stat(NULL::text,'a')"} {
			ftsInspectionBoth(t, p, b, c, fixture.Select(query, ftsStatColumns, ftsStatTypes, nil))
		}
	})
}
