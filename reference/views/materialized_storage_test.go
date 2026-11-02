package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceMaterializedStorageValues(t *testing.T) {
	materializedStorageRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		materializedStorageSeed(run)
		materializedStorageBaseline(run)
		run(fixture.Exec("INSERT INTO ms_source VALUES(9,'not refreshed')", 1))
		for _, tc := range []struct {
			value string
			want  any
		}{{"0", int64(0)}, {"7", int64(7)}, {"10000", int64(10000)}, {"-1", nil}, {"DEFAULT", nil}} {
			run(fixture.Exec("ALTER MATERIALIZED VIEW ms_mv ALTER COLUMN id SET STATISTICS "+tc.value, 0))
			run(materializedStorageColumns(tc.want, nil, "x", ""))
			run(materializedStorageRows())
		}
		run(fixture.Exec("ALTER MATERIALIZED VIEW ms_mv ALTER id SET (n_distinct=2,n_distinct_inherited=-0.5)", 0))
		run(materializedStorageColumns(nil, "{n_distinct=2,n_distinct_inherited=-0.5}", "x", ""))
		run(fixture.Exec("ALTER MATERIALIZED VIEW ms_mv ALTER id RESET (n_distinct,n_distinct_inherited)", 0))
		for _, tc := range []struct{ value, want string }{{"EXTENDED", "x"}, {"EXTERNAL", "e"}, {`"ExTeRnAl"`, "e"}, {"MAIN", "m"}, {"PLAIN", "p"}, {"DEFAULT", "x"}} {
			run(fixture.Exec("ALTER MATERIALIZED VIEW ms_mv ALTER note SET STORAGE "+tc.value, 0))
			run(materializedStorageColumns(nil, nil, tc.want, ""))
			run(fixture.Select("SELECT attstorage FROM pg_attribute WHERE attrelid='ms_b'::regclass AND attnum=1", []string{"attstorage"}, []string{"CHAR"}, [][]any{{tc.want}}))
		}
		for _, tc := range []struct{ value, want string }{{"pglz", "p"}, {"lz4", "l"}, {"default", ""}} {
			run(fixture.Exec("ALTER MATERIALIZED VIEW ms_mv ALTER note SET COMPRESSION "+tc.value, 0))
			run(materializedStorageColumns(nil, nil, "x", tc.want))
		}
		run(fixture.Exec("ALTER MATERIALIZED VIEW ms_mv ALTER id SET COMPRESSION default", 0))
		run(fixture.Exec("ALTER MATERIALIZED VIEW ms_mv SET (fillfactor=70,autovacuum_enabled='OFF')", 0))
		run(materializedStorageOptions("{fillfactor=70,autovacuum_enabled=OFF}"))
		run(fixture.Exec("ALTER MATERIALIZED VIEW ms_mv SET (fillfactor=80)", 0))
		run(materializedStorageOptions("{autovacuum_enabled=OFF,fillfactor=80}"))
		run(fixture.Exec("ALTER MATERIALIZED VIEW ms_mv RESET (fillfactor,autovacuum_enabled)", 0))
		run(materializedStorageOptions(nil))
		// Native stores TOAST intent without inventing a physical toast relation.
		run(fixture.Exec("ALTER MATERIALIZED VIEW ms_mv SET (toast.autovacuum_enabled=false)", 0))
		run(materializedStorageOptions(nil))
		if b.PG {
			run(fixture.Select("SELECT t.reloptions FROM pg_class c JOIN pg_class t ON t.oid=c.reltoastrelid WHERE c.oid='ms_mv'::regclass", []string{"reloptions"}, []string{"_TEXT"}, [][]any{{"{autovacuum_enabled=false}"}}))
		} else {
			run(fixture.Select("SELECT reltoastrelid::bigint FROM pg_class WHERE oid='ms_mv'::regclass", []string{"reltoastrelid"}, []string{"INT8"}, [][]any{{int64(0)}}))
		}
		run(fixture.Exec("ALTER MATERIALIZED VIEW ms_mv RESET (toast.autovacuum_enabled)", 0))
		if b.PG {
			run(fixture.Select("SELECT t.reloptions FROM pg_class c JOIN pg_class t ON t.oid=c.reltoastrelid WHERE c.oid='ms_mv'::regclass", []string{"reloptions"}, []string{"_TEXT"}, [][]any{{nil}}))
		}
		for _, name := range []string{"ms_empty", "ms_no"} {
			run(fixture.Exec("ALTER MATERIALIZED VIEW "+name+" ALTER note SET STORAGE EXTERNAL, ALTER id SET STATISTICS 7, SET (fillfactor=70)", 0))
			fixture.MaterializedViewPopulation(run, name, name == "ms_empty")
		}
		run(fixture.Select("SELECT id,note FROM ms_empty", []string{"id", "note"}, []string{"INT4", "TEXT"}, nil))
		run(fixture.MaterializedViewUnpopulated("ms_no"))
		run(materializedStorageRows())
	})
}

