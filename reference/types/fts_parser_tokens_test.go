package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFTSParserDescriptors(t *testing.T) {
	for _, parser := range []string{"'default'", "'pg_catalog.default'::text", "3722::oid"} {
		t.Run(parser, func(t *testing.T) {
			ftsParserScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				ftsParserSelect(t, p, b, "SELECT * FROM ts_token_type("+parser+") ORDER BY tokid", []string{"tokid", "alias", "description"}, []string{"INT4", "TEXT", "TEXT"}, ftsParserDescriptors())
			})
		})
	}
}

func TestReferenceFTSParserTokens(t *testing.T) {
	for _, tc := range []struct {
		name, document string
		want []ftsParserToken
	}{
		{"acceptance", "Word 123 foo@example.com http://example.com/stuff/index.html !", []ftsParserToken{{1,"Word"},{12," "},{22,"123"},{12," "},{4,"foo@example.com"},{12," "},{14,"http://"},{5,"example.com/stuff/index.html"},{6,"example.com"},{18,"/stuff/index.html"},{12," !"}}},
		{"numeric_hyphen", "foo-bar-beta1", []ftsParserToken{{15,"foo-bar-beta1"},{11,"foo"},{12,"-"},{11,"bar"},{12,"-"},{9,"beta1"}}},
		{"ascii_hyphen", "qwe-wer", []ftsParserToken{{16,"qwe-wer"},{11,"qwe"},{12,"-"},{11,"wer"}}},
		{"word_hyphen", "mañana-niño", []ftsParserToken{{17,"mañana-niño"},{10,"mañana"},{12,"-"},{10,"niño"}}},
		{"documented_blanks", "123 - a number", []ftsParserToken{{22,"123"},{12," "},{12,"- "},{1,"a"},{12," "},{1,"number"}}},
		{"asciiword", "elephant", []ftsParserToken{{1,"elephant"}}},
		{"word", "mañana", []ftsParserToken{{2,"mañana"}}},
		{"numword", "beta1", []ftsParserToken{{3,"beta1"}}},
		{"email", "foo@example.com", []ftsParserToken{{4,"foo@example.com"}}},
		{"scientific", "-1.234e56", []ftsParserToken{{7,"-1.234e56"}}},
		{"version", "8.3.0", []ftsParserToken{{8,"8.3.0"}}},
		{"file", "/usr/local/foo.txt", []ftsParserToken{{19,"/usr/local/foo.txt"}}},
		{"float", "-1.234", []ftsParserToken{{20,"-1.234"}}},
		{"signed", "-1234", []ftsParserToken{{21,"-1234"}}},
		{"unsigned", "1234", []ftsParserToken{{22,"1234"}}},
		{"entity", "&amp;", []ftsParserToken{{23,"&amp;"}}},
		{"email_fallback", "qwe@efd.r", []ftsParserToken{{1,"qwe"},{12,"@"},{19,"efd.r"}}},
		{"numeric_fallback", "readline-4.2", []ftsParserToken{{1,"readline"},{20,"-4.2"}}},
		{"file_fallback", "gist.c.", []ftsParserToken{{19,"gist.c"},{12,"."}}},
		{"malformed_tag", "<i <b>", []ftsParserToken{{12,"<"},{1,"i"},{12," "},{13,"<b>"}}},
		{"quoted_tag", `<a href="qwe<qwe>">`, []ftsParserToken{{13,`<a href="qwe<qwe>">`}}},
		{"slash_fallback", "http://www.com/ ", []ftsParserToken{{14,"http://"},{6,"www.com"},{12,"/ "}}},
		{"percent_path", "7aew.werc.ewr:8100/?ad=qwe&dw=%20%32", []ftsParserToken{{5,"7aew.werc.ewr:8100/?ad=qwe&dw=%20%32"},{6,"7aew.werc.ewr:8100"},{18,"/?ad=qwe&dw=%20%32"}}},
		{"comment", "<!--Word-->", []ftsParserToken{{13,"<!--Word-->"}}},
		{"processing_instruction", "<?xml version='1.0'?>", []ftsParserToken{{13,"<?xml version='1.0'?>"}}},
		// PG18.6 SpecialTags preserves content bytes as blank tokens.
		{"script", "<script>Word</script>", []ftsParserToken{{13,"<script>"},{12,"Word"},{13,"</script>"}}},
		{"style", "<style>Word</style>", []ftsParserToken{{13,"<style>"},{12,"Word"},{13,"</style>"}}},
		{"empty", "", nil},
		{"long_ascii", strings.Repeat("A",4096), []ftsParserToken{{1,strings.Repeat("A",4096)}}},
		{"released_regression", ftsParserRegressionDocument, ftsParserRegressionTokens()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ftsParserScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) { ftsParserParse(t,p,b,tc.document,tc.want) })
		})
	}
}

func TestReferenceFTSParserUnicode(t *testing.T) {
	for _, tc := range []struct {
		name, document string
		want []ftsParserToken
	}{
		{"emoji", "a🙂b", []ftsParserToken{{2,"a🙂b"}}},
		{"punctuation", "a—b", []ftsParserToken{{2,"a—b"}}},
		{"nbsp", "a\u00a0b", []ftsParserToken{{2,"a\u00a0b"}}},
		{"combining", "a\u0301b", []ftsParserToken{{2,"a\u0301b"}}},
		{"leading_combining", "\u0301ab", []ftsParserToken{{2,"\u0301ab"}}},
		{"ideographic_space", "a\u3000b", []ftsParserToken{{2,"a\u3000b"}}},
		{"arabic_digits", "a١b", []ftsParserToken{{2,"a١b"}}},
		{"ascii_control", "a b", []ftsParserToken{{1,"a"},{12," "},{1,"b"}}},
		{"url_termination", "http://example.com/a🙂b", []ftsParserToken{{14,"http://"},{5,"example.com/a"},{6,"example.com"},{18,"/a"},{2,"🙂b"}}},
		{"long_unicode", strings.Repeat("é",4096), []ftsParserToken{{2,strings.Repeat("é",4096)}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ftsParserScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) { ftsParserParse(t,p,b,tc.document,tc.want) })
		})
	}
}
