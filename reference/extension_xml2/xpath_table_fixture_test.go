package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

const xtParameters = "SELECT * FROM xpath_table($1::text,$2::text,$3::text,$4::text,$5::text) AS x(id integer,a text) ORDER BY id,a"

func xtQuote(s string) string { return fixture.CrosstabQuote(s) }

// Argument order follows PostgreSQL: key, document, relation, paths, criteria.
func xtQuery(key, document, relation, paths, criteria, definitions string) string {
	return "SELECT * FROM xpath_table(" + xtQuote(key) + "," + xtQuote(document) + "," + xtQuote(relation) + "," + xtQuote(paths) + "," + xtQuote(criteria) + ") AS x(" + definitions + ")"
}

func xtExec(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, queries ...string) {
	t.Helper()
	for _, q := range queries {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
	}
}

func xtSeed(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	xtExec(t, p, b, c, "CREATE TABLE xt_src(id integer,doc text)")
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO xt_src VALUES
(1,'<doc tag="one"><a>10</a><a>20</a><b>7</b></doc>'),
(2,'<doc><a/><a>30</a></doc>'),(3,'<doc/>'),(4,NULL),(5,'<doc>')`, 5))
}

// Each route independently asserts its metadata, rows, or full diagnostic.
func xtBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, name string, s fixture.Step) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		for _, route := range []string{"direct", "prepared"} {
			t.Run(route, func(t *testing.T) {
				s.Prepared = route == "prepared"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			})
		}
	})
}

func xtFailure(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, name, q string, want fixture.Diagnostic, prepareFailure bool, args ...any) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		xml2Failure(t, p, b, x, q, want, prepareFailure, args...)
	})
}

func xtInternal(want fixture.Diagnostic, query, position string) fixture.Diagnostic {
	want.InternalQuery, want.InternalPosition = query, position
	return want
}

func xtStatement(want fixture.Diagnostic, query string) fixture.Diagnostic {
	want.Context = "SQL statement \"" + query + "\""
	return want
}
