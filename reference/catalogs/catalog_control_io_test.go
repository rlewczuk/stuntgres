package reference_test

import (
	"bytes"
	"context"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceCatalogControlsReleaseLocksDuringIO(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p := fixture.NewScenario(t)
	fixture.CatalogControlSQLWitness(t, p)
	d := p.NativeConnector.Driver().(*stuntgres.Driver)
	db := fixture.CatalogControlPool(t, d, "reference/"+t.Name())
	other := fixture.CatalogControlPool(t, d, "reference/"+t.Name())
	fixture.CatalogControlExec(t, db, "CREATE TABLE public.sentinel (v INT)")
	fixture.CatalogControlExec(t, db, "INSERT INTO public.sentinel VALUES (77)")
	writer := &fixture.CatalogControlGatedWriter{Started: make(chan struct{}), Release: make(chan struct{})}
	dumpDone := make(chan error, 1)
	go func() { dumpDone <- stuntgres.Dump(ctx, db, writer) }()
	dumpReleased, dumpJoined := false, false
	defer func() {
		cancel()
		if !dumpReleased {
			close(writer.Release)
		}
		if !dumpJoined {
			<-dumpDone
		}
	}()
	select {
	case <-writer.Started:
	case <-ctx.Done():
		t.Fatal("Dump did not reach gated writer")
	}
	operation := make(chan error, 1)
	go func() {
		if err := fixture.CatalogControlValues(ctx, other, "SELECT v FROM sentinel", []int64{77}); err != nil {
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
		close(writer.Release)
		dumpReleased = true
		<-dumpDone
		dumpJoined = true
		t.Fatal("Dump held engine lock during writer I/O")
	}
	close(writer.Release)
	dumpReleased = true
	err := <-dumpDone
	dumpJoined = true
	if err != nil {
		t.Fatal(err)
	}
	independent := fixture.CatalogControlPool(t, stuntgres.NewDriver(), "dump_copy")
	if err := stuntgres.Restore(ctx, independent, bytes.NewReader(writer.Output.Bytes())); err != nil {
		t.Fatal(err)
	}
	fixture.CatalogControlTuples(t, independent, "SELECT v FROM sentinel", [][]any{{int64(77)}})
	reader := &fixture.CatalogControlGatedReader{Started: make(chan struct{}), Release: make(chan struct{})}
	restoreDone := make(chan error, 1)
	go func() { restoreDone <- stuntgres.Restore(ctx, db, reader) }()
	restoreReleased, restoreJoined := false, false
	defer func() {
		cancel()
		if !restoreReleased {
			close(reader.Release)
		}
		if !restoreJoined {
			<-restoreDone
		}
	}()
	select {
	case <-reader.Started:
	case <-ctx.Done():
		t.Fatal("Restore did not reach gated reader")
	}
	readDone := make(chan error, 1)
	go func() {
		readDone <- fixture.CatalogControlValues(ctx, other, "SELECT v FROM sentinel ORDER BY v", []int64{77, 88})
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
		close(reader.Release)
		restoreReleased = true
		<-restoreDone
		restoreJoined = true
		t.Fatal("Restore held engine lock during reader I/O")
	}
	close(reader.Release)
	restoreReleased = true
	err = <-restoreDone
	restoreJoined = true
	if err != nil {
		t.Fatal(err)
	}
	fixture.CatalogControlTuples(t, other, "SELECT v FROM replaced", [][]any{{int64(99)}})
}
