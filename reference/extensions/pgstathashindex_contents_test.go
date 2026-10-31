package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgstathashindexContents(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "ph_other")
	pgstathashindexCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		pgstathashindexSeed(t, p, b, c)
		for _, tc := range []struct{ name string; live int64 }{{"ph_index", 4}, {"ph_sibling", 4}, {"ph_expression", 5}} {
			pgstathashindexStats(t, p, b, c, "SELECT * FROM ph_part.pgstathashindex($1::regclass)", tc.live, 0, "ph_part."+tc.name)
		}
		for _, q := range []string{"CREATE TABLE ph_part.ph_nulls(k integer)", "CREATE SCHEMA ph_other", "CREATE TABLE ph_other.ph_heap(k integer)"} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO ph_part.ph_nulls VALUES(NULL),(NULL)", 2))
		run(fixture.Exec("CREATE INDEX ph_null_index ON ph_part.ph_nulls USING hash(k)", 0))
		pgstathashindexStats(t, p, b, c, "SELECT * FROM ph_part.pgstathashindex('ph_part.ph_null_index')", 0, 0)
		run(fixture.Exec("INSERT INTO ph_other.ph_heap VALUES(9)", 1))
		run(fixture.Exec("CREATE INDEX ph_index ON ph_other.ph_heap USING hash(k)", 0))
		q := "SELECT s.live_items,s.dead_items FROM (VALUES('ph_part.ph_index'),('ph_other.ph_index'),('ph_part.ph_sibling'),('ph_part.ph_expression')) AS i(n) CROSS JOIN LATERAL ph_part.pgstathashindex(i.n::regclass) s"
		s := fixture.Select(q, []string{"live_items", "dead_items"}, []string{"INT8", "INT8"}, [][]any{{int64(4), int64(0)}, {int64(1), int64(0)}, {int64(4), int64(0)}, {int64(5), int64(0)}})
		s.Bag = true
		pgstathashindexBoth(t, p, b, c, s)
		// Fresh rebuilds deliberately avoid equating LP_DEAD hints after DML
		// with the native effective-root sample.
		run(fixture.Exec("UPDATE ph_part.ph_heap SET k=NULL WHERE k=1", 2))
		run(fixture.Exec("DELETE FROM ph_part.ph_heap WHERE k=-1", 1))
		for _, name := range []string{"ph_index", "ph_sibling", "ph_expression"} {
			run(fixture.Exec("REINDEX INDEX ph_part."+name, 0))
		}
		for _, tc := range []struct{ name string; live int64 }{{"ph_index", 1}, {"ph_sibling", 1}, {"ph_expression", 4}} {
			pgstathashindexStats(t, p, b, c, "SELECT * FROM ph_part.pgstathashindex($1::regclass)", tc.live, 0, "ph_part."+tc.name)
		}
		pgstathashindexBoth(t, p, b, c, fixture.Select("SELECT k FROM ph_part.ph_heap ORDER BY k NULLS LAST", []string{"k"}, []string{"INT4"}, [][]any{{int64(2)}, {nil}, {nil}, {nil}}))
	})
}
