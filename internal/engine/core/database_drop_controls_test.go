package core_test

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/parser"
)

type databaseDropReader struct {
	entered, release chan struct{}
	once             sync.Once
	source           io.Reader
}

func (r *databaseDropReader) Read(p []byte) (int, error) {
	r.once.Do(func() { close(r.entered); <-r.release })
	return r.source.Read(p)
}

type databaseDropWriter struct {
	entered, release chan struct{}
	once             sync.Once
	bytes.Buffer
}

func (w *databaseDropWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.entered); <-w.release })
	return w.Buffer.Write(p)
}

func (w *databaseDropWriter) WriteString(s string) (int, error) {
	w.once.Do(func() { close(w.entered); <-w.release })
	return w.Buffer.WriteString(s)
}

func databaseDropOrdinaryRetired(t *testing.T, err error) {
	t.Helper()
	if err == nil || err.Error() != "stuntgres: database retired before control publication" {
		t.Fatal("retired control outcome", err)
	}
	if _, ok := err.(interface{ SQLState() string }); ok {
		t.Fatal("native control fabricated SQL diagnostic", err)
	}
}

func TestDatabaseDropControls(t *testing.T) {
	for _, mode := range []struct {
		name       string
		simulation bool
	}{{"no_simulation", false}, {"active_simulation", true}} {
		t.Run(mode.name, func(t *testing.T) { databaseDropControlsIO(t, mode.simulation) })
	}
}

func databaseDropControlsIO(t *testing.T, simulation bool) {
	e, target, admin := databaseDropControlFixture(t, simulation)
	enginetest.Run(t, target, "CREATE TABLE drop_control_backing(v integer)")
	enginetest.Run(t, target, "INSERT INTO drop_control_backing VALUES(11)")
	enginetest.Run(t, target, "CREATE INDEX drop_control_index ON drop_control_backing(v)")
	prior := target.CatalogRead()
	ref := enginetest.RoleTableRef(t, target, "drop_control_backing")
	stored := target.Storage.Relations[ref.Address]
	ctx, cancel := context.WithTimeout(t.Context(), enginetest.Liveness)
	authority := databaseDropDetachedAuthority(t, target)
	defer cancel()
	reader := &databaseDropReader{entered: make(chan struct{}), release: make(chan struct{}), source: strings.NewReader("CREATE TABLE public.drop_control_replayed(v integer); INSERT INTO public.drop_control_replayed VALUES(99);")}
	writer := &databaseDropWriter{entered: make(chan struct{}), release: make(chan struct{})}
	restoreDone, dumpDone, dropDone := make(chan error, 1), make(chan error, 1), make(chan error, 1)
	restoreStarted, restoreJoined, dumpJoined, dropStarted, dropJoined := false, false, false, false, false
	var release sync.Once
	unblock := func() { release.Do(func() { close(reader.release); close(writer.release) }) }
	defer func() {
		unblock()
		cancel()
		if restoreStarted && !restoreJoined {
			<-restoreDone
		}
		if !dumpJoined {
			<-dumpDone
		}
		if dropStarted && !dropJoined {
			<-dropDone
		}
	}()
	go func() { dumpDone <- engine.Dump(ctx, target, writer) }()
	select {
	case <-writer.entered:
	case err := <-dumpDone:
		dumpJoined = true
		t.Fatal("Dump returned before writer admission", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	restoreStarted = true
	go func() { restoreDone <- engine.RestoreWithAuthority(ctx, target, reader, authority) }()
	select {
	case <-reader.entered:
	case err := <-restoreDone:
		restoreJoined = true
		t.Fatal("Restore returned before reader admission", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// Keep DROP releasable even if a regression holds metadata/replacement
	// authority across reader or writer I/O. Cleanup opens both barriers and
	// cancels the operation before joining every launched worker.
	dropStarted = true
	go func() {
		_, err := databaseDropOwnerExecute(ctx, e, admin, databaseDropOwnerStatement(false))
		dropDone <- err
	}()
	dropErr := databaseDropReceive(t, dropDone)
	dropJoined = true
	if dropErr != nil {
		t.Fatal(dropErr)
	}
	if _, err := databaseDropOwnerExecute(ctx, e, admin, &parser.CreateDatabaseStmt{Name: target.DatabaseName}); err != nil {
		t.Fatal(err)
	}
	fresh, err := e.SelectDatabase(ctx, target.DatabaseName)
	if err != nil {
		t.Fatal(err)
	}
	freshRoot, peerRoot := fresh.CatalogRead().State(), e.CatalogRead().State()
	unblock()
	restoreErr := databaseDropReceive(t, restoreDone)
	restoreJoined = true
	dumpErr := databaseDropReceive(t, dumpDone)
	dumpJoined = true
	databaseDropOrdinaryRetired(t, restoreErr)
	if dumpErr != nil {
		t.Fatal("completed immutable capture lost I/O lifetime", dumpErr)
	}
	if writer.Len() == 0 || prior.Get(ref.Address) == nil || len(stored.Versions) != 1 {
		t.Fatal("detached capture/backing was mutated")
	}
	databaseDropOrdinaryRetired(t, engine.Reset(ctx, target))
	databaseDropOrdinaryRetired(t, engine.Restore(ctx, target, strings.NewReader("CREATE TABLE resurrected(v integer)")))
	var out bytes.Buffer
	databaseDropOrdinaryRetired(t, engine.Dump(ctx, target, &out))
	if out.Len() != 0 {
		t.Fatal("stale uncaptured runtime emitted a dump")
	}
	_, _, _, _, _, err = target.InspectIndex(ctx, "public", "drop_control_index")
	databaseDropOrdinaryRetired(t, err)
	if stage, err := target.RestoreStage(); stage != nil {
		t.Fatal("retired runtime built a replay stage")
	} else {
		databaseDropOrdinaryRetired(t, err)
	}
	if fresh.CatalogRead().State() != freshRoot || e.CatalogRead().State() != peerRoot || len(target.Storage.Relations) != 0 {
		t.Fatal("stale controls mutated new incarnation or peer authority")
	}
	// A completed old capture remains a usable independent replay input, never a
	// license to reinstall into the retired destination.
	replay := engine.New()
	if simulation {
		// Dump carries genuine sepgsql declarations and explicit labels. Replay
		// needs the same active runtime, not a provider-free destination that
		// rejects those definitions or silently loses their policy authority.
		replay.Mu.Lock()
		err := replay.InstallSepgsqlSimulation(target.Sepgsql, true)
		if err == nil {
			replay.InitializeSepgsqlSeedLabels()
		}
		replay.Mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := engine.Restore(ctx, replay, bytes.NewReader(writer.Bytes())); err != nil {
		t.Fatal(err)
	}
	if replay.CatalogRead().Get(enginetest.RoleTableRef(t, replay, "drop_control_backing").Address) == nil {
		t.Fatal("detached capture was not replayable")
	}
}
