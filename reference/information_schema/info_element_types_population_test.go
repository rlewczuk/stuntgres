package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoElementTypesPopulation(t *testing.T) {
	etScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, database string) {
		etExec(t, p, b, c,
			"CREATE DOMAIN et_int AS integer",
			"CREATE DOMAIN et_array AS integer[]",
			"CREATE DOMAIN et_outer AS et_array",
			`CREATE DOMAIN et_collated AS text[] COLLATE "C"`,
			`CREATE TABLE et_probe(plain integer,a integer[],matrix integer[2][3],bounded varchar(7)[] COLLATE "C",domarr et_array,arrelem et_int[],n numeric(8,2)[],nested et_array[],ts timestamp(2)[],iv interval day to second(2)[],txt text[])`,
		)
		want := [][]any{
			etRow(database, "public", "et_array", "DOMAIN", 1, "integer", "pg_catalog", "int4"),
			etCollated(etRow(database, "public", "et_collated", "DOMAIN", 1, "text", "pg_catalog", "text"), database, "C"),
			etRow(database, "public", "et_probe", "TABLE", 2, "integer", "pg_catalog", "int4"),
			etRow(database, "public", "et_probe", "TABLE", 3, "integer", "pg_catalog", "int4"),
			etCollated(etRow(database, "public", "et_probe", "TABLE", 4, "character varying", "pg_catalog", "varchar"), database, "C"),
			etRow(database, "public", "et_probe", "TABLE", 6, "USER-DEFINED", "public", "et_int"),
			etRow(database, "public", "et_probe", "TABLE", 7, "numeric", "pg_catalog", "numeric"),
			etRow(database, "public", "et_probe", "TABLE", 8, "USER-DEFINED", "public", "et_array"),
			etRow(database, "public", "et_probe", "TABLE", 9, "timestamp without time zone", "pg_catalog", "timestamp"),
			etRow(database, "public", "et_probe", "TABLE", 10, "interval", "pg_catalog", "interval"),
			etRow(database, "public", "et_probe", "TABLE", 11, "text", "pg_catalog", "text"),
		}
		where := "object_schema='public' AND object_name IN ('et_int','et_array','et_outer','et_collated','et_probe','et_missing')"
		check := func() { etCheck(t, p, b, c, etProjection(b.PG, where, want)) }
		check()
		// Declaration-derived rows do not depend on stored array values or dimensions.
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO et_probe(a,matrix,bounded,arrelem,n,nested) VALUES(NULL,NULL,NULL,NULL,NULL,NULL),('{}','{}','{}','{}','{}','{}'),('{1,2}','{{1,2},{3,4}}','{abc}','{1}','{1.25}','{}')", 3))
		check()
		etCheck(t, p, b, c, fixture.Select(`SELECT d.domain_name,e.collection_type_identifier,e.dtd_identifier FROM information_schema.domains d JOIN information_schema.element_types e ON e.object_catalog=d.domain_catalog AND e.object_schema=d.domain_schema AND e.object_name=d.domain_name AND e.object_type='DOMAIN' AND e.collection_type_identifier=d.dtd_identifier WHERE d.domain_schema='public' AND d.domain_name IN ('et_int','et_array','et_outer','et_collated') ORDER BY d.domain_name`, []string{"domain_name", "collection_type_identifier", "dtd_identifier"}, []string{"NAME", "NAME", "NAME"}, [][]any{{"et_array", "1", "a1"}, {"et_collated", "1", "a1"}}))

		etExec(t, p, b, c,
			"CREATE VIEW et_view AS SELECT a,bounded FROM et_probe",
			"CREATE TABLE et_parent(k integer,a integer[]) PARTITION BY RANGE(k)",
			"CREATE TABLE et_leaf PARTITION OF et_parent FOR VALUES FROM(0) TO(10)",
			"CREATE FOREIGN DATA WRAPPER et_fdw NO HANDLER NO VALIDATOR",
			"CREATE SERVER et_server FOREIGN DATA WRAPPER et_fdw",
			"CREATE FOREIGN TABLE et_foreign(s integer,a integer[]) SERVER et_server",
			`CREATE TYPE et_pair AS (a integer[],s integer,b text[] COLLATE "C")`,
			"CREATE MATERIALIZED VIEW et_mat AS SELECT a FROM et_probe WITH NO DATA",
			"CREATE INDEX et_index ON et_probe(plain)",
			"CREATE SEQUENCE et_sequence",
			"CREATE TABLE et_gaps(a integer[],gone text[],z bigint[])",
			"ALTER TABLE et_gaps DROP COLUMN gone",
			"ALTER TABLE et_gaps ADD COLUMN last text[]",
		)
		relations := [][]any{
			etRow(database, "public", "et_view", "TABLE", 1, "integer", "pg_catalog", "int4"),
			etCollated(etRow(database, "public", "et_view", "TABLE", 2, "character varying", "pg_catalog", "varchar"), database, "C"),
			etRow(database, "public", "et_parent", "TABLE", 2, "integer", "pg_catalog", "int4"),
			etRow(database, "public", "et_leaf", "TABLE", 2, "integer", "pg_catalog", "int4"),
			etRow(database, "public", "et_foreign", "TABLE", 2, "integer", "pg_catalog", "int4"),
			etRow(database, "public", "et_pair", "USER-DEFINED TYPE", 1, "integer", "pg_catalog", "int4"),
			etCollated(etRow(database, "public", "et_pair", "USER-DEFINED TYPE", 3, "text", "pg_catalog", "text"), database, "C"),
			etRow(database, "public", "et_gaps", "TABLE", 1, "integer", "pg_catalog", "int4"),
			etRow(database, "public", "et_gaps", "TABLE", 3, "bigint", "pg_catalog", "int8"),
			etRow(database, "public", "et_gaps", "TABLE", 4, "text", "pg_catalog", "text"),
		}
		etCheck(t, p, b, c, etProjection(b.PG, "object_schema='public' AND object_name IN ('et_view','et_parent','et_leaf','et_foreign','et_pair','et_mat','et_index','et_sequence','et_gaps','et_absent')", relations))
		// Scalar/system attributes, a dropped middle column and non-source kinds
		// cannot become fake element declarations.
		etCheck(t, p, b, c, etProjection(b.PG, "object_schema='public' AND ((object_name='et_gaps' AND collection_type_identifier='2') OR object_name IN ('et_mat','et_index','et_sequence','et_absent'))", nil))
	})
}
