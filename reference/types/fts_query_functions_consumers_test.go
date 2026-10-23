package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFTSQueryFunctionsConsumers(t *testing.T) {
	ftsQueryFunctionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE fts_qf_saved(id integer,v tsvector DEFAULT 'saved'::tsvector,q tsquery DEFAULT ts_rewrite('a'::tsquery,'a'::tsquery,'b'::tsquery))", 0))
		run(fixture.Exec("INSERT INTO fts_qf_saved(id) VALUES(1)", 1))
		run(fixture.Exec("INSERT INTO fts_qf_saved VALUES(2,'saved',ts_rewrite('a&b'::tsquery,'a'::tsquery,'foo|bar'::tsquery)),(3,NULL,NULL)", 2))
		run(fixture.Exec("CREATE VIEW fts_qf_view AS SELECT id,numnode(q) AS nodes,querytree(q) AS tree,ts_rewrite(q,'b'::tsquery,'z'::tsquery) AS rewritten FROM fts_qf_saved", 0))
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("SELECT id,nodes,tree FROM fts_qf_view ORDER BY id", []string{"id", "nodes", "tree"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(1), "'b'"}, {int64(2), int64(5), "'b' & ( 'foo' | 'bar' )"}, {int64(3), nil, nil}})
			s.Prepared = prepared
			run(s)
		}
		t.Run("stored_rendering", func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			s := fixture.Select("SELECT id,rewritten FROM fts_qf_view ORDER BY id", []string{"id", "rewritten"}, []string{"INT4", "TSQUERY"}, [][]any{{int64(1), "'z'"}, {int64(2), "'z' & ( 'bar' | 'foo' )"}, {int64(3), nil}})
			run(s)
			run(fixture.Prepared(s))
		})
		want := [][]any{{int64(1), "'saved'", "'b'"}, {int64(2), "'saved'", "'b' & ( 'foo' | 'bar' )"}, {int64(3), nil, nil}}
		run(fixture.Select("SELECT id,v,q FROM fts_qf_saved ORDER BY id", []string{"id", "v", "q"}, []string{"INT4", "TSVECTOR", "TSQUERY"}, want))
		// Explicit native-only logical controls in the same public scenario;
		// existing helper also checks retained rows after replacement and actual
		// PostgreSQL replay of the native dump when the oracle is present.
		if !b.PG {
			ftsTypesNativeControls(t, p, b, "fts_qf_saved", want)
		}
	})
}
