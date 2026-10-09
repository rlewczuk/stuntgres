package reference_test

import (
	"bytes"
	"context"
	"errors"
	"stuntgres"
	"testing"
)

func TestReferenceSequenceControlFailuresPreserveStateAndAbortedTransaction(t *testing.T) {
	p := newReferenceScenario(t)
	ctx := p.ctx
	db := p.native
	a := sequenceCoreConn(t, ctx, p.memory)
	b := sequenceCoreConn(t, ctx, p.memory)
	sequenceProbeExec(t, ctx, a, "CREATE SEQUENCE sentinel")
	sequenceProbeValue(t, ctx, a, "SELECT nextval('sentinel')", 1)
	held, err := a.PrepareContext(ctx, "SELECT nextval('sentinel')")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	failure := errors.New("sequence control I/O failure")
	if err = stuntgres.Dump(ctx, db, sequenceFailingWriter{failure}); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if err = stuntgres.Restore(ctx, db, sequenceFailingReader{failure}); !errors.Is(err, failure) {
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
	retained := referenceHeldStatement{referenceExecutor: a, statement: held, query: q}
	referenceRunStep(t, ctx, retained, p.memoryRecord, sequenceInt64Step(referenceStep{query: q, selectRows: true, columns: []string{"nextval"}, types: []string{"INT8"}, rows: [][]any{{int64(2)}}}))
	sequenceProbeExec(t, ctx, a, "BEGIN")
	_, err = a.ExecContext(ctx, "SELECT currval('missing')")
	if err == nil {
		t.Fatal("missing relation accepted")
	}
	if err = stuntgres.Restore(ctx, db, bytes.NewBufferString("CREATE SEQUENCE bad CACHE 0;")); err == nil {
		t.Fatal("bad restore accepted")
	}
	_, err = a.ExecContext(ctx, "SELECT nextval('sentinel')")
	sequenceNativeDiagnostic(t, err, stuntgres.Error{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
	sequenceProbeExec(t, ctx, a, "ROLLBACK")
	sequenceProbeValue(t, ctx, b, "SELECT nextval('sentinel')", 3)
}
