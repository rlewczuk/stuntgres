package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

const matviewProbeSQL = "SELECT * FROM pg_catalog.pg_matviews WHERE schemaname=$1 AND matviewname=$2"

func matviewProbe(b fixture.Backend, schema, name, owner string, populated bool, definition string) fixture.Step {
	return fixture.PGTableViewSelect(b, matviewProbeSQL, fixture.PGMatviewColumns, fixture.PGMatviewTypes, [][]any{fixture.PGMatviewRow(schema, name, owner, false, populated, definition)}, schema, name)
}

func matviewCheck(run func(fixture.Step), b fixture.Backend, schema, name, owner string, populated bool, definition string) {
	s := matviewProbe(b, schema, name, owner, populated, definition)
	run(s)
	run(fixture.Prepared(s))
}

func matviewFrontier(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, name, definition string, present bool) {
	t.Helper()
	s := matviewProbe(b, "public", name, fixture.PGTableViewsOwner(b), true, definition)
	if !present {
		s.Rows = nil
	}
	fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Prepared(s))
}
