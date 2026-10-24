package engine

import (
	"errors"
	"slices"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func wantErrorText(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || err.Error() != want {
		t.Fatalf("got %v, want %q", err, want)
	}
}

// Each LatestRow kind keeps its own gate: UPDATE and MERGE need the
// reference's epoch, DELETE only the candidate's storage identity, and a
// conflict update the probed parent's row shape. Missing rows report the
// kind's own predecessor error.
func TestStatementHostLatestRowGates(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE t(v INT)")
	sessionSQL(t, s, "INSERT INTO t VALUES(1),(2)")
	scope, b, source := deleteProbeScope(t, s)
	h := scope.writeHost()
	version := source.Committed[0]
	deletion := visibility.DeleteCandidate{Ref: b.Ref, Storage: b.Storage, Row: version.Row, Version: version.Version}
	update := dml.UpdateCandidate{RowVersion: version}
	stale := b.Ref
	stale.Epoch++

	for _, r := range []dml.RowRequest{dml.UpdateRowRequest(b.Ref, update), dml.MergeRowRequest(b.Ref, update), dml.DeleteRowRequest(deletion)} {
		latest, err := h.LatestRow(r)
		if err != nil || !latest.Live || latest.Version.Row != version.Row || latest.Version.Version != version.Version {
			t.Fatal(r.Kind, latest, err)
		}
	}
	for _, r := range []dml.RowRequest{dml.UpdateRowRequest(stale, update), dml.MergeRowRequest(stale, update)} {
		if _, err := h.LatestRow(r); !errors.Is(err, ids.ErrCatalogReplaced) {
			t.Fatal(r.Kind, "stale epoch admitted", err)
		}
	}
	staleDeletion := deletion
	staleDeletion.Ref = stale
	if latest, err := h.LatestRow(dml.DeleteRowRequest(staleDeletion)); err != nil || latest.Version.Row != version.Row {
		t.Fatal("deletion gates on storage identity, not the epoch", latest, err)
	}
	copied := *b.Storage
	moved := deletion
	moved.Storage = &copied
	if _, err := h.LatestRow(dml.DeleteRowRequest(moved)); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("copied storage admitted", err)
	}

	missing := update
	missing.Row = version.Row + 1000
	missingDeletion := deletion
	missingDeletion.Row = missing.Row
	_, err := h.LatestRow(dml.UpdateRowRequest(b.Ref, missing))
	wantErrorText(t, err, "stuntgres: invalid update predecessor")
	_, err = h.LatestRow(dml.MergeRowRequest(b.Ref, missing))
	wantErrorText(t, err, "stuntgres: invalid MERGE predecessor")
	_, err = h.LatestRow(dml.DeleteRowRequest(missingDeletion))
	wantErrorText(t, err, "stuntgres: invalid deletion predecessor")

	reshaped := *b.Relation
	reshaped.Columns = append(slices.Clone(reshaped.Columns), catalog.Column{Name: "extra"})
	reshaped.Attributes = append(slices.Clone(reshaped.Attributes), int32(len(reshaped.Columns)))
	conflict := dml.RowRequest{Kind: dml.RowConflictUpdate, Ref: b.Ref, Row: version.Row, Parent: &reshaped}
	if _, err := h.LatestRow(conflict); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("changed parent shape admitted", err)
	}
	_, err = h.LatestRow(dml.RowRequest{Kind: dml.RowConflictUpdate + 1, Ref: b.Ref, Row: version.Row})
	wantErrorText(t, err, "stuntgres: invalid latest row kind")
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	lockEmpty(t, e)
}

// Under a retained snapshot, UPDATE and DELETE fail on any later committed
// change, while MERGE follows a later update and fails only on a later
// delete.
func TestStatementHostLatestRowIsolation(t *testing.T) {
	e := New()
	reader := sessionStart(t, e, nil)
	writer := sessionStart(t, e, nil)
	sessionSQL(t, writer, "CREATE TABLE t(v INT)")
	sessionSQL(t, writer, "INSERT INTO t VALUES(1),(2)")
	sessionSQL(t, reader, "BEGIN ISOLATION LEVEL REPEATABLE READ")
	sessionSQL(t, reader, "SELECT v FROM t")
	sessionSQL(t, writer, "UPDATE t SET v = 10 WHERE v = 1")
	sessionSQL(t, writer, "DELETE FROM t WHERE v = 2")
	scope, b, source := deleteProbeScope(t, reader)
	h := scope.writeHost()
	if len(source.Committed) != 2 {
		t.Fatal("snapshot rows", len(source.Committed))
	}
	var updated, deleted dml.UpdateCandidate
	for _, version := range source.Committed {
		if v, _ := version.Values[0].Integer(); v == 1 {
			updated.RowVersion = version
		} else {
			deleted.RowVersion = version
		}
	}
	_, err := h.LatestRow(dml.UpdateRowRequest(b.Ref, updated))
	wantSerialization(t, err, "could not serialize access due to concurrent update")
	_, err = h.LatestRow(dml.UpdateRowRequest(b.Ref, deleted))
	wantSerialization(t, err, "could not serialize access due to concurrent delete")
	_, err = h.LatestRow(dml.DeleteRowRequest(visibility.DeleteCandidate{Ref: b.Ref, Storage: b.Storage, Row: deleted.Row, Version: deleted.Version}))
	wantSerialization(t, err, "could not serialize access due to concurrent delete")
	followed, err := h.LatestRow(dml.MergeRowRequest(b.Ref, updated))
	if err != nil || !followed.Live || followed.Version.Version == updated.Version {
		t.Fatal("MERGE did not follow the committed update", followed, err)
	}
	_, err = h.LatestRow(dml.MergeRowRequest(b.Ref, deleted))
	wantSerialization(t, err, "could not serialize access due to concurrent delete")
	_ = scope.Finish(err)
}

