package reference_test

import (
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePublicationColumnsGenerated(t *testing.T) {
	publicationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s ...fixture.Step) { publicationRun(t, p, b, b.DB, s...) }
		run(fixture.Exec("CREATE TABLE publication_rt_t(id integer PRIMARY KEY,a integer,b integer,g integer GENERATED ALWAYS AS (a+1) STORED,v integer GENERATED ALWAYS AS (a+2) VIRTUAL)", 0))
		run(fixture.Exec("CREATE PUBLICATION publication_rt_p FOR TABLE publication_rt_t(b,id)", 0), publicationDefinitions("publication_rt_p", []any{"publication_rt_t", "{id,b}", nil}))
		run(fixture.Exec("ALTER TABLE publication_rt_t ADD COLUMN later integer", 0), publicationDefinitions("publication_rt_p", []any{"publication_rt_t", "{id,b}", nil}))
		run(fixture.Exec("ALTER TABLE publication_rt_t RENAME COLUMN b TO renamed", 0), publicationDefinitions("publication_rt_p", []any{"publication_rt_t", "{id,renamed}", nil}))
		run(fixture.Exec("CREATE PUBLICATION publication_rt_default FOR TABLE publication_rt_t", 0), publicationDefinitions("publication_rt_default", []any{"publication_rt_t", "{id,a,renamed,later}", nil}))
		run(fixture.Exec("ALTER PUBLICATION publication_rt_default SET (publish_generated_columns=stored)", 0), publicationDefinitions("publication_rt_default", []any{"publication_rt_t", "{id,a,renamed,g,later}", nil}))
		run(fixture.Exec("ALTER PUBLICATION publication_rt_p SET TABLE publication_rt_t(g,id)", 0), publicationDefinitions("publication_rt_p", []any{"publication_rt_t", "{id,g}", nil}))
		for _, tc := range []struct{ list, state, message string }{
			{"missing", "42703", `column "missing" of relation "publication_rt_t" does not exist`},
			{"ctid", "42P10", `cannot use system column "ctid" in publication column list`},
			{"v", "42P10", `cannot use virtual generated column "v" in publication column list`},
			{"id,id", "42710", `duplicate column "id" in publication column list`},
		} {
			run(publicationDiagnostic("ALTER PUBLICATION publication_rt_p SET TABLE publication_rt_t("+tc.list+")", tc.state, tc.message, "", ""), publicationDefinitions("publication_rt_p", []any{"publication_rt_t", "{id,g}", nil}))
		}
		run(fixture.Exec("CREATE SCHEMA publication_rt_s", 0))
		run(publicationDiagnostic("ALTER PUBLICATION publication_rt_p ADD TABLES IN SCHEMA publication_rt_s", "22023", `cannot add schema to publication "publication_rt_p"`, "Schemas cannot be added if any tables that specify a column list are already part of the publication.", ""))
		run(fixture.Exec("CREATE PUBLICATION publication_rt_schema FOR TABLES IN SCHEMA publication_rt_s", 0))
		run(publicationDiagnostic("ALTER PUBLICATION publication_rt_schema ADD TABLE publication_rt_t(id)", "22023", `cannot use column list for relation "public.publication_rt_t" in publication "publication_rt_schema"`, "Column lists cannot be specified in publications containing FOR TABLES IN SCHEMA elements.", ""))
		run(publicationDiagnostic("ALTER PUBLICATION publication_rt_p DROP TABLE publication_rt_t(id)", "42601", "column list must not be specified in ALTER PUBLICATION ... DROP", "", ""))
		run(publicationDiagnostic("ALTER PUBLICATION publication_rt_p SET TABLE publication_rt_t(id),publication_rt_t(id)", "42710", `conflicting or redundant column lists for table "publication_rt_t"`, "", ""))
		run(fixture.Exec("CREATE TABLE publication_rt_root(id integer) PARTITION BY RANGE(id)", 0))
		run(publicationDiagnostic("CREATE PUBLICATION publication_rt_bad FOR TABLE publication_rt_root(id)", "22023", `cannot use column list for relation "public.publication_rt_root" in publication "publication_rt_bad"`, "Column lists cannot be specified for partitioned tables when publish_via_partition_root is false.", ""))
		run(fixture.Exec("CREATE PUBLICATION publication_rt_rootpub FOR TABLE publication_rt_root(id) WITH(publish_via_partition_root=true)", 0))
		run(publicationDiagnostic("ALTER PUBLICATION publication_rt_rootpub SET (publish_via_partition_root=false)", "22023", `cannot set parameter "publish_via_partition_root" to false for publication "publication_rt_rootpub"`, `The publication contains a column list for partitioned table "publication_rt_root", which is not allowed when "publish_via_partition_root" is false.`, ""))
	})
}

