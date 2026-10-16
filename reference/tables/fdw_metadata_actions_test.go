package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFDWMetadataActions(t *testing.T) {
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
		run = func(step fixture.Step) {
			if !t.Run(step.Query, func(t *testing.T) { fixture.RunStep(t, p.Ctx, c, b.Recorder, step) }) {
				t.FailNow()
			}
		}
		defer func() {
			for _, q := range []string{"ROLLBACK", "RESET ROLE", "DROP FOREIGN DATA WRAPPER IF EXISTS fdw_meta_renamed CASCADE"} {
				if _, err := c.ExecContext(p.Ctx, q); err != nil {
					t.Error(q, err)
				}
			}
		}()
		fdwLive(t, p, b, r, run)
		fdwMetadata(run)
		run(fixture.Exec("CREATE USER MAPPING FOR PUBLIC SERVER fdw_server OPTIONS(tag 'original')", 0))
		run(fixture.Exec("CREATE TABLE fdw_identity_before(wrapper_oid oid,server_oid oid,mapping_oid oid,table_oid oid)", 0))
		run(fixture.Exec("INSERT INTO fdw_identity_before SELECT w.oid,s.oid,m.oid,'fdw_data'::regclass::oid FROM pg_foreign_data_wrapper w JOIN pg_foreign_server s ON s.srvfdw=w.oid JOIN pg_user_mapping m ON m.umserver=s.oid WHERE w.fdwname='fdw_meta' AND s.srvname='fdw_server'", 1))
		if !b.PG {
			r.setHook(func(ctx context.Context, method string) error {
				if method != "Validate" {
					return fmt.Errorf("metadata attempted source callback %s", method)
				}
				return ctx.Err()
			})
			defer r.setHook(nil)
		}
		// PostgreSQL ignores copied index/identity/storage/compression properties
		// for a foreign destination, but preserves logical defaults, generation
		// and source PK-derived NOT NULL. No source rows are copied.
		run(fixture.Exec("CREATE TABLE fdw_like_template(id integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,label text DEFAULT 'fallback',computed integer GENERATED ALWAYS AS(id+10) STORED,virtual_value integer GENERATED ALWAYS AS(id+20) VIRTUAL)", 0))
		run(fixture.Exec("CREATE FOREIGN TABLE fdw_like_all(LIKE fdw_like_template INCLUDING ALL) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'data')", 0))
		run(fixture.Exec("CREATE FOREIGN TABLE fdw_like_plain(LIKE fdw_like_template INCLUDING INDEXES INCLUDING IDENTITY) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'data')", 0))
		run(fixture.Select("SELECT attname,attnotnull,attidentity AS identity_kind,atthasdef,attgenerated AS generated_kind FROM pg_attribute WHERE attrelid='fdw_like_all'::regclass::oid AND attnum>0 ORDER BY attnum", []string{"attname", "attnotnull", "identity_kind", "atthasdef", "generated_kind"}, []string{"NAME", "BOOL", "CHAR", "BOOL", "CHAR"}, [][]any{{"id", true, "", false, ""}, {"label", false, "", true, ""}, {"computed", false, "", true, "s"}, {"virtual_value", false, "", true, "v"}}))
		run(fixture.Select("SELECT attname,attnotnull,attidentity AS identity_kind,atthasdef,attgenerated AS generated_kind FROM pg_attribute WHERE attrelid='fdw_like_plain'::regclass::oid AND attnum>0 ORDER BY attnum", []string{"attname", "attnotnull", "identity_kind", "atthasdef", "generated_kind"}, []string{"NAME", "BOOL", "CHAR", "BOOL", "CHAR"}, [][]any{{"id", true, "", false, ""}, {"label", false, "", false, ""}, {"computed", false, "", false, ""}, {"virtual_value", false, "", false, ""}}))
		run(fixture.Select("SELECT count(*) FROM pg_index WHERE indrelid IN('fdw_like_all'::regclass::oid,'fdw_like_plain'::regclass::oid)", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Select("SELECT count(*) FROM pg_depend d JOIN pg_class c ON c.oid=d.objid WHERE d.classid='pg_class'::regclass::oid AND d.refobjid IN('fdw_like_all'::regclass::oid,'fdw_like_plain'::regclass::oid) AND c.relkind='S'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Exec("ALTER FOREIGN TABLE fdw_like_all ALTER COLUMN computed OPTIONS(column_name 'id')", 0))
		run(fixture.Exec("ALTER FOREIGN TABLE fdw_data ADD COLUMN extra integer OPTIONS(column_name 'id') NOT NULL DEFAULT 7 CONSTRAINT fdw_extra_ck CHECK(extra IS NOT NULL)", 0))
		run(fixture.Exec("CREATE TABLE fdw_attribute_before(attnum integer)", 0))
		run(fixture.Exec("INSERT INTO fdw_attribute_before SELECT attnum::integer FROM pg_attribute WHERE attrelid='fdw_data'::regclass::oid AND attname='extra'", 1))
		run(fixture.Exec("ALTER FOREIGN TABLE fdw_data ALTER COLUMN extra TYPE bigint", 0))
		renameSyntax := fdwDiagnostic("42601", `syntax error at or near "CONSTRAINT"`)
		renameSyntax.Position = fixture.Position(37)
		run(fixture.ErrorDiagnostic("ALTER FOREIGN TABLE fdw_data RENAME CONSTRAINT fdw_extra_ck TO fdw_extra_renamed", renameSyntax))
		run(fixture.Exec("ALTER TABLE fdw_data RENAME CONSTRAINT fdw_extra_ck TO fdw_extra_renamed", 0))
		run(fixture.Exec("ALTER FOREIGN TABLE fdw_data ALTER COLUMN extra SET DEFAULT 9,ALTER COLUMN extra OPTIONS(SET column_name 'id'),OPTIONS(SET table_name 'data')", 0))
		run(fixture.Exec("ALTER FOREIGN TABLE fdw_data ALTER COLUMN extra DROP DEFAULT", 0))
		run(fixture.Exec("ALTER FOREIGN TABLE fdw_data ALTER COLUMN id TYPE text", 0))
		// Foreign TYPE metadata does not require an old TEXT→INT assignment
		// conversion or fetch/rewrite any remote row.
		run(fixture.Exec("ALTER FOREIGN TABLE fdw_data ALTER COLUMN id TYPE integer", 0))
		run(fixture.ErrorDiagnostic("ALTER FOREIGN TABLE fdw_data ALTER COLUMN id TYPE bigint USING id::bigint", fdwDiagnostic("42809", `"fdw_data" is not a table`)))
		run(fixture.Exec("ALTER FOREIGN TABLE fdw_data DROP CONSTRAINT fdw_extra_renamed,DROP COLUMN extra", 0))
		run(fixture.Exec("ALTER FOREIGN TABLE fdw_data ADD COLUMN extra bigint OPTIONS(column_name 'id')", 0))
		run(fixture.Select("SELECT a.attnum>b.attnum AS fresh_attribute FROM pg_attribute a CROSS JOIN fdw_attribute_before b WHERE a.attrelid='fdw_data'::regclass::oid AND a.attname='extra'", []string{"fresh_attribute"}, []string{"BOOL"}, [][]any{{true}}))
		run(fixture.Exec("CREATE ROLE fdw_owner", 0))
		run(fixture.Exec("CREATE ROLE fdw_other", 0))
		run(fixture.Exec("CREATE ROLE fdw_reader", 0))
		run(fixture.Exec("ALTER FOREIGN DATA WRAPPER fdw_meta RENAME TO fdw_meta_renamed", 0))
		run(fixture.Exec("ALTER SERVER fdw_server RENAME TO fdw_server_renamed", 0))
		d := fdwDiagnostic("42501", `permission denied to change owner of foreign-data wrapper "fdw_meta_renamed"`)
		d.Hint = "The owner of a foreign-data wrapper must be a superuser."
		run(fixture.ErrorDiagnostic("ALTER FOREIGN DATA WRAPPER fdw_meta_renamed OWNER TO fdw_owner", d))
		run(fixture.Exec("ALTER ROLE fdw_owner SUPERUSER", 0))
		run(fixture.Exec("ALTER FOREIGN DATA WRAPPER fdw_meta_renamed OWNER TO fdw_owner", 0))
		// A superuser actor may transfer a server to a role lacking FDW USAGE.
		run(fixture.Exec("ALTER SERVER fdw_server_renamed OWNER TO fdw_other", 0))
		run(fixture.Exec("GRANT fdw_owner TO fdw_reader WITH INHERIT TRUE,SET FALSE", 0))
		run(fixture.Exec("SET ROLE fdw_reader", 0))
		// Generic wrapper RENAME checks inherited owner authority, not the
		// explicit superuser attribute required by options/handler ALTER.
		run(fixture.Exec("ALTER FOREIGN DATA WRAPPER fdw_meta_renamed RENAME TO fdw_meta", 0))
		// Same-owner server OWNER is a genuine no-op, even for a nonowner.
		run(fixture.Exec("ALTER SERVER fdw_server_renamed OWNER TO fdw_other", 0))
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("CREATE SCHEMA fdw_import AUTHORIZATION fdw_other", 0))
		run(fixture.Exec("ALTER FOREIGN TABLE fdw_data SET SCHEMA fdw_import", 0))
		run(fixture.Exec("ALTER FOREIGN TABLE fdw_import.fdw_data OWNER TO fdw_other", 0))
		run(fixture.Select("SELECT w.oid=b.wrapper_oid AS same_wrapper,s.oid=b.server_oid AS same_server,m.oid=b.mapping_oid AS same_mapping,'fdw_import.fdw_data'::regclass::oid=b.table_oid AS same_table FROM fdw_identity_before b JOIN pg_foreign_data_wrapper w ON w.fdwname='fdw_meta' JOIN pg_foreign_server s ON s.srvfdw=w.oid JOIN pg_user_mapping m ON m.umserver=s.oid", []string{"same_wrapper", "same_server", "same_mapping", "same_table"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true}}))
		run(fixture.Select("SELECT m.umoptions::text AS options FROM pg_user_mapping m JOIN pg_foreign_server s ON s.oid=m.umserver WHERE s.srvname='fdw_server_renamed'", []string{"options"}, []string{"TEXT"}, [][]any{{"{tag=original}"}}))
		if !b.PG {
			r.setHook(nil)
		}
		run(fixture.Select("SELECT extra FROM fdw_import.fdw_data ORDER BY id", []string{"extra"}, []string{"INT8"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}))
		run(fixture.Select("SELECT id,label,computed,virtual_value FROM fdw_like_all ORDER BY id", []string{"id", "label", "computed", "virtual_value"}, []string{"INT4", "TEXT", "INT4", "INT4"}, [][]any{{int64(1), "one", int64(1), int64(21)}, {int64(2), nil, int64(2), int64(22)}, {int64(3), "three", int64(3), int64(23)}}))
	})
}

