package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceGiSTBuiltinClasses(t *testing.T) {
	for _, tc := range []struct {
		typ, class, value, text string
		defaultClass            bool
	}{
		{"inet", "inet_ops", "10.0.0.1/24", "10.0.0.1/24", false},
		{"cidr", "inet_ops", "10.0.0.0/24", "10.0.0.0/24", false},
		{"box", "box_ops", "(2,2),(0,0)", "(2,2),(0,0)", true},
		{"point", "point_ops", "(3,4)", "(3,4)", true},
		{"polygon", "poly_ops", "((0,0),(4,0),(0,4))", "((0,0),(4,0),(0,4))", true},
		{"circle", "circle_ops", "<(0,0),1>", "<(0,0),1>", true},
		{"tsvector", "tsvector_ops", "a:1 b:2", "'a':1 'b':2", true},
		{"tsquery", "tsquery_ops", "a & b", "'a' & 'b'", true},
		{"int4range", "range_ops", "[1,3)", "[1,3)", true},
		{"int4multirange", "multirange_ops", "{[1,3),[7,9)}", "{[1,3),[7,9)}", true},
	} {
		t.Run(tc.typ, func(t *testing.T) {
			classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				conn, run := classConn(t, p, b)
				run(fixture.Exec("CREATE TABLE gist_class_rows(id integer,v "+tc.typ+")", 0))
				run(fixture.Prepared(fixture.Exec("CREATE INDEX gist_empty ON gist_class_rows USING gist(v pg_catalog."+tc.class+")", 0)))
				run(fixture.Exec("INSERT INTO gist_class_rows VALUES(1,'"+tc.value+"'),(2,NULL),(3,'"+tc.value+"')", 3))
				if tc.defaultClass {
					run(fixture.Exec("CREATE INDEX gist_default ON gist_class_rows USING gist(v)", 0))
				}
				run(fixture.Exec("CREATE INDEX gist_explicit ON gist_class_rows USING gist(v pg_catalog."+tc.class+")", 0))
				check := func(want [][]any) {
					classRoutes(t, p, b, conn, fixture.Select("SELECT id,v::text AS v FROM gist_class_rows ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, want))
				}
				check([][]any{{int64(1), tc.text}, {int64(2), nil}, {int64(3), tc.text}})
				if tc.typ == "tsvector" || tc.typ == "tsquery" {
					classRoutes(t, p, b, conn, fixture.Select("SELECT id FROM gist_class_rows WHERE v::text=($1::"+tc.typ+")::text ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(3)}}, tc.value))
				}
				run(fixture.Prepared(fixture.Exec("INSERT INTO gist_class_rows VALUES($1,$2)", 1, 4, tc.value)))
				run(fixture.Exec("UPDATE gist_class_rows SET v=NULL WHERE id=1", 1))
				run(fixture.Exec("DELETE FROM gist_class_rows WHERE id=3", 1))
				check([][]any{{int64(1), nil}, {int64(2), nil}, {int64(4), tc.text}})
				if tc.typ == "tsvector" || tc.typ == "tsquery" {
					classRoutes(t, p, b, conn, fixture.Select("SELECT id FROM gist_class_rows WHERE v::text=($1::"+tc.typ+")::text ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(4)}}, tc.value))
				}
			})
		})
	}
	t.Run("descriptor_defaults", func(t *testing.T) {
		classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			conn, run := classConn(t, p, b)
			for _, q := range []string{
				"CREATE TYPE gist_custom_range AS RANGE(subtype=integer,multirange_type_name=gist_custom_multi)",
				"CREATE DOMAIN gist_point_domain AS point",
				"CREATE DOMAIN gist_range_domain AS gist_custom_range",
				"CREATE DOMAIN gist_multi_domain AS gist_custom_multi",
				"CREATE TABLE gist_domains(id integer,p gist_point_domain,r gist_range_domain,m gist_multi_domain)",
				"INSERT INTO gist_domains VALUES(1,'(0,0)','[1,3)','{[1,3),[7,9)}')",
				"CREATE INDEX gist_domains_i ON gist_domains USING gist(p,r,m)",
			} {
				count := int64(0)
				if q[:6] == "INSERT" {
					count = 1
				}
				run(fixture.Exec(q, count))
			}
			classRoutes(t, p, b, conn, fixture.Select("SELECT id FROM gist_domains WHERE r @> 2 AND m @> 8 AND p ~= point '(0,0)'", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
		})
	})
}
