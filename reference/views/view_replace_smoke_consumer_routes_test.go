package reference_test

import (
	"bytes"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceViewReplaceSmokeCycleReplayControls(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "view_smoke_cycle")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			db, recorder := b.DB, b.Recorder
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, db, recorder, s) }
			for _, s := range []fixture.Step{
				fixture.Exec("CREATE SCHEMA view_smoke_cycle", 0), fixture.Exec("CREATE TABLE u(id INT,txt TEXT)", 0), fixture.Exec("INSERT INTO u VALUES(2,'b')", 1),
				fixture.Exec("CREATE VIEW v AS SELECT id+10 AS id,txt,TRUE AS extra FROM u", 0), fixture.Exec("CREATE VIEW w AS SELECT id FROM v", 0),
				fixture.Exec("ALTER VIEW v SET(security_invoker=false)", 0), fixture.Exec("ALTER VIEW v RESET(security_barrier)", 0),
				fixture.Exec("CREATE OR REPLACE VIEW v AS SELECT id+10 AS id,txt,TRUE AS extra FROM u", 0),
				fixture.Exec("ALTER VIEW v RENAME COLUMN id TO key", 0), fixture.Exec("ALTER VIEW v RENAME TO renamed", 0), fixture.Exec("ALTER VIEW renamed SET SCHEMA view_smoke_cycle", 0),
				fixture.Select("SELECT id FROM w", []string{"id"}, []string{"INT4"}, [][]any{{int64(12)}}),
				fixture.Exec("CREATE VIEW cyclic AS SELECT id FROM u", 0), fixture.Exec("CREATE OR REPLACE VIEW cyclic AS SELECT id FROM cyclic", 0),
			} {
				run(s)
			}
			rows, err := db.QueryContext(p.Ctx, "SELECT id FROM cyclic")
			if rows != nil {
				rows.Close()
				t.Fatal("cyclic read returned rows")
			}
			want := fixture.ControlsReferenceDiagnostic("42P17", "infinite recursion detected in rules for relation \"cyclic\"")
			fixture.AssertDiagnostic(t, err, &want)
			if !b.PG {
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err == nil || err.Error() != "stuntgres: cyclic view dump dependencies" {
					t.Fatal(err)
				}
			}
			run(fixture.Exec("CREATE OR REPLACE VIEW cyclic AS SELECT id FROM u", 0))
			if !b.PG {
				dump := fixture.ViewDump(t, p)
				restored := fixture.NewFixture(t, false, true)
				if err := stuntgres.Restore(p.Ctx, restored.Native, bytes.NewReader(dump)); err != nil {
					t.Fatal(err)
				}
				db, recorder = restored.Memory, restored.MemoryRecord
			}
			run(fixture.Exec("INSERT INTO u VALUES(3,'c')", 1))
			run(fixture.Select("SELECT id FROM w ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(12)}, {int64(13)}}))
			run(fixture.Select("SELECT key,txt,extra FROM view_smoke_cycle.renamed ORDER BY key", []string{"key", "txt", "extra"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(12), "b", true}, {int64(13), "c", true}}))
		})
	}
}
