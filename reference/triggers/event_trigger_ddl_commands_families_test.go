package reference_test

import (
	"context"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

type ddlCommandWitness struct {
	name                         string
	setup                        []string
	query, tag, class, kind      string
	schema                       any
	identity, catalog, predicate string
}

func ddlCommandFamilies(t *testing.T, cases []ddlCommandWitness) {
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if schema, ok := tc.schema.(string); ok && schema != "public" && schema != "pg_temp" {
				fixture.SchemaReferenceCleanup(t, schema)
			}
			if tc.class == "2615" {
				fixture.SchemaReferenceCleanup(t, tc.identity)
			}
			ddlCommandScenario(t, func(d ddlCommandCase) {
				if tc.class == "2328" || tc.class == "1417" || tc.kind == "foreign table" {
					defer func() {
						ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
						defer cancel()
						_, _ = d.c.ExecContext(ctx, "ROLLBACK")
						if _, err := d.c.ExecContext(ctx, "DROP FOREIGN DATA WRAPPER IF EXISTS et_dc_fdw CASCADE"); err != nil {
							t.Error(err)
						}
					}()
				}
				if tc.tag == "REINDEX" {
					defer func() {
						ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
						defer cancel()
						if _, err := d.c.ExecContext(ctx, "DROP TABLE IF EXISTS et_dc_base CASCADE"); err != nil {
							d.t.Error(err)
						}
					}()
				}
				d.exec(tc.setup...)
				d.install("'" + tc.tag + "'")
				for _, prepared := range []bool{false, true} {
					if tc.tag != "REINDEX" {
						d.exec("BEGIN")
					}
					s := fixture.Exec(tc.query, 0)
					s.Prepared = prepared
					d.run(s)
					d.rows(ddlCommandRows(tc.class, tc.tag, tc.kind, tc.schema, tc.identity))
					d.run(eventTriggerCount("SELECT count(*) AS n FROM et_dc_audit a JOIN "+tc.catalog+" c ON a.objid=c.oid WHERE a.classid="+tc.class+" AND "+tc.predicate, 2))
					if tc.tag == "REINDEX" {
						d.clear()
					} else {
						d.exec("ROLLBACK")
					}
					d.rows(nil)
				}
			})
		})
	}
}

