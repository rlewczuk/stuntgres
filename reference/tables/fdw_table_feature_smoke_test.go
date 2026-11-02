package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceFDWTableFeatureSmoke(t *testing.T) {
	fdwWriteScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
		fdwLive(t, p, b, r, run)
		fdwSourceExec(t, p, b, r, run, "CREATE TABLE fdw_source.smoke(id integer)", 0)
		fdwSourceExec(t, p, b, r, run, "INSERT INTO fdw_source.smoke VALUES(7)", 1)
		run(fixture.Exec("CREATE FOREIGN TABLE fdw_smoke(id integer OPTIONS(column_name 'id') DEFAULT 7 CONSTRAINT fdw_smoke_ck CHECK(id>0)) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'smoke')", 0))
		run(fixture.Select("SELECT id FROM fdw_smoke", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		for _, prepared := range []bool{false, true} {
			name := "constraint_direct"
			if prepared {
				name = "constraint_prepared"
			}
			t.Run(name, func(t *testing.T) {
				q := "CREATE FOREIGN TABLE fdw_smoke_bad(id integer CONSTRAINT smoke_uq UNIQUE) SERVER fdw_live"
				want := fdwDiagnostic("0A000", "unique constraints are not supported on foreign tables")
				want.Position = fixture.Position(utf8.RuneCountInString(q[:strings.Index(q, "CONSTRAINT")]) + 1)
				s := fixture.ErrorDiagnostic(q, want)
				s.Prepared, s.ErrorStage = prepared, "execute"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM pg_class WHERE relname='fdw_smoke_bad'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
			})
		}
		run(fixture.Exec("CREATE TABLE fdw_smoke_before AS SELECT c.oid AS relation_oid,c.reltype AS type_oid,a.attnum FROM pg_class c JOIN pg_attribute a ON a.attrelid=c.oid WHERE c.oid='fdw_smoke'::regclass::oid AND a.attname='id'", 1))
		run(fixture.Prepared(fixture.Exec("ALTER FOREIGN TABLE fdw_smoke ALTER COLUMN id SET DEFAULT 9,ALTER COLUMN id OPTIONS(SET column_name 'id'),OPTIONS(SET table_name 'smoke')", 0)))
		run(fixture.Exec("ALTER FOREIGN TABLE fdw_smoke RENAME TO fdw_smoke_renamed", 0))
		run(fixture.Select("SELECT c.oid=b.relation_oid AS same_relation,c.reltype=b.type_oid AS same_type,a.attnum=b.attnum AS same_attribute FROM pg_class c JOIN pg_attribute a ON a.attrelid=c.oid CROSS JOIN fdw_smoke_before b WHERE c.oid='fdw_smoke_renamed'::regclass::oid AND a.attname='id'", []string{"same_relation", "same_type", "same_attribute"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}))
		run(fixture.Prepared(fixture.Exec("INSERT INTO fdw_smoke_renamed DEFAULT VALUES", 1)))
		wantRows := [][]any{{int64(7)}, {int64(9)}}
		run(fixture.Select("SELECT id FROM fdw_smoke_renamed ORDER BY id", []string{"id"}, []string{"INT4"}, wantRows))
		fdwWriteSourceRows(t, p, b, r, fixture.Select("SELECT id FROM fdw_source.smoke ORDER BY id", []string{"id"}, []string{"INT4"}, wantRows))
		run(fixture.Exec("CREATE SCHEMA fdw_import", 0))
		q := "IMPORT FOREIGN SCHEMA fdw_source LIMIT TO(smoke) FROM SERVER fdw_live INTO fdw_import"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 0))
		run(fixture.Prepared(fixture.Select("SELECT id FROM fdw_import.smoke ORDER BY id", []string{"id"}, []string{"INT4"}, wantRows)))
		collision := fdwDiagnostic("42P07", `relation "smoke" already exists`)
		collision.Context = `importing foreign table "smoke"`
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(q, collision))
		if err := stmt.Close(); err != nil {
			t.Fatal(err)
		}
		run(fixture.Select("SELECT id FROM fdw_import.smoke ORDER BY id", []string{"id"}, []string{"INT4"}, wantRows))
		run(fixture.Exec("CREATE TABLE fdw_smoke_parent(id integer CHECK(id>0))", 0))
		run(fixture.Prepared(fixture.Exec("CREATE FOREIGN TABLE fdw_smoke_child() INHERITS(fdw_smoke_parent) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'smoke')", 0)))
		run(fixture.Select("SELECT id FROM ONLY fdw_smoke_parent", []string{"id"}, []string{"INT4"}, nil))
		run(fixture.Prepared(fixture.Select("SELECT id FROM fdw_smoke_parent ORDER BY id", []string{"id"}, []string{"INT4"}, wantRows)))
		fdwSourceExec(t, p, b, r, run, "CREATE TABLE fdw_source.smoke_partition(id integer)", 0)
		fdwSourceExec(t, p, b, r, run, "INSERT INTO fdw_source.smoke_partition VALUES(7)", 1)
		run(fixture.Exec("CREATE TABLE fdw_smoke_partitioned(id integer) PARTITION BY RANGE(id)", 0))
		run(fixture.Prepared(fixture.Exec("CREATE FOREIGN TABLE fdw_smoke_partition PARTITION OF fdw_smoke_partitioned FOR VALUES FROM(0) TO(10) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'smoke_partition')", 0)))
		run(fixture.Select("SELECT id FROM ONLY fdw_smoke_partitioned", []string{"id"}, []string{"INT4"}, nil))
		run(fixture.Prepared(fixture.Exec("INSERT INTO fdw_smoke_partitioned VALUES(9)", 1)))
		run(fixture.Prepared(fixture.Select("SELECT id FROM fdw_smoke_partitioned ORDER BY id", []string{"id"}, []string{"INT4"}, wantRows)))
		fdwWriteSourceRows(t, p, b, r, fixture.Select("SELECT id FROM fdw_source.smoke_partition ORDER BY id", []string{"id"}, []string{"INT4"}, wantRows))
		fdwSourceExec(t, p, b, r, run, "CREATE TABLE fdw_source.smoke_identity(id integer)", 0)
		run(fixture.Exec("CREATE FOREIGN TABLE fdw_smoke_identity(id integer GENERATED ALWAYS AS IDENTITY) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'smoke_identity')", 0))
		run(fixture.Prepared(fixture.Exec("INSERT INTO fdw_smoke_identity DEFAULT VALUES", 1)))
		fdwWriteSourceRows(t, p, b, r, fixture.Select("SELECT id FROM fdw_source.smoke_identity", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
		fdwMetadata(run)
		run(fixture.Exec("CREATE FOREIGN TABLE fdw_smoke_handlerless(id integer DEFAULT 1) SERVER fdw_server", 0))
		run(fixture.Prepared(fixture.Exec("ALTER FOREIGN TABLE fdw_smoke_handlerless ALTER COLUMN id SET DEFAULT 2", 0)))
		for _, query := range []string{"SELECT id FROM fdw_smoke_handlerless", "INSERT INTO fdw_smoke_handlerless VALUES(1)", "IMPORT FOREIGN SCHEMA fdw_source FROM SERVER fdw_server INTO fdw_import"} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(query, fdwDiagnostic("55000", `foreign-data wrapper "fdw_meta" has no handler`))
				s.Prepared = prepared
				if strings.HasPrefix(query, "SELECT") {
					s.SelectRows, s.ErrorStage = true, "execute"
				}
				run(s)
			}
		}
		if !b.PG {
			t.Run("native_mapping_independent", func(t *testing.T) {
				fdwSmokeMappingIndependent(t, p, r)
			})
			run(fixture.Exec("DROP USER MAPPING FOR CURRENT_USER SERVER fdw_live", 0))
			missing := fixture.ErrorDiagnostic("IMPORT FOREIGN SCHEMA fdw_source FROM SERVER fdw_live INTO fdw_import", fdwDiagnostic("42704", `user mapping not found for user "postgres", server "fdw_live"`))
			missing.Prepared, missing.ErrorStage = true, "execute"
			run(missing)
			run(fixture.Exec("CREATE USER MAPPING FOR CURRENT_USER SERVER fdw_live OPTIONS(user 'postgres')", 0))
			t.Run("native_controls", func(t *testing.T) {
				before := r.snapshot()
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				if dump.Len() == 0 || r.snapshot() != before {
					t.Fatal("capture invoked source or emitted no definitions")
				}
				for _, name := range []string{"fdw_smoke_renamed", "fdw_smoke_child", "fdw_smoke_partition", "fdw_smoke_identity"} {
					if strings.Contains(dump.String(), `INSERT INTO "public"."`+name+`"`) {
						t.Fatal("dump serialized foreign rows for", name)
					}
				}
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM pg_foreign_table", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
				fdwWriteSourceRows(t, p, b, r, fixture.Select("SELECT id FROM fdw_source.smoke ORDER BY id", []string{"id"}, []string{"INT4"}, wantRows))
				if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
					t.Fatal(err)
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id FROM fdw_smoke_renamed ORDER BY id", []string{"id"}, []string{"INT4"}, wantRows))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id FROM fdw_import.smoke ORDER BY id", []string{"id"}, []string{"INT4"}, wantRows))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM pg_constraint WHERE conrelid='fdw_smoke_renamed'::regclass::oid AND conname='fdw_smoke_ck' AND convalidated", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id FROM fdw_smoke_parent ORDER BY id", []string{"id"}, []string{"INT4"}, wantRows))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id FROM ONLY fdw_smoke_parent", []string{"id"}, []string{"INT4"}, nil))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id FROM fdw_smoke_partitioned ORDER BY id", []string{"id"}, []string{"INT4"}, wantRows))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT c.relkind AS kind,c.relispartition,p.relname AS parent FROM pg_class c JOIN pg_inherits i ON i.inhrelid=c.oid JOIN pg_class p ON p.oid=i.inhparent WHERE c.oid='fdw_smoke_partition'::regclass::oid", []string{"kind", "relispartition", "parent"}, []string{"CHAR", "BOOL", "NAME"}, [][]any{{"f", true, "fdw_smoke_partitioned"}}))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT attidentity AS identity_kind,attnotnull FROM pg_attribute WHERE attrelid='fdw_smoke_identity'::regclass::oid AND attname='id'", []string{"identity_kind", "attnotnull"}, []string{"CHAR", "BOOL"}, [][]any{{"a", true}}))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM pg_depend d JOIN pg_class s ON s.oid=d.objid WHERE d.classid='pg_class'::regclass::oid AND d.refclassid='pg_class'::regclass::oid AND d.refobjid='fdw_smoke_identity'::regclass::oid AND s.relkind='S' AND d.deptype='i'", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO fdw_smoke_identity DEFAULT VALUES", 1))
				fdwWriteSourceRows(t, p, b, r, fixture.Select("SELECT id FROM fdw_source.smoke_identity ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
			})
		}
		run(fixture.Exec("DROP FOREIGN TABLE fdw_smoke_renamed,fdw_import.smoke", 0))
		fdwWriteSourceRows(t, p, b, r, fixture.Select("SELECT id FROM fdw_source.smoke ORDER BY id", []string{"id"}, []string{"INT4"}, wantRows))
		run(fixture.Exec("DROP TABLE fdw_smoke_parent,fdw_smoke_partitioned CASCADE", 0))
		run(fixture.Exec("DROP FOREIGN TABLE fdw_smoke_identity", 0))
		fdwWriteSourceRows(t, p, b, r, fixture.Select("SELECT id FROM fdw_source.smoke_partition ORDER BY id", []string{"id"}, []string{"INT4"}, wantRows))
	})
}

