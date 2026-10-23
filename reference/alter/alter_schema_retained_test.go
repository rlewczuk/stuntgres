package reference_test

import (
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterSchemaRetainedMetadata(t *testing.T) {
	p := fixture.NewScenario(t)
	fixture.SchemaReferenceCleanup(t, "s1", "s2")
	c, err := p.Memory.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	referenceAlterSchemaSetup(t, p, c, p.MemoryRecord)
	if err = c.Close(); err != nil {
		t.Fatal(err)
	}
	held, err := p.Native.QueryContext(p.Ctx, "SELECT id,note FROM s1.t ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	before, err := held.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	fixture.TableDDLColumnTypes(t, false, before, []string{"id", "note"}, []string{"INT4", "TEXT"}, []bool{false, true})
	fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Exec("ALTER TABLE s1.t SET SCHEMA s2", 0))
	canonical := referenceDDLNativeDump(t, p)
	if err = stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(canonical)); err != nil {
		t.Fatal(err)
	}
	after, err := held.ColumnTypes()
	if err != nil || len(after) != len(before) {
		t.Fatal(after, err)
	}
	for i, column := range after {
		nullable, known := column.Nullable()
		oldNullable, oldKnown := before[i].Nullable()
		if column.Name() != before[i].Name() || column.DatabaseTypeName() != before[i].DatabaseTypeName() || column.ScanType() != before[i].ScanType() || nullable != oldNullable || known != oldKnown {
			t.Fatal("retained metadata changed", i, column)
		}
	}
	referenceSchemaHeldRows(t, held)
	if referenceDDLNativeDump(t, p) != canonical {
		t.Fatal("retained descriptor replay changed bytes")
	}
	if p.Postgres != nil {
		// The canonical stream ends with SELECT setval, whose PG command tag is SELECT 1.
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Exec(canonical, 1))
		fixture.TableDDLMetadata(t, p.Ctx, p.Postgres, true, true, "SELECT id,note FROM s2.t ORDER BY id", []string{"id", "note"}, []string{"INT4", "TEXT"}, []bool{false, true}, 2)
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, referenceAlterSchemaRows("SELECT id,note FROM s2.t ORDER BY id"))
	}
}

func TestReferenceAlterSchemaViewsAndFailedRestore(t *testing.T) {
	p := fixture.NewScenario(t)
	fixture.SchemaReferenceCleanup(t, "s1", "s2")
	for _, backend := range p.Backends() {
		func() {
			c, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			r := backend.Recorder
			referenceAlterSchemaSetup(t, p, c, r)
			for _, q := range []string{"CREATE VIEW public.pairs AS SELECT l.id AS left_id,r.id AS right_id FROM s1.t AS l JOIN s1.t AS r ON l.id<r.id", "CREATE VIEW public.nested AS SELECT left_id,right_id FROM public.pairs", "ALTER TABLE s1.t SET SCHEMA s2"} {
				fixture.RunStep(t, p.Ctx, c, r, fixture.Exec(q, 0))
			}
			for _, name := range []string{"pairs", "nested"} {
				fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT left_id,right_id FROM public."+name, []string{"left_id", "right_id"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(9)}}))
			}
			var definition string
			if err = c.QueryRowContext(p.Ctx, "SELECT definition FROM pg_catalog.pg_views WHERE viewname='pairs'").Scan(&definition); err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE VIEW public.replayed AS "+definition, 0))
			fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT left_id,right_id FROM public.replayed", []string{"left_id", "right_id"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(9)}}))
			if !backend.PG {
				before := referenceDDLNativeDump(t, p)
				_, err = c.ExecContext(p.Ctx, "DROP TABLE s2.t RESTRICT")
				// Original dependent-drop contract specifies SQLSTATE; the complete detail depends on all three views.
				fixture.State(t, err, "2BP01")
				if referenceDDLNativeDump(t, p) != before {
					t.Fatal("restricted DROP changed dependent catalog")
				}
				if err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(before+"CREATE TABLE late_duplicate(id INT); CREATE TABLE late_duplicate(id INT);")); err == nil {
					t.Fatal("late failing Restore accepted")
				}
				if referenceDDLNativeDump(t, p) != before {
					t.Fatal("failed Restore replaced moved catalog")
				}
			}
			fixture.RunStep(t, p.Ctx, c, r, referenceAlterSchemaRows("SELECT id,note FROM s2.t ORDER BY id"))
		}()
	}
}
