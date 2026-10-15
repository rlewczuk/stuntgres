package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"sync"
	"testing"
	"time"
)

// Called only in the native control phase, after independently checked SQL.
// Barriers witness external I/O rather than relying on scheduling or sleeps.
func jsonbRecordControlIO(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, dump []byte, checks []fixture.Step) {
	t.Helper()
	ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
	defer cancel()
	writer := &fixture.CatalogControlGatedWriter{Started: make(chan struct{}), Release: make(chan struct{})}
	var release sync.Once
	done := make(chan error, 1)
	joined := false
	go func() { done <- stuntgres.Dump(ctx, p.Native, writer) }()
	defer func() {
		cancel()
		release.Do(func() { close(writer.Release) })
		if !joined {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("Dump worker did not join")
			}
		}
	}()
	select {
	case <-writer.Started:
	case err := <-done:
		joined = true
		t.Fatal("Dump ended before writer admission", err)
	case <-ctx.Done():
		t.Fatal("Dump writer admission", ctx.Err())
	}
	// External writer I/O must not retain an engine lock blocking ordinary SQL.
	fixture.RunStep(t, ctx, c, b.Recorder, checks[0])
	release.Do(func() { close(writer.Release) })
	select {
	case err := <-done:
		joined = true
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("Dump worker join", ctx.Err())
	}
	if !bytes.Equal(dump, writer.Output.Bytes()) {
		t.Fatal("gated Dump changed bytes")
	}

	restoreCtx, restoreCancel := context.WithCancel(ctx)
	defer restoreCancel()
	reader := &fixture.SequenceGatedReader{Started: make(chan struct{}), Release: make(chan struct{}), Reader: bytes.NewReader(dump)}
	var releaseReader sync.Once
	restored := make(chan error, 1)
	restoreJoined := false
	go func() { restored <- stuntgres.Restore(restoreCtx, p.Native, reader) }()
	defer func() {
		restoreCancel()
		releaseReader.Do(func() { close(reader.Release) })
		if !restoreJoined {
			select {
			case <-restored:
			case <-time.After(5 * time.Second):
				t.Error("Restore worker did not join")
			}
		}
	}()
	select {
	case <-reader.Started:
	case err := <-restored:
		restoreJoined = true
		t.Fatal("Restore ended before reader admission", err)
	case <-ctx.Done():
		t.Fatal("Restore reader admission", ctx.Err())
	}
	fixture.RunStep(t, ctx, c, b.Recorder, checks[0])
	restoreCancel()
	releaseReader.Do(func() { close(reader.Release) })
	select {
	case err := <-restored:
		restoreJoined = true
		fixture.LexicalOrdinary(t, err, context.Canceled)
	case <-ctx.Done():
		t.Fatal("Restore worker join", ctx.Err())
	}
	for _, s := range checks {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
}
