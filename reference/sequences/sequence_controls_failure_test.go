package reference_test

import (
	"bytes"
	"context"
	"errors"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSequenceControlFailuresPreserveStateAndAbortedTransaction(t *testing.T) {
	p := fixture.NewScenario(t)
	ctx := p.Ctx
	db := p.Native
	a := fixture.SequenceCoreConn(t, ctx, p.Memory)
	b := fixture.SequenceCoreConn(t, ctx, p.Memory)
	fixture.SequenceProbeExec(t, ctx, a, "CREATE SEQUENCE sentinel")
	fixture.SequenceProbeValue(t, ctx, a, "SELECT nextval('sentinel')", 1)
	held, err := a.PrepareContext(ctx, "SELECT nextval('sentinel')")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	failure := errors.New("sequence control I/O failure")
	if err = stuntgres.Dump(ctx, db, fixture.SequenceFailingWriter{failure}); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if err = stuntgres.Restore(ctx, db, fixture.SequenceFailingReader{failure}); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err = stuntgres.Restore(canceled, db, bytes.NewBufferString("CREATE SEQUENCE replacement;")); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, stream := range []string{"CREATE SEQUENCE replacement; SELECT setval('replacement',$1,true);", "CREATE SEQUENCE replacement; SELECT setval('replacement',1,true,false);", "SET SESSION AUTHORIZATION missing_owner; CREATE SEQUENCE replacement;"} {
		if err = stuntgres.Restore(ctx, db, bytes.NewBufferString(stream)); err == nil {
			t.Fatal("invalid stream accepted", stream)
		}
	}
	q := "SELECT nextval('sentinel')"
	retained := fixture.HeldStatement{Executor: a, Statement: held, SQL: q}
	fixture.RunStep(t, ctx, retained, p.MemoryRecord, fixture.SequenceInt64Step(fixture.Step{Query: q, SelectRows: true, Columns: []string{"nextval"}, Types: []string{"INT8"}, Rows: [][]any{{int64(2)}}}))
	fixture.SequenceProbeExec(t, ctx, a, "BEGIN")
	_, err = a.ExecContext(ctx, "SELECT currval('missing')")
	if err == nil {
		t.Fatal("missing relation accepted")
	}
	if err = stuntgres.Restore(ctx, db, bytes.NewBufferString("CREATE SEQUENCE bad CACHE 0;")); err == nil {
		t.Fatal("bad restore accepted")
	}
	_, err = a.ExecContext(ctx, "SELECT nextval('sentinel')")
	fixture.SequenceNativeDiagnostic(t, err, stuntgres.Error{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
	fixture.SequenceProbeExec(t, ctx, a, "ROLLBACK")
	fixture.SequenceProbeValue(t, ctx, b, "SELECT nextval('sentinel')", 3)
}
