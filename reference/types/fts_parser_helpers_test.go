package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func ftsParserScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b) })
	}
}

func ftsParserSelect(t *testing.T, p *fixture.Pair, b fixture.Backend, query string, columns, types []string, rows [][]any, args ...any) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := fixture.Select(query, columns, types, rows, args...)
		s.Prepared = prepared
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
	}
}

type ftsParserToken struct {
	id int64
	text string
}

func ftsParserTokenRows(tokens []ftsParserToken) [][]any {
	rows := make([][]any, len(tokens))
	for i, token := range tokens {
		rows[i] = []any{token.id, token.text, int64(i + 1)}
	}
	return rows
}

func ftsParserParse(t *testing.T, p *fixture.Pair, b fixture.Backend, document string, tokens []ftsParserToken) {
	t.Helper()
	query := "SELECT tokid,token,ordinality FROM ts_parse('default'," + ftsTypesQuote(document) + ") WITH ORDINALITY ORDER BY ordinality"
	ftsParserSelect(t, p, b, query, []string{"tokid", "token", "ordinality"}, []string{"INT4", "TEXT", "INT8"}, ftsParserTokenRows(tokens))
	ftsParserSelect(t, p, b, "SELECT tokid,token,ordinality FROM ts_parse($1::oid,$2::text) WITH ORDINALITY ORDER BY ordinality", []string{"tokid", "token", "ordinality"}, []string{"INT4", "TEXT", "INT8"}, ftsParserTokenRows(tokens), int64(3722), document)
}

// Literal PG18 manual/release descriptors, independent of production data.
func ftsParserDescriptors() [][]any {
	return [][]any{
		{int64(1), "asciiword", "Word, all ASCII"},
		{int64(2), "word", "Word, all letters"},
		{int64(3), "numword", "Word, letters and digits"},
		{int64(4), "email", "Email address"},
		{int64(5), "url", "URL"},
		{int64(6), "host", "Host"},
		{int64(7), "sfloat", "Scientific notation"},
		{int64(8), "version", "Version number"},
		{int64(9), "hword_numpart", "Hyphenated word part, letters and digits"},
		{int64(10), "hword_part", "Hyphenated word part, all letters"},
		{int64(11), "hword_asciipart", "Hyphenated word part, all ASCII"},
		{int64(12), "blank", "Space symbols"},
		{int64(13), "tag", "XML tag"},
		{int64(14), "protocol", "Protocol head"},
		{int64(15), "numhword", "Hyphenated word, letters and digits"},
		{int64(16), "asciihword", "Hyphenated word, all ASCII"},
		{int64(17), "hword", "Hyphenated word, all letters"},
		{int64(18), "url_path", "URL path"},
		{int64(19), "file", "File or path name"},
		{int64(20), "float", "Decimal notation"},
		{int64(21), "int", "Signed integer"},
		{int64(22), "uint", "Unsigned integer"},
		{int64(23), "entity", "XML entity"},
	}
}
