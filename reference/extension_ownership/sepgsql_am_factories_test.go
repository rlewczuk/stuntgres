package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Ordinary pinned PostgreSQL is the inactive oracle. The separate active phase
// specifies simulated FHET outcomes from REL_18_6 amapi.c/amutils.c, not an
// active SELinux oracle or a promise of backend relcache invocation counts.
func TestReferenceSepgsqlSimulationAccessMethodFactories(t *testing.T) {
	t.Run("bloom_factory_lifecycle_and_order", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"sepgsql_am_owner"})
		cleanMethods := fixture.AccessMethodReferenceCleanup(t, []string{"sepgsql_bloom_alias", "sepgsql_am_metadata"})
		setup := func(run sepgsqlRun) {
			run(fixture.Exec("CREATE EXTENSION bloom", 0))
			run(fixture.Exec("CREATE ROLE sepgsql_am_owner", 0))
			run(fixture.Exec("GRANT USAGE, CREATE ON SCHEMA public TO sepgsql_am_owner", 0))
			run(fixture.Exec("CREATE TABLE sepgsql_am_rows(i integer)", 0))
			run(fixture.Exec("ALTER TABLE sepgsql_am_rows OWNER TO sepgsql_am_owner", 0))
			run(fixture.Exec("INSERT INTO sepgsql_am_rows VALUES (1),(2)", 2))
			run(fixture.Exec("REVOKE ALL ON FUNCTION blhandler(internal) FROM PUBLIC", 0))
			run(fixture.Exec("CREATE ACCESS METHOD sepgsql_bloom_alias TYPE INDEX HANDLER blhandler", 0))
			run(fixture.Exec("CREATE OPERATOR CLASS sepgsql_bloom_int DEFAULT FOR TYPE integer USING sepgsql_bloom_alias AS OPERATOR 1 pg_catalog.=, FUNCTION 1 pg_catalog.hashint4(integer)", 0))
		}
		ordinary := func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
			cleanMethods()
			setup(run)
			run(fixture.ImplicitError("CREATE UNIQUE INDEX sepgsql_am_unique ON sepgsql_am_rows USING bloom(i)", fixture.Diagnostic{Code: "0A000", Message: `access method "bloom" does not support unique indexes`}))
			run(fixture.Exec("CREATE OPERATOR CLASS sepgsql_am_extra FOR TYPE integer USING bloom AS OPERATOR 1 pg_catalog.=, FUNCTION 1 pg_catalog.hashint4(integer)", 0))
			run(fixture.ImplicitError("ALTER OPERATOR FAMILY sepgsql_am_missing USING bloom DROP FUNCTION 1(integer,integer)", fixture.Diagnostic{Code: "42704", Message: `operator family "sepgsql_am_missing" does not exist for access method "bloom"`}))
			run(fixture.Exec("SET ROLE sepgsql_am_owner", 0))
			run(sepgsqlBool("SELECT has_function_privilege(current_user,'blhandler(internal)','EXECUTE') AS allowed", "allowed", false))
			run(fixture.Exec("CREATE INDEX sepgsql_am_idx ON sepgsql_am_rows USING bloom(i)", 0))
			run(fixture.Exec("CREATE INDEX sepgsql_am_alias_idx ON sepgsql_am_rows USING sepgsql_bloom_alias(i)", 0))
			for _, prepared := range []bool{false, true} {
				step := sepgsqlBool("SELECT pg_indexam_has_property((SELECT oid FROM pg_am WHERE amname='bloom'),'can_multi_col') AS supported", "supported", true)
				step.Prepared = prepared
				run(step)
				step = fixture.Select("SELECT pg_index_column_has_property('sepgsql_am_idx'::regclass,99,'unknown') AS supported", []string{"supported"}, []string{"BOOL"}, [][]any{{nil}})
				step.Prepared = prepared
				run(step)
			}
			run(fixture.Exec("BEGIN", 0))
			run(sepgsqlCount("SELECT count(*) FROM sepgsql_am_rows WHERE i=1", 1))
			run(fixture.Prepared(sepgsqlCount("SELECT count(*) FROM sepgsql_am_rows WHERE i=1", 1)))
			run(fixture.Exec("COMMIT", 0))
			run(fixture.Exec("REINDEX INDEX sepgsql_am_idx", 0))
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("ALTER FUNCTION blhandler(internal) RENAME TO sepgsql_bloom_handler", 0))
			run(sepgsqlBool("SELECT pg_indexam_has_property((SELECT oid FROM pg_am WHERE amname='sepgsql_bloom_alias'),'can_multi_col') AS supported", "supported", true))
			run(fixture.Exec("DROP INDEX sepgsql_am_alias_idx", 0))
			run(fixture.Exec("DROP OPERATOR CLASS sepgsql_bloom_int USING sepgsql_bloom_alias", 0))
			run(fixture.Exec("DROP OPERATOR FAMILY sepgsql_bloom_int USING sepgsql_bloom_alias", 0))
			run(fixture.Exec("DROP ACCESS METHOD sepgsql_bloom_alias", 0))
			run(fixture.Select("SELECT count(*) FROM pg_am WHERE amname='sepgsql_bloom_alias'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		}
		sepgsqlCase(t, ordinary, func(config *stuntgres.SepgsqlSimulationConfig) {
			for _, class := range []string{"db_database", "db_schema", "db_table", "db_column"} {
				config.Policy.Rules = append(config.Policy.Rules, stuntgres.SepgsqlRule{Source: sepgsqlR, Target: "*", Class: class, Allow: []string{"*"}})
			}
			config.Policy.Rules = append(config.Policy.Rules, stuntgres.SepgsqlRule{Source: sepgsqlR, Target: sepgsqlS, Class: "db_procedure", Deny: []string{"execute"}})
			config.Policy.TrustedTransitions = append(config.Policy.TrustedTransitions, stuntgres.SepgsqlTrustedTransition{Source: sepgsqlR, Procedure: sepgsqlU, Result: sepgsqlT})
		}, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
			setup(run)
			run(fixture.Exec("SECURITY LABEL ON FUNCTION blhandler(internal) IS '"+sepgsqlS+"'", 0))
			run(sepgsqlSet(sepgsqlR))
			run(fixture.Exec("SET ROLE sepgsql_am_owner", 0))
			run(sepgsqlBool("SELECT has_function_privilege(current_user,'blhandler(internal)','EXECUTE') AS allowed", "allowed", false))
			// The factory returns its real Bloom authority despite both SQL ACL
			// and MAC execute denial. T cannot create an index: return must leave
			// the trusted body before the actual index creation hooks run.
			run(fixture.Exec("CREATE INDEX sepgsql_am_idx ON sepgsql_am_rows USING bloom(i)", 0))
			run(fixture.Exec("CREATE INDEX sepgsql_am_alias_idx ON sepgsql_am_rows USING sepgsql_bloom_alias(i)", 0))
			run(sepgsqlCount("SELECT count(*) FROM sepgsql_am_rows WHERE i=1", 1))
			run(fixture.ImplicitError("CREATE UNIQUE INDEX sepgsql_am_unique ON sepgsql_am_rows USING bloom(i)", fixture.Diagnostic{Code: "0A000", Message: `access method "bloom" does not support unique indexes`}))
			run(fixture.Exec("REINDEX INDEX sepgsql_am_idx", 0))
			run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
			run(fixture.Exec("RESET ROLE", 0))
			run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
			run(fixture.Exec("CREATE SCHEMA sepgsql_am_helpers", 0))
			run(fixture.Exec("ALTER FUNCTION blhandler(internal) RENAME TO sepgsql_bloom_handler", 0))
			run(fixture.Exec("ALTER FUNCTION sepgsql_bloom_handler(internal) SET SCHEMA sepgsql_am_helpers", 0))
			run(sepgsqlSet(sepgsqlR))
			run(fixture.Exec("CREATE OPERATOR CLASS sepgsql_am_extra FOR TYPE integer USING bloom AS OPERATOR 1 pg_catalog.=, FUNCTION 1 pg_catalog.hashint4(integer)", 0))
			run(fixture.Exec("CREATE INDEX sepgsql_am_moved_idx ON sepgsql_am_rows USING bloom(i)", 0))
			run(sepgsqlBool("SELECT pg_index_has_property('sepgsql_am_alias_idx'::regclass,'bitmap_scan') AS supported", "supported", true))
			run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
			run(fixture.Exec("SECURITY LABEL ON FUNCTION sepgsql_am_helpers.sepgsql_bloom_handler(internal) IS '"+sepgsqlU+"'", 0))
			run(sepgsqlSet(sepgsqlR))
			// Registration is metadata, not an invocation of the denied handler.
			run(fixture.Exec("CREATE ACCESS METHOD sepgsql_am_metadata TYPE INDEX HANDLER sepgsql_am_helpers.sepgsql_bloom_handler", 0))
			run(sepgsqlDenied("SELECT pg_indexam_has_property((SELECT oid FROM pg_am WHERE amname='sepgsql_am_metadata'),'can_order')"))
			run(fixture.Exec("DROP ACCESS METHOD sepgsql_am_metadata", 0))
			run(fixture.Exec("CREATE OPERATOR FAMILY sepgsql_am_metadata_family USING bloom", 0))
			run(fixture.Exec("DROP OPERATOR FAMILY sepgsql_am_metadata_family USING bloom", 0))
			run(sepgsqlCount("SELECT count(*) FROM pg_am WHERE amname='bloom'", 1))
			// A real factory is earlier than unsupported UNIQUE/key diagnostics.
			run(sepgsqlDenied("CREATE UNIQUE INDEX sepgsql_am_denied ON sepgsql_am_rows USING bloom(missing)"))
			// Captured Bloom roots are eligible only in implicit statements
			// without a catalog overlay (query_admission.go). This explicit
			// transaction deterministically scans without invoking the handler;
			// a WHERE predicate alone does not promise a native method factory.
			run(fixture.Exec("BEGIN", 0))
			run(sepgsqlCount("SELECT count(*) FROM sepgsql_am_rows WHERE i=1", 1))
			run(fixture.Prepared(sepgsqlCount("SELECT count(*) FROM sepgsql_am_rows WHERE i=1", 1)))
			run(fixture.Exec("COMMIT", 0))
			// In contrast, the amutils property inquiry actually calls the
			// factory even for an unknown property, before returning SQL NULL.
			for _, query := range []string{
				"SELECT pg_indexam_has_property((SELECT oid FROM pg_am WHERE amname='bloom'),'unknown')",
				"SELECT pg_index_has_property('sepgsql_am_idx'::regclass,'unknown')",
				"SELECT pg_index_column_has_property('sepgsql_am_idx'::regclass,1,'unknown')",
				"REINDEX INDEX sepgsql_am_idx",
				"INSERT INTO sepgsql_am_rows VALUES(3)",
				"VACUUM sepgsql_am_rows",
				"CREATE OPERATOR CLASS sepgsql_am_denied_class FOR TYPE sepgsql_missing_type USING bloom AS OPERATOR 1 pg_catalog.=, FUNCTION 1 pg_catalog.hashint4(integer)",
				"ALTER OPERATOR FAMILY sepgsql_am_missing USING bloom DROP FUNCTION 1(integer,integer)",
			} {
				run(sepgsqlDenied(query))
			}
			for _, prepared := range []bool{false, true} {
				for _, query := range []string{
					"SELECT pg_indexam_has_property(0,'unknown') AS supported",
					"SELECT pg_index_column_has_property('sepgsql_am_idx'::regclass,99,'unknown') AS supported",
				} {
					step := fixture.Select(query, []string{"supported"}, []string{"BOOL"}, [][]any{{nil}})
					step.Prepared = prepared
					run(step)
				}
			}
			run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
			run(fixture.Exec("DROP INDEX sepgsql_am_alias_idx", 0))
			run(fixture.Exec("DROP OPERATOR CLASS sepgsql_bloom_int USING sepgsql_bloom_alias", 0))
			run(fixture.Exec("DROP OPERATOR FAMILY sepgsql_bloom_int USING sepgsql_bloom_alias", 0))
			run(fixture.Exec("DROP ACCESS METHOD sepgsql_bloom_alias", 0))
			run(sepgsqlCount("SELECT count(*) FROM pg_am WHERE amname='sepgsql_bloom_alias'", 0))
			run(fixture.Exec("CREATE ACCESS METHOD sepgsql_bloom_alias TYPE INDEX HANDLER sepgsql_am_helpers.sepgsql_bloom_handler", 0))
			run(sepgsqlDenied("SELECT pg_indexam_has_property((SELECT oid FROM pg_am WHERE amname='sepgsql_bloom_alias'),'can_multi_col')"))
			run(fixture.Exec("DROP ACCESS METHOD sepgsql_bloom_alias", 0))
			run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
			run(sepgsqlCount("SELECT count(*) FROM sepgsql_am_rows", 2))
			run(sepgsqlCount("SELECT count(*) FROM pg_proc WHERE proname IN ('ambuild','aminsert','ambulkdelete','amvacuumcleanup')", 0))
		})
	})
}

