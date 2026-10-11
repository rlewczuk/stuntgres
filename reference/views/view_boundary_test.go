package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceViewWriteCapabilityBoundary(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE t(id INT)", 0), fixture.Exec("INSERT INTO t VALUES(7)", 1), fixture.Exec("CREATE VIEW v AS SELECT id FROM t", 0)} {
				run(s)
			}
			queries := []string{"INSERT INTO v VALUES(8)", "UPDATE v SET id=8", "DELETE FROM v"}
			expected := [][][]any{{{int64(7)}, {int64(8)}}, {{int64(8)}, {int64(8)}}, {}}
			for i, q := range queries {
				count := int64(2)
				if i == 0 {
					count = 1
				}
				run(fixture.Exec(q, count))
				s := fixture.Select("SELECT id FROM t", []string{"id"}, []string{"INT4"}, expected[i])
				s.Bag = true
				run(s)
			}
		})
	}
}

func TestReferenceViewInternalCHARStaysProviderBounded(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			if b.PG {
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(`CREATE TABLE bad(x "char")`, 0))
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT x FROM bad", []string{"x"}, []string{"CHAR"}, nil))
			} else {
				s := fixture.ImplicitError(`CREATE TABLE bad(x "char")`, fixture.Diagnostic{Code: "0A000", Message: "unsupported column type", Position: "20"})
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
		})
	}
}
