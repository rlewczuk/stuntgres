package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFTSHeadlineOptions(t *testing.T) {
	// REL_18_6 deserialize_deflist/buildDefItem and prsd_headline:
	// this grammar is not comma splitting, CSV, or SQL string reparsing.
	for _, tc := range []struct{ name, options, want string }{
		{"default", "", "<b>Cat</b> rat"},
		{"whitespace", "  \t\n  ", "<b>Cat</b> rat"},
		{"commas", ", , ,", "<b>Cat</b> rat"},
		{"case", "sTaRtSeL=<<,sToPsEl=>>", "<<Cat>> rat"},
		{"last_duplicate", "StartSel=[,StartSel=<<,StopSel=],StopSel=>>", "<<Cat>> rat"},
		{"quoted_key", `"StartSel"="[" "StopSel"="]"`, "[Cat] rat"},
		{"quoted_comma_space", `StartSel="a, b " StopSel=" c,d"`, "a, b Cat c,d rat"},
		{"double_quote_escape", `StartSel="a""b" StopSel="c""d"`, "a\"bCatc\"d rat"},
		{"single_quote_escape", `StartSel='a''b' StopSel='c''d'`, "a'bCatc'd rat"},
		{"single_backslash", `StartSel='a\\b' StopSel='c\\d'`, "a\\bCatc\\d rat"},
		{"e_single_quotes", `StartSel=E'a''b' StopSel=E'c\\d'`, "a'bCatc\\d rat"},
		{"backslash_not_general_sql", `StartSel='\n' StopSel='\t'`, "\\nCat\\t rat"},
		{"no_separator_after_quote", `StartSel="["StopSel="]"`, "[Cat] rat"},
		{"empty_markers", `StartSel="",StopSel=''`, "Cat rat"},
		{"raw_numeric_spelling", "StartSel=00012,StopSel=+0003", "12Cat3 rat"},
		// WAITVALUE accepts '=' as the first byte of an unquoted value.
		{"extra_equal", "StartSel==x", "=xCat</b> rat"},
		{"integer_options", "MaxWords=+004,MinWords=1,ShortWord=0,MaxFragments=0", "<b>Cat</b>"},
		{"quoted_integer_whitespace", `MaxWords=" 4 ",MinWords=" +1 "`, "<b>Cat</b>"},
		{"all_options", `MaxWords=4,MinWords=1,ShortWord=0,MaxFragments=1,HighlightAll=false,StartSel=[,StopSel=],FragmentDelimiter=" | "`, "[Cat] rat"},
		{"highlightall_range_bypass", "HighlightAll=true,MaxWords=0,MinWords=-1,ShortWord=-1,MaxFragments=0", "<b>Cat</b> rat"},
		{"boolean_nonsense_false", "HighlightAll=nonsense", "<b>Cat</b> rat"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				ftsRankRoutes(t, p, b, c, "SELECT ts_headline('simple'::regconfig,'Cat rat','cat'::tsquery,$1::text)", "ts_headline", "TEXT", tc.want, tc.options)
			})
		})
	}
	for _, value := range []string{"1", "on", "true", "t", "y", "yes", "TrUe", "ON"} {
		t.Run("boolean_"+value, func(t *testing.T) {
			ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				ftsRankRoutes(t, p, b, c, "SELECT ts_headline('simple'::regconfig,'first second Cat fourth fifth','cat'::tsquery,$1::text)", "ts_headline", "TEXT", "first second <b>Cat</b> fourth fifth", "MaxWords=2,MinWords=1,HighlightAll="+value)
			})
		})
	}
	// Marker limits count bytes. Both ASCII and UTF8 approach the same
	// 32767-byte boundary, and every marker has its own diagnostic witness.
	for _, marker := range []string{"StartSel", "StopSel", "FragmentDelimiter"} {
		for _, unicode := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s_utf8_%t", marker, unicode), func(t *testing.T) {
				ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					value := strings.Repeat("x", 32767)
					if unicode {
						value = strings.Repeat("é", 16383) + "x"
					}
					options := marker + "=\"" + value + "\""
					want := "<b>Cat</b> rat"
					if marker == "StartSel" {
						want = value + "Cat</b> rat"
					}
					if marker == "StopSel" {
						want = "<b>Cat" + value + " rat"
					}
					ftsRankRoutes(t, p, b, c, "SELECT ts_headline('simple'::regconfig,'Cat rat','cat'::tsquery,$1::text)", "ts_headline", "TEXT", want, options)
				})
			})
		}
	}
}
