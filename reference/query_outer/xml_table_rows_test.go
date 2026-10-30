package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

const xmlTableNamespaceQuery = `SELECT x.* FROM XMLTABLE(
 XMLNAMESPACES('urn:r' AS p, 'urn:a' AS a),
 '/p:r/p:v' PASSING
 '<r xmlns="urn:r" xmlns:z="urn:a"><v z:id="7">11</v><v z:id="8">12</v></r>'
 COLUMNS n FOR ORDINALITY, id int PATH '@a:id', value int PATH '.'
) AS x ORDER BY n`

func TestReferenceXMLTableNamespacesAndTypes(t *testing.T) {
	for _, tc := range []struct {
		name, query string
		columns, types []string
		rows [][]any
		args []any
	}{
		{"URI aliases", xmlTableNamespaceQuery, []string{"n", "id", "value"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(7), int64(11)}, {int64(2), int64(8), int64(12)}}, nil},
		{"quoted alias and wildcard", `SELECT * FROM XMLTABLE(XMLNAMESPACES($1::text AS "P"), '/P:r/P:*' PASSING ($2::xml) COLUMNS n FOR ORDINALITY, v int PATH '.') ORDER BY n`, []string{"n", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(7)}, {int64(2), int64(8)}}, []any{"urn:r", `<r xmlns="urn:r"><a>7</a><b>8</b></r>`}},
		{"attribute wildcard", `SELECT * FROM XMLTABLE(XMLNAMESPACES('urn:a' AS p), '/r' PASSING '<r xmlns:a="urn:a" a:k="7"/>' COLUMNS v int PATH '@p:*')`, []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}, nil},
		{"default path and alias list", `SELECT renamed.* FROM XMLTABLE('/r' PASSING '<r><v>7</v></r>' COLUMNS v integer) AS renamed(answer)`, []string{"answer"}, []string{"INT4"}, [][]any{{int64(7)}}, nil},
		{"quoted default path", `SELECT * FROM XMLTABLE('/r' PASSING '<r><V>8</V></r>' COLUMNS "V" pg_catalog.int4)`, []string{"V"}, []string{"INT4"}, [][]any{{int64(8)}}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resultsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				fixture.SQLJSONBoth(t, p, b, c, fixture.Select(tc.query, tc.columns, tc.types, tc.rows, tc.args...))
			})
		})
	}
	for _, spelling := range []string{"", "BY REF ", "BY VALUE "} {
		for _, suffix := range []string{"", " BY REF", " BY VALUE"} {
			t.Run("passing "+spelling+suffix, func(t *testing.T) {
				resultsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					q := `SELECT * FROM XMLTABLE('/r' PASSING ` + spelling + `'<r><v>9</v></r>'` + suffix + ` COLUMNS v int)`
					fixture.SQLJSONBoth(t, p, b, c, fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(9)}}))
				})
			})
		}
	}
}

func TestReferenceXMLTableMissingDefaults(t *testing.T) {
	resultsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := `SELECT * FROM XMLTABLE('/r' PASSING '<r xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"><v/><w>9</w><nil xsi:nil="true"/></r>' COLUMNS v text DEFAULT 'fallback', missing text NULL, supplied int DEFAULT 42 NOT NULL, w int, nil text DEFAULT 'fallback')`
		fixture.SQLJSONBoth(t, p, b, c, fixture.Select(q, []string{"v", "missing", "supplied", "w", "nil"}, []string{"TEXT", "TEXT", "INT4", "INT4", "TEXT"}, [][]any{{"", nil, int64(42), int64(9), ""}}))
		// A present node must not demand the failing DEFAULT, including when
		// that node's string-value is empty rather than SQL NULL.
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE SEQUENCE xmltable_present_seq`, 0))
		q = `SELECT * FROM XMLTABLE('/r' PASSING ($1::xml) COLUMNS v text DEFAULT (1 / (nextval('xmltable_present_seq') - 1))::text)`
		fixture.SQLJSONBoth(t, p, b, c, fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{""}}, `<r><v/></r>`))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(`SELECT last_value, is_called FROM xmltable_present_seq`, []string{"last_value","is_called"}, []string{"INT8","BOOL"}, [][]any{{int64(1),false}}))
		for _, options := range []string{
			`PATH 'absent' DEFAULT 42 NOT NULL`,
			`DEFAULT 42 NOT NULL PATH 'absent'`,
			`NOT NULL PATH 'absent' DEFAULT 42`,
		} {
			fixture.SQLJSONBoth(t, p, b, c, fixture.Select(`SELECT * FROM XMLTABLE('/r' PASSING '<r/>' COLUMNS v int `+options+`)`, []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE SEQUENCE xmltable_defaults_seq START WITH 41`, 0))
		q = `SELECT * FROM XMLTABLE('/r/v' PASSING ($1::xml) COLUMNS n FOR ORDINALITY, v int PATH '@value' DEFAULT nextval('xmltable_defaults_seq')) ORDER BY n`
		doc := `<r><v value="7"/><v/><v/></r>`
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, executor := range []fixture.Executor{c, held} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`ALTER SEQUENCE xmltable_defaults_seq RESTART WITH 41`, 0))
			for _, first := range []int64{41, 43} {
				fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Select(q, []string{"n", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(7)}, {int64(2), first}, {int64(3), first + 1}}, doc))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(`SELECT last_value, is_called FROM xmltable_defaults_seq`, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{first + 1, true}}))
			}
		}
	})
}