func wantSerialization(t *testing.T, err error, message string) {
	t.Helper()
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "40001" || diagnostic.Message != message {
		t.Fatalf("got %v, want 40001 %q", err, message)
	}
}

// StageReplacement leaves the batch unstaged when the unique binding is
// stale, drops queued physical events on failure and flushes them after a
// successful window.
func TestStatementHostStageReplacement(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE t(v INT)")
	sessionSQL(t, s, "INSERT INTO t VALUES(1)")
	scope, b, source := deleteProbeScope(t, s)
	h := scope.writeHost()
	old := source.Committed[:1]
	batch := index.WriteBatch{Values: [][]sqltype.Value{old[0].Values}, ActionMode: lock.MultiNoKeyAction}

	scope.UniqueBinding = &index.UniqueWriteBinding{}
	stale, err := h.StageReplacement(b.Ref, old, batch, nil, dml.ReplaceMerge)
	if err != nil || !stale || len(scope.stage.rows[b.Ref.Address]) != 0 {
		t.Fatal("stale binding staged", stale, err)
	}
	scope.UniqueBinding = nil

	scope.multiEvents = []lock.MultiEvent{{}}
	_, err = h.StageReplacement(b.Ref, old, index.WriteBatch{ActionMode: lock.MultiNoKeyAction}, nil, dml.ReplaceMerge)
	wantErrorText(t, err, "stuntgres: invalid update stage")
	if scope.multiEvents != nil {
		t.Fatal("failed window kept its physical events")
	}

	if err := h.AcquireUpdateRow(b.Ref, old[0].Row, lock.ForNoKeyUpdate); err != nil {
		t.Fatal(err)
	}
	stale, err = h.StageReplacement(b.Ref, old, batch, nil, dml.ReplaceMerge)
	staged := scope.stage.rows[b.Ref.Address]
	if err != nil || stale || len(staged) != 1 || staged[0].Row != old[0].Row || staged[0].Version == old[0].Version {
		t.Fatal("replacement not staged", stale, err, staged)
	}
	if len(scope.multiEvents) != 0 {
		t.Fatal("physical events not flushed")
	}
	_ = scope.Finish(errors.New("rollback"))
	lockEmpty(t, e)
}

// PrepareReplacement checks the stage authority before the statement, and
// the statement before an internal FK action's admission.
func TestStatementHostPrepareReplacement(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE t(v INT)")
	sessionSQL(t, s, "INSERT INTO t VALUES(1)")
	scope, b, source := deleteProbeScope(t, s)
	old := source.Committed[:1]
	if err := scope.writeHost().PrepareReplacement(b.Ref, old); err != nil {
		t.Fatal(err)
	}
	scope.foreignActionAuth = &AuthorizationView{}
	wantErrorText(t, scope.writeHost().PrepareReplacement(b.Ref, old), "stuntgres: missing writable internal action admission")
	scope.foreignActionAuth = nil
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	copied := *scope
	wantErrorText(t, copied.writeHost().PrepareReplacement(b.Ref, old), "stuntgres: foreign or missing write stage")
	scope.foreignActionAuth = &AuthorizationView{}
	wantErrorText(t, scope.writeHost().PrepareReplacement(b.Ref, old), "stuntgres: inactive statement scope")
	scope.foreignActionAuth = nil
	lockEmpty(t, e)
}

// The statement slots read without allocating until a writer allocates them,
// and the statement's ticket, catalog reads and activity follow the scope.
func TestStatementHostStatementSlots(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE t(v INT)")
	scope, _, _ := deleteProbeScope(t, s)
	h := scope.writeHost()
	if h.IndexScope(false) != nil || h.ForeignActions(false) != nil {
		t.Fatal("slot read allocated")
	}
	indexes, actions := h.IndexScope(true), h.ForeignActions(true)
	if indexes == nil || actions == nil || h.IndexScope(false) != indexes || h.ForeignActions(false) != actions {
		t.Fatal("allocated slot not retained", indexes, actions)
	}
	if h.Ticket() == nil || h.Ticket() != scope.ticket {
		t.Fatal("ticket", h.Ticket())
	}
	if h.Operation() == nil || h.Operation() != scope.op {
		t.Fatal("operation of another statement", h.Operation())
	}
	state := e.catalog.State
	if read := h.CatalogRead(state); read.State() != state {
		t.Fatal("catalog read of another state")
	}
	if err := h.Active(); err != nil {
		t.Fatal(err)
	}
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	wantErrorText(t, h.Active(), "stuntgres: inactive statement scope")
	lockEmpty(t, e)
}

// ReadmitRevalidation readmits under the current authority and releases
// Engine.mu on both its success and its capture failure.
func TestStatementHostReadmitRevalidation(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE t(v INT UNIQUE)")
	scope, b, _ := deleteProbeScope(t, s)
	h := scope.writeHost()
	binding, err := dml.BindUniqueWrite(scope.writeHost(), b.Ref)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.ReadmitRevalidation(binding, binding); err != nil {
		t.Fatal(err)
	}
	if !e.mu.TryLock() {
		t.Fatal("Engine.mu held after readmission")
	}
	e.mu.Unlock()
	scope.foreignActionAuth = &AuthorizationView{}
	if err := h.ReadmitRevalidation(binding, binding); err == nil {
		t.Fatal("unknown acting principal admitted")
	}
	scope.foreignActionAuth = nil
	if !e.mu.TryLock() {
		t.Fatal("Engine.mu held after a failed capture")
	}
	e.mu.Unlock()
	_ = scope.Finish(errors.New("rollback"))
	lockEmpty(t, e)
}
