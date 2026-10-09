package reference_test

import (
	"bytes"
	"context"
	"stuntgres"
	"testing"
	"time"
)

func TestReferenceSequenceRestoreRejectsRecreatedCapturedOwner(t *testing.T) {
	p := newReferenceScenario(t)
	ctx, cancel := context.WithTimeout(p.ctx, 5*time.Second)
	defer cancel()
	db := p.native
	a := sequenceCoreConn(t, ctx, p.memory)
	for _, q := range []string{"CREATE USER imported_owner", "SET SESSION AUTHORIZATION imported_owner", "CREATE SEQUENCE imported", "RESET SESSION AUTHORIZATION", "CREATE SEQUENCE sentinel"} {
		sequenceProbeExec(t, ctx, a, q)
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	sequenceProbeExec(t, ctx, a, "DROP SEQUENCE imported")
	reader := &sequenceGatedReader{started: make(chan struct{}), release: make(chan struct{}), reader: bytes.NewReader(dump.Bytes())}
	done := make(chan error, 1)
	go func() { done <- stuntgres.Restore(ctx, db, reader) }()
	joined := false
	defer func() {
		select {
		case <-reader.release:
		default:
			close(reader.release)
		}
		if !joined {
			<-done
		}
	}()
	select {
	case <-reader.started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	for _, q := range []string{"DROP ROLE imported_owner", "CREATE USER imported_owner"} {
		sequenceProbeExec(t, ctx, a, q)
	}
	close(reader.release)
	err := <-done
	joined = true
	if err == nil {
		t.Fatal("captured owner silently adopted recreated role")
	}
	referenceRunStep(t, ctx, a, p.memoryRecord, sequenceInt64Step(referenceStep{query: "SELECT nextval('sentinel')", selectRows: true, columns: []string{"nextval"}, types: []string{"INT8"}, rows: [][]any{{int64(1)}}}))
	if _, err := a.ExecContext(ctx, "SELECT nextval('imported')"); err == nil {
		t.Fatal("failed restore published imported sequence")
	}
}
