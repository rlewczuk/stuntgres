package reference_test

import (
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceAlterSchemaRetainedMetadata(t *testing.T) {
	p := newReferenceScenario(t)
	schemaReferenceCleanup(t, "s1", "s2")
	c, err := p.memory.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	referenceAlterSchemaSetup(t, p, c, p.memoryRecord)
	if err = c.Close(); err != nil {
		t.Fatal(err)
	}
	held, err := p.native.QueryContext(p.ctx, "SELECT id,note FROM s1.t ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	before, err := held.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	referenceTableDDLColumnTypes(t, false, before, []string{"id", "note"}, []string{"INT4", "TEXT"}, []bool{false, true})
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceExec("ALTER TABLE s1.t SET SCHEMA s2", 0))
	canonical := referenceDDLNativeDump(t, p)
	if err = stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	if err = stuntgres.Restore(p.ctx, p.native, strings.NewReader(canonical)); err != nil {
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
	if p.postgres != nil {
		// The canonical stream ends with SELECT setval, whose PG command tag is SELECT 1.
		referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceExec(canonical, 1))
		referenceTableDDLMetadata(t, p.ctx, p.postgres, true, true, "SELECT id,note FROM s2.t ORDER BY id", []string{"id", "note"}, []string{"INT4", "TEXT"}, []bool{false, true}, 2)
		referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceAlterSchemaRows("SELECT id,note FROM s2.t ORDER BY id"))
	}
}

func TestReferenceAlterSchemaViewsAndFailedRestore(t *testing.T) {
	p := newReferenceScenario(t)
	schemaReferenceCleanup(t, "s1", "s2")
	for _, backend := range p.backends() {
		func() {
			c, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			r := backend.recorder
			referenceAlterSchemaSetup(t, p, c, r)
			for _, q := range []string{"CREATE VIEW public.pairs AS SELECT l.id AS left_id,r.id AS right_id FROM s1.t AS l JOIN s1.t AS r ON l.id<r.id", "CREATE VIEW public.nested AS SELECT left_id,right_id FROM public.pairs", "ALTER TABLE s1.t SET SCHEMA s2"} {
				referenceRunStep(t, p.ctx, c, r, referenceExec(q, 0))
			}
			for _, name := range []string{"pairs", "nested"} {
				referenceRunStep(t, p.ctx, c, r, referenceSelect("SELECT left_id,right_id FROM public."+name, []string{"left_id", "right_id"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(9)}}))
			}
			var definition string
			if err = c.QueryRowContext(p.ctx, "SELECT definition FROM pg_catalog.pg_views WHERE viewname='pairs'").Scan(&definition); err != nil {
				t.Fatal(err)
			}
			source, old := `"s2"."t"`, `"s1"."t"`
			if backend.pg {
				source, old = "s2.t", "s1.t"
			}
			if !strings.Contains(definition, source) || strings.Contains(definition, old) {
				t.Fatal("stale source namespace", definition)
			}
			referenceRunStep(t, p.ctx, c, r, referenceExec("CREATE VIEW public.replayed AS "+definition, 0))
			referenceRunStep(t, p.ctx, c, r, referenceSelect("SELECT left_id,right_id FROM public.replayed", []string{"left_id", "right_id"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(9)}}))
			if !backend.pg {
				before := referenceDDLNativeDump(t, p)
				_, err = c.ExecContext(p.ctx, "DROP TABLE s2.t RESTRICT")
				// Original dependent-drop contract specifies SQLSTATE; the complete detail depends on all three views.
				referenceState(t, err, "2BP01")
				if referenceDDLNativeDump(t, p) != before {
					t.Fatal("restricted DROP changed dependent catalog")
				}
				if err = stuntgres.Restore(p.ctx, p.native, strings.NewReader(before+"CREATE TABLE late_duplicate(id INT); CREATE TABLE late_duplicate(id INT);")); err == nil {
					t.Fatal("late failing Restore accepted")
				}
				if referenceDDLNativeDump(t, p) != before {
					t.Fatal("failed Restore replaced moved catalog")
				}
			}
			referenceRunStep(t, p.ctx, c, r, referenceAlterSchemaRows("SELECT id,note FROM s2.t ORDER BY id"))
		}()
	}
}
