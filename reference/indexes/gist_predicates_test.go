package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceGiSTRangeNetworkTextSearch(t *testing.T) {
	classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, run := classConn(t, p, b)
		run(fixture.Exec("CREATE TABLE gist_predicates(id integer,r int4range,m int4multirange,i inet,q tsquery,v tsvector)", 0))
		run(fixture.Exec("INSERT INTO gist_predicates VALUES(1,'[1,3)','{[1,3),[7,9)}','10.0.0.1/24','a & b','a:1 b:2'),(2,'empty','{}','10.0.1.2','a','a:1'),(3,NULL,NULL,NULL,NULL,NULL),(4,'(,)','{(,)}','::1','','')", 4))
		check := func() {
			for _, signature := range []struct{ left, right string }{{"r", "'[7,9)'::int4range"}, {"r", "'{[7,9)}'::int4multirange"}, {"m", "'[10,12)'::int4range"}, {"m", "'{[10,12)}'::int4multirange"}} {
				for _, op := range []struct {
					name string
					want bool
				}{{"<<", true}, {"&<", true}, {"&&", false}, {"&>", false}, {">>", false}, {"-|-", false}, {"@>", false}, {"<@", false}} {
					classRoutes(t, p, b, conn, fixture.Select("SELECT "+signature.left+" "+op.name+" "+signature.right+" AS hit FROM gist_predicates WHERE id=1", []string{"hit"}, []string{"BOOL"}, [][]any{{op.want}}))
				}
			}
			classRoutes(t, p, b, conn, fixture.Select("SELECT id,m && '[4,6)'::int4range AS overlap,m @> 2 AS two,m @> 5 AS five,m -|- '[9,10)'::int4range AS adjacent,r @> 2 AS contained,r = '[1,3)'::int4range AS same,m = '{[1,3),[7,9)}'::int4multirange AS same_multi FROM gist_predicates ORDER BY id", []string{"id", "overlap", "two", "five", "adjacent", "contained", "same", "same_multi"}, []string{"INT4", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{int64(1), false, true, false, true, true, true, true}, {int64(2), false, false, false, false, false, false, false}, {int64(3), nil, nil, nil, nil, nil, nil, nil}, {int64(4), true, true, true, false, true, false, false}}))
			for _, op := range []struct {
				name string
				want bool
			}{{"&&", true}, {"=", true}, {"<>", false}, {"<", false}, {"<=", true}, {">", false}, {">=", true}, {"<<", false}, {"<<=", true}, {">>", false}, {">>=", true}} {
				classRoutes(t, p, b, conn, fixture.Select("SELECT i "+op.name+" '10.0.0.1/24'::inet AS hit FROM gist_predicates WHERE id=1", []string{"hit"}, []string{"BOOL"}, [][]any{{op.want}}))
			}
			classRoutes(t, p, b, conn, fixture.Select("SELECT id,i && '10.0.0.2'::inet AS overlap,q @> 'a'::tsquery AS contains,q <@ 'a & b'::tsquery AS contained FROM gist_predicates WHERE id<4 ORDER BY id", []string{"id", "overlap", "contains", "contained"}, []string{"INT4", "BOOL", "BOOL", "BOOL"}, [][]any{{int64(1), true, true, true}, {int64(2), false, true, true}, {int64(3), nil, nil, nil}}))
			classRoutes(t, p, b, conn, fixture.Select("SELECT q @> 'a & b'::tsquery AS contains,q <-> 'b'::tsquery AS phrase FROM gist_predicates WHERE id=2", []string{"contains", "phrase"}, []string{"BOOL", "TSQUERY"}, [][]any{{false, "'a' <-> 'b'"}}))
		}
		check()
		run(fixture.Exec("CREATE INDEX gist_predicates_i ON gist_predicates USING gist(r,m,i inet_ops,q,v)", 0))
		check()
		run(fixture.Exec("INSERT INTO gist_predicates SELECT 5,r,m,i,q,v FROM gist_predicates WHERE id=1", 1))
		run(fixture.Exec("UPDATE gist_predicates SET m='{[4,6)}' WHERE id=5", 1))
		run(fixture.Exec("DELETE FROM gist_predicates WHERE id=2", 1))
		classRoutes(t, p, b, conn, fixture.Select("SELECT id FROM gist_predicates WHERE m && '[4,6)'::int4range ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(4)}, {int64(5)}}))
		// Scalar matching remains correct independently of GiST admission.
		const matched = "SELECT id FROM gist_predicates WHERE v @@ 'a'::tsquery ORDER BY id"
		classRoutes(t, p, b, conn, fixture.Select(matched, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(5)}}))
	})
}
