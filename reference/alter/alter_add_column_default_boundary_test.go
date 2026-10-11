package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterAddColumnMixedDefaultBoundary(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
		run(fixture.Exec("CREATE TABLE t(id INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
		query := "ALTER TABLE t ADD b INT DEFAULT 7, ALTER b SET DEFAULT 9"
		if backend.PG {
			run(fixture.Exec(query, 0))
			run(fixture.Exec("INSERT INTO t(id) VALUES(2)", 1))
			run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(7)}, {int64(2), int64(9)}}))
		} else {
			position := fixture.Position(len([]rune(query[:strings.LastIndex(query, "ALTER ")])) + 1)
			failure := fixture.ImplicitError(query, fixture.Diagnostic{Code: "0A000", Message: "ALTER TABLE action lists are unsupported", Position: position})
			failure.ErrorStage = "execute"
			run(failure)
			run(fixture.Select("SELECT * FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
		}
	}
}
