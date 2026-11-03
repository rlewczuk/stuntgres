package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFTSHeadlineFragments(t *testing.T) {
	// First five literals are REL_18_6 tsearch.out headline regressions.
	// Remaining short documents are independent source-derived discriminators
	// for mark_hl_words/mark_hl_fragments. Root must pin their exact literals,
	// including punctuation/space boundaries, before production is authorized.
	for _, tc := range []struct{ name, config, document, query, options, want string }{
		{"released_phrase_cover", "simple", "1 2 3 1 3", "1 <-> 3", "MaxWords=2,MinWords=1", "<b>1</b> <b>3</b>"},
		{"released_whole_cover", "simple", "1 2 3 1 3", "1 & 3", "MaxWords=4,MinWords=1", "<b>1</b> 2 <b>3</b>"},
		{"released_phrase_not_first_hit", "simple", "1 2 3 1 3", "1 <-> 3", "MaxWords=4,MinWords=1", "<b>1</b> <b>3</b>"},
		{"released_boolean_phrase", "english", "Lorem ipsum urna.  Nullam nullam ullamcorper urna.", "lorem & (ullamcorp <-> urna)", "MaxWords=100,MinWords=1", "<b>Lorem</b> ipsum <b>urna</b>.  Nullam nullam <b>ullamcorper</b> <b>urna</b>"},
		{"released_extend_left", "english", "Lorem ipsum urna.  Nullam nullam ullamcorper urna.", "ullamcorp <-> urna", "MaxWords=100,MinWords=5", "<b>urna</b>.  Nullam nullam <b>ullamcorper</b> <b>urna</b>."},
		{"first_minwords_fallback", "simple", "alpha beta gamma delta epsilon", "missing", "MaxWords=4,MinWords=2", "alpha beta"},
		{"nonmatching_prefix_fallback", "simple", "alpha beta gamma delta epsilon", "zeb:*", "MaxWords=4,MinWords=2,MaxFragments=2", "alpha beta"},
		{"whole_cover_over_early_partial", "simple", "cat alpha beta gamma rat cat rat final", "cat & rat", "MaxWords=3,MinWords=1,ShortWord=0", "<b>rat</b> <b>cat</b>"},
		{"earliest_equal_interest", "simple", "cat alpha beta cat gamma delta", "cat", "MaxWords=2,MinWords=1,ShortWord=0,MaxFragments=1", "<b>cat</b> alpha"},
		{"interesting_count_before_earliest", "simple", "cat alpha beta gamma rat rat delta", "cat | (rat <-> rat)", "MaxWords=3,MinWords=1,ShortWord=0,MaxFragments=1", "<b>rat</b> <b>rat</b>"},
		{"shortest_tie", "simple", "cat alpha beta rat gamma delta cat rat", "cat & rat", "MaxWords=4,MinWords=1,ShortWord=0,MaxFragments=1", "delta <b>cat</b> <b>rat</b>"},
		{"left_right_stretch", "simple", "alpha beta cat gamma delta epsilon", "cat", "MaxWords=5,MinWords=1,ShortWord=0,MaxFragments=1", "alpha beta <b>cat</b> gamma delta"},
		{"overlaps_excluded", "simple", "alpha cat beta cat gamma delta", "cat", "MaxWords=4,MinWords=1,ShortWord=0,MaxFragments=3", "alpha <b>cat</b> beta <b>cat</b>"},
		{"document_order_not_score_order", "simple", "cat alpha beta gamma delta epsilon rat rat zeta", "cat | (rat <-> rat)", "MaxWords=2,MinWords=1,ShortWord=0,MaxFragments=2,FragmentDelimiter=|", "<b>cat</b> alpha|<b>rat</b>"},
		{"fragment_cap", "simple", "cat alpha beta gamma delta epsilon rat rat zeta", "cat | (rat <-> rat)", "MaxWords=2,MinWords=1,ShortWord=0,MaxFragments=1", "<b>cat</b> alpha"},
		// Distinct phrase operands avoid repeated-association word counts:
		// the later two-interest cover wins first, but output stays in source order.
		{"document_order_after_later_score_wins", "simple", "cat alpha beta gamma delta epsilon rat dog zeta", "cat | (rat <-> dog)", "MaxWords=2,MinWords=1,ShortWord=0,MaxFragments=2,FragmentDelimiter=|", "<b>cat</b> alpha|<b>rat</b> <b>dog</b>"},
		{"distinct_terms_fragment_cap", "simple", "cat alpha beta gamma delta epsilon rat dog zeta", "cat | (rat <-> dog)", "MaxWords=2,MinWords=1,ShortWord=0,MaxFragments=1", "<b>rat</b> <b>dog</b>"},
		{"highlightall_fragment_not_whole", "simple", "alpha beta gamma cat delta epsilon zeta", "cat", "MaxWords=3,MinWords=1,ShortWord=0,MaxFragments=1,HighlightAll=true", "gamma <b>cat</b> delta"},
		{"shortword_trim", "simple", "a an alpha cat beta a an", "cat", "MaxWords=6,MinWords=2,ShortWord=3,MaxFragments=1", "alpha <b>cat</b> beta"},
		{"shortword_utf8_bytes", "simple", "é éé cat éé é", "cat", "MaxWords=5,MinWords=1,ShortWord=3,MaxFragments=1", "éé <b>cat</b> éé"},
		{"parser_hyphen_word_count", "simple", "alpha fat-cat beta gamma", "cat", "MaxWords=3,MinWords=1,ShortWord=0,MaxFragments=1", "fat-<b>cat</b> beta"},
		{"phrase_stopword_width", "english", "cat rat alpha cat and rat beta", "cat <2> rat", "MaxWords=4,MinWords=1,ShortWord=0", "<b>cat</b> and <b>rat</b>"},
		{"negative_subrange_recheck", "simple", "cat rat alpha beta cat gamma delta", "cat & !rat", "MaxWords=3,MinWords=1,ShortWord=0,MaxFragments=1", "<b>cat</b>"},
		{"or_not_fallback", "simple", "alpha beta gamma delta", "cat | !rat", "MaxWords=3,MinWords=2,ShortWord=0,MaxFragments=2", "alpha beta"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				ftsRankRoutes(t, p, b, c, "SELECT ts_headline($1::regconfig,$2::text,$3::tsquery,$4::text)", "ts_headline", "TEXT", tc.want, tc.config, tc.document, tc.query, tc.options)
			})
		})
	}
}
