package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFTSQueryFunctionsFeatureSmoke(t *testing.T) {
	ftsQueryFunctionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE fts_qf_smoke_rules(ord integer,t tsquery,s tsquery)", 0))
		run(fixture.Exec("INSERT INTO fts_qf_smoke_rules VALUES(1,'a','b'),(2,'b','c')", 2))
		run(fixture.Exec("CREATE TABLE fts_qf_smoke_values(id integer,q tsquery)", 0))
		run(fixture.Exec("INSERT INTO fts_qf_smoke_values VALUES(1,ts_rewrite('a&b'::tsquery,'a'::tsquery,'foo|bar'::tsquery)),(2,NULL)", 2))
		query := "SELECT id,q,numnode(q) AS nodes,querytree(q) AS tree FROM fts_qf_smoke_values ORDER BY id"
		for _, prepared := range []bool{false, true} {
			s := fixture.Select(query, []string{"id", "q", "nodes", "tree"}, []string{"INT4", "TSQUERY", "INT4", "TEXT"}, [][]any{{int64(1), "'b' & ( 'foo' | 'bar' )", int64(5), "'b' & ( 'foo' | 'bar' )"}, {int64(2), nil, nil, nil}})
			s.Prepared = prepared
			run(s)
		}
		query = "SELECT ts_rewrite('a'::tsquery,'SELECT t,s FROM fts_qf_smoke_rules ORDER BY ord') AS rewritten"
		stmt, err := conn.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: query}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(query, []string{"rewritten"}, []string{"TSQUERY"}, [][]any{{"'c'"}}))
		run(fixture.Exec("UPDATE fts_qf_smoke_rules SET s='d'::tsquery WHERE ord=2", 1))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(query, []string{"rewritten"}, []string{"TSQUERY"}, [][]any{{"'d'"}}))
		run(fixture.Select("SELECT querytree('a|!b'::tsquery) AS tree,numnode('!!a'::tsquery) AS nodes,ts_rewrite('a'::tsquery,NULL::text) AS strict", []string{"tree", "nodes", "strict"}, []string{"TEXT", "INT4", "TSQUERY"}, [][]any{{"T", int64(3), nil}}))
		badShape := fixture.ErrorDiagnostic("SELECT ts_rewrite('a'::tsquery,'SELECT ''a''::tsquery')", ftsQueryFunctionsShapeError())
		badShape.SelectRows = true
		badShape.ErrorStage = "execute"
		run(badShape)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(query, []string{"rewritten"}, []string{"TSQUERY"}, [][]any{{"'d'"}}))
		run(fixture.Select("SELECT ord,t,s FROM fts_qf_smoke_rules ORDER BY ord", []string{"ord", "t", "s"}, []string{"INT4", "TSQUERY", "TSQUERY"}, [][]any{{int64(1), "'a'", "'b'"}, {int64(2), "'b'", "'d'"}}))
	})
}