func TestReferenceEventTriggerDDLCommandsFamiliesRelations(t *testing.T) {
	ddlCommandFamilies(t, []ddlCommandWitness{
		{"view", nil, "CREATE VIEW et_dc_view AS SELECT 1 AS i", "CREATE VIEW", "1259", "view", "public", "public.et_dc_view", "pg_class", "c.relname='et_dc_view'"},
		{"sequence", nil, "CREATE SEQUENCE et_dc_seq", "CREATE SEQUENCE", "1259", "sequence", "public", "public.et_dc_seq", "pg_class", "c.relname='et_dc_seq'"},
		{"alter_sequence", []string{"CREATE SEQUENCE et_dc_seq"}, "ALTER SEQUENCE et_dc_seq INCREMENT BY 2", "ALTER SEQUENCE", "1259", "sequence", "public", "public.et_dc_seq", "pg_class", "c.relname='et_dc_seq'"},
		{"index", []string{"CREATE TABLE et_dc_base(i integer)"}, "CREATE INDEX et_dc_idx ON et_dc_base(i)", "CREATE INDEX", "1259", "index", "public", "public.et_dc_idx", "pg_class", "c.relname='et_dc_idx'"},
		{"rename", []string{"CREATE TABLE et_dc_base(i integer)"}, "ALTER TABLE et_dc_base RENAME TO et_dc_renamed", "ALTER TABLE", "1259", "table", "public", "public.et_dc_renamed", "pg_class", "c.relname='et_dc_renamed'"},
		{"materialized", nil, "CREATE MATERIALIZED VIEW et_dc_mv AS SELECT 1 AS i WITH NO DATA", "CREATE MATERIALIZED VIEW", "1259", "materialized view", "public", "public.et_dc_mv", "pg_class", "c.relname='et_dc_mv'"},
		{"refresh", []string{"CREATE MATERIALIZED VIEW et_dc_mv AS SELECT 1 AS i WITH NO DATA"}, "REFRESH MATERIALIZED VIEW et_dc_mv", "REFRESH MATERIALIZED VIEW", "1259", "materialized view", "public", "public.et_dc_mv", "pg_class", "c.relname='et_dc_mv'"},
		{"reindex", []string{"CREATE TABLE et_dc_base(i integer)", "CREATE INDEX et_dc_idx ON et_dc_base(i)"}, "REINDEX INDEX et_dc_idx", "REINDEX", "1259", "index", "public", "public.et_dc_idx", "pg_class", "c.relname='et_dc_idx'"},
		{"reindex_table", []string{"CREATE TABLE et_dc_base(i integer)", "CREATE INDEX et_dc_idx ON et_dc_base(i)"}, "REINDEX TABLE et_dc_base", "REINDEX", "1259", "index", "public", "public.et_dc_idx", "pg_class", "c.relname='et_dc_idx'"},
		{"temporary", nil, "CREATE TEMP TABLE et_dc_temp(i integer)", "CREATE TABLE", "1259", "table", "pg_temp", "pg_temp.et_dc_temp", "pg_class", "c.relname='et_dc_temp'"},
		{"inherited", []string{"CREATE TABLE et_dc_parent(i integer)"}, "CREATE TABLE et_dc_child() INHERITS(et_dc_parent)", "CREATE TABLE", "1259", "table", "public", "public.et_dc_child", "pg_class", "c.relname='et_dc_child'"},
		{"partition_child", []string{"CREATE TABLE et_dc_parent(i integer PRIMARY KEY) PARTITION BY RANGE(i)"}, "CREATE TABLE et_dc_child PARTITION OF et_dc_parent FOR VALUES FROM(0) TO(10)", "CREATE TABLE", "1259", "table", "public", "public.et_dc_child", "pg_class", "c.relname='et_dc_child'"},
		{"partition_leaf_index", []string{"CREATE TABLE et_dc_parent(i integer) PARTITION BY RANGE(i)", "CREATE TABLE et_dc_child PARTITION OF et_dc_parent FOR VALUES FROM(0) TO(10)"}, "CREATE INDEX et_dc_partition_idx ON et_dc_child(i)", "CREATE INDEX", "1259", "index", "public", "public.et_dc_partition_idx", "pg_class", "c.relname='et_dc_partition_idx'"},
	})
	t.Run("query_creation", ddlCommandsQueryCreation)
	t.Run("same_tablespace", ddlCommandsSameTablespace)
	t.Run("partition_changes", ddlCommandsPartitionChanges)
	t.Run("alter_constraint_indexes", ddlCommandsAlterConstraintIndexes)
	t.Run("like_indexes", ddlCommandsLikeIndexes)
	t.Run("temporary_namespace_identity", ddlCommandsTemporaryNamespaceIdentity)
	t.Run("partition_index_boundary", ddlCommandsPartitionIndexBoundary)
}
func TestReferenceEventTriggerDDLCommandsFamiliesTypes(t *testing.T) {
	ddlCommandFamilies(t, []ddlCommandWitness{
		{"enum", nil, "CREATE TYPE et_dc_enum AS ENUM('a','b')", "CREATE TYPE", "1247", "type", "public", "public.et_dc_enum", "pg_type", "c.typname='et_dc_enum'"},
		{"enum_rename", []string{"CREATE TYPE et_dc_enum AS ENUM('a','b')"}, "ALTER TYPE et_dc_enum RENAME VALUE 'a' TO 'c'", "ALTER TYPE", "1247", "type", "public", "public.et_dc_enum", "pg_type", "c.typname='et_dc_enum'"},
		{"domain", nil, "CREATE DOMAIN et_dc_domain AS integer CHECK(VALUE>0)", "CREATE DOMAIN", "1247", "type", "public", "public.et_dc_domain", "pg_type", "c.typname='et_dc_domain'"},
		{"domain_alter", []string{"CREATE DOMAIN et_dc_domain AS integer"}, "ALTER DOMAIN et_dc_domain SET NOT NULL", "ALTER DOMAIN", "1247", "type", "public", "public.et_dc_domain", "pg_type", "c.typname='et_dc_domain'"},
		{"composite", nil, "CREATE TYPE et_dc_composite AS (i integer)", "CREATE TYPE", "1247", "type", "public", "public.et_dc_composite", "pg_type", "c.typname='et_dc_composite'"},
		{"range", nil, "CREATE TYPE et_dc_range AS RANGE(subtype=integer)", "CREATE TYPE", "1247", "type", "public", "public.et_dc_range", "pg_type", "c.typname='et_dc_range'"},
	})
	t.Run("noop_distinctions", ddlCommandsNoopDistinctions)
	t.Run("composite_cascade", ddlCommandsCompositeCascade)
}
func TestReferenceEventTriggerDDLCommandsFamiliesRoutines(t *testing.T) {
	ddlCommandFamilies(t, []ddlCommandWitness{
		{"function", nil, "CREATE FUNCTION et_dc_f(integer) RETURNS integer LANGUAGE SQL AS 'SELECT $1'", "CREATE FUNCTION", "1255", "function", "public", "public.et_dc_f(integer)", "pg_proc", "c.proname='et_dc_f'"},
		{"function_rename", []string{"CREATE FUNCTION et_dc_f(integer) RETURNS integer LANGUAGE SQL AS 'SELECT $1'"}, "ALTER FUNCTION et_dc_f(integer) RENAME TO et_dc_g", "ALTER FUNCTION", "1255", "function", "public", "public.et_dc_g(integer)", "pg_proc", "c.proname='et_dc_g'"},
		{"aggregate", nil, "CREATE AGGREGATE et_dc_agg(integer) (SFUNC=int4pl, STYPE=integer, INITCOND='0')", "CREATE AGGREGATE", "1255", "aggregate", "public", "public.et_dc_agg(integer)", "pg_proc", "c.proname='et_dc_agg'"},
		{"family", nil, "CREATE OPERATOR FAMILY et_dc_family USING btree", "CREATE OPERATOR FAMILY", "2753", "operator family", "public", "public.et_dc_family USING btree", "pg_opfamily", "c.opfname='et_dc_family'"},
		{"family_add", []string{"CREATE OPERATOR FAMILY et_dc_family USING btree"}, "ALTER OPERATOR FAMILY et_dc_family USING btree ADD FUNCTION 1 pg_catalog.btint4cmp(integer,integer)", "ALTER OPERATOR FAMILY", "2753", "operator family", "public", "public.et_dc_family USING btree", "pg_opfamily", "c.opfname='et_dc_family'"},
		{"family_drop", []string{"CREATE OPERATOR FAMILY et_dc_family USING btree", "ALTER OPERATOR FAMILY et_dc_family USING btree ADD FUNCTION 1 pg_catalog.btint4cmp(integer,integer)"}, "ALTER OPERATOR FAMILY et_dc_family USING btree DROP FUNCTION 1 (integer,integer)", "ALTER OPERATOR FAMILY", "2753", "operator family", "public", "public.et_dc_family USING btree", "pg_opfamily", "c.opfname='et_dc_family'"},
		{"class", []string{"CREATE OPERATOR FAMILY et_dc_family USING btree"}, "CREATE OPERATOR CLASS et_dc_class FOR TYPE integer USING btree FAMILY et_dc_family AS FUNCTION 1 pg_catalog.btint4cmp(integer,integer)", "CREATE OPERATOR CLASS", "2616", "operator class", "public", "public.et_dc_class USING btree", "pg_opclass", "c.opcname='et_dc_class'"},
		{"operator", nil, "CREATE OPERATOR public.=== (LEFTARG=integer,RIGHTARG=integer,FUNCTION=pg_catalog.int4eq)", "CREATE OPERATOR", "2617", "operator", "public", "public.===(integer,integer)", "pg_operator", "c.oprname='==='"},
		{"cast", []string{"CREATE TYPE et_dc_enum AS ENUM('1')"}, "CREATE CAST(et_dc_enum AS integer) WITH INOUT", "CREATE CAST", "2605", "cast", nil, "(public.et_dc_enum AS integer)", "pg_cast", "c.castsource='et_dc_enum'::regtype AND c.casttarget='integer'::regtype"},
		{"access_method", nil, "CREATE ACCESS METHOD et_dc_method TYPE INDEX HANDLER pg_catalog.bthandler", "CREATE ACCESS METHOD", "2601", "access method", nil, "et_dc_method", "pg_am", "c.amname='et_dc_method'"},
	})
	t.Run("implicit_family", ddlCommandsImplicitFamily)
}
func TestReferenceEventTriggerDDLCommandsFamiliesCatalog(t *testing.T) {
	ddlCommandFamilies(t, []ddlCommandWitness{
		{"statistics", []string{"CREATE TABLE et_dc_base(i integer,j integer)"}, "CREATE STATISTICS et_dc_stat ON i,j FROM et_dc_base", "CREATE STATISTICS", "3381", "statistics object", "public", "public.et_dc_stat", "pg_statistic_ext", "c.stxname='et_dc_stat'"},
		{"dictionary", nil, "CREATE TEXT SEARCH DICTIONARY et_dc_dict(TEMPLATE=pg_catalog.simple)", "CREATE TEXT SEARCH DICTIONARY", "3600", "text search dictionary", "public", "public.et_dc_dict", "pg_ts_dict", "c.dictname='et_dc_dict'"},
		{"configuration", nil, "CREATE TEXT SEARCH CONFIGURATION et_dc_config(COPY=pg_catalog.simple)", "CREATE TEXT SEARCH CONFIGURATION", "3602", "text search configuration", "public", "public.et_dc_config", "pg_ts_config", "c.cfgname='et_dc_config'"},
		{"collation", nil, "CREATE COLLATION et_dc_coll FROM pg_catalog.\"C\"", "CREATE COLLATION", "3456", "collation", "public", "public.et_dc_coll", "pg_collation", "c.collname='et_dc_coll'"},
		{"conversion", nil, "CREATE CONVERSION et_dc_conv FOR 'UTF8' TO 'LATIN1' FROM pg_catalog.utf8_to_iso8859_1", "CREATE CONVERSION", "2607", "conversion", "public", "public.et_dc_conv", "pg_conversion", "c.conname='et_dc_conv'"},
		{"template", nil, "CREATE TEXT SEARCH TEMPLATE et_dc_template(INIT=pg_catalog.dsimple_init,LEXIZE=pg_catalog.dsimple_lexize)", "CREATE TEXT SEARCH TEMPLATE", "3764", "text search template", "public", "public.et_dc_template", "pg_ts_template", "c.tmplname='et_dc_template'"},
		{"parser", nil, "CREATE TEXT SEARCH PARSER et_dc_parser(START=pg_catalog.prsd_start,GETTOKEN=pg_catalog.prsd_nexttoken,END=pg_catalog.prsd_end,LEXTYPES=pg_catalog.prsd_lextype,HEADLINE=pg_catalog.prsd_headline)", "CREATE TEXT SEARCH PARSER", "3601", "text search parser", "public", "public.et_dc_parser", "pg_ts_parser", "c.prsname='et_dc_parser'"},
	})
}
func TestReferenceEventTriggerDDLCommandsFamiliesOwned(t *testing.T) {
	ddlCommandFamilies(t, []ddlCommandWitness{
		{"trigger", []string{"CREATE TABLE et_dc_base(i integer)", "CREATE FUNCTION et_dc_trig_fn() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$"}, "CREATE TRIGGER et_dc_trig BEFORE INSERT ON et_dc_base FOR EACH ROW EXECUTE FUNCTION et_dc_trig_fn()", "CREATE TRIGGER", "2620", "trigger", nil, "et_dc_trig on public.et_dc_base", "pg_trigger", "c.tgname='et_dc_trig'"},
		{"same_owner", []string{"CREATE TABLE et_dc_base(i integer)"}, "ALTER TABLE et_dc_base OWNER TO CURRENT_USER", "ALTER TABLE", "1259", "table", "public", "public.et_dc_base", "pg_class", "c.relname='et_dc_base'"},
		{"same_schema", []string{"CREATE TABLE et_dc_base(i integer)"}, "ALTER TABLE et_dc_base SET SCHEMA public", "ALTER TABLE", "1259", "table", "public", "public.et_dc_base", "pg_class", "c.relname='et_dc_base'"},
		{"policy", []string{"CREATE TABLE et_dc_base(i integer)"}, "CREATE POLICY et_dc_policy ON et_dc_base USING(i>0)", "CREATE POLICY", "3256", "policy", nil, "et_dc_policy on public.et_dc_base", "pg_policy", "c.polname='et_dc_policy'"},
		{"rule", []string{"CREATE TABLE et_dc_base(i integer)"}, "CREATE RULE et_dc_rule AS ON INSERT TO et_dc_base DO NOTHING", "CREATE RULE", "2618", "rule", nil, "et_dc_rule on public.et_dc_base", "pg_rewrite", "c.rulename='et_dc_rule'"},
		{"comment", []string{"CREATE TABLE et_dc_base(i integer)"}, "COMMENT ON TABLE et_dc_base IS 'inspected'", "COMMENT", "1259", "table", "public", "public.et_dc_base", "pg_class", "c.relname='et_dc_base'"},
		{"schema", nil, "CREATE SCHEMA et_dc_schema", "CREATE SCHEMA", "2615", "schema", nil, "et_dc_schema", "pg_namespace", "c.nspname='et_dc_schema'"},
		{"raw_namespace", []string{"CREATE SCHEMA \"DC Odd\""}, "CREATE TABLE \"DC Odd\".\"Ta Ble\"(i integer)", "CREATE TABLE", "1259", "table", "DC Odd", "\"DC Odd\".\"Ta Ble\"", "pg_class", "c.relname='Ta Ble' AND c.relnamespace='\"DC Odd\"'::regnamespace"},
	})
	t.Run("unchanged_data", ddlCommandsNoopData)
	t.Run("subobjects", ddlCommandsSubobjects)
	t.Run("trigger_rename", ddlCommandsTriggerRename)
	t.Run("select_rule", ddlCommandsSelectRule)
}
func TestReferenceEventTriggerDDLCommandsFamiliesPrivileges(t *testing.T) {
	for _, tc := range []struct{ query, tag, kind string }{{"GRANT SELECT ON et_dc_base TO PUBLIC", "GRANT", "TABLE"}, {"REVOKE SELECT ON et_dc_base FROM PUBLIC", "REVOKE", "TABLE"}, {"ALTER DEFAULT PRIVILEGES GRANT SELECT ON TABLES TO PUBLIC", "ALTER DEFAULT PRIVILEGES", "TABLES"}} {
		t.Run(tc.tag, func(t *testing.T) {
			ddlCommandScenario(t, func(d ddlCommandCase) {
				d.exec("CREATE TABLE et_dc_base(i integer)")
				d.install("'" + tc.tag + "'")
				for _, prepared := range []bool{false, true} {
					d.exec("BEGIN")
					s := fixture.Exec(tc.query, 0)
					s.Prepared = prepared
					d.run(s)
					d.rows([][]any{{int64(1), nil, nil, tc.tag, tc.kind, nil, nil, false, true}, {int64(2), nil, nil, tc.tag, tc.kind, nil, nil, false, true}})
					d.run(eventTriggerCount("SELECT count(*) AS n FROM et_dc_audit WHERE objid IS NULL", 2))
					d.exec("ROLLBACK")
				}
			})
		})
	}
	t.Run("drop_owned", ddlCommandsDropOwned)
}
func TestReferenceEventTriggerDDLCommandsFamiliesForeign(t *testing.T) {
	ddlCommandFamilies(t, []ddlCommandWitness{
		{"wrapper", nil, "CREATE FOREIGN DATA WRAPPER et_dc_fdw", "CREATE FOREIGN DATA WRAPPER", "2328", "foreign-data wrapper", nil, "et_dc_fdw", "pg_foreign_data_wrapper", "c.fdwname='et_dc_fdw'"},
		{"server", []string{"CREATE FOREIGN DATA WRAPPER et_dc_fdw"}, "CREATE SERVER et_dc_server FOREIGN DATA WRAPPER et_dc_fdw", "CREATE SERVER", "1417", "server", nil, "et_dc_server", "pg_foreign_server", "c.srvname='et_dc_server'"},
		{"foreign_table", []string{"CREATE FOREIGN DATA WRAPPER et_dc_fdw", "CREATE SERVER et_dc_server FOREIGN DATA WRAPPER et_dc_fdw"}, "CREATE FOREIGN TABLE et_dc_foreign(i integer) SERVER et_dc_server", "CREATE FOREIGN TABLE", "1259", "foreign table", "public", "public.et_dc_foreign", "pg_class", "c.relname='et_dc_foreign'"},
	})
	t.Run("user_mapping", ddlCommandsUserMapping)
}
func TestReferenceEventTriggerDDLCommandsFamiliesExtensions(t *testing.T) {
	ddlCommandsExtensionOracle(t)
}
