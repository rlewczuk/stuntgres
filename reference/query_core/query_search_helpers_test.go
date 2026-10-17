package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func searchReference(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			body(t, p, backend, cycleConn(t, p, backend))
		})
	}
}

var searchModes = []string{"DEPTH", "BREADTH"}

func searchType(mode string) string {
	if mode == "BREADTH" {
		return "RECORD"
	}
	return "_RECORD"
}

func searchClause(mode, keys string) string {
	return " SEARCH " + mode + " FIRST BY " + keys + " SET seq "
}

func searchTree(mode string) string {
	return "WITH RECURSIVE r(id) AS (SELECT id FROM search_nodes WHERE parent IS NULL UNION ALL SELECT n.id FROM search_nodes n JOIN r ON n.parent=r.id)" + searchClause(mode, "id")
}

func searchSeed(run func(fixture.Step)) {
	run(fixture.Exec("CREATE TABLE search_nodes(id INT,parent INT)", 0))
	run(fixture.Exec("INSERT INTO search_nodes VALUES(11,10),(4,2),(10,1),(5,3),(1,NULL),(3,2),(2,1)", 7))
}

func searchTreeRows(mode string) [][]any {
	if mode == "BREADTH" {
		return [][]any{{int64(1), "(0,1)"}, {int64(2), "(1,2)"}, {int64(10), "(1,10)"}, {int64(3), "(2,3)"}, {int64(4), "(2,4)"}, {int64(11), "(2,11)"}, {int64(5), "(3,5)"}}
	}
	return [][]any{{int64(1), "{(1)}"}, {int64(2), "{(1),(2)}"}, {int64(3), "{(1),(2),(3)}"}, {int64(5), "{(1),(2),(3),(5)}"}, {int64(4), "{(1),(2),(4)}"}, {int64(10), "{(1),(10)}"}, {int64(11), "{(1),(10),(11)}"}}
}

func searchTreeStep(mode, q string) fixture.Step {
	return fixture.Select(q, []string{"id", "seq"}, []string{"INT4", searchType(mode)}, searchTreeRows(mode))
}

func searchNulls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := subqueryRun(t, p, b, c)
	run(fixture.Exec("CREATE TABLE search_nullable(id INT,parent INT,k INT)", 0))
	run(fixture.Exec("INSERT INTO search_nullable VALUES(100,NULL,0),(201,100,1),(202,100,NULL),(203,201,NULL),(204,202,-1)", 5))
	for _, mode := range searchModes {
		q := "WITH RECURSIVE r(id,k) AS (SELECT id,k FROM search_nullable WHERE parent IS NULL UNION ALL SELECT n.id,n.k FROM r JOIN search_nullable n ON n.parent=r.id)" + searchClause(mode, "k") + "SELECT id,seq FROM r ORDER BY seq NULLS FIRST"
		want := [][]any{{int64(100), "{(0)}"}, {int64(201), "{(0),(1)}"}, {int64(203), "{(0),(1),()}"}, {int64(202), "{(0),()}"}, {int64(204), "{(0),(),(-1)}"}}
		if mode == "BREADTH" {
			want = [][]any{{int64(100), "(0,0)"}, {int64(201), "(1,1)"}, {int64(202), "(1,)"}, {int64(204), "(2,-1)"}, {int64(203), "(2,)"}}
		}
		recursiveCTERoutes(t, p, b, c, fixture.Select(q, []string{"id", "seq"}, []string{"INT4", searchType(mode)}, want))
	}
}
