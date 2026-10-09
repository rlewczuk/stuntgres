package reference_test

import (
	"bytes"
	"context"
	"stuntgres"
	"testing"
	"time"
)

func TestReferenceCatalogControlsReleaseLocksDuringIO(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p := newReferenceScenario(t)
	catalogControlSQLWitness(t, p)
	d := p.nativeConnector.Driver().(*stuntgres.Driver)
	db := catalogControlPool(t, d, "reference/"+t.Name())
	other := catalogControlPool(t, d, "reference/"+t.Name())
	catalogControlExec(t, db, "CREATE TABLE public.sentinel (v INT)")
	catalogControlExec(t, db, "INSERT INTO public.sentinel VALUES (77)")
	writer := &catalogControlGatedWriter{started: make(chan struct{}), release: make(chan struct{})}
	dumpDone := make(chan error, 1)
	go func() { dumpDone <- stuntgres.Dump(ctx, db, writer) }()
	dumpReleased, dumpJoined := false, false
	defer func() {
		cancel()
		if !dumpReleased {
			close(writer.release)
		}
		if !dumpJoined {
			<-dumpDone
		}
	}()
	select {
	case <-writer.started:
	case <-ctx.Done():
		t.Fatal("Dump did not reach gated writer")
	}
	operation := make(chan error, 1)
	go func() {
		if err := catalogControlValues(ctx, other, "SELECT v FROM sentinel", []int64{77}); err != nil {
			operation <- err
			return
		}
		_, err := other.ExecContext(ctx, "INSERT INTO sentinel VALUES (88)")
		operation <- err
	}()
	operationJoined := false
	defer func() {
		cancel()
		if !operationJoined {
			<-operation
		}
	}()
	select {
	case err := <-operation:
		operationJoined = true
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		close(writer.release)
		dumpReleased = true
		<-dumpDone
		dumpJoined = true
		t.Fatal("Dump held engine lock during writer I/O")
	}
	close(writer.release)
	dumpReleased = true
	err := <-dumpDone
	dumpJoined = true
	if err != nil {
		t.Fatal(err)
	}
	independent := catalogControlPool(t, stuntgres.NewDriver(), "dump_copy")
	if err := stuntgres.Restore(ctx, independent, bytes.NewReader(writer.output.Bytes())); err != nil {
		t.Fatal(err)
	}
	catalogControlTuples(t, independent, "SELECT v FROM sentinel", [][]any{{int64(77)}})
	reader := &catalogControlGatedReader{started: make(chan struct{}), release: make(chan struct{})}
	restoreDone := make(chan error, 1)
	go func() { restoreDone <- stuntgres.Restore(ctx, db, reader) }()
	restoreReleased, restoreJoined := false, false
	defer func() {
		cancel()
		if !restoreReleased {
			close(reader.release)
		}
		if !restoreJoined {
			<-restoreDone
		}
	}()
	select {
	case <-reader.started:
	case <-ctx.Done():
		t.Fatal("Restore did not reach gated reader")
	}
	readDone := make(chan error, 1)
	go func() {
		readDone <- catalogControlValues(ctx, other, "SELECT v FROM sentinel ORDER BY v", []int64{77, 88})
	}()
	readJoined := false
	defer func() {
		cancel()
		if !readJoined {
			<-readDone
		}
	}()
	select {
	case err := <-readDone:
		readJoined = true
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		close(reader.release)
		restoreReleased = true
		<-restoreDone
		restoreJoined = true
		t.Fatal("Restore held engine lock during reader I/O")
	}
	close(reader.release)
	restoreReleased = true
	err = <-restoreDone
	restoreJoined = true
	if err != nil {
		t.Fatal(err)
	}
	catalogControlTuples(t, other, "SELECT v FROM replaced", [][]any{{int64(99)}})
}
