package reference_test

import (
	"bytes"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceViewNestedDependencies(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			for _, step := range []fixture.Step{
				fixture.Exec("CREATE TABLE t(id INT)", 0),
				fixture.Exec("CREATE VIEW v AS SELECT id FROM t", 0),
				fixture.Exec("CREATE VIEW w AS SELECT id FROM v", 0),
				fixture.ImplicitError("DROP TABLE t RESTRICT", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop table t because other objects depend on it", Detail: "view v depends on table t\nview w depends on view v", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}),
				fixture.Exec("CREATE SEQUENCE s", 0),
				fixture.Exec("CREATE VIEW sv AS SELECT last_value FROM s", 0),
				fixture.Exec("CREATE VIEW sw AS SELECT last_value FROM sv", 0),
				fixture.ImplicitError("DROP SEQUENCE s RESTRICT", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop sequence s because other objects depend on it", Detail: "view sv depends on sequence s\nview sw depends on view sv", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}),
				fixture.Exec("DROP TABLE t CASCADE", 0),
				fixture.Exec("DROP SEQUENCE s CASCADE", 0),
			} {
				step.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, step)
			}
		})
	}
}

func TestReferenceViewIdentityDependenciesAndRestore(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			for _, s := range []fixture.Step{
				fixture.Exec("CREATE TABLE t(unused INT,id INT,visible BOOL)", 0), fixture.Exec("INSERT INTO t VALUES(0,7,true)", 1),
				fixture.Exec("CREATE VIEW v(k) WITH(security_barrier=true) AS SELECT id FROM t WHERE visible", 0), fixture.Exec("CREATE VIEW w AS SELECT k FROM v", 0),
				fixture.Exec("ALTER TABLE t RENAME TO renamed", 0), fixture.Exec("ALTER TABLE renamed RENAME COLUMN id TO new_id", 0), fixture.Exec("ALTER TABLE renamed DROP COLUMN unused", 0), fixture.Exec("CREATE TABLE t(id INT)", 0), fixture.Exec("INSERT INTO t VALUES(99)", 1),
			} {
				run(s)
			}
			run(fixture.Select("SELECT k FROM w", []string{"k"}, []string{"INT4"}, [][]any{{int64(7)}}))
			s := fixture.ImplicitError("ALTER TABLE renamed DROP COLUMN visible", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop column visible of table renamed because other objects depend on it", Detail: "view v depends on column visible of table renamed\nview w depends on view v", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
			s.ErrorStage = "execute"
			run(s)
			if b.PG {
				run(fixture.Exec("INSERT INTO renamed VALUES(8,true)", 1))
				run(fixture.Select("SELECT k FROM w ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(8)}}))
			}
		})
	}
	dump := fixture.ViewDump(t, p)
	t.Run("native_destination", func(t *testing.T) {
		destination := fixture.NewFixture(t, false, true)
		fixture.ViewRestore(t, destination, dump)
		run := func(s fixture.Step) {
			fixture.RunStep(t, destination.Ctx, destination.Memory, destination.MemoryRecord, s)
		}
		run(fixture.Select("SELECT k FROM w", []string{"k"}, []string{"INT4"}, [][]any{{int64(7)}}))
		run(fixture.Exec("INSERT INTO renamed VALUES(8,true)", 1))
		s := fixture.Select("SELECT k FROM w ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(8)}})
		run(s)
		if err := stuntgres.Restore(destination.Ctx, destination.Native, bytes.NewBufferString("CREATE TABLE replacement(i INT); CREATE VIEW broken AS SELECT missing FROM replacement;")); err == nil {
			t.Fatal("invalid restore accepted")
		}
		run(s)
	})
}