func TestReferenceXMLTableNodeResults(t *testing.T) {
	for _, tc := range []struct {
		name, doc, row, columns string
		names, types []string
		values [][]any
	}{
		{"descendant string value", `<r><v>A<!--x--><b>B</b>C</v></r>`, "/r", `v text PATH 'v', content xml PATH 'v/text()'`, []string{"v", "content"}, []string{"TEXT", "XML"}, [][]any{{"ABC", "AC"}}},
		{"multiple elements", `<r><v>A</v><v>B</v></r>`, "/r", `v xml PATH 'v'`, []string{"v"}, []string{"XML"}, [][]any{{`<v>A</v><v>B</v>`}}},
		{"inherited namespaces", `<r xmlns:p="urn:p"><p:v p:a="7"><p:w/></p:v></r>`, "/r", `v xml PATH '*'`, []string{"v"}, []string{"XML"}, [][]any{{`<p:v xmlns:p="urn:p" p:a="7"><p:w/></p:v>`}}},
		{"unused inherited namespace", `<r xmlns:p="urn:p" xmlns:u="urn:unused"><p:v/></r>`, "/r", `v xml PATH '*'`, []string{"v"}, []string{"XML"}, [][]any{{`<p:v xmlns:p="urn:p"/>`}}},
		{"unused local namespace", `<r><v xmlns:u="urn:unused"/></r>`, "/r", `v xml PATH '*'`, []string{"v"}, []string{"XML"}, [][]any{{`<v xmlns:u="urn:unused"/>`}}},
		{"descendant inherited namespace", `<r xmlns:p="urn:p"><v><p:w/></v></r>`, "/r", `v xml PATH '*'`, []string{"v"}, []string{"XML"}, [][]any{{`<v xmlns:p="urn:p"><p:w/></v>`}}},
		{"node kinds", `<r a="A&amp;B">T&amp;U<!--c--><?p data?><v/></r>`, "/r", `a text PATH '@a', ax xml PATH '@a', tx xml PATH 'text()', c xml PATH 'comment()', p xml PATH 'processing-instruction()'`, []string{"a", "ax", "tx", "c", "p"}, []string{"TEXT", "XML", "XML", "XML", "XML"}, [][]any{{"A&B", `A&amp;B`, `T&amp;U`, `<!--c-->`, `<?p data?>`}}},
		// Literal expectations follow REL_18_6 node-kind serialization, not
		// native output or XML-equivalence normalization.
		{"single CDATA", `<r><![CDATA[A&B]]></r>`, "/r", `v xml PATH 'text()', t text PATH 'text()', s xml PATH 'string(text())'`, []string{"v","t","s"}, []string{"XML","TEXT","XML"}, [][]any{{`<![CDATA[A&B]]>`, `A&B`, `A&amp;B`}}},
		{"adjacent CDATA", `<r><![CDATA[A&]]><![CDATA[B<C]]></r>`, "/r", `v xml PATH 'text()', t text PATH 'text()', s xml PATH 'string(text())'`, []string{"v","t","s"}, []string{"XML","TEXT","XML"}, [][]any{{`<![CDATA[A&B<C]]>`, `A&B<C`, `A&amp;B&lt;C`}}},
		{"adjacent CDATA delimiter", `<r><![CDATA[A]]]]><![CDATA[>B]]></r>`, "/r", `v xml PATH 'text()', t text PATH 'text()'`, []string{"v","t"}, []string{"XML","TEXT"}, [][]any{{`<![CDATA[A]]]]><![CDATA[>B]]>`, `A]]>B`}}},
		{"mixed text and CDATA", `<r>T&amp;<![CDATA[A<B]]>U&amp;</r>`, "/r", `v xml PATH 'text()', t text PATH 'string(.)', first text PATH 'text()[1]', middle text PATH 'text()[2]', n int PATH 'count(text())'`, []string{"v","t","first","middle","n"}, []string{"XML","TEXT","TEXT","TEXT","INT4"}, [][]any{{`T&amp;<![CDATA[A<B]]>U&amp;`, `T&A<BU&`, `T&`, `A<B`, int64(3)}}},
		{"document node", `<r><v/></r>`, "/", `v xml PATH '.'`, []string{"v"}, []string{"XML"}, [][]any{{"<r><v/></r>\n"}}},
		{"namespace node", `<r xmlns:p="urn:p"/>`, "/r", `v text PATH 'namespace::p', x xml PATH 'namespace::p'`, []string{"v", "x"}, []string{"TEXT", "XML"}, [][]any{{"urn:p", "urn:p"}}},
		{"absolute and relative", `<r><v>7</v><v>8</v></r>`, "/r/v", `n FOR ORDINALITY, current int PATH '.', first int PATH '/r/v[1]'`, []string{"n", "current", "first"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(7), int64(7)}, {int64(2), int64(8), int64(7)}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resultsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				q := `SELECT * FROM XMLTABLE(($1::text) PASSING ($2::xml) COLUMNS ` + tc.columns + `)`
				s := fixture.Select(q, tc.names, tc.types, tc.values, tc.row, tc.doc)
				s.Bag = true
				fixture.SQLJSONBoth(t, p, b, c, s)
			})
		})
	}
}
