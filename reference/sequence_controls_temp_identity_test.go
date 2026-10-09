package reference_test

import (
	"bytes"
	"stuntgres"
	"testing"
)

func TestReferenceSequenceTemporaryPreparedIdentitySurvivesPersistentControls(t *testing.T) {
	p := newReferenceScenario(t)
	ctx := p.ctx
	db := p.native
	a := sequenceCoreConn(t, ctx, p.memory)
	b := sequenceCoreConn(t, ctx, p.memory)
	sequenceProbeExec(t, ctx, a, "CREATE SEQUENCE persistent")
	sequenceProbeExec(t, ctx, a, "CREATE TEMP SEQUENCE temporary START 50 CACHE 7")
	next, err := a.PrepareContext(ctx, "SELECT nextval('pg_temp.temporary')")
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	current, err := a.PrepareContext(ctx, "SELECT currval('pg_temp.temporary')")
	if err != nil {
		t.Fatal(err)
	}
	defer current.Close()
	var dump bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	nextQuery := "SELECT nextval('pg_temp.temporary')"
	currentQuery := "SELECT currval('pg_temp.temporary')"
	nextHeld := referenceHeldStatement{referenceExecutor: a, statement: next, query: nextQuery}
	currentHeld := referenceHeldStatement{referenceExecutor: a, statement: current, query: currentQuery}
	referenceRunStep(t, ctx, nextHeld, p.memoryRecord, sequenceInt64Step(referenceStep{query: nextQuery, selectRows: true, columns: []string{"nextval"}, types: []string{"INT8"}, rows: [][]any{{int64(50)}}}))
	for i, replace := range []func() error{func() error { return stuntgres.Reset(ctx, db) }, func() error { return stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())) }} {
		if err = replace(); err != nil {
			t.Fatal(err)
		}
		referenceRunStep(t, ctx, currentHeld, p.memoryRecord, sequenceInt64Step(referenceStep{query: currentQuery, selectRows: true, columns: []string{"currval"}, types: []string{"INT8"}, rows: [][]any{{int64(50 + i)}}}))
		sequenceProbeValue(t, ctx, a, "SELECT lastval()", int64(50+i))
		referenceRunStep(t, ctx, nextHeld, p.memoryRecord, sequenceInt64Step(referenceStep{query: nextQuery, selectRows: true, columns: []string{"nextval"}, types: []string{"INT8"}, rows: [][]any{{int64(51 + i)}}}))
		if _, err = b.ExecContext(ctx, "SELECT nextval('pg_temp.temporary')"); err == nil {
			t.Fatal("peer adopted temporary identity")
		}
	}
	// A genuine namespace-environment change must not erase original native
	// replacement provenance when reanalyzing an exact retained temporary OID.
	sequenceProbeExec(t, ctx, a, "SET search_path TO pg_catalog,public")
	referenceRunStep(t, ctx, nextHeld, p.memoryRecord, sequenceInt64Step(referenceStep{query: nextQuery, selectRows: true, columns: []string{"nextval"}, types: []string{"INT8"}, rows: [][]any{{int64(53)}}}))
	sequenceProbeExec(t, ctx, a, "DROP SEQUENCE pg_temp.temporary")
	sequenceProbeExec(t, ctx, a, "CREATE TEMP SEQUENCE temporary START 100")
	var v int64
	if err = next.QueryRowContext(ctx).Scan(&v); err == nil {
		t.Fatal("retired temporary literal adopted recreated identity")
	}
	sequenceProbeValue(t, ctx, a, "SELECT nextval('temporary')", 100)
}
