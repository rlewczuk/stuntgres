package reference_test

import (
	"stuntgres"
	"testing"
)

func TestReferenceSequenceReplacementEpoch(t *testing.T) {
	p := newReferenceScenario(t)
	ctx := p.ctx
	c := sequenceCoreConn(t, ctx, p.memory)
	sequenceProbeExec(t, ctx, c, "CREATE SEQUENCE s")
	held, err := c.PrepareContext(ctx, "SELECT nextval('s')")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if err = stuntgres.Reset(ctx, p.native); err != nil {
		t.Fatal(err)
	}
	sequenceProbeExec(t, ctx, c, "CREATE SEQUENCE s START 50")
	var v int64
	if err = held.QueryRowContext(ctx).Scan(&v); err == nil {
		t.Fatal("old prepared identity crossed replacement epoch", v)
	}
	referenceRunStep(t, ctx, c, p.memoryRecord, sequenceInt64Step(referenceStep{query: "SELECT nextval('s')", selectRows: true, columns: []string{"nextval"}, types: []string{"INT8"}, rows: [][]any{{int64(50)}}}))
}
