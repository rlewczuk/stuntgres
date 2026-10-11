package reference_test

import (
	"bytes"
	"context"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceSequenceRestoreRejectsRecreatedCapturedOwner(t *testing.T) {
	p := fixture.NewScenario(t)
	ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
	defer cancel()
	db := p.Native
	a := fixture.SequenceCoreConn(t, ctx, p.Memory)
	for _, q := range []string{"CREATE USER imported_owner", "SET SESSION AUTHORIZATION imported_owner", "CREATE SEQUENCE imported", "RESET SESSION AUTHORIZATION", "CREATE SEQUENCE sentinel"} {
		fixture.SequenceProbeExec(t, ctx, a, q)
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	fixture.SequenceProbeExec(t, ctx, a, "DROP SEQUENCE imported")
	reader := &fixture.SequenceGatedReader{Started: make(chan struct{}), Release: make(chan struct{}), Reader: bytes.NewReader(dump.Bytes())}
	done := make(chan error, 1)
	go func() { done <- stuntgres.Restore(ctx, db, reader) }()
	joined := false
	defer func() {
		select {
		case <-reader.Release:
		default:
			close(reader.Release)
		}
		if !joined {
			<-done
		}
	}()
	select {
	case <-reader.Started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	for _, q := range []string{"DROP ROLE imported_owner", "CREATE USER imported_owner"} {
		fixture.SequenceProbeExec(t, ctx, a, q)
	}
	close(reader.Release)
	err := <-done
	joined = true
	if err == nil {
		t.Fatal("captured owner silently adopted recreated role")
	}
	fixture.RunStep(t, ctx, a, p.MemoryRecord, fixture.SequenceInt64Step(fixture.Step{Query: "SELECT nextval('sentinel')", SelectRows: true, Columns: []string{"nextval"}, Types: []string{"INT8"}, Rows: [][]any{{int64(1)}}}))
	if _, err := a.ExecContext(ctx, "SELECT nextval('imported')"); err == nil {
		t.Fatal("failed restore published imported sequence")
	}
}
