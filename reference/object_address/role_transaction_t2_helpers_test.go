package reference_test

import (
	"context"
	"testing"

	"stuntgres/reference/fixture"
)

func roleTransactionT2Cases(t *testing.T, names, schemas []string, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, names)
	for _, schema := range schemas {
		fixture.SchemaReferenceCleanup(t, schema)
	}
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b) })
	}
}

func roleTransactionT2Worker(t *testing.T, p *fixture.Pair, c fixture.Executor, query string, held bool) (func() fixture.RelationOutcome, <-chan struct{}) {
	t.Helper()
	completed := make(chan struct{})
	join := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
		defer close(completed)
		return fixture.RowExecute(ctx, c, query, held)
	})
	return join, completed
}

func roleTransactionT2Success(t *testing.T, outcome fixture.RelationOutcome) {
	t.Helper()
	if outcome.Err != nil {
		t.Fatal(outcome.Err)
	}
	fixture.LockAffected(t, outcome.Result, 0)
}
