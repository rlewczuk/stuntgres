package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFDWTableDeclarations(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		mode := "direct"
		if prepared {
			mode = "prepared"
		}
		t.Run(mode, func(t *testing.T) {
			t.Run("fields_and_alter", func(t *testing.T) {
				fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
					fdwLive(t, p, b, r, run)
					route := func(q string) { s := fixture.Exec(q, 0); s.Prepared = prepared; run(s) }
					route("CREATE FOREIGN TABLE fdw_decl(id integer OPTIONS(column_name 'id') NOT NULL DEFAULT 7,label varchar(7) COLLATE \"C\" DEFAULT 'alpha',amount numeric(6,2),flag boolean,CONSTRAINT fdw_decl_ck CHECK(id>0)) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'data')")
					run(fixture.Select("SELECT a.attname,a.atttypid::regtype::text AS type,a.atttypmod,a.attnotnull,a.atthasdef,co.collname,a.attfdwoptions::text AS options FROM pg_attribute a LEFT JOIN pg_collation co ON co.oid=a.attcollation WHERE a.attrelid='fdw_decl'::regclass::oid AND a.attnum>0 ORDER BY a.attnum", []string{"attname", "type", "atttypmod", "attnotnull", "atthasdef", "collname", "options"}, []string{"NAME", "TEXT", "INT4", "BOOL", "BOOL", "NAME", "TEXT"}, [][]any{
						{"id", "integer", int64(-1), true, true, nil, "{column_name=id}"},
						{"label", "character varying", int64(11), false, true, "C", nil},
						{"amount", "numeric", int64(393222), false, false, nil, nil},
						{"flag", "boolean", int64(-1), false, false, nil, nil},
					}))
					run(fixture.Select("SELECT c.relkind AS kind,t.typrelid=c.oid AS same_relation FROM pg_class c JOIN pg_type t ON t.oid=c.reltype WHERE c.oid='fdw_decl'::regclass::oid", []string{"kind", "same_relation"}, []string{"CHAR", "BOOL"}, [][]any{{"f", true}}))
					run(fixture.Select("SELECT conname,convalidated FROM pg_constraint WHERE conrelid='fdw_decl'::regclass::oid AND contype='c'", []string{"conname", "convalidated"}, []string{"NAME", "BOOL"}, [][]any{{"fdw_decl_ck", true}}))
					run(fixture.Exec("CREATE TABLE fdw_decl_before AS SELECT c.oid AS relation_oid,c.reltype AS type_oid,a.attnum FROM pg_class c JOIN pg_attribute a ON a.attrelid=c.oid WHERE c.oid='fdw_decl'::regclass::oid AND a.attname='id'", 1))
					route("ALTER FOREIGN TABLE fdw_decl ALTER COLUMN id SET DEFAULT 9,ADD COLUMN extra integer OPTIONS(column_name 'id')")
					route("ALTER FOREIGN TABLE fdw_decl ALTER COLUMN extra TYPE bigint")
					route("ALTER FOREIGN TABLE fdw_decl ALTER COLUMN id DROP DEFAULT")
					route("ALTER FOREIGN TABLE fdw_decl ADD CONSTRAINT fdw_decl_later CHECK(id<0) NOT VALID")
					run(fixture.Select("SELECT convalidated FROM pg_constraint WHERE conrelid='fdw_decl'::regclass::oid AND conname='fdw_decl_later'", []string{"convalidated"}, []string{"BOOL"}, [][]any{{false}}))
					// VALIDATE changes only the foreign assumption: genuine source rows
					// deliberately violate this CHECK and must not be scanned/rejected.
					route("ALTER FOREIGN TABLE fdw_decl VALIDATE CONSTRAINT fdw_decl_later")
					run(fixture.Select("SELECT convalidated FROM pg_constraint WHERE conrelid='fdw_decl'::regclass::oid AND conname='fdw_decl_later'", []string{"convalidated"}, []string{"BOOL"}, [][]any{{true}}))
					route("ALTER FOREIGN TABLE fdw_decl DROP CONSTRAINT fdw_decl_later,DROP COLUMN extra")
					failure := fixture.ErrorDiagnostic("ALTER FOREIGN TABLE fdw_decl ADD COLUMN earlier integer,ALTER COLUMN missing SET DEFAULT 1", fdwDiagnostic("42703", `column "missing" of relation "fdw_decl" does not exist`))
					failure.Prepared, failure.ErrorStage = prepared, "execute"
					run(failure)
					run(fixture.Select("SELECT attname,atthasdef FROM pg_attribute WHERE attrelid='fdw_decl'::regclass::oid AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname", "atthasdef"}, []string{"NAME", "BOOL"}, [][]any{{"id", false}, {"label", true}, {"amount", false}, {"flag", false}}))
					failure = fixture.ErrorDiagnostic("ALTER FOREIGN TABLE fdw_decl DROP CONSTRAINT absent_ck", fdwDiagnostic("42704", `constraint "absent_ck" of relation "fdw_decl" does not exist`))
					failure.Prepared, failure.ErrorStage = prepared, "execute"
					run(failure)
					route("ALTER FOREIGN TABLE fdw_decl RENAME TO fdw_decl_renamed")
					route("CREATE SCHEMA fdw_import")
					route("ALTER FOREIGN TABLE fdw_decl_renamed SET SCHEMA fdw_import")
					run(fixture.Select("SELECT c.oid=b.relation_oid AS same_relation,c.reltype=b.type_oid AS same_type,a.attnum=b.attnum AS same_attribute FROM pg_class c JOIN pg_attribute a ON a.attrelid=c.oid CROSS JOIN fdw_decl_before b WHERE c.oid='fdw_import.fdw_decl_renamed'::regclass::oid AND a.attname='id'", []string{"same_relation", "same_type", "same_attribute"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}))
					route("DROP FOREIGN TABLE fdw_import.fdw_decl_renamed")
					fdwWriteSourceRows(t, p, b, r, fdwRows("SELECT id,label,flag FROM fdw_source.data ORDER BY id", [][]any{{int64(1), "one", true}, {int64(2), nil, false}, {int64(3), "three", nil}}))
				})
			})
			t.Run("zero_columns", func(t *testing.T) {
				fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
					fdwMetadata(run)
					for _, q := range []string{"CREATE FOREIGN TABLE fdw_zero() SERVER fdw_server", "ALTER FOREIGN TABLE fdw_zero ADD COLUMN id integer", "ALTER FOREIGN TABLE fdw_zero DROP COLUMN id"} {
						s := fixture.Exec(q, 0)
						s.Prepared = prepared
						run(s)
					}
					run(fixture.Select("SELECT count(*) FROM pg_attribute WHERE attrelid='fdw_zero'::regclass::oid AND attnum>0 AND NOT attisdropped", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
				})
			})
			t.Run("identity", func(t *testing.T) {
				fdwWriteScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
					fdwLive(t, p, b, r, run)
					fdwSourceExec(t, p, b, r, run, "CREATE TABLE fdw_source.identity_decl(id integer,label text)", 0)
					s := fixture.Exec("CREATE FOREIGN TABLE fdw_identity_decl(id integer GENERATED ALWAYS AS IDENTITY,label text) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'identity_decl')", 0)
					s.Prepared = prepared
					run(s)
					run(fixture.Select("SELECT attidentity AS identity_kind,attnotnull FROM pg_attribute WHERE attrelid='fdw_identity_decl'::regclass::oid AND attname='id'", []string{"identity_kind", "attnotnull"}, []string{"CHAR", "BOOL"}, [][]any{{"a", true}}))
					run(fixture.Select("SELECT pg_get_serial_sequence('fdw_identity_decl','id') AS sequence_name", []string{"sequence_name"}, []string{"TEXT"}, [][]any{{"public.fdw_identity_decl_id_seq"}}))
					run(fixture.Select("SELECT count(*) FROM pg_depend d JOIN pg_class s ON s.oid=d.objid WHERE d.classid='pg_class'::regclass::oid AND d.refclassid='pg_class'::regclass::oid AND d.refobjid='fdw_identity_decl'::regclass::oid AND s.relkind='S' AND d.deptype='i'", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
					s = fixture.Exec("ALTER FOREIGN TABLE fdw_identity_decl ALTER COLUMN id SET GENERATED BY DEFAULT", 0)
					s.Prepared = prepared
					run(s)
					run(fixture.Select("SELECT attidentity AS identity_kind FROM pg_attribute WHERE attrelid='fdw_identity_decl'::regclass::oid AND attname='id'", []string{"identity_kind"}, []string{"CHAR"}, [][]any{{"d"}}))
					s = fixture.Exec("ALTER FOREIGN TABLE fdw_identity_decl ALTER COLUMN id SET GENERATED ALWAYS", 0)
					s.Prepared = prepared
					run(s)
					s = fixture.Exec("INSERT INTO fdw_identity_decl(label) VALUES('generated')", 1)
					s.Prepared = prepared
					run(s)
					fdwWriteSourceRows(t, p, b, r, fixture.Select("SELECT id,label FROM fdw_source.identity_decl", []string{"id", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "generated"}}))
					s = fixture.Exec("DROP FOREIGN TABLE fdw_identity_decl", 0)
					s.Prepared = prepared
					run(s)
					run(fixture.Select("SELECT count(*) FROM pg_class WHERE relname='fdw_identity_decl_id_seq'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
					fdwWriteSourceRows(t, p, b, r, fixture.Select("SELECT id,label FROM fdw_source.identity_decl", []string{"id", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "generated"}}))
				})
			})
			t.Run("drop_diagnostics", func(t *testing.T) {
				fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
					run(fixture.Exec("CREATE TABLE fdw_not_foreign(id integer)", 0))
					wrong := fdwDiagnostic("42809", `"fdw_not_foreign" is not a foreign table`)
					wrong.Hint = "Use DROP TABLE to remove a table."
					for _, s := range []fixture.Step{
						fixture.ErrorDiagnostic("DROP FOREIGN TABLE fdw_not_foreign", wrong),
						fixture.ErrorDiagnostic("DROP FOREIGN TABLE fdw_missing", fdwDiagnostic("42704", `foreign table "fdw_missing" does not exist`)),
					} {
						s.Prepared, s.ErrorStage = prepared, "execute"
						run(s)
					}
					var notices []fixture.Diagnostic
					_, nc, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
					s := fixture.Exec("DROP FOREIGN TABLE IF EXISTS fdw_missing", 0)
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, nc, recorder, s)
					want := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `foreign table "fdw_missing" does not exist, skipping`}}
					if !reflect.DeepEqual(notices, want) {
						t.Fatalf("notices=%#v want=%#v", notices, want)
					}
					run(fixture.Select("SELECT id FROM fdw_not_foreign", []string{"id"}, []string{"INT4"}, nil))
				})
			})
		})
	}
	for _, tc := range []struct {
		name, options               string
		defaults, generated, checks bool
	}{
		{"plain", "", false, false, false},
		{"defaults", "INCLUDING DEFAULTS", true, false, false},
		{"constraints", "INCLUDING CONSTRAINTS", false, false, true},
		{"generated", "INCLUDING GENERATED", false, true, false},
		{"all", "INCLUDING ALL", true, true, true},
		{"excluding", "INCLUDING ALL EXCLUDING DEFAULTS EXCLUDING GENERATED EXCLUDING CONSTRAINTS", false, false, false},
	} {
		t.Run("like/"+tc.name, func(t *testing.T) {
			fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
				fdwMetadata(run)
				run(fixture.Exec("CREATE TABLE fdw_shape(id integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,label text DEFAULT 'fallback',s integer GENERATED ALWAYS AS(id+10) STORED,v integer GENERATED ALWAYS AS(id+20) VIRTUAL,CONSTRAINT fdw_shape_ck CHECK(id>0))", 0))
				for _, prepared := range []bool{false, true} {
					s := fixture.Exec("CREATE FOREIGN TABLE fdw_shape_copy(LIKE fdw_shape "+tc.options+") SERVER fdw_server", 0)
					s.Prepared = prepared
					run(s)
					stored, virtual := "", ""
					if tc.generated {
						stored, virtual = "s", "v"
					}
					run(fixture.Select("SELECT attname,attnotnull,attidentity AS identity_kind,atthasdef,attgenerated AS generated_kind FROM pg_attribute WHERE attrelid='fdw_shape_copy'::regclass::oid AND attnum>0 ORDER BY attnum", []string{"attname", "attnotnull", "identity_kind", "atthasdef", "generated_kind"}, []string{"NAME", "BOOL", "CHAR", "BOOL", "CHAR"}, [][]any{{"id", true, "", false, ""}, {"label", false, "", tc.defaults, ""}, {"s", false, "", tc.generated, stored}, {"v", false, "", tc.generated, virtual}}))
					count := int64(0)
					if tc.checks {
						count = 1
					}
					run(fixture.Select("SELECT count(*) FROM pg_constraint WHERE conrelid='fdw_shape_copy'::regclass::oid AND contype='c'", []string{"count"}, []string{"INT8"}, [][]any{{count}}))
					run(fixture.Select("SELECT count(*) FROM pg_index WHERE indrelid='fdw_shape_copy'::regclass::oid", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
					run(fixture.Select("SELECT count(*) FROM pg_depend d JOIN pg_class s ON s.oid=d.objid WHERE d.classid='pg_class'::regclass::oid AND d.refobjid='fdw_shape_copy'::regclass::oid AND s.relkind='S'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
					run(fixture.Exec("DROP FOREIGN TABLE fdw_shape_copy", 0))
				}
				run(fixture.Exec("CREATE FOREIGN TABLE fdw_shape_copy(LIKE fdw_shape "+tc.options+") SERVER fdw_server", 0))
				run(fixture.Exec("DROP TABLE fdw_shape RESTRICT", 0))
				run(fixture.Select("SELECT count(*) FROM pg_attribute WHERE attrelid='fdw_shape_copy'::regclass::oid AND attnum>0 AND NOT attisdropped", []string{"count"}, []string{"INT8"}, [][]any{{int64(4)}}))
			})
		})
	}
}
