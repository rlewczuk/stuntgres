package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceViewSequenceCatalogPrivateVisibility(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			owner, peer := fixture.LockConnections(t, p.Ctx, b.DB)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, owner, b.Recorder, s) }
			run(fixture.Exec("CREATE SEQUENCE s", 0))
			run(fixture.Exec("CREATE VIEW sequence_view AS SELECT last_value FROM s", 0))
			run(fixture.Exec("BEGIN", 0))
			defer owner.ExecContext(p.Ctx, "ROLLBACK")
			run(fixture.Exec("DROP SEQUENCE s CASCADE", 0))
			queries := []struct{ query, column string }{
				{"SELECT viewname FROM pg_catalog.pg_views WHERE viewname='sequence_view'", "viewname"},
				{"SELECT relname FROM pg_catalog.pg_class WHERE relname='sequence_view'", "relname"},
			}
			for _, q := range queries {
				run(fixture.Select(q.query, []string{q.column}, []string{"NAME"}, nil))
				fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select(q.query, []string{q.column}, []string{"NAME"}, [][]any{{"sequence_view"}}))
			}
			run(fixture.Exec("ROLLBACK", 0))
			for _, q := range queries {
				run(fixture.Select(q.query, []string{q.column}, []string{"NAME"}, [][]any{{"sequence_view"}}))
			}
			run(fixture.Select("SELECT last_value FROM sequence_view", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(1)}}))
		})
	}
}