func TestReferenceFDWWrapperAlterBoundaries(t *testing.T) {
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
		fdwLive(t, p, b, r, run)
		handler, validator := "fdw_sql_handler", "fdw_sql_validator"
		if b.PG {
			handler, validator = "postgres_fdw_handler", "postgres_fdw_validator"
		}
		run(fixture.Exec("CREATE ROLE fdw_owner SUPERUSER", 0))
		run(fixture.Exec("CREATE ROLE fdw_reader", 0))
		run(fixture.Exec("CREATE FOREIGN DATA WRAPPER fdw_meta HANDLER "+handler+" VALIDATOR "+validator, 0))
		run(fixture.Exec("CREATE SERVER fdw_server FOREIGN DATA WRAPPER fdw_meta", 0))
		run(fixture.Exec("ALTER FOREIGN DATA WRAPPER fdw_meta OWNER TO fdw_owner", 0))
		run(fixture.Exec("GRANT USAGE ON FOREIGN DATA WRAPPER fdw_meta TO fdw_reader", 0))
		run(fixture.Exec("GRANT fdw_owner TO fdw_reader WITH INHERIT TRUE,SET FALSE", 0))
		run(fixture.Exec("CREATE TABLE fdw_alter_before AS SELECT oid AS object_oid,fdwowner AS owner_oid,fdwhandler AS handler_oid,fdwvalidator AS validator_oid,fdwacl::text AS acl FROM pg_foreign_data_wrapper WHERE fdwname='fdw_meta'", 1))
		run(fixture.Exec("GRANT SELECT ON fdw_alter_before TO fdw_reader", 0))
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO fdw_reader", 0))
		run(fixture.Exec("SET ROLE fdw_reader", 0))
		unchanged := func(t *testing.T) {
			t.Helper()
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT w.oid=b.object_oid AS same_object,w.fdwowner=b.owner_oid AS same_owner,w.fdwhandler=b.handler_oid AS same_handler,w.fdwvalidator=b.validator_oid AS same_validator,w.fdwacl::text=b.acl AS same_acl,w.fdwoptions IS NULL AS no_options,w.fdwhandler<>0 AS has_handler,w.fdwvalidator<>0 AS has_validator FROM pg_foreign_data_wrapper w CROSS JOIN fdw_alter_before b WHERE w.fdwname='fdw_meta'", []string{"same_object", "same_owner", "same_handler", "same_validator", "same_acl", "no_options", "has_handler", "has_validator"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true, true, true, true, true}}))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM pg_depend d CROSS JOIN fdw_alter_before b WHERE d.classid=2328::oid AND d.objid=b.object_oid AND d.refclassid=1255::oid AND (d.refobjid=b.handler_oid OR d.refobjid=b.validator_oid) AND d.deptype='n'", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM pg_foreign_server s CROSS JOIN fdw_alter_before b WHERE s.srvname='fdw_server' AND s.srvfdw=b.object_oid", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
		}
		unchanged(t)
		for _, prepared := range []bool{false, true} {
			for _, option := range []string{"", "HANDLER " + handler, "NO HANDLER", "VALIDATOR " + validator, "NO VALIDATOR"} {
				tails := []string{""}
				if option != "" {
					tails = []string{"RENAME TO fdw_meta_changed", "OWNER TO fdw_owner"}
				}
				for _, tail := range tails {
					q := "ALTER FOREIGN DATA WRAPPER fdw_meta"
					if option != "" {
						q += " " + option + " " + tail
					}
					name := fmt.Sprintf("prepared=%t/%s", prepared, q)
					if !t.Run(name, func(t *testing.T) {
						before := r.snapshot()
						diagnostic := fdwDiagnostic("42601", "syntax error at end of input")
						diagnostic.Position = fixture.Position(len([]rune(q)) + 1)
						if tail != "" {
							token := strings.Fields(tail)[0]
							diagnostic.Message = `syntax error at or near "` + token + `"`
							diagnostic.Position = fixture.Position(len([]rune(q[:strings.Index(q, token)])) + 1)
						}
						step := fixture.ErrorDiagnostic(q, diagnostic)
						step.Prepared = prepared
						step.ErrorStage = "execute"
						if prepared {
							step.ErrorStage = "prepare"
						}
						fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
						unchanged(t)
						if !b.PG && r.snapshot() != before {
							t.Fatal("rejected wrapper syntax invoked application callbacks", before, r.snapshot())
						}
					}) {
						t.FailNow()
					}
				}
			}
			// Inherited ownership intentionally permits an ordinary RENAME,
			// unlike handler/validator/options capability changes.
			for _, q := range []string{"ALTER FOREIGN DATA WRAPPER fdw_meta RENAME TO fdw_meta_changed", "ALTER FOREIGN DATA WRAPPER fdw_meta_changed RENAME TO fdw_meta"} {
				step := fixture.Exec(q, 0)
				step.Prepared = prepared
				run(step)
			}
			unchanged(t)
		}
		run(fixture.Exec("RESET ROLE", 0))
	})
}
