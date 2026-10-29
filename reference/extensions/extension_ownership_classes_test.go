package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

type ownershipClassCase struct {
	name, target, lookup string
	class                int64
	setup                []string
	closure              int64
}

// Every represented ALTER EXTENSION member grammar family has a public witness.
// Each leaf owns a fresh scenario and rolls back global definitions on failure.
func TestReferenceExtensionOwnershipMemberClasses(t *testing.T) {
	t.Run("template", ownershipInstalledTemplateClass)
	t.Run("configuration_authority", ownershipConfigurationAuthority)
	t.Run("parser_authority", ownershipParserAuthority)
	t.Run("parser_seed_membership", ownershipParserSeedMembership)
	t.Run("snowball_bootstrap_membership", ownershipSnowballBootstrapMembership)
	t.Run("synonym_bootstrap_membership", ownershipSynonymBootstrapMembership)
	cases := []ownershipClassCase{
		{"function", "FUNCTION eo_old.item(integer)", `SELECT 'eo_old.item(integer)'::regprocedure::bigint`, 1255, []string{`CREATE FUNCTION eo_old.item(integer) RETURNS integer LANGUAGE SQL AS 'SELECT $1'`}, 1},
		{"procedure", "PROCEDURE eo_old.item(integer)", `SELECT 'eo_old.item(integer)'::regprocedure::bigint`, 1255, []string{`CREATE PROCEDURE eo_old.item(integer) LANGUAGE SQL AS 'SELECT $1'`}, 1},
		{"routine", "ROUTINE eo_old.item(integer)", `SELECT 'eo_old.item(integer)'::regprocedure::bigint`, 1255, []string{`CREATE FUNCTION eo_old.item(integer) RETURNS integer LANGUAGE SQL AS 'SELECT $1'`}, 1},
		{"aggregate", "AGGREGATE eo_old.item(integer)", `SELECT 'eo_old.item(integer)'::regprocedure::bigint`, 1255, []string{`CREATE AGGREGATE eo_old.item(integer) (SFUNC=pg_catalog.int4pl,STYPE=integer,INITCOND='0')`}, 1},
		{"enum", "TYPE eo_old.item", `SELECT 'eo_old.item'::regtype::bigint`, 1247, []string{`CREATE TYPE eo_old.item AS ENUM ('one','two')`}, 2},
		{"domain", "DOMAIN eo_old.item", `SELECT 'eo_old.item'::regtype::bigint`, 1247, []string{`CREATE DOMAIN eo_old.item AS integer CHECK(VALUE>0)`}, 2},
		{"range", "TYPE eo_old.item", `SELECT 'eo_old.item'::regtype::bigint`, 1247, []string{`CREATE TYPE eo_old.item AS RANGE (SUBTYPE=integer,MULTIRANGE_TYPE_NAME=eo_old.items)`}, 4},
		{"table", "TABLE eo_old.item", `SELECT 'eo_old.item'::regclass::bigint`, 1259, []string{`CREATE TABLE eo_old.item(id serial PRIMARY KEY,v text)`}, 3},
		{"foreign_table", "FOREIGN TABLE eo_old.item", `SELECT 'eo_old.item'::regclass::bigint`, 1259, []string{`CREATE FOREIGN DATA WRAPPER eo_fdw NO HANDLER NO VALIDATOR`, `CREATE SERVER eo_server FOREIGN DATA WRAPPER eo_fdw`, `CREATE FOREIGN TABLE eo_old.item(v integer) SERVER eo_server`}, 3},
		{"view", "VIEW eo_old.item", `SELECT 'eo_old.item'::regclass::bigint`, 1259, []string{`CREATE VIEW eo_old.item AS SELECT 1 AS v`}, 3},
		{"materialized_view", "MATERIALIZED VIEW eo_old.item", `SELECT 'eo_old.item'::regclass::bigint`, 1259, []string{`CREATE MATERIALIZED VIEW eo_old.item AS SELECT 1 AS v WITH NO DATA`}, 3},
		{"sequence", "SEQUENCE eo_old.item", `SELECT 'eo_old.item'::regclass::bigint`, 1259, []string{`CREATE SEQUENCE eo_old.item`}, 1},
		{"operator", "OPERATOR eo_old.#~#(integer,integer)", `SELECT oid::bigint FROM pg_operator WHERE oprnamespace='eo_old'::regnamespace AND oprname='#~#'`, 2617, []string{`CREATE OPERATOR eo_old.#~# (LEFTARG=integer,RIGHTARG=integer,FUNCTION=pg_catalog.int4pl)`}, 1},
		{"operator_family", "OPERATOR FAMILY eo_old.item USING btree", `SELECT oid::bigint FROM pg_opfamily WHERE opfnamespace='eo_old'::regnamespace AND opfname='item'`, 2753, []string{`CREATE OPERATOR FAMILY eo_old.item USING btree`}, 1},
		{"operator_class", "OPERATOR CLASS eo_old.item USING btree", `SELECT oid::bigint FROM pg_opclass WHERE opcnamespace='eo_old'::regnamespace AND opcname='item'`, 2616, []string{`CREATE OPERATOR CLASS eo_old.item FOR TYPE integer USING btree AS OPERATOR 1 pg_catalog.<(integer,integer),OPERATOR 3 pg_catalog.=(integer,integer),FUNCTION 1 pg_catalog.btint4cmp(integer,integer)`}, 1},
		{"dictionary", "TEXT SEARCH DICTIONARY eo_old.item", `SELECT oid::bigint FROM pg_ts_dict WHERE dictnamespace='eo_old'::regnamespace AND dictname='item'`, 3600, []string{`CREATE TEXT SEARCH DICTIONARY eo_old.item (TEMPLATE=pg_catalog.simple)`}, 1},
		{"dictionary_snowball", "TEXT SEARCH DICTIONARY eo_old.item", `SELECT oid::bigint FROM pg_ts_dict WHERE dictnamespace='eo_old'::regnamespace AND dictname='item'`, 3600, []string{`CREATE TEXT SEARCH DICTIONARY eo_old.item (TEMPLATE=pg_catalog.snowball,Language=english,StopWords=english)`}, 1},
		{"parser", "TEXT SEARCH PARSER eo_old.item", `SELECT oid::bigint FROM pg_ts_parser WHERE prsnamespace='eo_old'::regnamespace AND prsname='item'`, 3601, ownershipParserSetup(), 1},
		{"configuration", "TEXT SEARCH CONFIGURATION eo_old.item", `SELECT 'eo_old.item'::regconfig::bigint`, 3602, []string{`CREATE TEXT SEARCH CONFIGURATION eo_old.item (COPY=pg_catalog.simple)`}, 1},
		{"collation", "COLLATION eo_old.item", `SELECT oid::bigint FROM pg_collation WHERE collnamespace='eo_old'::regnamespace AND collname='item'`, 3456, []string{`CREATE COLLATION eo_old.item FROM pg_catalog."C"`}, 1},
		{"conversion", "CONVERSION eo_old.item", `SELECT oid::bigint FROM pg_conversion WHERE connamespace='eo_old'::regnamespace AND conname='item'`, 2607, []string{`CREATE CONVERSION eo_old.item FOR 'UTF8' TO 'LATIN1' FROM pg_catalog.utf8_to_iso8859_1`}, 1},
		{"schema", "SCHEMA eo_other", `SELECT 'eo_other'::regnamespace::bigint`, 2615, nil, 1},
		{"language", "PROCEDURAL LANGUAGE plpgsql", `SELECT oid::bigint FROM pg_language WHERE lanname='plpgsql'`, 2612, []string{`ALTER EXTENSION plpgsql DROP LANGUAGE plpgsql`}, 1},
		{"access_method", "ACCESS METHOD eo_method", `SELECT oid::bigint FROM pg_am WHERE amname='eo_method'`, 2601, []string{`CREATE ACCESS METHOD eo_method TYPE INDEX HANDLER pg_catalog.bthandler`}, 1},
		{"event_trigger", "EVENT TRIGGER eo_event", `SELECT oid::bigint FROM pg_event_trigger WHERE evtname='eo_event'`, 3466, []string{`CREATE FUNCTION eo_old.event_fn() RETURNS event_trigger LANGUAGE plpgsql AS 'BEGIN RETURN; END'`, `CREATE EVENT TRIGGER eo_event ON ddl_command_start EXECUTE FUNCTION eo_old.event_fn()`, `ALTER EVENT TRIGGER eo_event DISABLE`}, 1},
		{"foreign_wrapper", "FOREIGN DATA WRAPPER eo_fdw", `SELECT oid::bigint FROM pg_foreign_data_wrapper WHERE fdwname='eo_fdw'`, 2328, []string{`CREATE FOREIGN DATA WRAPPER eo_fdw NO HANDLER NO VALIDATOR`}, 1},
		{"server", "SERVER eo_server", `SELECT oid::bigint FROM pg_foreign_server WHERE srvname='eo_server'`, 1417, []string{`CREATE FOREIGN DATA WRAPPER eo_fdw NO HANDLER NO VALIDATOR`, `CREATE SERVER eo_server FOREIGN DATA WRAPPER eo_fdw`}, 1},
		{"cast", "CAST (eo_old.source AS eo_old.destination)", `SELECT oid::bigint FROM pg_cast WHERE castsource='eo_old.source'::regtype AND casttarget='eo_old.destination'::regtype`, 2605, []string{`CREATE TYPE eo_old.source AS ENUM ('one')`, `CREATE TYPE eo_old.destination AS ENUM ('one')`, `CREATE CAST (eo_old.source AS eo_old.destination) WITH INOUT`}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ownershipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := ownershipRun(t, p, b, c)
				ownershipSetup(run)
				baselineMembers := ownershipSnapshot(t, p, c, ownershipClassMemberSet)
				// Procedure definition is a committed setup boundary in the
				// native executor; membership and relocation remain transactional.
				if tc.name == "procedure" {
					for _, q := range tc.setup {
						run(fixture.Exec(q, 0))
					}
				}
				if tc.name == "language" {
					run(fixture.Exec(`CREATE PROCEDURE public.eo_witness(INOUT n integer) LANGUAGE plpgsql AS 'BEGIN n:=n+1; END'`, 0))
				}
				run(fixture.Exec("BEGIN", 0))
				if tc.name != "procedure" {
					for _, q := range tc.setup {
						run(fixture.Exec(q, 0))
					}
				}
				id := ownershipID(t, p, c, tc.lookup)
				// A root's ordinary dependencies must not be rewritten by membership.
				snapshot := ownershipSnapshot(t, p, c, `SELECT refclassid::bigint,refobjid::bigint,deptype FROM pg_depend WHERE classid=`+ownershipNumber(tc.class)+` AND objid=`+ownershipNumber(id)+` AND deptype<>'e' ORDER BY refclassid,refobjid,deptype`)
				configurationError := func(step fixture.Step) {
					run(fixture.Exec("SAVEPOINT eo_configuration_error", 0))
					run(step)
					run(fixture.Exec("ROLLBACK TO SAVEPOINT eo_configuration_error", 0))
					run(fixture.Exec("RELEASE SAVEPOINT eo_configuration_error", 0))
				}
				var configuration [][][]any
				if tc.class == 3602 {
					configuration = ownershipConfigurationSnapshot(t, p, c, id)
				}
				var parser [][][]any
				if tc.class == 3601 {
					parser = ownershipParserSnapshot(t, p, c, id)
				}
				run(fixture.Exec("ALTER EXTENSION unaccent ADD "+tc.target, 0))
				ownershipMemberCount(run, 6+tc.closure)
				ownershipClassWitness(run, tc.name, "eo_old")
				if step, ok := ownershipClassMemberDrop(tc, "eo_old"); ok {
					configurationError(step)
					if tc.class == 3601 {
						step.Query = strings.TrimSuffix(step.Query, " CASCADE")
						configurationError(step)
					}
				}
				run(ownershipCount(`SELECT count(*) FROM pg_depend WHERE classid=$1 AND objid=$2 AND refclassid='pg_extension'::regclass AND refobjid=(SELECT oid FROM pg_extension WHERE extname='unaccent') AND deptype='e'`, 1, tc.class, id))
				if tc.class == 3602 || tc.class == 3601 {
					noun := "text search configuration"
					if tc.class == 3601 {
						noun = "text search parser"
					}
					configurationError(ownershipError("ALTER EXTENSION unaccent ADD "+tc.target, "55000", noun+` eo_old.item is already a member of extension "unaccent"`))
					configurationError(ownershipError("ALTER EXTENSION plpgsql DROP "+tc.target, "55000", noun+` eo_old.item is not a member of extension "plpgsql"`))
				}
				if got := ownershipID(t, p, c, tc.lookup); got != id {
					t.Fatalf("membership replaced identity %d -> %d", id, got)
				}
				run(fixture.Exec("ALTER EXTENSION unaccent DROP "+tc.target, 0))
				// Compare the true reciprocal member inventory, not a schema's
				// children or a relaxed stock member count.
				ownershipUnchanged(t, p, c, ownershipClassMemberSet, baselineMembers)
				ownershipMemberCount(run, 6)
				run(ownershipCount(`SELECT count(*) FROM pg_depend WHERE classid=$1 AND objid=$2 AND refclassid='pg_extension'::regclass AND deptype='e'`, 0, tc.class, id))
				ownershipClassWitness(run, tc.name, "eo_old")
				if got := ownershipID(t, p, c, tc.lookup); got != id {
					t.Fatalf("detach deleted/replaced object %d -> %d", id, got)
				}
				ownershipUnchanged(t, p, c, `SELECT refclassid::bigint,refobjid::bigint,deptype FROM pg_depend WHERE classid=`+ownershipNumber(tc.class)+` AND objid=`+ownershipNumber(id)+` AND deptype<>'e' ORDER BY refclassid,refobjid,deptype`, snapshot)
				if tc.class == 3602 || tc.class == 3601 {
					noun := "text search configuration"
					if tc.class == 3601 {
						noun = "text search parser"
						ownershipClassMetadataEqual(t, parser, ownershipParserSnapshot(t, p, c, id))
						configurationError(ownershipParserRestrict("eo_old"))
					}
					configurationError(ownershipError("ALTER EXTENSION unaccent DROP "+tc.target, "55000", noun+` eo_old.item is not a member of extension "unaccent"`))
				}
				run(fixture.Exec("ALTER EXTENSION unaccent ADD "+tc.target, 0))
				if tc.class == 3601 {
					ownershipParserRelocationFailures(t, p, c, run, configurationError, id)
				}
				run(fixture.Exec("ALTER EXTENSION unaccent SET SCHEMA eo_new", 0))
				lookup := tc.lookup
				switch tc.class {
				case 2615, 2612, 2601, 3466, 2328, 1417, 2605:
					// Global members retain identity; CAST endpoint types are not
					// implicitly members and therefore stay in their old schema.
				default:
					lookup = strings.ReplaceAll(lookup, "eo_old", "eo_new")
				}
				if got := ownershipID(t, p, c, lookup); got != id {
					t.Fatalf("class relocation replaced identity %d -> %d", id, got)
				}
				ownershipMemberCount(run, 6+tc.closure)
				witnessSchema := "eo_new"
				if tc.class == 2605 {
					witnessSchema = "eo_old"
				}
				ownershipClassWitness(run, tc.name, witnessSchema)
				if tc.class == 3602 {
					ownershipClassMetadataEqual(t, configuration, ownershipConfigurationSnapshot(t, p, c, id))
				}
				if tc.class == 3601 {
					ownershipClassMetadataEqual(t, parser, ownershipParserSnapshot(t, p, c, id))
				}
				run(fixture.Exec("ROLLBACK", 0))
				ownershipMemberCount(run, 6)
				ownershipUnchanged(t, p, c, ownershipClassMemberSet, baselineMembers)
				run(ownershipCount(`SELECT count(*) FROM pg_extension WHERE extname='unaccent' AND extnamespace='eo_old'::regnamespace`, 1))
				ownershipClassControls(t, p, b, c, tc)
			})
		})
	}
}
