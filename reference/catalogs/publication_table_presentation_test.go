package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

// REL_18_6 pg_publication.c looks up attributes and quals on the presented
// relation, after membership/root deduplication. system_views.sql derives
// attnames with array_agg, whose empty input is NULL, not an empty array.
// https://github.com/postgres/postgres/blob/REL_18_6/src/backend/catalog/pg_publication.c
// https://github.com/postgres/postgres/blob/REL_18_6/src/backend/catalog/system_views.sql
func TestReferencePublicationTablePresentation(t *testing.T) {
	t.Run("CrossSchemaLeaf", func(t *testing.T) {
		publicationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			c := publicationConn(t, p, b)
			defer c.Close()
			exec := func(q string) { publicationRun(t, p, b, c, fixture.Exec(q, 0)) }
			check := func(s fixture.Step) {
				publicationRun(t, p, b, c, s, fixture.Prepared(s))
			}
			exec("CREATE SCHEMA publication_rt_s")
			exec("CREATE SCHEMA publication_rt_other")
			exec("CREATE TABLE publication_rt_s.root(id integer,v integer) PARTITION BY RANGE(id)")
			exec("CREATE TABLE publication_rt_other.leaf PARTITION OF publication_rt_s.root FOR VALUES FROM(0) TO(10)")
			exec("CREATE PUBLICATION publication_rt_p FOR TABLE publication_rt_other.leaf WHERE(v>0), TABLES IN SCHEMA publication_rt_s WITH(publish='insert')")
			// Ancestor-schema coverage makes DML unrestricted, but the presented
			// leaf is in another schema and keeps its direct explicit qual.
			for _, s := range publicationPresentation("publication_rt_p", "publication_rt_other", "leaf", "{id,v}", "1 2", "(v > 0)") {
				check(s)
			}
			// Only coverage of the presented leaf's own namespace suppresses it.
			exec("ALTER PUBLICATION publication_rt_p ADD TABLES IN SCHEMA publication_rt_other")
			for _, s := range publicationPresentation("publication_rt_p", "publication_rt_other", "leaf", "{id,v}", "1 2", nil) {
				check(s)
			}
			exec("ALTER PUBLICATION publication_rt_p DROP TABLES IN SCHEMA publication_rt_other")
			for _, s := range publicationPresentation("publication_rt_p", "publication_rt_other", "leaf", "{id,v}", "1 2", "(v > 0)") {
				check(s)
			}
		})
	})

	// Reverse both relation allocation and explicit mapping insertion order.
	// A pre-created leaf is attached with the existing partition DDL, so the
	// leaf can have the lower OID without relying on allocator constants.
	for _, relationOrder := range []string{"RootFirst", "LeafFirst"} {
		for _, mappingOrder := range []string{"RootMappingFirst", "LeafMappingFirst"} {
			t.Run(relationOrder+"/"+mappingOrder, func(t *testing.T) {
				publicationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					c := publicationConn(t, p, b)
					defer c.Close()
					exec := func(q string) { publicationRun(t, p, b, c, fixture.Exec(q, 0)) }
					check := func(s fixture.Step) {
						publicationRun(t, p, b, c, s, fixture.Prepared(s))
					}
					exec("CREATE SCHEMA publication_rt_s")
					exec("CREATE SCHEMA publication_rt_other")
					if relationOrder == "LeafFirst" {
						exec("CREATE TABLE publication_rt_other.leaf(id integer,v integer)")
					}
					exec("CREATE TABLE publication_rt_s.root(id integer,v integer) PARTITION BY RANGE(id)")
					if relationOrder == "LeafFirst" {
						exec("ALTER TABLE publication_rt_s.root ATTACH PARTITION publication_rt_other.leaf FOR VALUES FROM(0) TO(10)")
					} else {
						exec("CREATE TABLE publication_rt_other.leaf PARTITION OF publication_rt_s.root FOR VALUES FROM(0) TO(10)")
					}
					check(fixture.Select("SELECT 'publication_rt_s.root'::regclass::oid < 'publication_rt_other.leaf'::regclass::oid AS root_allocated_first", []string{"root_allocated_first"}, []string{"BOOL"}, [][]any{{relationOrder == "RootFirst"}}))
					exec("CREATE PUBLICATION publication_rt_p WITH(publish='insert',publish_via_partition_root=true)")
					root := "ALTER PUBLICATION publication_rt_p ADD TABLE publication_rt_s.root(id) WHERE(id>0)"
					leaf := "ALTER PUBLICATION publication_rt_p ADD TABLE publication_rt_other.leaf(v) WHERE(v>10)"
					if mappingOrder == "RootMappingFirst" {
						exec(root)
						exec(leaf)
					} else {
						exec(leaf)
						exec(root)
					}
					// Exactly one root is presented. Its own list/qual wins,
					// never the leaf's differing list/qual or traversal order.
					for _, s := range publicationPresentation("publication_rt_p", "publication_rt_s", "root", "{id}", "1", "(id > 0)") {
						check(s)
					}
				})
			})
		}
	}

	t.Run("GeneratedOnly", func(t *testing.T) {
		publicationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			c := publicationConn(t, p, b)
			defer c.Close()
			exec := func(q string) { publicationRun(t, p, b, c, fixture.Exec(q, 0)) }
			check := func(s fixture.Step) {
				publicationRun(t, p, b, c, s, fixture.Prepared(s))
			}
			exec("CREATE TABLE publication_rt_t(g integer GENERATED ALWAYS AS(7) STORED)")
			publicationRun(t, p, b, c, fixture.Exec("INSERT INTO publication_rt_t DEFAULT VALUES", 1))
			exec("CREATE PUBLICATION publication_rt_p FOR TABLE publication_rt_t WITH(publish='insert')")
			assertProjection := func(names, attrs any) {
				for _, s := range publicationPresentation("publication_rt_p", "public", "publication_rt_t", names, attrs, nil) {
					check(s)
				}
			}
			assertProjection(nil, nil)
			check(fixture.Select("SELECT g FROM publication_rt_t", []string{"g"}, []string{"INT4"}, [][]any{{int64(7)}}))
			exec("ALTER PUBLICATION publication_rt_p SET(publish_generated_columns=stored)")
			assertProjection("{g}", "1")
			exec("ALTER PUBLICATION publication_rt_p SET(publish_generated_columns=none)")
			assertProjection(nil, nil)
			exec("ALTER TABLE publication_rt_t ADD COLUMN v integer DEFAULT 11")
			assertProjection("{v}", "2")
			check(fixture.Select("SELECT g,v FROM publication_rt_t", []string{"g", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(11)}}))
			exec("ALTER PUBLICATION publication_rt_p SET(publish_generated_columns=stored)")
			assertProjection("{g,v}", "1 2")
			check(fixture.Select("SELECT g,v FROM publication_rt_t", []string{"g", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(11)}}))
		})
	})

	t.Run("ZeroColumns", func(t *testing.T) {
		publicationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			c := publicationConn(t, p, b)
			defer c.Close()
			exec := func(q string) { publicationRun(t, p, b, c, fixture.Exec(q, 0)) }
			check := func(s fixture.Step) {
				publicationRun(t, p, b, c, s, fixture.Prepared(s))
			}
			// Empty table definitions already have ordinary ALTER reference
			// coverage; this adds only their publication presentation boundary.
			exec("CREATE TABLE publication_rt_t()")
			publicationRun(t, p, b, c, fixture.Exec("INSERT INTO publication_rt_t DEFAULT VALUES", 1))
			exec("CREATE PUBLICATION publication_rt_p FOR TABLE publication_rt_t WITH(publish='insert')")
			for _, s := range publicationPresentation("publication_rt_p", "public", "publication_rt_t", nil, nil, nil) {
				check(s)
			}
			exec("ALTER PUBLICATION publication_rt_p SET(publish_generated_columns=stored)")
			for _, s := range publicationPresentation("publication_rt_p", "public", "publication_rt_t", nil, nil, nil) {
				check(s)
			}
			check(fixture.Select("SELECT count(*) AS n FROM publication_rt_t", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
			exec("ALTER TABLE publication_rt_t ADD COLUMN id integer DEFAULT 9")
			for _, s := range publicationPresentation("publication_rt_p", "public", "publication_rt_t", "{id}", "1", nil) {
				check(s)
			}
			check(fixture.Select("SELECT id FROM publication_rt_t", []string{"id"}, []string{"INT4"}, [][]any{{int64(9)}}))
		})
	})
}

