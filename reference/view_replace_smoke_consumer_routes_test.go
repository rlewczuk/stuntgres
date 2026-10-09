package reference_test

import (
	"bytes"
	"testing"

	"stuntgres"
)

func TestReferenceViewReplaceSmokeCycleReplayControls(t *testing.T) {
	schemaReferenceCleanup(t, "view_smoke_cycle")
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			db, recorder := b.db, b.recorder
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, db, recorder, s) }
			for _, s := range []referenceStep{
				referenceExec("CREATE SCHEMA view_smoke_cycle", 0), referenceExec("CREATE TABLE u(id INT,txt TEXT)", 0), referenceExec("INSERT INTO u VALUES(2,'b')", 1),
				referenceExec("CREATE VIEW v AS SELECT id+10 AS id,txt,TRUE AS extra FROM u", 0), referenceExec("CREATE VIEW w AS SELECT id FROM v", 0),
				referenceExec("ALTER VIEW v SET(security_invoker=false)", 0), referenceExec("ALTER VIEW v RESET(security_barrier)", 0),
				referenceExec("CREATE OR REPLACE VIEW v AS SELECT id+10 AS id,txt,TRUE AS extra FROM u", 0),
				referenceExec("ALTER VIEW v RENAME COLUMN id TO key", 0), referenceExec("ALTER VIEW v RENAME TO renamed", 0), referenceExec("ALTER VIEW renamed SET SCHEMA view_smoke_cycle", 0),
				referenceSelect("SELECT id FROM w", []string{"id"}, []string{"INT4"}, [][]any{{int64(12)}}),
				referenceExec("CREATE VIEW cyclic AS SELECT id FROM u", 0), referenceExec("CREATE OR REPLACE VIEW cyclic AS SELECT id FROM cyclic", 0),
			} {
				run(s)
			}
			rows, err := db.QueryContext(p.ctx, "SELECT id FROM cyclic")
			if rows != nil {
				rows.Close()
				t.Fatal("cyclic read returned rows")
			}
			want := controlsReferenceDiagnostic("42P17", "infinite recursion detected in rules for relation \"cyclic\"")
			referenceAssertDiagnostic(t, err, &want)
			if !b.pg {
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.ctx, p.native, &dump); err == nil || err.Error() != "stuntgres: cyclic view dump dependencies" {
					t.Fatal(err)
				}
			}
			run(referenceExec("CREATE OR REPLACE VIEW cyclic AS SELECT id FROM u", 0))
			if !b.pg {
				dump := referenceViewDump(t, p)
				restored := newReferenceFixture(t, false, true)
				if err := stuntgres.Restore(p.ctx, restored.native, bytes.NewReader(dump)); err != nil {
					t.Fatal(err)
				}
				db, recorder = restored.memory, restored.memoryRecord
			}
			run(referenceExec("INSERT INTO u VALUES(3,'c')", 1))
			run(referenceSelect("SELECT id FROM w ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(12)}, {int64(13)}}))
			run(referenceSelect("SELECT key,txt,extra FROM view_smoke_cycle.renamed ORDER BY key", []string{"key", "txt", "extra"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(12), "b", true}, {int64(13), "c", true}}))
		})
	}
}
