package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterAddColumnMixedDefaultBoundary(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
		run(fixture.Exec("CREATE TABLE t(id INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
		run(fixture.Exec("ALTER TABLE t ADD b INT DEFAULT 7, ALTER b SET DEFAULT 9", 0))
		run(fixture.Exec("INSERT INTO t(id) VALUES(2)", 1))
		run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(7)}, {int64(2), int64(9)}}))
	}
}
