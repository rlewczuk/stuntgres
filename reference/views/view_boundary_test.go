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

func TestReferenceViewInternalCHARStorage(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(`CREATE TABLE view_char_source(x "char")`, 0))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(`CREATE VIEW view_char_values AS SELECT x FROM view_char_source`, 0))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT x FROM view_char_values", []string{"x"}, []string{"CHAR"}, nil))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(`INSERT INTO view_char_source VALUES('r'),(''),(NULL)`, 3))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT x FROM view_char_values ORDER BY x NULLS LAST", []string{"x"}, []string{"CHAR"}, [][]any{{""}, {"r"}, {nil}}))
		})
	}
}
