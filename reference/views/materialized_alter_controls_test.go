package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMaterializedAlterControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"alter_control_owner"})
	fixture.SchemaReferenceCleanup(t, "alter_addon", "alter_control", "alter_control_source")
	p := fixture.NewScenario(t)
	var dump []byte
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, run := fixture.QueryTableCreateSession(t, p, b)
			if b.PG {
				// Replay needs ordinary PUBLIC lookup, not blanket CREATE.
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO PUBLIC", 0))
			}
			run(fixture.Exec("CREATE USER alter_control_owner", 0))
			run(fixture.Exec("CREATE SCHEMA alter_control_source AUTHORIZATION alter_control_owner", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION alter_control_owner", 0))
			run(fixture.Exec("SET search_path=alter_control_source,public", 0))
			materializedViewSeed(run)
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("RESET search_path", 0))
			run(fixture.Exec("ALTER TABLE alter_control_source.mv_base SET SCHEMA public", 0))
			run(fixture.Exec("DROP SCHEMA alter_control_source", 0))
			run(fixture.Exec("CREATE MATERIALIZED VIEW alter_mv AS SELECT i,s FROM mv_base", 3))
			alterExtensionSeed(run)
			alterIndexSeed(run)
			run(fixture.Exec("CREATE MATERIALIZED VIEW alter_no_data AS SELECT i FROM mv_base WITH NO DATA", 0))
			run(fixture.Exec("ALTER MATERIALIZED VIEW alter_no_data RENAME COLUMN i TO key", 0))
			run(fixture.Exec("CREATE SCHEMA alter_control AUTHORIZATION alter_control_owner", 0))
			run(fixture.Exec("ALTER MATERIALIZED VIEW alter_mv RENAME COLUMN i TO key", 0))
			run(fixture.Exec("ALTER MATERIALIZED VIEW alter_mv RENAME TO saved", 0))
			run(fixture.Exec("ALTER MATERIALIZED VIEW saved SET SCHEMA alter_control", 0))
			run(fixture.Exec("ALTER MATERIALIZED VIEW alter_control.saved OWNER TO alter_control_owner", 0))
			run(fixture.Exec("ALTER MATERIALIZED VIEW alter_control.saved DEPENDS ON EXTENSION unaccent", 0))
			run(fixture.Exec("ALTER MATERIALIZED VIEW indexed DEPENDS ON EXTENSION unaccent", 0))
			run(fixture.Exec("INSERT INTO mv_base VALUES(3,'c')", 1))
			run(fixture.Exec("REFRESH MATERIALIZED VIEW alter_control.saved", 0))
			// Source ownership authorizes refresh without materializing an explicit ACL.
			run(fixture.Exec("INSERT INTO mv_base VALUES(4,'diverged')", 1))
			run(fixture.Exec("INSERT INTO index_source VALUES(3,'new')", 1))
			check := func(run func(fixture.Step)) {
				run(fixture.MaterializedViewUnpopulated("alter_no_data"))
				fixture.MaterializedViewPopulation(run, "alter_no_data", false)
				run(alterBag("alter_control.saved", alterFour))
				fixture.MaterializedViewPopulation(run, "saved", true)
				alterX(run, "alter_control.saved", 1)
				alterX(run, "indexed", 1)
				run(fixture.Select("SELECT matviewowner FROM pg_matviews WHERE matviewname='saved'", []string{"matviewowner"}, []string{"NAME"}, [][]any{{"alter_control_owner"}}))
				run(fixture.Select("SELECT i,s FROM indexed ORDER BY i", []string{"i", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(2), nil}}))
				run(fixture.Select("SELECT pg_get_indexdef('mv_u'::regclass) AS d", []string{"d"}, []string{"TEXT"}, [][]any{{"CREATE UNIQUE INDEX mv_u ON public.indexed USING btree (i) INCLUDE (s)"}}))
			}
			check(run)
			if !b.PG {
				retained, err := p.Native.QueryContext(p.Ctx, "SELECT i,s FROM indexed ORDER BY i")
				if err != nil {
					t.Fatal(err)
				}
				defer retained.Close()
				var saved bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &saved); err != nil {
					t.Fatal(err)
				}
				dump = bytes.Clone(saved.Bytes())
				for range 2 {
					if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil {
						t.Fatal(err)
					}
					check(run)
				}
				fixture.CatalogControlTuples(t, p.Native, "SELECT i,s FROM indexed ORDER BY i", [][]any{{int64(1), "a"}, {int64(2), nil}})
				peer := sql.OpenDB(p.NativeConnector)
				defer peer.Close()
				check(func(s fixture.Step) { fixture.RunStep(t, p.Ctx, peer, p.MemoryRecord, s) })
				fresh := fixture.NewFixture(t, false, true)
				fixture.RunStep(t, p.Ctx, fresh.Memory, fresh.MemoryRecord, fixture.Exec("CREATE USER alter_control_owner", 0))
				if err := stuntgres.Restore(p.Ctx, fresh.Native, bytes.NewReader(dump)); err != nil {
					t.Fatal(err)
				}
				check(func(s fixture.Step) { fixture.RunStep(t, p.Ctx, fresh.Memory, fresh.MemoryRecord, s) })
				cols, err := retained.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				fixture.TableDDLColumnTypes(t, false, cols, []string{"i", "s"}, []string{"INT4", "TEXT"}, []bool{true, true})
				var rows [][]any
				for retained.Next() {
					var i int64
					var s any
					if err := retained.Scan(&i, &s); err != nil {
						t.Fatal(err)
					}
					rows = append(rows, []any{i, s})
				}
				if err := retained.Err(); err != nil {
					t.Fatal(err)
				}
				if len(rows) != 2 || rows[0][0] != int64(1) || rows[0][1] != "a" || rows[1][0] != int64(2) || rows[1][1] != nil {
					t.Fatal("retained indexed rows", rows)
				}
			} else if dump != nil {
				run(fixture.Exec("DROP MATERIALIZED VIEW alter_no_data", 0))
				run(fixture.Exec("DROP SCHEMA alter_control CASCADE", 0))
				run(fixture.Exec("DROP MATERIALIZED VIEW indexed", 0))
				run(fixture.Exec("DROP TABLE mv_base,index_source", 0))
				run(fixture.Exec("DROP SCHEMA alter_addon CASCADE", 0))
				fixture.WithPublicSchemaOwner(t, p, b, "alter_control_owner", func() {
					if _, err := c.ExecContext(p.Ctx, string(dump)); err != nil {
						t.Fatal("actual emitted SQL replay", err)
					}
				})
				unpopulated := fixture.MaterializedViewUnpopulated("saved")
				unpopulated.Query = "SELECT * FROM alter_control.saved"
				run(unpopulated)
				run(fixture.MaterializedViewUnpopulated("indexed"))
				alterX(run, "alter_control.saved", 1)
				alterX(run, "indexed", 1)
				run(fixture.Exec("REFRESH MATERIALIZED VIEW alter_control.saved", 0))
				run(alterBag("alter_control.saved", append(append([][]any{}, alterFour...), []any{int64(4), "diverged"})))
			}
		})
	}
}
func TestReferenceMaterializedAlterControlFailures(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"alter_import_outsider"})
	fixture.SchemaReferenceCleanup(t, "imported_schema")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, run := fixture.QueryTableCreateSession(t, p, b)
			run(fixture.Exec("CREATE USER alter_import_outsider", 0))
			alterSeed(run)
			alterIndexSeed(run)
			run(fixture.Exec("UPDATE index_source SET s='diverged' WHERE i=1", 1))
			run(fixture.Exec("INSERT INTO index_source VALUES(3,'later')", 1))
			var retained *sql.Rows
			if !b.PG {
				var err error
				retained, err = p.Native.QueryContext(p.Ctx, "SELECT i,s FROM indexed ORDER BY i")
				if err != nil {
					t.Fatal(err)
				}
				defer retained.Close()
			}
			var parent, index int64
			if err := c.QueryRowContext(p.Ctx, "SELECT 'indexed'::regclass::oid,'mv_u'::regclass::oid").Scan(&parent, &index); err != nil {
				t.Fatal(err)
			}
			check := func() {
				run(fixture.Select("SELECT i,s FROM indexed ORDER BY i", []string{"i", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(2), nil}}))
				run(fixture.Select("SELECT pg_get_indexdef('mv_u'::regclass) AS d", []string{"d"}, []string{"TEXT"}, [][]any{{"CREATE UNIQUE INDEX mv_u ON public.indexed USING btree (i) INCLUDE (s)"}}))
				run(fixture.Select("SELECT relowner=(SELECT oid FROM pg_roles WHERE rolname=current_user) AS owner FROM pg_class WHERE relname='indexed'", []string{"owner"}, []string{"BOOL"}, [][]any{{true}}))
				fixture.MaterializedViewPopulation(run, "indexed", true)
				var currentParent, currentIndex int64
				if err := c.QueryRowContext(p.Ctx, "SELECT 'indexed'::regclass::oid,'mv_u'::regclass::oid").Scan(&currentParent, &currentIndex); err != nil {
					t.Fatal(err)
				}
				if currentParent != parent || currentIndex != index {
					t.Fatal("failed candidate changed indexed sentinel identities")
				}
			}
			check()
			consumeIndex := func() {
				// Exercise the retained genuine binding/root through failure and
				// recovery, rather than treating a dump string as index proof.
				run(fixture.Exec("INSERT INTO index_source VALUES(1,'duplicate')", 1))
				want := fixture.ControlsReferenceDiagnostic("23505", `could not create unique index "mv_u"`)
				want.Detail = "Key (i)=(1) is duplicated."
				want.Schema, want.Table, want.Constraint = "public", "indexed", "mv_u"
				alterError(t, p, c, "REFRESH MATERIALIZED VIEW indexed", want, "execute")
				check()
				run(fixture.Exec("DELETE FROM index_source WHERE s='duplicate'", 1))
				run(fixture.Exec("REFRESH MATERIALIZED VIEW indexed", 0))
				run(fixture.Select("SELECT i,s FROM indexed ORDER BY i", []string{"i", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "diverged"}, {int64(2), nil}, {int64(3), "later"}}))
				fixture.MaterializedViewPopulation(run, "indexed", true)
			}
			var before bytes.Buffer
			if !b.PG {
				if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
					t.Fatal(err)
				}
			}
			for _, tc := range []struct {
				name, stream string
				want         fixture.Diagnostic
				nativeOnly   bool
			}{
				{"missing_provider", "CREATE MATERIALIZED VIEW public.imported AS SELECT 1 AS i WITH NO DATA; ALTER MATERIALIZED VIEW public.imported DEPENDS ON EXTENSION absent_ext;", fixture.ControlsReferenceDiagnostic("42704", `extension "absent_ext" does not exist`), false},
				{"missing_provider_remove", "CREATE MATERIALIZED VIEW public.imported AS SELECT 1 AS i WITH NO DATA; ALTER MATERIALIZED VIEW public.imported NO DEPENDS ON EXTENSION absent_ext;", fixture.ControlsReferenceDiagnostic("42704", `extension "absent_ext" does not exist`), false},
				{"unauthorized_target", "CREATE SCHEMA imported_schema AUTHORIZATION alter_import_outsider; CREATE EXTENSION unaccent SCHEMA imported_schema; CREATE MATERIALIZED VIEW imported_schema.imported AS SELECT 1 AS i WITH NO DATA; SET SESSION AUTHORIZATION alter_import_outsider; ALTER MATERIALIZED VIEW imported_schema.imported DEPENDS ON EXTENSION unaccent;", fixture.ControlsReferenceDiagnostic("42501", "must be owner of materialized view imported"), false},
				{"unauthorized_target_remove", "CREATE SCHEMA imported_schema AUTHORIZATION alter_import_outsider; CREATE EXTENSION unaccent SCHEMA imported_schema; CREATE MATERIALIZED VIEW imported_schema.imported AS SELECT 1 AS i WITH NO DATA; SET SESSION AUTHORIZATION alter_import_outsider; ALTER MATERIALIZED VIEW imported_schema.imported NO DEPENDS ON EXTENSION unaccent;", fixture.ControlsReferenceDiagnostic("42501", "must be owner of materialized view imported"), false},
				{"wrong_kind", "CREATE EXTENSION unaccent; CREATE TABLE public.imported(i INT); ALTER MATERIALIZED VIEW public.imported DEPENDS ON EXTENSION unaccent;", fixture.ControlsReferenceDiagnostic("42809", `"imported" is not a materialized view`), false},
				{"indexed_collision", "CREATE MATERIALIZED VIEW public.imported AS SELECT 1 AS i WITH NO DATA; CREATE UNIQUE INDEX imported_u ON public.imported(i); CREATE TABLE public.imported_u(i INT);", fixture.ControlsReferenceDiagnostic("42P07", `relation "imported_u" already exists`), false},
				{"type_only_collision", "CREATE MATERIALIZED VIEW public.imported AS SELECT 1 AS i WITH NO DATA; CREATE TYPE public.imported AS ENUM ('ok');", fixture.ControlsReferenceDiagnostic("42710", `type "imported" already exists`), false},
				{"excluded_metadata_replay", "CREATE MATERIALIZED VIEW public.imported AS SELECT 1 AS i WITH NO DATA; ALTER MATERIALIZED VIEW public.imported RENAME TO other;", fixture.ControlsReferenceDiagnostic("0A000", "restore accepts only materialized extension dependency ALTER"), true},
				{"conditional_dependency_grammar", "CREATE MATERIALIZED VIEW public.imported AS SELECT 1 AS i WITH NO DATA; ALTER MATERIALIZED VIEW IF EXISTS public.imported DEPENDS ON EXTENSION unaccent;", fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near "DEPENDS"`), false},
			} {
				if b.PG && tc.nativeOnly {
					continue
				}
				if tc.name == "conditional_dependency_grammar" {
					tc.want.Position = strconv.Itoa(strings.Index(tc.stream, "DEPENDS") + 1)
				}
				var err error
				if b.PG {
					// Genuine transactional server candidate complements the native
					// replacement API; rollback preserves the independent sentinel.
					tx, beginErr := c.BeginTx(p.Ctx, nil)
					if beginErr != nil {
						t.Fatal(beginErr)
					}
					_, err = tx.ExecContext(p.Ctx, tc.stream)
					if rollbackErr := tx.Rollback(); rollbackErr != nil {
						t.Fatal(rollbackErr)
					}
				} else {
					err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(tc.stream))
				}
				if err == nil {
					t.Errorf("%s candidate unexpectedly succeeded", tc.name)
				} else if got := fixture.ExtractDiagnostic(t, err); got != tc.want {
					t.Errorf("ORACLE %s candidate diagnostic=%#v want=%#v", tc.name, got, tc.want)
				}
				check()
				if !b.PG {
					var after bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(before.Bytes(), after.Bytes()) {
						t.Fatal("failed candidate changed complete target", tc.name)
					}
				}
			}
			if b.PG {
				consumeIndex()
				return // Reader/cancel control API has no fabricated server counterpart.
			}
			sentinel := errors.New("alter reader failure")
			if err := stuntgres.Restore(p.Ctx, p.Native, fixture.CatalogControlFailingReader{Err: sentinel}); !errors.Is(err, sentinel) {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(p.Ctx)
			defer cancel()
			if err := stuntgres.Restore(ctx, p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel}); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			check()
			cols, err := retained.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			fixture.TableDDLColumnTypes(t, false, cols, []string{"i", "s"}, []string{"INT4", "TEXT"}, []bool{true, true})
			var rows [][]any
			for retained.Next() {
				var i int64
				var s any
				if err := retained.Scan(&i, &s); err != nil {
					t.Fatal(err)
				}
				rows = append(rows, []any{i, s})
			}
			if err := retained.Err(); err != nil {
				t.Fatal(err)
			}
			if len(rows) != 2 || rows[0][0] != int64(1) || rows[0][1] != "a" || rows[1][0] != int64(2) || rows[1][1] != nil {
				t.Fatal("failed candidates changed captured indexed rows", rows)
			}
			var after bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before.Bytes(), after.Bytes()) {
				t.Fatal("reader failure/cancel changed complete indexed target")
			}
			consumeIndex()
		})
	}
}
func TestReferenceMaterializedAlterFeatureSmoke(t *testing.T) {
	t.Run("identity", TestReferenceMaterializedAlterIdentity)
	t.Run("definition", TestReferenceMaterializedAlterDefinition)
	t.Run("indexes", TestReferenceMaterializedAlterIndexRefreshAtomicity)
	t.Run("extension", TestReferenceMaterializedAlterExtension)
	t.Run("retirement", TestReferenceMaterializedAlterExtensionRetirement)
	t.Run("controls", TestReferenceMaterializedAlterControls)
}
