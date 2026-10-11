package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryDistinctOnDuplicateSubqueryLabels(t *testing.T) {
	queryDistinctOnScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE events(id INT,k INT)", 0))
		run(fixture.Exec("INSERT INTO events VALUES(1,1),(2,2)", 2))
		run(fixture.Exec("CREATE TABLE only_key(k INT)", 0))
		run(fixture.Exec("INSERT INTO only_key VALUES(1)", 1))
		for _, suffix := range []string{"", " ORDER BY x"} {
			// Same inner source/Var and alias namespace; qualification changes neither
			// target identity nor the first equivalent SQL92 output occurrence.
			head := "SELECT DISTINCT ON(x) (SELECT k FROM only_key) AS x,(SELECT only_key.k FROM only_key) AS x FROM events"
			q := head + suffix
			s := fixture.Select(q, []string{"x", "x"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}})
			fixture.QueryDistinctBoth(run, s)
			queryDistinctOnHeld(t, p, b, c, s)
			empty := fixture.Select(head+" WHERE FALSE"+suffix, []string{"x", "x"}, []string{"INT4", "INT4"}, nil)
			fixture.QueryDistinctBoth(run, empty)
			queryDistinctOnHeld(t, p, b, c, empty)
		}
		// Ordinary ORDER uses its own projection/cache pipeline and must retain
		// both outer occurrences rather than inheriting ON cardinality.
		ordinaryHead := "SELECT (SELECT k FROM only_key) AS x,(SELECT only_key.k FROM only_key) AS x FROM events"
		ordinary := fixture.Select(ordinaryHead+" ORDER BY x", []string{"x", "x"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(1), int64(1)}})
		fixture.QueryDistinctBoth(run, ordinary)
		queryDistinctOnHeld(t, p, b, c, ordinary)
		ordinaryEmpty := fixture.Select(ordinaryHead+" WHERE FALSE ORDER BY x", []string{"x", "x"}, []string{"INT4", "INT4"}, nil)
		fixture.QueryDistinctBoth(run, ordinaryEmpty)
		queryDistinctOnHeld(t, p, b, c, ordinaryEmpty)
		for _, pair := range []string{
			"(SELECT k FROM only_key) AS x,(SELECT k+1 FROM only_key) AS x",
			"(SELECT q.k FROM only_key AS q) AS x,(SELECT r.k FROM only_key AS r) AS x",
			"(SELECT q.k FROM only_key AS q) AS x,(SELECT e.k FROM only_key AS q) AS x",
		} {
			// Value coincidence is not analyzed identity: distinct expressions, RTE
			// aliases and outer correlations all keep complete ambiguity diagnostics.
			q, want := queryDistinctOnMarked("SELECT DISTINCT ON(|x) "+pair+" FROM events AS e", "42702", "DISTINCT ON \"x\" is ambiguous")
			fixture.QueryDistinctAnalysis(t, p, b, c, q, want)
			q, want = queryDistinctOnMarked("SELECT DISTINCT ON(x) "+pair+" FROM events AS e ORDER BY |x", "42702", "ORDER BY \"x\" is ambiguous")
			fixture.QueryDistinctAnalysis(t, p, b, c, q, want)
			q, want = queryDistinctOnMarked("SELECT "+pair+" FROM events AS e ORDER BY |x", "42702", "ORDER BY \"x\" is ambiguous")
			fixture.QueryDistinctAnalysis(t, p, b, c, q, want)
		}
	})
}
