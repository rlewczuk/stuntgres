package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceFTSStatQueries(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"stat_query_reader"})
	ftsParserScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c := ftsInspectionConnection(t, p, b)
		ftsStatSetup(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		query := "SELECT * FROM ts_stat($1::text) ORDER BY word"
		stmt, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
		check := func(inner string, want [][]any) {
			ftsInspectionBoth(t, p, b, c, fixture.Select(query, ftsStatColumns, ftsStatTypes, want, inner))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(query, ftsStatColumns, ftsStatTypes, want, inner))
		}
		inner := "SELECT vector FROM fts_stat_documents"
		check(inner, ftsStatBase())
		check(inner+" WHERE id=1", [][]any{{"cat", int64(1), int64(2)}, {"dog", int64(1), int64(1)}, {"solo", int64(1), int64(1)}})
		run(fixture.Exec("CREATE TABLE fts_stat_multiplicity(n integer)", 0))
		run(fixture.Exec("INSERT INTO fts_stat_multiplicity VALUES(1),(2)", 2))
		check("SELECT vector FROM fts_stat_documents CROSS JOIN fts_stat_multiplicity WHERE id<3", [][]any{{"cat", int64(4), int64(6)}, {"dog", int64(4), int64(6)}, {"solo", int64(2), int64(2)}})
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("UPDATE fts_stat_documents SET vector='cat:1A' WHERE id=2", 1))
		changed := [][]any{{"cat", int64(2), int64(3)}, {"dog", int64(1), int64(1)}, {"solo", int64(1), int64(1)}}
		check(inner, changed)
		run(fixture.Exec("DELETE FROM fts_stat_documents WHERE id=1", 1))
		check(inner, [][]any{{"cat", int64(1), int64(1)}})
		run(fixture.Exec("INSERT INTO fts_stat_documents VALUES(5,'owl:1,2')", 1))
		check(inner, [][]any{{"cat", int64(1), int64(1)}, {"owl", int64(1), int64(2)}})
		run(fixture.Exec("ROLLBACK", 0))
		check(inner, ftsStatBase())
		run(fixture.Exec("UPDATE fts_stat_documents SET vector='cat:1A' WHERE id=2", 1))
		check(inner, changed)
		run(fixture.Exec("CREATE DOMAIN fts_stat_vector_domain AS tsvector", 0))
		check("SELECT 'cat:1'::fts_stat_vector_domain", [][]any{{"cat", int64(1), int64(1)}})
		check("SELECT vector FROM fts_stat_documents WHERE FALSE", nil)
		run(fixture.Exec("CREATE TABLE fts_stat_many(ord integer,v tsvector)", 0))
		run(fixture.Exec("INSERT INTO fts_stat_many SELECT n,'x:1,2'::tsvector FROM generate_series(1,101) n", 101))
		check("SELECT v FROM fts_stat_many", [][]any{{"x", int64(101), int64(202)}})
		run(fixture.Exec(`CREATE TABLE "Stat Quoted"("Vector Column" tsvector)`, 0))
		run(fixture.Exec(`INSERT INTO "Stat Quoted" VALUES('quoted:1')`, 1))
		check(`SELECT "Vector Column" FROM "Stat Quoted"`, [][]any{{"quoted", int64(1), int64(1)}})
		run(fixture.Exec("CREATE SEQUENCE fts_stat_sequence", 0))
		// Each independent direct/fresh/held execution consumes two nextvals.
		check("SELECT CASE WHEN nextval('fts_stat_sequence')>0 THEN 'tick:1'::tsvector ELSE ''::tsvector END FROM fts_stat_multiplicity", [][]any{{"tick", int64(2), int64(2)}})
		run(fixture.Select("SELECT last_value FROM fts_stat_sequence", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(6)}}))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("CREATE SCHEMA fts_stat_private", 0))
		run(fixture.Exec("CREATE TABLE fts_stat_private.fts_stat_documents(vector tsvector)", 0))
		run(fixture.Exec("INSERT INTO fts_stat_private.fts_stat_documents VALUES('shadow:1,2')", 1))
		run(fixture.Exec("SET LOCAL search_path=fts_stat_private,pg_catalog,public", 0))
		check(inner, [][]any{{"shadow", int64(1), int64(2)}})
		run(fixture.Exec("ROLLBACK", 0))
		check(inner, changed)
		run(fixture.Exec("CREATE ROLE stat_query_reader", 0))
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO stat_query_reader", 0))
		aclInner := "SELECT vector FROM public.fts_stat_documents"
		run(fixture.Exec("SET ROLE stat_query_reader", 0))
		defer c.ExecContext(p.Ctx, "RESET ROLE")
		denied := fixture.ControlsReferenceDiagnostic("42501", "permission denied for table fts_stat_documents")
		denied.Context = "SQL statement \"" + aclInner + "\""
		ftsInspectionDiagnostic(t, p, b, c, query, denied, false, aclInner)
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("GRANT SELECT ON public.fts_stat_documents TO stat_query_reader", 0))
		run(fixture.Exec("SET ROLE stat_query_reader", 0))
		check(aclInner, changed)
		run(fixture.Exec("RESET ROLE", 0))
	})
}