func TestReferencePublicationFilters(t *testing.T) {
	publicationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s ...fixture.Step) { publicationRun(t, p, b, b.DB, s...) }
		run(fixture.Exec("CREATE TABLE publication_rt_t(id integer PRIMARY KEY,v integer,virtual integer GENERATED ALWAYS AS(v+1) VIRTUAL)", 0))
		// Retained definitions use the ordinary SQL expression surface, not a
		// bespoke arithmetic predicate language. Local SQL is never filtered.
		for i, expression := range []string{"true", "false", "NULL::boolean", "id > 0", "COALESCE(v,0) > 0", "CASE WHEN v IS NULL THEN false ELSE v > 0 END", "NULLIF(v,0) IS NOT NULL", "id = ANY (ARRAY[1,2])", "(id,v) IS NOT NULL", "abs(id)>0", "v IS DISTINCT FROM 1", "(v>0) IS TRUE", "XMLPARSE(DOCUMENT '<a/>') IS NOT NULL"} {
			t.Run(expression, func(t *testing.T) {
				run := func(s ...fixture.Step) { publicationRun(t, p, b, b.DB, s...) }
				if i == 0 {
					run(fixture.Exec("CREATE PUBLICATION publication_rt_p FOR TABLE publication_rt_t WHERE ("+expression+") WITH(publish='insert')", 0))
				} else {
					run(fixture.Exec("ALTER PUBLICATION publication_rt_p SET TABLE publication_rt_t WHERE ("+expression+")", 0))
				}
				run(fixture.Select("SELECT rowfilter IS NOT NULL AS retained FROM pg_catalog.pg_publication_tables WHERE pubname='publication_rt_p'", []string{"retained"}, []string{"BOOL"}, [][]any{{true}}))
			})
		}
		run(fixture.Exec("ALTER PUBLICATION publication_rt_p SET TABLE publication_rt_t WHERE (v > 0)", 0), publicationDefinitions("publication_rt_p", []any{"publication_rt_t", "{id,v}", "(v > 0)"}))
		run(fixture.Exec("INSERT INTO publication_rt_t VALUES(1,1),(2,0),(3,NULL)", 3), fixture.Exec("UPDATE publication_rt_t SET v=-1", 3), fixture.Select("SELECT id,v FROM publication_rt_t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(-1)}, {int64(2), int64(-1)}, {int64(3), int64(-1)}}))
		run(fixture.Exec("CREATE FUNCTION public.publication_rt_function(integer) RETURNS boolean LANGUAGE SQL IMMUTABLE AS 'SELECT $1>0'", 0),
			fixture.Exec("CREATE TYPE publication_rt_enum AS ENUM('ok')", 0),
			fixture.Exec("CREATE COLLATION publication_rt_collation FROM \"C\"", 0),
			fixture.Exec("CREATE OPERATOR public.=== (LEFTARG=integer,RIGHTARG=integer,FUNCTION=int4eq)", 0))
		cases := []struct{ expression, token, state, message, detail string }{
			{"ctid IS NOT NULL", "ctid", "0A000", "invalid publication WHERE expression", "System columns are not allowed."},
			{"random()>0", "random", "0A000", "invalid publication WHERE expression", "User-defined or built-in mutable functions are not allowed."},
			{"(SELECT true)", "(SELECT", "0A000", "invalid publication WHERE expression", "Only columns, constants, built-in operators, built-in data types, built-in collations, and immutable built-in functions are allowed."},
			{"sum(id)>0", "sum", "42803", "aggregate functions are not allowed in WHERE", ""},
			{"row_number() OVER()>0", "row_number", "42P20", "window functions are not allowed in WHERE", ""},
			{"public.publication_rt_function(id)", "public.publication_rt_function", "0A000", "invalid publication WHERE expression", "User-defined or built-in mutable functions are not allowed."},
			{"id === 1", "id", "0A000", "invalid publication WHERE expression", "User-defined operators are not allowed."},
			{"'ok'::publication_rt_enum IS NOT NULL", "'ok'", "0A000", "invalid publication WHERE expression", "User-defined types are not allowed."},
			{"(id::text COLLATE publication_rt_collation)='1'", "id::text", "0A000", "invalid publication WHERE expression", "User-defined collations are not allowed."},
		}
		for _, tc := range cases {
			q := "ALTER PUBLICATION publication_rt_p SET TABLE publication_rt_t WHERE (" + tc.expression + ")"
			d := fixture.ControlsReferenceDiagnostic(tc.state, tc.message)
			d.Detail, d.Position = tc.detail, strconv.Itoa(strings.Index(q, tc.token)+1)
			run(fixture.ErrorDiagnostic(q, d), publicationDefinitions("publication_rt_p", []any{"publication_rt_t", "{id,v}", "(v > 0)"}))
		}
		q := "ALTER PUBLICATION publication_rt_p SET TABLE publication_rt_t WHERE (1)"
		d := fixture.ControlsReferenceDiagnostic("42804", "argument of PUBLICATION WHERE must be type boolean, not type integer")
		d.Position = strconv.Itoa(strings.LastIndex(q, "1") + 1)
		run(fixture.ErrorDiagnostic(q, d))
		run(publicationDiagnostic("ALTER PUBLICATION publication_rt_p DROP TABLE publication_rt_t WHERE (true)", "42601", "cannot use a WHERE clause when removing a table from a publication", "", ""))
		run(publicationDiagnostic("ALTER PUBLICATION publication_rt_p SET TABLE publication_rt_t WHERE (true),publication_rt_t WHERE (false)", "42710", `conflicting or redundant WHERE clauses for table "publication_rt_t"`, "", ""))
		run(fixture.Exec("CREATE TABLE publication_rt_root(id integer) PARTITION BY RANGE(id)", 0))
		run(publicationDiagnostic("CREATE PUBLICATION publication_rt_bad FOR TABLE publication_rt_root WHERE(id>0)", "22023", `cannot use publication WHERE clause for relation "publication_rt_root"`, "WHERE clause cannot be used for a partitioned table when publish_via_partition_root is false.", ""))
		run(fixture.Exec("CREATE PUBLICATION publication_rt_rootpub FOR TABLE publication_rt_root WHERE(id>0) WITH(publish_via_partition_root=true)", 0))
		run(publicationDiagnostic("ALTER PUBLICATION publication_rt_rootpub SET(publish_via_partition_root=false)", "22023", `cannot set parameter "publish_via_partition_root" to false for publication "publication_rt_rootpub"`, `The publication contains a WHERE clause for partitioned table "publication_rt_root", which is not allowed when "publish_via_partition_root" is false.`, ""))
		run(fixture.Exec("ALTER PUBLICATION publication_rt_p SET TABLE publication_rt_t WHERE(virtual>0)", 0))
		run(publicationDiagnostic("ALTER TABLE publication_rt_t ALTER COLUMN virtual SET EXPRESSION AS(v+2)", "0A000", "ALTER TABLE / SET EXPRESSION is not supported for virtual generated columns in tables that are part of a publication", `Column "virtual" of relation "publication_rt_t" is a virtual generated column.`, ""))
		run(fixture.Exec("CREATE SCHEMA publication_rt_s", 0), fixture.Exec("ALTER TABLE publication_rt_t SET SCHEMA publication_rt_s", 0), fixture.Exec("ALTER PUBLICATION publication_rt_p ADD TABLES IN SCHEMA publication_rt_s", 0), publicationDefinitions("publication_rt_p", []any{"publication_rt_t", "{id,v}", nil}))
		run(fixture.Exec("ALTER PUBLICATION publication_rt_p DROP TABLES IN SCHEMA publication_rt_s", 0), fixture.Select("SELECT rowfilter IS NOT NULL AS retained FROM pg_catalog.pg_publication_tables WHERE pubname='publication_rt_p'", []string{"retained"}, []string{"BOOL"}, [][]any{{true}}))
	})
}
