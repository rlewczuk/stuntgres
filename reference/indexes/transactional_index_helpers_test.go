package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func transactionalIndexRun(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor) func(fixture.Step) {
	t.Helper()
	return func(s fixture.Step) { fixture.RunStep(t, p.Ctx, e, b.Recorder, s) }
}

func transactionalIndexName(name string, present bool) fixture.Step {
	var rows [][]any
	if present {
		rows = [][]any{{name}}
	}
	return fixture.Select("SELECT relname FROM pg_class WHERE relnamespace='public'::regnamespace AND relname=$1", []string{"relname"}, []string{"NAME"}, rows, name)
}

func transactionalIndexAborted() fixture.Diagnostic {
	return fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")
}
