package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func hstoreComparisonUniqueSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE TABLE hs_cmp_unique(v hstore)", 0),
		fixture.Exec("CREATE UNIQUE INDEX hs_cmp_unique_key ON hs_cmp_unique(v)", 0),
		fixture.Exec("INSERT INTO hs_cmp_unique VALUES ('a=>1,b=>2'),(NULL),(NULL),('a=>bc'),('ab=>c'),('a=>NULL'),('a=>\"\"')", 7),
	} {
		hstoreRun(t, p, b, c, s)
	}
}

func hstoreComparisonUniqueChecks(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	baseline := fixture.Select("SELECT count(*) AS n FROM hs_cmp_unique", []string{"n"}, []string{"INT8"}, [][]any{{int64(7)}})
	want := hstoreComparisonUniqueDiagnostic("hs_cmp_unique", "hs_cmp_unique_key")
	for _, prepared := range []bool{false, true} {
		hstoreError(t, p, b, c, "INSERT INTO hs_cmp_unique VALUES ('new=>key'),('b=>2,a=>1')", prepared, false, false, want)
		hstoreRun(t, p, b, c, baseline)
		hstoreRun(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM hs_cmp_unique WHERE v='new=>key'::hstore", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		hstoreRun(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM hs_cmp_unique WHERE v='b=>2,a=>1'::hstore", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
	}
	q := "INSERT INTO hs_cmp_unique VALUES ($1::hstore)"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	hstoreError(t, p, b, held, q, false, false, false, want, "b=>2,a=>1")
	hstoreRun(t, p, b, held, fixture.Exec(q, 1, "new=>key"))
	hstoreRun(t, p, b, c, fixture.Exec("DELETE FROM hs_cmp_unique WHERE v='new=>key'::hstore", 1))
	hstoreRun(t, p, b, c, baseline)
	// A rejected update must not remove the old entry or insert a duplicate.
	hstoreError(t, p, b, c, "UPDATE hs_cmp_unique SET v='b=>2,a=>1' WHERE v='ab=>c'::hstore", false, false, false, want)
	hstoreRun(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM hs_cmp_unique WHERE v='ab=>c'::hstore", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
}

func TestReferenceHstoreComparisonIndexes(t *testing.T) {
	for _, tc := range []struct {
		name, method, class string
	}{{"btree_default", "", ""}, {"btree_explicit", " USING btree", " public.btree_hstore_ops"}, {"hash_default", " USING hash", ""}, {"hash_explicit", " USING hash", " public.hash_hstore_ops"}} {
		t.Run(tc.name, func(t *testing.T) {
			hstoreComparisonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				hstoreInstall(t, p, b, c)
				hstoreComparisonTable(t, p, b, c)
				hstoreRun(t, p, b, c, fixture.Exec("CREATE INDEX hs_cmp_index ON hs_cmp"+tc.method+"(v"+tc.class+")", 0))
				for _, s := range []fixture.Step{
					hstoreComparisonIDs("SELECT id FROM hs_cmp WHERE v='b=>2,a=>1'::hstore ORDER BY id", 1, 2),
					hstoreComparisonIDs("SELECT id FROM hs_cmp WHERE v IS NULL ORDER BY id", 3, 8),
				} {
					hstoreRun(t, p, b, c, s)
					hstoreRun(t, p, b, c, fixture.Prepared(s))
				}
				hstoreComparisonHeldConsumer(t, p, b, c)
				for _, s := range []fixture.Step{
					fixture.Exec("INSERT INTO hs_cmp VALUES (9,'a=>bc'),(10,'ab=>c'),(11,'a=>NULL'),(12,'a=>\"\"')", 4),
					hstoreComparisonIDs("SELECT id FROM hs_cmp WHERE v='a=>bc'::hstore ORDER BY id", 9),
					hstoreComparisonIDs("SELECT id FROM hs_cmp WHERE v='ab=>c'::hstore ORDER BY id", 10),
					hstoreComparisonIDs("SELECT id FROM hs_cmp WHERE v='a=>NULL'::hstore ORDER BY id", 5, 11),
					hstoreComparisonIDs("SELECT id FROM hs_cmp WHERE v='a=>\"\"'::hstore ORDER BY id", 6, 12),
					fixture.Prepared(fixture.Exec("UPDATE hs_cmp SET v=$1::hstore WHERE id=9", 1, "b=>2,a=>1")),
					hstoreComparisonIDs("SELECT id FROM hs_cmp WHERE v='a=>bc'::hstore ORDER BY id"),
					hstoreComparisonIDs("SELECT id FROM hs_cmp WHERE v='b=>2,a=>1'::hstore ORDER BY id", 1, 2, 9),
					fixture.Exec("DELETE FROM hs_cmp WHERE v='a=>1,b=>2'::hstore", 3),
					hstoreComparisonIDs("SELECT id FROM hs_cmp WHERE v='b=>2,a=>1'::hstore ORDER BY id"),
				} {
					hstoreRun(t, p, b, c, s)
				}
				hstoreComparisonUniqueSetup(t, p, b, c)
				hstoreComparisonUniqueChecks(t, p, b, c)
			})
		})
	}
}
