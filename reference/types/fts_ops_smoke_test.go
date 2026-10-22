package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFTSOpsFeatureSmoke(t *testing.T) {
	ftsOpsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE fts_ops_smoke(id integer,q tsquery,v tsvector)", 0))
		query := "INSERT INTO fts_ops_smoke VALUES($1::integer,tsquery_phrase($2::tsquery,$3::tsquery,$4::int4),$5::tsvector || $6::tsvector)"
		stmt, err := b.DB.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: query}
		insert := func(id int, left any, distance int) {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(query, 1, id, left, "c:*A", distance, "a:1 z:10", "a:2B"))
		}
		insert(1, "a | b", 2)
		insert(2, nil, 2)
		invalid := fixture.ErrorDiagnostic(query, ftsOpsDistanceError(), 3, "a | b", "c:*A", 16385, "a:1 z:10", "a:2B")
		invalid.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, invalid)
		insert(4, "", 2)
		want := [][]any{{int64(1), "( 'a' | 'b' ) <2> 'c':*A", "'a':1,12B 'z':10"}, {int64(2), nil, "'a':1,12B 'z':10"}, {int64(4), "'c':*A", "'a':1,12B 'z':10"}}
		saved := fixture.Select("SELECT id,q,v FROM fts_ops_smoke ORDER BY id", []string{"id", "q", "v"}, []string{"INT4", "TSQUERY", "TSVECTOR"}, want)
		run(saved)
		run(fixture.Prepared(saved))
		selected := fixture.Select("SELECT !!q AS query_part,v AS vector_part,q @> 'a'::tsquery AS has_a,'a'::tsquery <@ q AS reverse_a FROM fts_ops_smoke WHERE id=1", []string{"query_part", "vector_part", "has_a", "reverse_a"}, []string{"TSQUERY", "TSVECTOR", "BOOL", "BOOL"}, [][]any{{"!( ( 'a' | 'b' ) <2> 'c':*A )", "'a':1,12B 'z':10", true, true}})
		run(selected)
		run(fixture.Prepared(selected))
		selectSQL := "SELECT q && $1::tsquery AS conjunction,q || $1::tsquery AS disjunction,q <-> $1::tsquery AS phrase FROM fts_ops_smoke WHERE id=4"
		selectStmt, err := b.DB.PrepareContext(p.Ctx, selectSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer selectStmt.Close()
		for _, word := range []string{"x", "y"} {
			s := fixture.Select(selectSQL, []string{"conjunction", "disjunction", "phrase"}, []string{"TSQUERY", "TSQUERY", "TSQUERY"}, [][]any{{"'c':*A & '" + word + "'", "'c':*A | '" + word + "'", "'c':*A <-> '" + word + "'"}}, word)
			run(s)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b.DB, Statement: selectStmt, SQL: selectSQL}, b.Recorder, s)
		}
		if !b.PG {
			ftsOpsNativeControls(t, p, b, "fts_ops_smoke", saved)
		}
	})
}
