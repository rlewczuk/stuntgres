package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func ltreeComparisonUniqueChecks(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE TABLE lt_cmp_unique(v ltree)", 0),
		fixture.Exec("CREATE UNIQUE INDEX lt_cmp_unique_key ON lt_cmp_unique(v)", 0),
		fixture.Exec("INSERT INTO lt_cmp_unique VALUES ('a.b'),(NULL),(NULL),('ab')", 4),
	} { ltreeComparisonRun(t, p, b, c, s) }
	want := ltreeComparisonUniqueDiagnostic()
	baseline := fixture.Select("SELECT count(*) AS n FROM lt_cmp_unique", []string{"n"}, []string{"INT8"}, [][]any{{int64(4)}})
	for _, prepared := range []bool{false,true} {
		for _, q := range []string{"INSERT INTO lt_cmp_unique VALUES ('a.b')", "INSERT INTO lt_cmp_unique VALUES ('new'),('a.b')", "UPDATE lt_cmp_unique SET v='a.b' WHERE v='ab'::ltree"} {
			ltreeError(t, p, b, c, q, prepared, false, false, want)
			ltreeComparisonRun(t, p, b, c, baseline)
			ltreeComparisonRun(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM lt_cmp_unique WHERE v='new'::ltree", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
			ltreeComparisonRun(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM lt_cmp_unique WHERE v='ab'::ltree", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
		}
	}
	q := "INSERT INTO lt_cmp_unique VALUES ($1::ltree)"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil { t.Fatal(err) }
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	ltreeComparisonRun(t, p, b, held, fixture.ErrorDiagnostic(q, want, "a.b"))
	ltreeComparisonRun(t, p, b, held, fixture.Exec(q, 1, "new"))
	ltreeComparisonRun(t, p, b, c, fixture.Exec("DELETE FROM lt_cmp_unique WHERE v='new'::ltree", 1))
	ltreeComparisonRun(t, p, b, c, baseline)
}

func TestReferenceLtreeComparisonIndexes(t *testing.T) {
	for _, tc := range []struct{ name,method,class string }{
		{"btree_default", "", ""}, {"btree_explicit", " USING btree", " public.ltree_ops"},
		{"hash_default", " USING hash", ""}, {"hash_explicit", " USING hash", " public.hash_ltree_ops"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ltreeComparisonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				ltreeInstall(t, p, b, c)
				ltreeComparisonTable(t, p, b, c)
				ltreeComparisonRun(t, p, b, c, fixture.Exec("CREATE INDEX lt_cmp_key ON lt_cmp"+tc.method+"(v"+tc.class+")", 0))
				for _, s := range ltreeComparisonConsumerChecks(b) { ltreeComparisonRun(t, p, b, c, s) }
				q := "SELECT id FROM lt_cmp WHERE v=$1::ltree ORDER BY id"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil { t.Fatal(err) }
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				step := ltreeComparisonIDs(q, 2,6); step.Args = []any{[]byte("a.b")}
				ltreeComparisonRun(t, p, b, held, step)
				for _, s := range []fixture.Step{
					fixture.Exec("INSERT INTO lt_cmp VALUES (7,'ab'),(8,'A'),(9,'a.b')", 3),
					ltreeComparisonIDs("SELECT id FROM lt_cmp WHERE v='a.b'::ltree ORDER BY id", 2,6,9),
					fixture.Prepared(fixture.Exec("UPDATE lt_cmp SET v=$1::ltree WHERE id=7", 1, "a.b")),
					ltreeComparisonIDs("SELECT id FROM lt_cmp WHERE v='ab'::ltree ORDER BY id"),
					ltreeComparisonIDs("SELECT id FROM lt_cmp WHERE v='a.b'::ltree ORDER BY id", 2,6,7,9),
					fixture.Exec("DELETE FROM lt_cmp WHERE v='a.b'::ltree", 4),
					ltreeComparisonIDs("SELECT id FROM lt_cmp WHERE v='a.b'::ltree ORDER BY id"),
					ltreeComparisonIDs("SELECT id FROM lt_cmp WHERE v='A'::ltree ORDER BY id", 8),
				} { ltreeComparisonRun(t, p, b, c, s) }
				step = ltreeComparisonIDs(q); step.Args = []any{"a.b"}
				ltreeComparisonRun(t, p, b, held, step)
				ltreeComparisonUniqueChecks(t, p, b, c)
			})
		})
	}
}
