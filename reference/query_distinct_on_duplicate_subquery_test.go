package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceQueryDistinctOnDuplicateSubqueryLabels(t *testing.T) {
	queryDistinctOnScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		run(referenceExec("CREATE TABLE events(id INT,k INT)", 0))
		run(referenceExec("INSERT INTO events VALUES(1,1),(2,2)", 2))
		run(referenceExec("CREATE TABLE only_key(k INT)", 0))
		run(referenceExec("INSERT INTO only_key VALUES(1)", 1))
		for _, suffix := range []string{"", " ORDER BY x"} {
			// Same inner source/Var and alias namespace; qualification changes neither
			// target identity nor the first equivalent SQL92 output occurrence.
			head := "SELECT DISTINCT ON(x) (SELECT k FROM only_key) AS x,(SELECT only_key.k FROM only_key) AS x FROM events"
			q := head + suffix
			s := referenceSelect(q, []string{"x", "x"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}})
			queryDistinctBoth(run, s)
			queryDistinctOnHeld(t, p, b, c, s)
			empty := referenceSelect(head+" WHERE FALSE"+suffix, []string{"x", "x"}, []string{"INT4", "INT4"}, nil)
			queryDistinctBoth(run, empty)
			queryDistinctOnHeld(t, p, b, c, empty)
		}
		// Ordinary ORDER uses its own projection/cache pipeline and must retain
		// both outer occurrences rather than inheriting ON cardinality.
		ordinaryHead := "SELECT (SELECT k FROM only_key) AS x,(SELECT only_key.k FROM only_key) AS x FROM events"
		ordinary := referenceSelect(ordinaryHead+" ORDER BY x", []string{"x", "x"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(1), int64(1)}})
		queryDistinctBoth(run, ordinary)
		queryDistinctOnHeld(t, p, b, c, ordinary)
		ordinaryEmpty := referenceSelect(ordinaryHead+" WHERE FALSE ORDER BY x", []string{"x", "x"}, []string{"INT4", "INT4"}, nil)
		queryDistinctBoth(run, ordinaryEmpty)
		queryDistinctOnHeld(t, p, b, c, ordinaryEmpty)
		for _, pair := range []string{
			"(SELECT k FROM only_key) AS x,(SELECT k+1 FROM only_key) AS x",
			"(SELECT q.k FROM only_key AS q) AS x,(SELECT r.k FROM only_key AS r) AS x",
			"(SELECT q.k FROM only_key AS q) AS x,(SELECT e.k FROM only_key AS q) AS x",
		} {
			// Value coincidence is not analyzed identity: distinct expressions, RTE
			// aliases and outer correlations all keep complete ambiguity diagnostics.
			q, want := queryDistinctOnMarked("SELECT DISTINCT ON(|x) "+pair+" FROM events AS e", "42702", "DISTINCT ON \"x\" is ambiguous")
			queryDistinctAnalysis(t, p, b, c, q, want)
			q, want = queryDistinctOnMarked("SELECT DISTINCT ON(x) "+pair+" FROM events AS e ORDER BY |x", "42702", "ORDER BY \"x\" is ambiguous")
			queryDistinctAnalysis(t, p, b, c, q, want)
			q, want = queryDistinctOnMarked("SELECT "+pair+" FROM events AS e ORDER BY |x", "42702", "ORDER BY \"x\" is ambiguous")
			queryDistinctAnalysis(t, p, b, c, q, want)
		}
	})
}
