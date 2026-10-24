package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func citextComparisonCollationSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	citextInstall(t, p, b, c)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE TABLE citext_compare_coll(id integer,a citext COLLATE "C",b citext COLLATE "POSIX")`, 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO citext_compare_coll VALUES (1,'ABC','abc'),(2,'ABC','abd'),(3,NULL,'abc'),(4,'ABC',NULL),(5,NULL,NULL)", 5))
}

func TestReferenceCitextComparisonCollation(t *testing.T) {
	t.Run("providers", func(t *testing.T) {
		citextComparisonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			citextInstall(t, p, b, c)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE COLLATION citext_compare_copy FROM pg_catalog.pg_c_utf8`, 0))
			for _, coll := range []string{`"C"`, `"POSIX"`, "pg_c_utf8", "pg_unicode_fast", "citext_compare_copy"} {
				q := "SELECT " + citextComparisonProjection("'ABC'::citext COLLATE "+coll, "'abc'::citext COLLATE "+coll)
				citextComparisonRoutes(t, p, b, c, fixture.Select(q, citextComparisonNames, citextComparisonTypes, [][]any{{true, false, false, true, false, true}}))
				q = "SELECT " + citextComparisonProjection("'É'::citext COLLATE "+coll, "'é'::citext COLLATE "+coll)
				citextComparisonRoutes(t, p, b, c, fixture.Select(q, citextComparisonNames, citextComparisonTypes, [][]any{{false, true, true, true, false, false}}))
				lower := "é"
				if coll == `"C"` || coll == `"POSIX"` {
					lower = "É"
				}
				citextComparisonRoutes(t, p, b, c, fixture.Select("SELECT lower('É'::text COLLATE "+coll+") AS folded,('É'::citext COLLATE "+coll+")::text AS original", []string{"folded", "original"}, []string{"TEXT", "TEXT"}, [][]any{{lower, "É"}}))
			}
		})
	})
	t.Run("implicit_demand", func(t *testing.T) {
		citextComparisonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			citextComparisonCollationSetup(t, p, b, c)
			citextComparisonRoutes(t, p, b, c, fixture.Select("SELECT a=b AS eq,a<>b AS ne FROM citext_compare_coll WHERE id<=2 ORDER BY id", []string{"eq", "ne"}, []string{"BOOL", "BOOL"}, [][]any{{true, false}, {false, true}}))
			for _, op := range []string{"<", "<=", ">", ">="} {
				for _, tc := range []struct {
					where string
					rows  [][]any
				}{
					{"id>=3 ORDER BY id", [][]any{{nil}, {nil}, {nil}}},
					{"FALSE", nil},
				} {
					citextComparisonRoutes(t, p, b, c, fixture.Select("SELECT a"+op+"b AS v FROM citext_compare_coll WHERE "+tc.where, []string{"v"}, []string{"BOOL"}, tc.rows))
				}
				citextComparisonRoutes(t, p, b, c, fixture.Select("SELECT CASE WHEN FALSE THEN a"+op+"b ELSE TRUE END AS v FROM citext_compare_coll WHERE id=1", []string{"v"}, []string{"BOOL"}, [][]any{{true}}))
			}
			citextComparisonRoutes(t, p, b, c, fixture.Select("SELECT "+citextComparisonProjection(`a COLLATE "C"`, "b")+" FROM citext_compare_coll WHERE id=2", citextComparisonNames, citextComparisonTypes, [][]any{{false, true, true, true, false, false}}))
		})
	})
}
