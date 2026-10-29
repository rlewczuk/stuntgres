package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

// Expectations follow REL_18_0 pg_publication.c's SQL-callable inquiries and
// pg_proc.dat's VARIADIC text[] signature, independently of native execution.
func TestReferencePublicationInquiry(t *testing.T) {
	publicationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c := publicationConn(t, p, b)
		defer c.Close()
		run := func(s fixture.Step) {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		}
		exec := func(q string) { fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0)) }
		exec("CREATE SCHEMA publication_rt_s")
		exec("CREATE SCHEMA publication_rt_other")
		exec("CREATE TABLE publication_rt_t(id integer PRIMARY KEY,v integer,g integer GENERATED ALWAYS AS(v+1) STORED,virtual integer GENERATED ALWAYS AS(v+2) VIRTUAL)")
		exec("CREATE TABLE publication_rt_s.t(id integer)")
		exec("CREATE UNLOGGED TABLE publication_rt_s.unlogged(id integer)")
		exec("CREATE TEMP TABLE publication_rt_temp(id integer)")
		exec("CREATE VIEW publication_rt_view AS SELECT 1 AS id")
		exec("CREATE SEQUENCE publication_rt_seq")
		// Eligibility is intrinsic, not a test of whether any publication exists.
		run(fixture.Select("SELECT pg_relation_is_publishable('publication_rt_t'::regclass) AS logged,pg_relation_is_publishable('publication_rt_s.unlogged'::regclass) AS unlogged,pg_relation_is_publishable('publication_rt_temp'::regclass) AS temporary,pg_relation_is_publishable('publication_rt_view'::regclass) AS view,pg_relation_is_publishable('publication_rt_seq'::regclass) AS sequence,pg_relation_is_publishable('pg_catalog.pg_class'::regclass) AS system", []string{"logged", "unlogged", "temporary", "view", "sequence", "system"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, false, false, false, false, false}}))
		run(fixture.Select("SELECT pg_relation_is_publishable(NULL::regclass) AS null_input,pg_relation_is_publishable(0::oid::regclass) AS missing", []string{"null_input", "missing"}, []string{"BOOL", "BOOL"}, [][]any{{nil, nil}}))
		exec("CREATE TABLE publication_rt_retired(id integer)")
		var retired int64
		if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_catalog.pg_class WHERE oid='publication_rt_retired'::regclass").Scan(&retired); err != nil || retired <= 0 {
			t.Fatal("actual retired relation identity", retired, err)
		}
		exec("DROP TABLE publication_rt_retired")
		run(fixture.Select("SELECT pg_relation_is_publishable($1::oid::regclass) AS retired", []string{"retired"}, []string{"BOOL"}, [][]any{{nil}}, retired))
		for _, argument := range []string{"NULL::text[]", "ARRAY[]::text[]"} {
			run(fixture.Select("SELECT * FROM pg_catalog.pg_get_publication_tables(VARIADIC "+argument+")", []string{"pubid", "relid", "attrs", "qual"}, []string{"OID", "OID", "INT2VECTOR", "PG_NODE_TREE"}, nil))
		}
		exec("CREATE PUBLICATION publication_rt_empty")
		exec("CREATE PUBLICATION publication_rt_explicit FOR TABLE publication_rt_t(v,id) WHERE(v>0) WITH(publish='insert')")
		exec("CREATE PUBLICATION publication_rt_schema FOR TABLES IN SCHEMA publication_rt_s WITH(publish='')")
		exec("CREATE PUBLICATION publication_rt_all FOR ALL TABLES WITH(publish='truncate',publish_generated_columns=stored)")
		run(fixture.Select("SELECT * FROM pg_catalog.pg_get_publication_tables('publication_rt_empty')", []string{"pubid", "relid", "attrs", "qual"}, []string{"OID", "OID", "INT2VECTOR", "PG_NODE_TREE"}, nil))
		// Semantic OID relationships are resolved against the actual source
		// owners. Allocator values are neither harvested nor compared cross-mode.
		inquiry := func(arguments string, want ...[]any) fixture.Step {
			return fixture.Select("SELECT p.pubname,n.nspname,c.relname,t.attrs::text,pg_get_expr(t.qual,t.relid) AS filter,t.pubid=p.oid AS publication_link,t.relid=c.oid AS relation_link FROM pg_catalog.pg_get_publication_tables("+arguments+") t JOIN pg_catalog.pg_publication p ON p.oid=t.pubid JOIN pg_catalog.pg_class c ON c.oid=t.relid JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace ORDER BY p.pubname,n.nspname,c.relname", []string{"pubname", "nspname", "relname", "attrs", "filter", "publication_link", "relation_link"}, []string{"NAME", "NAME", "NAME", "TEXT", "TEXT", "BOOL", "BOOL"}, want)
		}
		explicit := []any{"publication_rt_explicit", "public", "publication_rt_t", "1 2", "(v > 0)", true, true}
		schema := []any{"publication_rt_schema", "publication_rt_s", "t", "1", nil, true, true}
		run(inquiry("'publication_rt_explicit'", explicit))
		run(inquiry("VARIADIC ARRAY['publication_rt_explicit']::text[]", explicit))
		run(inquiry("VARIADIC '[0:0]={publication_rt_explicit}'::text[]", explicit))
		run(inquiry("'publication_rt_schema'", schema))
		run(inquiry("'publication_rt_all'", []any{"publication_rt_all", "public", "publication_rt_t", "1 2 3", nil, true, true}, []any{"publication_rt_all", "publication_rt_s", "t", "1", nil, true, true}))
		run(inquiry("'publication_rt_explicit','publication_rt_schema'", explicit, schema))
		// Repeated input names and overlapping publications retain multiplicity.
		run(inquiry("VARIADIC ARRAY['publication_rt_explicit','publication_rt_explicit']", explicit, explicit))
		run(inquiry("VARIADIC ARRAY[['publication_rt_explicit','publication_rt_schema'],['publication_rt_explicit','publication_rt_empty']]", explicit, explicit, schema))
		exec("ALTER PUBLICATION publication_rt_explicit SET TABLE publication_rt_t")
		run(inquiry("'publication_rt_explicit'", []any{"publication_rt_explicit", "public", "publication_rt_t", "1 2", nil, true, true}))
		exec("ALTER PUBLICATION publication_rt_explicit SET(publish_generated_columns=stored)")
		run(inquiry("'publication_rt_explicit'", []any{"publication_rt_explicit", "public", "publication_rt_t", "1 2 3", nil, true, true}))
		exec("CREATE TABLE publication_rt_s.future(id integer)")
		run(inquiry("'publication_rt_schema'", []any{"publication_rt_schema", "publication_rt_s", "future", "1", nil, true, true}, schema))
		// Published action flags do not remove tables from this metadata inquiry.
		run(inquiry("'publication_rt_schema','publication_rt_empty'", []any{"publication_rt_schema", "publication_rt_s", "future", "1", nil, true, true}, schema))
		for _, argument := range []string{"'publication_rt_missing'", "VARIADIC ARRAY['publication_rt_explicit','publication_rt_missing']", "''"} {
			name := "publication_rt_missing"
			if argument == "''" {
				name = ""
			}
			s := publicationDiagnostic("SELECT * FROM pg_catalog.pg_get_publication_tables("+argument+")", "42704", `publication "`+name+`" does not exist`, "", "")
			s.SelectRows, s.ErrorStage = true, "execute"
			run(s)
		}
		for _, argument := range []string{"NULL::text", "VARIADIC ARRAY[NULL]::text[]", "VARIADIC ARRAY['publication_rt_explicit',NULL]::text[]"} {
			s := publicationDiagnostic("SELECT * FROM pg_catalog.pg_get_publication_tables("+argument+")", "22004", "null array element not allowed in this context", "", "")
			s.SelectRows, s.ErrorStage = true, "execute"
			run(s)
		}
		// Partition inquiries use current ancestry and apply via-root suppression
		// across the complete input publication array, not one publication at a time.
		exec("CREATE TABLE publication_rt_s.root(id integer) PARTITION BY RANGE(id)")
		exec("CREATE TABLE publication_rt_other.leaf PARTITION OF publication_rt_s.root FOR VALUES FROM(0) TO(10)")
		exec("CREATE PUBLICATION publication_rt_leafpub FOR TABLE publication_rt_s.root")
		exec("CREATE PUBLICATION publication_rt_rootpub FOR TABLE publication_rt_s.root WITH(publish_via_partition_root=true)")
		run(fixture.Select("SELECT pg_relation_is_publishable('publication_rt_s.root'::regclass) AS root,pg_relation_is_publishable('publication_rt_other.leaf'::regclass) AS leaf", []string{"root", "leaf"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
		leaf := []any{"publication_rt_leafpub", "publication_rt_other", "leaf", "1", nil, true, true}
		root := []any{"publication_rt_rootpub", "publication_rt_s", "root", "1", nil, true, true}
		run(inquiry("'publication_rt_leafpub'", leaf))
		run(inquiry("'publication_rt_rootpub'", root))
		run(inquiry("'publication_rt_leafpub','publication_rt_rootpub'", root))
		run(inquiry("'publication_rt_rootpub','publication_rt_leafpub'", root))
		exec("CREATE TABLE publication_rt_s.next PARTITION OF publication_rt_s.root FOR VALUES FROM(10) TO(20)")
		run(inquiry("'publication_rt_leafpub'", leaf, []any{"publication_rt_leafpub", "publication_rt_s", "next", "1", nil, true, true}))
		exec("ALTER TABLE publication_rt_s.root DETACH PARTITION publication_rt_s.next")
		run(inquiry("'publication_rt_leafpub'", leaf))
		run(inquiry("'publication_rt_rootpub'", root))
	})
}