// Both surfaces have separate literal expectations; agreement alone is not a
// proof. IS NULL also distinguishes an empty projection from an empty array.
func publicationPresentation(name, schema, table string, names, attrs, filter any) []fixture.Step {
	return []fixture.Step{
		fixture.Select("SELECT pubname,schemaname,tablename,attnames::text,attnames IS NULL AS names_null,rowfilter FROM pg_catalog.pg_publication_tables WHERE pubname=$1 ORDER BY schemaname,tablename", []string{"pubname", "schemaname", "tablename", "attnames", "names_null", "rowfilter"}, []string{"NAME", "NAME", "NAME", "TEXT", "BOOL", "TEXT"}, [][]any{{name, schema, table, names, names == nil, filter}}, name),
		fixture.Select("SELECT p.pubname,n.nspname,c.relname,t.attrs::text,t.attrs IS NULL AS attrs_null,pg_get_expr(t.qual,t.relid) AS rowfilter,t.pubid=p.oid AS publication_link,t.relid=c.oid AS relation_link FROM pg_catalog.pg_get_publication_tables($1::text) t JOIN pg_catalog.pg_publication p ON p.oid=t.pubid JOIN pg_catalog.pg_class c ON c.oid=t.relid JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace ORDER BY n.nspname,c.relname", []string{"pubname", "nspname", "relname", "attrs", "attrs_null", "rowfilter", "publication_link", "relation_link"}, []string{"NAME", "NAME", "NAME", "TEXT", "BOOL", "TEXT", "BOOL", "BOOL"}, [][]any{{name, schema, table, attrs, attrs == nil, filter, true, true}}, name),
	}
}