func TestReferenceSepgsqlSimulationOriginalAccessMethodFactories(t *testing.T) {
	setup := func(run sepgsqlRun) {
		run(fixture.Exec("CREATE SCHEMA sepgsql_original_handlers", 0))
		run(fixture.Exec("ALTER FUNCTION pg_catalog.bthandler(internal) RENAME TO sepgsql_original_btree", 0))
		run(fixture.Exec("ALTER FUNCTION pg_catalog.sepgsql_original_btree(internal) SET SCHEMA sepgsql_original_handlers", 0))
		run(fixture.Exec("ALTER FUNCTION pg_catalog.heap_tableam_handler(internal) RENAME TO sepgsql_original_heap", 0))
		run(fixture.Exec("ALTER FUNCTION pg_catalog.sepgsql_original_heap(internal) SET SCHEMA sepgsql_original_handlers", 0))
	}
	work := func(run sepgsqlRun) {
		run(fixture.Exec("CREATE TABLE sepgsql_original_rows(i integer)", 0))
		run(fixture.Exec("INSERT INTO sepgsql_original_rows VALUES (1),(2)", 2))
		run(fixture.Exec("CREATE INDEX sepgsql_original_idx ON sepgsql_original_rows USING btree(i)", 0))
		run(sepgsqlCount("SELECT count(*) FROM sepgsql_original_rows", 2))
		for _, prepared := range []bool{false, true} {
			step := sepgsqlBool("SELECT pg_indexam_has_property(403,'can_order') AS supported", "supported", true)
			step.Prepared = prepared
			run(step)
		}
	}
	sepgsqlCase(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		run(fixture.Exec("BEGIN", 0))
		defer run(fixture.Exec("ROLLBACK", 0))
		setup(run)
		work(run)
	}, func(config *stuntgres.SepgsqlSimulationConfig) {
		config.Policy.Rules = append(config.Policy.Rules,
			stuntgres.SepgsqlRule{Source: sepgsqlR, Target: "*", Class: "*", Allow: []string{"*"}},
			stuntgres.SepgsqlRule{Source: sepgsqlR, Target: sepgsqlU, Class: "db_procedure", Deny: []string{"execute", "entrypoint"}})
		config.Policy.TrustedTransitions = append(config.Policy.TrustedTransitions, stuntgres.SepgsqlTrustedTransition{Source: sepgsqlR, Procedure: sepgsqlU, Result: sepgsqlT})
	}, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		run(fixture.Exec("BEGIN", 0))
		defer run(fixture.Exec("ROLLBACK", 0))
		setup(run)
		run(fixture.Exec("SECURITY LABEL ON FUNCTION sepgsql_original_handlers.sepgsql_original_btree(internal) IS '"+sepgsqlU+"'", 0))
		run(fixture.Exec("SECURITY LABEL ON FUNCTION sepgsql_original_handlers.sepgsql_original_heap(internal) IS '"+sepgsqlU+"'", 0))
		run(sepgsqlSet(sepgsqlR))
		work(run)
		run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
	})
}
