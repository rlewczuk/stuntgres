package reference_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func materializedStorageControlState(run func(fixture.Step), populated, physical bool) {
	run(materializedStorageColumns(int64(7), "{n_distinct=2,n_distinct_inherited=-0.5}", "e", "p"))
	run(materializedStorageOptions("{fillfactor=70,autovacuum_enabled=OFF}"))
	run(materializedStorageCluster(false, true))
	run(fixture.Select("SELECT a.amname,c.reltablespace::bigint,r.rolname,c.relkind,c.relispopulated FROM pg_class c JOIN pg_am a ON a.oid=c.relam JOIN pg_roles r ON r.oid=c.relowner WHERE c.oid='ms_mv'::regclass", []string{"amname", "reltablespace", "rolname", "relkind", "relispopulated"}, []string{"NAME", "INT8", "NAME", "CHAR", "BOOL"}, [][]any{{"ms_heap2", int64(0), "ms_owner", "m", populated}}))
	run(fixture.Select("SELECT pg_get_viewdef('ms_mv'::regclass,true) AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{" SELECT id,\n    note\n   FROM ms_source;"}}))
	fixture.MaterializedViewPopulation(run, "ms_empty", populated)
	fixture.MaterializedViewPopulation(run, "ms_no", false)
	run(fixture.MaterializedViewUnpopulated("ms_no"))
	if physical {
		run(fixture.Select("SELECT t.reloptions FROM pg_class c JOIN pg_class t ON t.oid=c.reltoastrelid WHERE c.oid='ms_mv'::regclass", []string{"reloptions"}, []string{"_TEXT"}, [][]any{{"{autovacuum_enabled=false}"}}))
	} else {
		run(fixture.Select("SELECT reltoastrelid::bigint FROM pg_class WHERE oid='ms_mv'::regclass", []string{"reltoastrelid"}, []string{"INT8"}, [][]any{{int64(0)}}))
	}
}

func TestReferenceMaterializedStorageControls(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "held"}[prepared], func(t *testing.T) {
			cleanup := fixture.AccessMethodReferenceCleanup(t, []string{"ms_heap2"})
			fixture.RoleReferenceCleanup(t, []string{"ms_owner"})
			p := fixture.NewScenario(t)
			cleanup()
			var dump []byte
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c, run := fixture.QueryTableCreateSession(t, p, b)
					x := func(s fixture.Step) { s.Prepared = prepared; run(s) }
					x(fixture.Exec("CREATE ROLE ms_owner", 0))
					if b.PG {
						if len(dump) == 0 {
							t.Fatal("missing actual native dump for PostgreSQL replay")
						}
						if _, err := c.ExecContext(p.Ctx, string(dump)); err != nil {
							t.Fatal("actual native emitted SQL replay", err)
						}
						materializedStorageControlState(x, false, true)
						x(fixture.MaterializedViewUnpopulated("ms_mv"))
						x(fixture.MaterializedViewUnpopulated("ms_empty"))
						x(fixture.Exec("REFRESH MATERIALIZED VIEW ms_mv", 0))
						s := materializedStorageRows()
						s.Rows = append(s.Rows, []any{int64(9), "diverged"})
						x(s)
						x(fixture.Exec("REFRESH MATERIALIZED VIEW ms_empty", 0))
						x(fixture.Select("SELECT id,note FROM ms_empty", []string{"id", "note"}, []string{"INT4", "TEXT"}, nil))
						materializedStorageControlState(x, true, true)
						return
					}
					materializedStorageSeed(x)
					x(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA public TO ms_owner", 0))
					x(fixture.Exec("GRANT SELECT ON ms_source TO ms_owner", 0))
					x(fixture.Exec("CREATE ACCESS METHOD ms_heap2 TYPE TABLE HANDLER heap_tableam_handler", 0))
					// Shared ALTER TABLE spelling seeds persistence independently
					// of the MV-specific spelling exercised by the other scenarios.
					for _, q := range []string{
						"ALTER TABLE ms_mv ALTER id SET STATISTICS 7",
						"ALTER TABLE ms_mv ALTER id SET (n_distinct=2,n_distinct_inherited=-0.5)",
						"ALTER TABLE ms_mv ALTER note SET STORAGE EXTERNAL",
						"ALTER TABLE ms_mv ALTER note SET COMPRESSION pglz",
						"ALTER TABLE ms_mv SET (fillfactor=70,autovacuum_enabled='OFF',toast.autovacuum_enabled=false)",
						"ALTER MATERIALIZED VIEW ms_mv CLUSTER ON ms_b",
						"ALTER MATERIALIZED VIEW ms_mv SET ACCESS METHOD ms_heap2",
						"ALTER MATERIALIZED VIEW ms_mv OWNER TO ms_owner",
					} {
						x(fixture.Exec(q, 0))
					}
					x(fixture.Exec("INSERT INTO ms_source VALUES(9,'diverged')", 1))
					check := func(run func(fixture.Step)) {
						materializedStorageControlState(run, true, false)
						run(materializedStorageRows())
						run(fixture.Select("SELECT id,note FROM ms_empty", []string{"id", "note"}, []string{"INT4", "TEXT"}, nil))
					}
					check(x)
					dump = materializedStorageDump(t, p)
					fresh := fixture.NewFixture(t, false, true)
					freshRun := func(s fixture.Step) {
						s.Prepared = prepared
						fixture.RunStep(t, p.Ctx, fresh.Memory, fresh.MemoryRecord, s)
					}
					freshRun(fixture.Exec("CREATE ROLE ms_owner", 0))
					if err := stuntgres.Restore(p.Ctx, fresh.Native, bytes.NewReader(dump)); err != nil {
						t.Fatal("fresh Driver Restore", err)
					}
					check(freshRun)
					var redump bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, fresh.Native, &redump); err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(dump, redump.Bytes()) {
						t.Fatal("fresh Restore redump changed represented storage state")
					}
					retained, err := c.QueryContext(p.Ctx, "SELECT id,note FROM ms_mv ORDER BY id,note NULLS FIRST")
					if err != nil {
						t.Fatal(err)
					}
					defer retained.Close()
					columns, err := retained.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					fixture.TableDDLColumnTypes(t, false, columns, []string{"id", "note"}, []string{"INT4", "TEXT"}, []bool{true, true})
					x(fixture.Exec("CREATE TEMP TABLE ms_control_temp(v text)", 0))
					x(fixture.Exec("INSERT INTO ms_control_temp VALUES('live temp')", 1))
					x(fixture.Exec("CREATE TABLE ms_sentinel(v text)", 0))
					x(fixture.Exec("INSERT INTO ms_sentinel VALUES('destination')", 1))
					before := materializedStorageDump(t, p)
					unchanged := func() {
						if !bytes.Equal(before, materializedStorageDump(t, p)) {
							t.Fatal("failed Restore published a prefix")
						}
						check(x)
						x(fixture.Select("SELECT v FROM ms_sentinel", []string{"v"}, []string{"TEXT"}, [][]any{{"destination"}}))
						x(fixture.Select("SELECT v FROM ms_control_temp", []string{"v"}, []string{"TEXT"}, [][]any{{"live temp"}}))
					}
					for _, tc := range []struct{ suffix, code, message string }{
						{"ALTER TABLE public.ms_mv SET (fillfactor=80); ALTER TABLE public.ms_mv SET (bogus=1);", "22023", `unrecognized parameter "bogus"`},
						{"ALTER TABLE public.ms_mv SET ACCESS METHOD absent_am;", "42704", `access method "absent_am" does not exist`},
					} {
						err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(string(dump)+tc.suffix))
						want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
						fixture.AssertDiagnostic(t, err, &want)
						unchanged()
					}
					malformed := materializedViewReplaceSnapshotRows(t, dump, "ms_mv", "INSERT INTO public.ms_mv VALUES('not an integer',NULL);\n")
					if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(malformed)); err == nil {
						t.Fatal("malformed actual snapshot admitted")
					}
					unchanged()
					failure := errors.New("materialized storage import reader failed")
					reader := io.MultiReader(bytes.NewReader(dump), fixture.CatalogControlFailingReader{Err: failure})
					if err := stuntgres.Restore(p.Ctx, p.Native, reader); !errors.Is(err, failure) {
						t.Fatal(err)
					}
					unchanged()
					ctx, cancel := context.WithCancel(p.Ctx)
					reader = io.MultiReader(bytes.NewReader(dump), &fixture.CatalogControlCancelReader{Cancel: cancel})
					if err := stuntgres.Restore(ctx, p.Native, reader); !errors.Is(err, context.Canceled) {
						t.Fatal(err)
					}
					cancel()
					unchanged()
					if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil {
						t.Fatal(err)
					}
					check(x)
					if !bytes.Equal(dump, materializedStorageDump(t, p)) {
						t.Fatal("same Driver roundtrip changed exact dump")
					}
					x(fixture.Select("SELECT v FROM ms_control_temp", []string{"v"}, []string{"TEXT"}, [][]any{{"live temp"}}))
					for _, want := range [][]any{{int64(1), nil}, {int64(1), nil}, {int64(2), "two"}} {
						if !retained.Next() {
							t.Fatal("retained old row absent", retained.Err())
						}
						var id int64
						var note any
						if err := retained.Scan(&id, &note); err != nil || id != want[0] || note != want[1] {
							t.Fatal(id, note, err)
						}
					}
					if retained.Next() || retained.Err() != nil {
						t.Fatal("retained old row suffix", retained.Err())
					}
					retained.Close()
				})
			}
		})
	}
}