func TestReferenceMaterializedStorageCluster(t *testing.T) {
	materializedStorageRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		materializedStorageSeed(run)
		for _, name := range []string{"ms_a", "ms_a", "ms_b"} {
			run(fixture.Exec("ALTER MATERIALIZED VIEW ms_mv CLUSTER ON "+name, 0))
			run(materializedStorageCluster(name == "ms_a", name == "ms_b"))
			run(materializedStorageRows())
		}
		run(fixture.Exec("ALTER MATERIALIZED VIEW ms_mv SET WITHOUT CLUSTER", 0))
		run(materializedStorageCluster(false, false))
		run(fixture.Exec("CREATE INDEX ms_expression ON ms_mv((id+1))", 0))
		run(fixture.Exec("ALTER MATERIALIZED VIEW ms_mv CLUSTER ON ms_expression", 0))
		run(fixture.Select("SELECT indisclustered FROM pg_index WHERE indexrelid='ms_expression'::regclass", []string{"indisclustered"}, []string{"BOOL"}, [][]any{{true}}))
		run(fixture.Exec("CREATE SCHEMA ms_shadow", 0))
		run(fixture.Exec("CREATE TABLE ms_shadow.decoy(id int)", 0))
		run(fixture.Exec("CREATE INDEX ms_a ON ms_shadow.decoy(id)", 0))
		run(fixture.Exec("SET search_path=ms_shadow,public", 0))
		run(fixture.Exec("ALTER MATERIALIZED VIEW public.ms_mv CLUSTER ON ms_a", 0))
		run(fixture.Exec("RESET search_path", 0))
		run(materializedStorageCluster(true, false))
		run(materializedStorageRows())
	})
}

func TestReferenceMaterializedStorageMethods(t *testing.T) {
	materializedStorageRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		materializedStorageSeed(run)
		for _, name := range []string{"ms_heap2", "ms_heap3"} {
			run(fixture.Exec("CREATE ACCESS METHOD "+name+" TYPE TABLE HANDLER heap_tableam_handler", 0))
		}
		var oid, rowtype, owner int64
		var definition string
		if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint,reltype::bigint,relowner::bigint,pg_get_viewdef(oid,true) FROM pg_class WHERE oid='ms_mv'::regclass").Scan(&oid, &rowtype, &owner, &definition); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"ms_heap2", "ms_heap3", "heap"} {
			run(fixture.Exec("ALTER MATERIALIZED VIEW ms_mv SET ACCESS METHOD "+name, 0))
			run(materializedStorageMethod(name))
			run(materializedStorageRows())
			run(fixture.Select("SELECT oid::bigint,reltype::bigint,relowner::bigint,pg_get_viewdef(oid,true) AS definition FROM pg_class WHERE oid='ms_mv'::regclass", []string{"oid", "reltype", "relowner", "definition"}, []string{"INT8", "INT8", "INT8", "TEXT"}, [][]any{{oid, rowtype, owner, definition}}))
		}
		q := "ALTER MATERIALIZED VIEW ms_mv SET ACCESS METHOD DEFAULT"
		held, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		for _, name := range []string{"ms_heap2", "ms_heap3"} {
			run(fixture.Exec("SET default_table_access_method="+name, 0))
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: q}, b.Recorder, fixture.Exec(q, 0))
			run(materializedStorageMethod(name))
		}
		run(fixture.Exec("RESET default_table_access_method", 0))
		run(fixture.Exec("ALTER MATERIALIZED VIEW ms_mv SET TABLESPACE pg_default", 0))
		run(materializedStorageMethod("ms_heap3"))
		run(materializedStorageRows())
		for _, name := range []string{"ms_empty", "ms_no"} {
			run(fixture.Exec("ALTER MATERIALIZED VIEW "+name+" SET ACCESS METHOD ms_heap2, SET TABLESPACE pg_default", 0))
			fixture.MaterializedViewPopulation(run, name, name == "ms_empty")
			run(fixture.Select("SELECT a.amname FROM pg_class c JOIN pg_am a ON a.oid=c.relam WHERE c.oid='"+name+"'::regclass", []string{"amname"}, []string{"NAME"}, [][]any{{"ms_heap2"}}))
		}
		run(fixture.Select("SELECT id,note FROM ms_empty", []string{"id", "note"}, []string{"INT4", "TEXT"}, nil))
		run(fixture.MaterializedViewUnpopulated("ms_no"))
	})
}

func TestReferenceMaterializedStorageResults(t *testing.T) {
	materializedStorageRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		materializedStorageSeed(run)
		for _, suffix := range []string{"ALTER id SET STATISTICS 7", "ALTER note SET STORAGE EXTERNAL", "SET (fillfactor=70)", "CLUSTER ON ms_a", "SET ACCESS METHOD heap", "SET TABLESPACE pg_default"} {
			q := "ALTER MATERIALIZED VIEW ms_mv " + suffix
			run(fixture.Exec(q, 0)) // includes unsupported LastInsertId
			run(fixture.Select(q, []string{}, []string{}, nil))
		}
		run(fixture.Select("ALTER MATERIALIZED VIEW ALL IN TABLESPACE pg_default OWNED BY CURRENT_USER SET TABLESPACE pg_default NOWAIT", []string{}, []string{}, nil))
		run(materializedStorageRows())
		run(fixture.ErrorDiagnostic("INSERT INTO ms_mv VALUES(3,'forbidden')", fixture.ControlsReferenceDiagnostic("42809", `cannot change materialized view "ms_mv"`)))
		// DDL retains a zero-parameter description; a real held statement rejects
		// an extra argument before effects, then still executes normally.
		q := "ALTER MATERIALIZED VIEW ms_mv ALTER id SET STATISTICS 9"
		s, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		if result, err := s.ExecContext(p.Ctx, 1); err == nil || result != nil {
			t.Fatal("zero-parameter ALTER accepted an argument", result, err)
		}
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: s, SQL: q}, b.Recorder, fixture.Exec(q, 0))
	})
}