// Native registration has no SQL equivalent. This phase deliberately delegates
// discovery with the test-owned source authority, then uses a normal mapping
// for the actual SELECT; it never relaxes the ordinary reader's policy.
func fdwSmokeMappingIndependent(t *testing.T, p *fixture.Pair, reader *fdwSQLReader) {
	t.Helper()
	d := stuntgres.NewDriver()
	if err := reader.configure(d); err != nil {
		t.Fatal(err)
	}
	provider := &fdwTrustedImporter{fdwSQLReader: reader}
	if err := d.RegisterForeignDataWrapper(stuntgres.ForeignDataWrapperDefinition{HandlerName: stuntgres.ForeignFunctionName{Schema: "public", Name: "fdw_smoke_trusted_handler"}, Wrapper: provider}); err != nil {
		t.Fatal(err)
	}
	connector, err := d.OpenConnector("fdw-smoke-trusted/" + t.Name())
	if err != nil {
		t.Fatal(err)
	}
	db := fdwRecordedPool(t, connector)
	defer db.Close()
	recorder := &fixture.Recorder{RawBackend: fixture.RawNative}
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, db, recorder, s) }
	run(fixture.Exec("CREATE FOREIGN DATA WRAPPER smoke_trusted HANDLER fdw_smoke_trusted_handler VALIDATOR fdw_sql_validator", 0))
	run(fixture.Exec("CREATE SERVER smoke_trusted FOREIGN DATA WRAPPER smoke_trusted", 0))
	run(fixture.Exec("CREATE SCHEMA fdw_import", 0))
	run(fixture.Prepared(fixture.Exec("IMPORT FOREIGN SCHEMA fdw_source LIMIT TO(smoke) FROM SERVER smoke_trusted INTO fdw_import", 0)))
	if len(provider.requests) != 1 || provider.requests[0].MappingPresent {
		t.Fatal("mapping-independent discovery lost absence metadata")
	}
	run(fixture.Select("SELECT c.relkind AS kind,a.attname,a.atttypid::regtype::text AS type FROM pg_class c JOIN pg_attribute a ON a.attrelid=c.oid WHERE c.oid='fdw_import.smoke'::regclass::oid AND a.attnum>0", []string{"kind", "attname", "type"}, []string{"CHAR", "NAME", "TEXT"}, [][]any{{"f", "id", "integer"}}))
	run(fixture.Exec("CREATE USER MAPPING FOR CURRENT_USER SERVER smoke_trusted", 0))
	run(fixture.Prepared(fixture.Select("SELECT id FROM fdw_import.smoke ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(9)}})))
}
