package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSPGiSTBuiltinClasses(t *testing.T) {
	for _, tc := range []struct{ typ, class, value string }{
		{"inet", "inet_ops", "10.0.0.1/24"}, {"cidr", "inet_ops", "10.0.0.0/24"},
		{"point", "quad_point_ops", "(3,4)"}, {"point", "kd_point_ops", "(3,4)"},
		{"box", "box_ops", "(2,2),(0,0)"}, {"polygon", "poly_ops", "((0,0),(4,0),(0,4))"},
		{"text", "text_ops", "a "}, {"varchar", "text_ops", "é"},
		{"int4range", "range_ops", "[1,3)"}, {"int8range", "range_ops", "[1,3)"}, {"numrange", "range_ops", "[1,3)"},
		{"daterange", "range_ops", "[2020-01-01,2020-01-03)"}, {"tsrange", "range_ops", "[2020-01-01,2020-01-03)"}, {"tstzrange", "range_ops", "[2020-01-01,2020-01-03)"},
	} {
		t.Run(tc.typ+"_"+tc.class, func(t *testing.T) {
			classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				conn, run := classConn(t, p, b)
				run(fixture.Exec("CREATE TABLE spgist_classes(id integer,v "+tc.typ+")", 0))
				run(fixture.Prepared(fixture.Exec("CREATE INDEX spgist_empty ON spgist_classes USING spgist(v pg_catalog."+tc.class+")", 0)))
				run(fixture.Exec("INSERT INTO spgist_classes VALUES(1,NULL)", 1))
				run(fixture.Exec("CREATE INDEX spgist_nulls ON spgist_classes USING spgist(v)", 0))
				run(fixture.Prepared(fixture.Exec("INSERT INTO spgist_classes VALUES(2,$1),(3,$1)", 2, tc.value)))
				run(fixture.Exec("CREATE INDEX spgist_populated ON spgist_classes USING spgist(v pg_catalog."+tc.class+")", 0))
				classRoutes(t, p, b, conn, fixture.Select("SELECT id FROM spgist_classes WHERE v IS NOT NULL ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}}))
				run(fixture.Exec("UPDATE spgist_classes SET v=NULL WHERE id=2", 1))
				run(fixture.Exec("DELETE FROM spgist_classes WHERE id=3", 1))
				classRoutes(t, p, b, conn, fixture.Select("SELECT id FROM spgist_classes WHERE v IS NULL ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
			})
		})
	}
	t.Run("domains_and_custom_range", func(t *testing.T) {
		classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			conn, run := classConn(t, p, b)
			for _, q := range []string{"CREATE TYPE spgist_custom_range AS RANGE(subtype=integer)", "CREATE DOMAIN spgist_range_domain AS spgist_custom_range", "CREATE DOMAIN spgist_point_domain AS point", "CREATE DOMAIN spgist_text_domain AS varchar", "CREATE TABLE spgist_domains(p spgist_point_domain,r spgist_range_domain,s spgist_text_domain)", "CREATE INDEX spgist_dp ON spgist_domains USING spgist(p)", "CREATE INDEX spgist_dr ON spgist_domains USING spgist(r)", "CREATE INDEX spgist_ds ON spgist_domains USING spgist(s)"} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Exec("INSERT INTO spgist_domains VALUES('(0,0)','[1,3)','abc')", 1))
			classRoutes(t, p, b, conn, fixture.Select("SELECT p ~= point '(0,0)' AS p,r @> 2 AS r,s ^@ 'a' AS s FROM spgist_domains", []string{"p", "r", "s"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}))
		})
	})
}
