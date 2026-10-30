package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceXMLTableSmoke(t *testing.T) {
	resultsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.SQLJSONBoth(t, p, b, c, fixture.Select(xmlTableNamespaceQuery, []string{"n", "id", "value"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(7), int64(11)}, {int64(2), int64(8), int64(12)}}))
		q := `SELECT * FROM XMLTABLE(($1::text) PASSING ($2::xml) COLUMNS n FOR ORDINALITY, missing text, supplied int DEFAULT 42 NOT NULL)`
		fixture.SQLJSONBoth(t, p, b, c, fixture.Select(q, []string{"n", "missing", "supplied"}, []string{"INT4", "TEXT", "INT4"}, [][]any{{int64(1), nil, int64(42)}}, "/r", `<r/>`))
		fixture.SQLJSONBoth(t, p, b, c, fixture.Select(q, []string{"n", "missing", "supplied"}, []string{"INT4", "TEXT", "INT4"}, nil, "/missing", `<r/>`))
		fixture.SQLJSONDiagnostic(t, p, b, c, q, xmlTableDiagnostic("2200M", "could not parse XML document", "line 1: Extra content at the end of the document\n<a/><b/>\n    ^"), false, "/r", `<a/><b/>`)
		q = `SELECT * FROM XMLTABLE('/r' PASSING ($1::xml) COLUMNS v int)`
		fixture.SQLJSONDiagnostic(t, p, b, c, q, xmlTableDiagnostic("21000", "more than one value returned by column XPath expression", ""), false, `<r><v>7</v><v>8</v></r>`)
	})
}
