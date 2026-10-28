package reference_test

import (
	"context"
	"errors"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func mcvControlIO(t *testing.T, s ndistinctScenario) {
	t.Helper()
	ctx, cancel := context.WithCancel(s.pair.Ctx)
	defer cancel()
	writer := &fixture.CatalogControlGatedWriter{Started: make(chan struct{}), Release: make(chan struct{})}
	done := make(chan error, 1)
	go func() { done <- stuntgres.Dump(ctx, s.pair.Native, writer) }()
	released, joined := false, false
	defer func() {
		cancel()
		if !released {
			close(writer.Release)
		}
		if !joined {
			<-done
		}
	}()
	select {
	case <-writer.Started:
	case <-ctx.Done():
		t.Fatal("MCV Dump never reached external writer")
	}
	// The writer has admitted and is blocked. A public distribution read must
	// complete before that external party permits I/O to continue.
	mcvAssert(t, s, "mcv_s", mcvFive...)
	close(writer.Release)
	released = true
	select {
	case err := <-done:
		joined = true
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	reader := &fixture.CatalogControlGatedReader{Started: make(chan struct{}), Release: make(chan struct{})}
	restoreCtx, stop := context.WithCancel(s.pair.Ctx)
	defer stop()
	restoreDone := make(chan error, 1)
	go func() { restoreDone <- stuntgres.Restore(restoreCtx, s.pair.Native, reader) }()
	readerReleased, restoreJoined := false, false
	defer func() {
		stop()
		if !readerReleased {
			close(reader.Release)
		}
		if !restoreJoined {
			<-restoreDone
		}
	}()
	select {
	case <-reader.Started:
	case <-restoreCtx.Done():
		t.Fatal("MCV Restore never reached external reader")
	}
	mcvAssert(t, s, "mcv_s", mcvFive...)
	stop()
	close(reader.Release)
	readerReleased = true
	select {
	case err := <-restoreDone:
		restoreJoined = true
		if !errors.Is(err, context.Canceled) {
			t.Fatal("canceled blocked MCV Restore", err)
		}
	case <-s.pair.Ctx.Done():
		t.Fatal(s.pair.Ctx.Err())
	}
	mcvAssert(t, s, "mcv_s", mcvFive...)
}
