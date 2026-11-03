package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFTSHeadlineValues(t *testing.T) {
	for _, tc := range []struct{ name, config, document, query, options, want string }{
		{"table_example", "english", "The fat cat ate the rat.", "cat", "", "The fat <b>cat</b> ate the rat."},
		{"stem_original_case", "english", "Rats & cats", "rat", "HighlightAll=true", "<b>Rats</b> & cats"},
		{"simple_no_stem", "simple", "Rats & cats", "rat", "HighlightAll=true", "Rats & cats"},
		{"raw_query_not_normalized", "english", "Rats & cats", "Rats", "HighlightAll=true", "Rats & cats"},
		{"prefix", "english", "Cats, cattle; CAT!", "cat:*", "HighlightAll=true", "<b>Cats</b>, <b>cattle</b>; <b>CAT</b>!"},
		{"phrase_marks_outside_match", "english", "cat rat cat bird rat", "cat <-> rat", "HighlightAll=true", "<b>cat</b> <b>rat</b> <b>cat</b> bird <b>rat</b>"},
		{"boolean_marks_negative", "english", "cat rat bird", "cat & !rat", "HighlightAll=true", "<b>cat</b> <b>rat</b> bird"},
		{"mask_ignored", "english", "Cat", "cat:A", "HighlightAll=true", "<b>Cat</b>"},
		{"duplicate_associations", "english", "Cat cats", "cat|cat:*", "HighlightAll=true", "<b>Cat</b> <b>cats</b>"},
		{"utf8_punctuation", "simple", "Éléphant, café!\n猫 & café?", "café", "HighlightAll=true", "Éléphant, <b>café</b>!\n猫 & <b>café</b>?"},
		{"no_html_escape", "simple", "Cat & dog \"cat\" < > &amp;", "cat", "HighlightAll=true", "<b>Cat</b> & dog \"<b>cat</b>\" < > &amp;"},
		{"tags_removed", "simple", "<em>cat</em> &amp; dog", "cat", "", " <b>cat</b>  &amp; dog"},
		{"tags_preserved", "simple", "<em>cat</em> &amp; dog", "cat", "HighlightAll=true", "<em><b>cat</b></em> &amp; dog"},
		{"hyphen_components", "simple", "fat-cat cat-dog", "cat", "HighlightAll=true", "fat-<b>cat</b> <b>cat</b>-dog"},
		{"url_components", "simple", "http://example.com/cat cat", "cat", "HighlightAll=true", "http://example.com/cat <b>cat</b>"},
		{"no_match", "simple", "alpha beta gamma", "cat", "", "alpha beta gamma"},
		{"empty_document", "english", "", "cat", "", ""},
		{"empty_query", "english", "Cat and rat.", "", "", "Cat and rat."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				defer ftsConvertSetting(t, p, c)()
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET default_text_search_config='"+tc.config+"'", 0))
				// All four text overloads, including the two different three-arg
				// shapes, are exercised through one shared body.
				for _, explicit := range []bool{false, true} {
					prefix := ""
					if explicit {
						prefix = "'" + tc.config + "'::regconfig,"
					}
					for _, options := range []bool{false, true} {
						if !options && tc.options != "" {
							continue
						}
						query := "SELECT ts_headline(" + prefix + "$1::text,$2::tsquery"
						args := []any{tc.document, tc.query}
						if options {
							query += ",$3::text"
							args = append(args, tc.options)
						}
						query += ")"
						ftsRankRoutes(t, p, b, c, query, "ts_headline", "TEXT", tc.want, args...)
					}
				}
			})
		})
	}
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		// Exact PG18 §12.3.4 examples. Newlines below are original source,
		// not psql's '+' display continuation markers.
		const first = "The most common type of search\nis to find all documents containing given query terms\nand return them in order of their similarity to the\nquery."
		ftsRankRoutes(t, p, b, c, "SELECT ts_headline('english'::regconfig,$1::text,to_tsquery('english','query & similarity'))", "ts_headline", "TEXT", "containing given <b>query</b> terms\nand return them in order of their <b>similarity</b> to the\n<b>query</b>.", first)
		const second = "Search terms may occur\nmany times in a document,\nrequiring ranking of the search matches to decide which\noccurrences to display in the result."
		ftsRankRoutes(t, p, b, c, "SELECT ts_headline('english'::regconfig,$1::text,to_tsquery('english','search & term'),'MaxFragments=10, MaxWords=7, MinWords=3, StartSel=<<, StopSel=>>')", "ts_headline", "TEXT", "<<Search>> <<terms>> may occur\nmany times ... ranking of the <<search>> matches to decide", second)
		ftsRankEmptyQueryNotices(t, p, b, "ts_headline")
	})
}
