package reference_test

import (
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSequenceReplacementEpoch(t *testing.T) {
	p := fixture.NewScenario(t)
	ctx := p.Ctx
	c := fixture.SequenceCoreConn(t, ctx, p.Memory)
	fixture.SequenceProbeExec(t, ctx, c, "CREATE SEQUENCE s")
	held, err := c.PrepareContext(ctx, "SELECT nextval('s')")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if err = stuntgres.Reset(ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	fixture.SequenceProbeExec(t, ctx, c, "CREATE SEQUENCE s START 50")
	var v int64
	if err = held.QueryRowContext(ctx).Scan(&v); err == nil {
		t.Fatal("old prepared identity crossed replacement epoch", v)
	}
	fixture.RunStep(t, ctx, c, p.MemoryRecord, fixture.SequenceInt64Step(fixture.Step{Query: "SELECT nextval('s')", SelectRows: true, Columns: []string{"nextval"}, Types: []string{"INT8"}, Rows: [][]any{{int64(50)}}}))
}
