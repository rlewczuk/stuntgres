package reference_test

import (
	"bytes"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSequenceTemporaryPreparedIdentitySurvivesPersistentControls(t *testing.T) {
	p := fixture.NewScenario(t)
	ctx := p.Ctx
	db := p.Native
	a := fixture.SequenceCoreConn(t, ctx, p.Memory)
	b := fixture.SequenceCoreConn(t, ctx, p.Memory)
	fixture.SequenceProbeExec(t, ctx, a, "CREATE SEQUENCE persistent")
	fixture.SequenceProbeExec(t, ctx, a, "CREATE TEMP SEQUENCE temporary START 50 CACHE 7")
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
	nextHeld := fixture.HeldStatement{Executor: a, Statement: next, SQL: nextQuery}
	currentHeld := fixture.HeldStatement{Executor: a, Statement: current, SQL: currentQuery}
	fixture.RunStep(t, ctx, nextHeld, p.MemoryRecord, fixture.SequenceInt64Step(fixture.Step{Query: nextQuery, SelectRows: true, Columns: []string{"nextval"}, Types: []string{"INT8"}, Rows: [][]any{{int64(50)}}}))
	for i, replace := range []func() error{func() error { return stuntgres.Reset(ctx, db) }, func() error { return stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())) }} {
		if err = replace(); err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, ctx, currentHeld, p.MemoryRecord, fixture.SequenceInt64Step(fixture.Step{Query: currentQuery, SelectRows: true, Columns: []string{"currval"}, Types: []string{"INT8"}, Rows: [][]any{{int64(50 + i)}}}))
		fixture.SequenceProbeValue(t, ctx, a, "SELECT lastval()", int64(50+i))
		fixture.RunStep(t, ctx, nextHeld, p.MemoryRecord, fixture.SequenceInt64Step(fixture.Step{Query: nextQuery, SelectRows: true, Columns: []string{"nextval"}, Types: []string{"INT8"}, Rows: [][]any{{int64(51 + i)}}}))
		if _, err = b.ExecContext(ctx, "SELECT nextval('pg_temp.temporary')"); err == nil {
			t.Fatal("peer adopted temporary identity")
		}
	}
	// A genuine namespace-environment change must not erase original native
	// replacement provenance when reanalyzing an exact retained temporary OID.
	fixture.SequenceProbeExec(t, ctx, a, "SET search_path TO pg_catalog,public")
	fixture.RunStep(t, ctx, nextHeld, p.MemoryRecord, fixture.SequenceInt64Step(fixture.Step{Query: nextQuery, SelectRows: true, Columns: []string{"nextval"}, Types: []string{"INT8"}, Rows: [][]any{{int64(53)}}}))
	fixture.SequenceProbeExec(t, ctx, a, "DROP SEQUENCE pg_temp.temporary")
	fixture.SequenceProbeExec(t, ctx, a, "CREATE TEMP SEQUENCE temporary START 100")
	var v int64
	if err = next.QueryRowContext(ctx).Scan(&v); err == nil {
		t.Fatal("retired temporary literal adopted recreated identity")
	}
	fixture.SequenceProbeValue(t, ctx, a, "SELECT nextval('temporary')", 100)
}
