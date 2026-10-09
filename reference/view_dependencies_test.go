package reference_test

import (
	"bytes"
	"stuntgres"
	"testing"
)

func TestReferenceViewNestedDependencies(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			for _, step := range []referenceStep{
				referenceExec("CREATE TABLE t(id INT)", 0),
				referenceExec("CREATE VIEW v AS SELECT id FROM t", 0),
				referenceExec("CREATE VIEW w AS SELECT id FROM v", 0),
				referenceImplicitError("DROP TABLE t RESTRICT", referenceDiagnostic{Code: "2BP01", Message: "cannot drop table t because other objects depend on it", Detail: "view v depends on table t\nview w depends on view v", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}),
				referenceExec("CREATE SEQUENCE s", 0),
				referenceExec("CREATE VIEW sv AS SELECT last_value FROM s", 0),
				referenceExec("CREATE VIEW sw AS SELECT last_value FROM sv", 0),
				referenceImplicitError("DROP SEQUENCE s RESTRICT", referenceDiagnostic{Code: "2BP01", Message: "cannot drop sequence s because other objects depend on it", Detail: "view sv depends on sequence s\nview sw depends on view sv", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}),
				referenceExec("DROP TABLE t CASCADE", 0),
				referenceExec("DROP SEQUENCE s CASCADE", 0),
			} {
				step.errorStage = "execute"
				referenceRunStep(t, p.ctx, b.db, b.recorder, step)
			}
		})
	}
}

func TestReferenceViewIdentityDependenciesAndRestore(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
			for _, s := range []referenceStep{
				referenceExec("CREATE TABLE t(unused INT,id INT,visible BOOL)", 0), referenceExec("INSERT INTO t VALUES(0,7,true)", 1),
				referenceExec("CREATE VIEW v(k) WITH(security_barrier=true) AS SELECT id FROM t WHERE visible", 0), referenceExec("CREATE VIEW w AS SELECT k FROM v", 0),
				referenceExec("ALTER TABLE t RENAME TO renamed", 0), referenceExec("ALTER TABLE renamed RENAME COLUMN id TO new_id", 0), referenceExec("ALTER TABLE renamed DROP COLUMN unused", 0), referenceExec("CREATE TABLE t(id INT)", 0), referenceExec("INSERT INTO t VALUES(99)", 1),
			} {
				run(s)
			}
			run(referenceSelect("SELECT k FROM w", []string{"k"}, []string{"INT4"}, [][]any{{int64(7)}}))
			s := referenceImplicitError("ALTER TABLE renamed DROP COLUMN visible", referenceDiagnostic{Code: "2BP01", Message: "cannot drop column visible of table renamed because other objects depend on it", Detail: "view v depends on column visible of table renamed\nview w depends on view v", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
			s.errorStage = "execute"
			run(s)
			if b.pg {
				run(referenceExec("INSERT INTO renamed VALUES(8,true)", 1))
				run(referenceSelect("SELECT k FROM w ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(8)}}))
			}
		})
	}
	dump := referenceViewDump(t, p)
	t.Run("native_destination", func(t *testing.T) {
		destination := newReferenceFixture(t, false, true)
		referenceViewRestore(t, destination, dump)
		run := func(s referenceStep) {
			referenceRunStep(t, destination.ctx, destination.memory, destination.memoryRecord, s)
		}
		run(referenceSelect("SELECT k FROM w", []string{"k"}, []string{"INT4"}, [][]any{{int64(7)}}))
		run(referenceExec("INSERT INTO renamed VALUES(8,true)", 1))
		s := referenceSelect("SELECT k FROM w ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(8)}})
		run(s)
		if err := stuntgres.Restore(destination.ctx, destination.native, bytes.NewBufferString("CREATE TABLE replacement(i INT); CREATE VIEW broken AS SELECT missing FROM replacement;")); err == nil {
			t.Fatal("invalid restore accepted")
		}
		run(s)
	})
}
