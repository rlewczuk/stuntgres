package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func hstoreComparisonConsumerChecks() []fixture.Step {
	checks := []fixture.Step{
		hstoreComparisonIDs("SELECT id FROM hs_cmp ORDER BY v,id", 4, 6, 5, 1, 2, 7, 3, 8),
		hstoreComparisonIDs("SELECT id FROM hs_cmp ORDER BY v ASC NULLS LAST,id", 4, 6, 5, 1, 2, 7, 3, 8),
		hstoreComparisonIDs("SELECT id FROM hs_cmp ORDER BY v ASC NULLS FIRST,id", 3, 8, 4, 6, 5, 1, 2, 7),
		hstoreComparisonIDs("SELECT id FROM hs_cmp ORDER BY v DESC,id", 3, 8, 7, 1, 2, 5, 6, 4),
		hstoreComparisonIDs("SELECT id FROM hs_cmp ORDER BY v DESC NULLS FIRST,id", 3, 8, 7, 1, 2, 5, 6, 4),
		hstoreComparisonIDs("SELECT id FROM hs_cmp ORDER BY v DESC NULLS LAST,id", 7, 1, 2, 5, 6, 4, 3, 8),
		hstoreComparisonIDs("SELECT id FROM hs_cmp WHERE v='b=>2,a=>1'::hstore ORDER BY id", 1, 2),
		hstoreComparisonIDs("SELECT id FROM hs_cmp WHERE v<>'b=>2,a=>1'::hstore ORDER BY id", 4, 5, 6, 7),
		hstoreComparisonIDs("SELECT id FROM hs_cmp WHERE v IS NOT DISTINCT FROM NULL::hstore ORDER BY id", 3, 8),
		hstoreComparisonIDs("SELECT id FROM hs_cmp WHERE v IS DISTINCT FROM NULL::hstore ORDER BY id", 1, 2, 4, 5, 6, 7),
		hstoreComparisonIDs("SELECT id FROM hs_cmp WHERE v IS NOT DISTINCT FROM 'b=>2,a=>1'::hstore ORDER BY id", 1, 2),
		fixture.Select("SELECT count(*) AS n FROM (SELECT DISTINCT v FROM hs_cmp) d", []string{"n"}, []string{"INT8"}, [][]any{{int64(6)}}),
		fixture.Select("SELECT min(id) AS first,count(*) AS n FROM hs_cmp GROUP BY v ORDER BY min(id)", []string{"first", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(2)}, {int64(3), int64(2)}, {int64(4), int64(1)}, {int64(5), int64(1)}, {int64(6), int64(1)}, {int64(7), int64(1)}}),
		fixture.Select("SELECT a.id AS a,b.id AS b FROM hs_cmp a JOIN hs_cmp b ON a.v=b.v WHERE a.v='a=>1,b=>2'::hstore ORDER BY a.id,b.id", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(1), int64(2)}, {int64(2), int64(1)}, {int64(2), int64(2)}}),
		fixture.Select("SELECT id,v IN ('a=>1,b=>2'::hstore,NULL::hstore) AS member,v NOT IN ('a=>1,b=>2'::hstore,NULL::hstore) AS nonmember,CASE v WHEN 'b=>2,a=>1'::hstore THEN true ELSE false END AS chosen,NULLIF(v,'b=>2,a=>1'::hstore) IS NULL AS nullified FROM hs_cmp ORDER BY id", []string{"id", "member", "nonmember", "chosen", "nullified"}, []string{"INT4", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{
			{int64(1), true, false, true, true}, {int64(2), true, false, true, true},
			{int64(3), nil, nil, false, true}, {int64(4), nil, nil, false, false},
			{int64(5), nil, nil, false, false}, {int64(6), nil, nil, false, false},
			{int64(7), nil, nil, false, false}, {int64(8), nil, nil, false, true},
		}),
		fixture.Select("SELECT NULL::hstore IS NOT DISTINCT FROM NULL::hstore AS same,'b=>2,a=>1'::hstore IS DISTINCT FROM 'a=>1,b=>2'::hstore AS different,NULL::hstore IN ('a=>1'::hstore) AS member", []string{"same", "different", "member"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, false, nil}}),
	}
	return checks
}

func hstoreComparisonHeldConsumer(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	q := "SELECT id FROM hs_cmp WHERE v=$1::hstore ORDER BY v,id"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	for _, tc := range []struct {
		arg any
		ids []int64
	}{{"b=>2,a=>1", []int64{1, 2}}, {[]byte("a=>NULL"), []int64{5}}, {nil, nil}, {"", []int64{4}}, {"a=>1,a=>1,b=>2", []int64{1, 2}}} {
		s := hstoreComparisonIDs(q, tc.ids...)
		s.Args = []any{tc.arg}
		hstoreRun(t, p, b, held, s)
	}
}

func TestReferenceHstoreComparisonConsumers(t *testing.T) {
	t.Run("qualified_authority", hstoreComparisonBinding)
	hstoreComparisonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		hstoreInstall(t, p, b, c)
		hstoreComparisonTable(t, p, b, c)
		for _, s := range hstoreComparisonConsumerChecks() {
			hstoreRun(t, p, b, c, s)
			hstoreRun(t, p, b, c, fixture.Prepared(s))
		}
		hstoreComparisonHeldConsumer(t, p, b, c)
		hstoreRun(t, p, b, c, fixture.Exec("CREATE TABLE hs_cmp_empty(id integer,v hstore)", 0))
		for _, suffix := range []string{"ORDER BY v ASC NULLS LAST,id", "ORDER BY v DESC NULLS FIRST,id"} {
			s := hstoreComparisonIDs("SELECT id FROM hs_cmp_empty " + suffix)
			hstoreRun(t, p, b, c, s)
			hstoreRun(t, p, b, c, fixture.Prepared(s))
		}
		hstoreRun(t, p, b, c, fixture.Exec("INSERT INTO hs_cmp_empty VALUES (1,NULL),(2,NULL)", 2))
		for _, suffix := range []string{"ORDER BY v ASC NULLS LAST,id", "ORDER BY v DESC NULLS FIRST,id"} {
			s := hstoreComparisonIDs("SELECT id FROM hs_cmp_empty "+suffix, 1, 2)
			hstoreRun(t, p, b, c, s)
			hstoreRun(t, p, b, c, fixture.Prepared(s))
		}
	})
}