func TestReferenceMaterializedStorageFeatureSmoke(t *testing.T) {
	materializedStorageRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		materializedStorageSeed(run)
		run(fixture.Exec("CREATE ACCESS METHOD ms_heap2 TYPE TABLE HANDLER heap_tableam_handler", 0))
		run(fixture.Exec("INSERT INTO ms_source VALUES(9,'current recipe only')", 1))
		for _, suffix := range []string{"ALTER id SET STATISTICS 7", "ALTER id SET (n_distinct=2)", "ALTER note SET STORAGE EXTERNAL", "ALTER note SET COMPRESSION pglz", "SET (fillfactor=70)", "CLUSTER ON ms_a", "SET ACCESS METHOD ms_heap2", "SET TABLESPACE pg_default"} {
			run(fixture.Exec("ALTER MATERIALIZED VIEW ms_mv "+suffix, 0))
		}
		run(fixture.Exec("ALTER MATERIALIZED VIEW ALL IN TABLESPACE pg_default OWNED BY CURRENT_USER SET TABLESPACE pg_default NOWAIT", 0))
		run(materializedStorageColumns(int64(7), "{n_distinct=2}", "e", "p"))
		run(materializedStorageOptions("{fillfactor=70}"))
		run(materializedStorageCluster(true, false))
		run(materializedStorageMethod("ms_heap2"))
		run(materializedStorageRows())
		fixture.MaterializedViewPopulation(run, "ms_empty", true)
		fixture.MaterializedViewPopulation(run, "ms_no", false)
		run(fixture.MaterializedViewUnpopulated("ms_no"))
		run(fixture.ErrorDiagnostic("ALTER MATERIALIZED VIEW ms_mv SET ACCESS METHOD absent_am", fixture.ControlsReferenceDiagnostic("42704", `access method "absent_am" does not exist`)))
		run(fixture.ErrorDiagnostic("ALTER MATERIALIZED VIEW ms_mv SET (bogus=1)", fixture.ControlsReferenceDiagnostic("22023", `unrecognized parameter "bogus"`)))
		run(materializedStorageColumns(int64(7), "{n_distinct=2}", "e", "p"))
		run(materializedStorageMethod("ms_heap2"))
		run(materializedStorageRows())
		bounds := fixture.ControlsReferenceDiagnostic("22023", `value 9 out of bounds for option "fillfactor"`)
		bounds.Detail = `Valid values are between "10" and "100".`
		run(fixture.ErrorDiagnostic("ALTER MATERIALIZED VIEW ms_mv ALTER id SET STATISTICS 9, SET (fillfactor=9)", bounds))
		run(materializedStorageColumns(int64(7), "{n_distinct=2}", "e", "p"))
		run(materializedStorageOptions("{fillfactor=70}"))
		run(materializedStorageCluster(true, false))
		run(materializedStorageRows())
		if !b.PG {
			dump := materializedStorageDump(t, p)
			fresh := fixture.NewFixture(t, false, true)
			if err := stuntgres.Restore(p.Ctx, fresh.Native, bytes.NewReader(dump)); err != nil {
				t.Fatal(err)
			}
			for _, step := range []fixture.Step{materializedStorageColumns(int64(7), "{n_distinct=2}", "e", "p"), materializedStorageOptions("{fillfactor=70}"), materializedStorageCluster(true, false), materializedStorageMethod("ms_heap2"), materializedStorageRows(), fixture.MaterializedViewUnpopulated("ms_no")} {
				fixture.RunStep(t, p.Ctx, fresh.Memory, fresh.MemoryRecord, step)
			}
			fixture.MaterializedViewPopulation(func(step fixture.Step) { fixture.RunStep(t, p.Ctx, fresh.Memory, fresh.MemoryRecord, step) }, "ms_empty", true)
		}
		run(fixture.Exec("ALTER MATERIALIZED VIEW ms_mv SET WITHOUT CLUSTER, RESET (fillfactor), ALTER id SET STATISTICS DEFAULT, ALTER id RESET (n_distinct), ALTER note SET STORAGE DEFAULT, ALTER note SET COMPRESSION default", 0))
		run(materializedStorageColumns(nil, nil, "x", ""))
		run(materializedStorageOptions(nil))
		run(materializedStorageCluster(false, false))
		run(materializedStorageMethod("ms_heap2"))
		run(materializedStorageRows())
	})
}
