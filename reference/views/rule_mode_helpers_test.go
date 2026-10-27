package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

type ruleModeCase struct {
	code, action string
}

var ruleModeCases = []ruleModeCase{
	{"O", "ENABLE RULE"},
	{"D", "DISABLE RULE"},
	{"R", "ENABLE REPLICA RULE"},
	{"A", "ENABLE ALWAYS RULE"},
}

func ruleModeActive(code, role string) bool {
	return code == "A" || code == "O" && role != "replica" || code == "R" && role == "replica"
}

func ruleModeRoutes(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, bool)) {
	t.Helper()
	ruleRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		body(t, p, b, held)
	})
}

func ruleModeTransactions(t *testing.T, explicit bool, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *sql.Conn, string, bool)) {
	t.Helper()
	transactionalRulesRoutes(t, explicit, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c, peer *sql.Conn, mode string, held bool) {
		body(t, p, b, c, peer, mode, held)
	})
}

func ruleModeSetup(run func(fixture.Step)) {
	for _, q := range []string{
		"CREATE TABLE src(id INT PRIMARY KEY,v INT)",
		"CREATE TABLE sink1(id INT,v INT)",
		"CREATE TABLE sink2(id INT,v INT)",
		"CREATE RULE r AS ON INSERT TO src DO ALSO INSERT INTO sink1 VALUES(NEW.id,NEW.v)",
	} {
		run(fixture.Exec(q, 0))
	}
}

func ruleModeCatalog(code string) fixture.Step {
	return fixture.Select("SELECT ev_enabled::TEXT,ev_type::TEXT,is_instead FROM pg_rewrite WHERE ev_class='src'::regclass AND rulename='r'", []string{"ev_enabled", "ev_type", "is_instead"}, []string{"TEXT", "TEXT", "BOOL"}, [][]any{{code, "3", false}})
}

func ruleModeMissing(query, name, relation string) fixture.Step {
	return ruleError(query, "42704", "rule \""+name+"\" for relation \""+relation+"\" does not exist")
}

func ruleModeConn(t *testing.T, p *fixture.Pair, b fixture.Backend) *sql.Conn {
	t.Helper()
	c, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func ruleModeCount(table string, n int64) fixture.Step {
	return fixture.Select("SELECT count(*) FROM "+table, []string{"count"}, []string{"INT8"}, [][]any{{n}})
}

func ruleModeValueRows(table string, rows ...[]any) fixture.Step {
	return fixture.Select("SELECT id,v FROM "+table+" ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, rows)
}
