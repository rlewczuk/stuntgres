package tablecmds_test

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/util"
)

// These captures retain both publication identities and independently owned
// values. The fixture uses integer payloads and ordinary B-tree roots only.
type vacuumPauseState struct {
	ref               ids.ObjectRef
	store             *storage.Relation
	rows, values      []storage.RowVersion
	ends              *storage.DeletionRoot
	endValues         map[ids.RowID]storage.RowEnd
	predecessorRoot   *storage.HeapPredecessor
	predecessors      []*storage.HeapPredecessor
	predecessorValues []storage.HeapPredecessor
	generation        uint64
	maintenance       storage.Maintenance
	metadata          *storage.RelationMetadata
	metadataValue     storage.RelationMetadata
	imported          storage.ImportedRelationStatistics
	catalog           *catalog.State
	descriptor        *catalog.Relation
	descriptorValue   catalog.Relation
	indexes           map[ids.ObjectAddress]*storage.IndexRoot
	indexValues       map[ids.ObjectAddress]storage.IndexRoot
	indexDescriptors  map[ids.ObjectAddress]catalog.Relation
	captured          time.Time
	databaseFrozenXID uint64
}

func vacuumPauseFixture(t *testing.T, e *core.Engine, owner *session.Session, name string) vacuumPauseState {
	t.Helper()
	sessionSQL(t, owner, "CREATE TABLE "+name+"(id INT)")
	var insert strings.Builder
	fmt.Fprintf(&insert, "INSERT INTO %s VALUES", name)
	for i := range 129 {
		if i != 0 {
			insert.WriteByte(',')
		}
		fmt.Fprintf(&insert, "(%d)", i)
	}
	sessionSQL(t, owner, insert.String())
	for _, q := range []string{
		"CREATE INDEX " + name + "_a ON " + name + "(id)",
		"CREATE INDEX " + name + "_b ON " + name + "(id)",
	} {
		sessionSQL(t, owner, q)
	}
	ref, store := vacuumStore(t, e, name)
	e.Mu.Lock()
	initialRetained := store.Retained
	e.Mu.Unlock()
	if initialRetained != nil {
		t.Fatal("new fixture unexpectedly has retained predecessors")
	}
	// Genuine committed updates build a nonempty retained chain without changing
	// current heap cardinality, final survivor IDs or first-prefix accounting.
	sessionSQL(t, owner, "UPDATE "+name+" SET id=1001 WHERE id=1")
	sessionSQL(t, owner, "UPDATE "+name+" SET id=1 WHERE id=1001")
	sessionSQL(t, owner, "DELETE FROM "+name+" WHERE id % 2 = 0")
	indexA := catalogRef(t, e, e.CatalogRead(), name+"_a")
	indexB := catalogRef(t, e, e.CatalogRead(), name+"_b")
	e.Mu.Lock()
	defer e.Mu.Unlock()
	// Seed through the canonical owner map, not a second statistics store.
	if e.Storage.RelationMetadata == nil {
		e.Storage.RelationMetadata = make(map[ids.ObjectAddress]*storage.RelationMetadata)
	}
	e.Storage.RelationMetadata[ref.Address] = &storage.RelationMetadata{Imported: &storage.ImportedRelationStatistics{Mask: storage.RelationStatisticsAll, Pages: 7, Tuples: 129, AllVisible: 2, AllFrozen: 1}}
	state := vacuumPauseState{ref: ref, store: store, rows: store.Versions, values: slices.Clone(store.Versions), ends: store.Ends, endValues: maps.Clone(store.Ends.Rows), generation: store.Generation, maintenance: store.Maintenance, metadata: e.Storage.RelationMetadata[ref.Address], catalog: e.Catalog.State, descriptor: e.Catalog.State.MustRelation(ref.Address), indexes: make(map[ids.ObjectAddress]*storage.IndexRoot), indexValues: make(map[ids.ObjectAddress]storage.IndexRoot), indexDescriptors: make(map[ids.ObjectAddress]catalog.Relation)}
	state.predecessorRoot = store.Retained
	for prior := store.Retained; prior != nil; prior = prior.Next {
		state.predecessors = append(state.predecessors, prior)
		saved := *prior
		saved.Version.Values = slices.Clone(prior.Version.Values)
		state.predecessorValues = append(state.predecessorValues, saved)
	}
	if len(state.predecessors) != 2 {
		t.Fatal("committed updates did not retain exactly two predecessors", len(state.predecessors))
	}
	successor := store.Versions[1]
	for i, prior := range state.predecessors {
		value, err := prior.Version.Values[0].Integer()
		want := int64(1001)
		if i == 1 {
			want = 1
		}
		if err != nil || value != want || prior.Version.Row != successor.Row || prior.Version.Version >= successor.Version || prior.Version.Born == 0 || prior.End == 0 || prior.EndFullXID < 3 || prior.Version.PrivateHistory != nil {
			t.Fatal("invalid genuine retained predecessor", i, prior, err)
		}
		successor = prior.Version
	}
	for i := range state.values {
		state.values[i].Values = slices.Clone(state.values[i].Values)
	}
	state.metadataValue, state.imported, state.descriptorValue = *state.metadata, *state.metadata.Imported, *state.descriptor
	state.databaseFrozenXID = e.DatabaseFrozenXID
	state.descriptorValue.Columns = slices.Clone(state.descriptor.Columns)
	state.descriptorValue.Index = maps.Clone(state.descriptor.Index)
	state.descriptorValue.Attributes = slices.Clone(state.descriptor.Attributes)
	for _, indexRef := range []ids.ObjectRef{indexA, indexB} {
		root := e.Storage.IndexRoots[indexRef.Address]
		copy := *root
		copy.Entries = slices.Clone(root.Entries)
		for i := range copy.Entries {
			copy.Entries[i].Keys = slices.Clone(copy.Entries[i].Keys)
		}
		copy.Membership, copy.RowEntries = maps.Clone(root.Membership), maps.Clone(root.RowEntries)
		state.indexes[indexRef.Address], state.indexValues[indexRef.Address] = root, copy
		state.indexDescriptors[indexRef.Address] = *e.Catalog.State.MustRelation(indexRef.Address)
		indexDefinition := *state.indexDescriptors[indexRef.Address].SQLIndex
		indexDefinition.Keys = slices.Clone(indexDefinition.Keys)
		indexDescriptor := state.indexDescriptors[indexRef.Address]
		indexDescriptor.SQLIndex = &indexDefinition
		state.indexDescriptors[indexRef.Address] = indexDescriptor
	}
	state.captured = time.Now().UTC().Truncate(time.Microsecond)
	if len(state.rows) != 129 || len(state.endValues) != 65 || state.maintenance.VacuumCount != 0 {
		t.Fatal("invalid independent seed", len(state.rows), len(state.endValues), state.maintenance)
	}
	return state
}

func (before vacuumPauseState) retained(t *testing.T) {
	t.Helper()
	if !reflect.DeepEqual(before.rows, before.values) || !reflect.DeepEqual(before.ends.Rows, before.endValues) || *before.metadata.Imported != before.imported || !reflect.DeepEqual(*before.metadata, before.metadataValue) || !reflect.DeepEqual(*before.descriptor, before.descriptorValue) {
		t.Fatal("retained heap/deletion/metadata/catalog values mutated")
	}
	for i, prior := range before.predecessors {
		if prior.Next != before.predecessorValues[i].Next || !reflect.DeepEqual(*prior, before.predecessorValues[i]) {
			t.Fatal("retained predecessor header/payload/end/link mutated", i)
		}
	}
	for address, root := range before.indexes {
		if !reflect.DeepEqual(*root, before.indexValues[address]) {
			t.Fatal("retained index root mutated", address)
		}
	}
}

func (before vacuumPauseState) unchanged(t *testing.T, e *core.Engine) {
	t.Helper()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	store := e.Storage.Relations[before.ref.Address]
	if store != before.store || !storage.SameVersionRoot(store.Versions, before.rows) || store.Ends != before.ends || store.Generation != before.generation || store.Maintenance != before.maintenance || e.Storage.RelationMetadata[before.ref.Address] != before.metadata || e.Catalog.State != before.catalog || e.Catalog.State.MustRelation(before.ref.Address) != before.descriptor {
		t.Fatal("paused/canceled inspection published state")
	}
	if store.Retained != before.predecessorRoot {
		t.Fatal("paused/canceled inspection replaced retained predecessor root")
	}
	if e.DatabaseFrozenXID != before.databaseFrozenXID {
		t.Fatal("paused/canceled database frozen bound changed")
	}
	for address, root := range before.indexes {
		if e.Storage.IndexRoots[address] != root || !reflect.DeepEqual(*e.Catalog.State.MustRelation(address), before.indexDescriptors[address]) {
			t.Fatal("paused/canceled index publication", address)
		}
	}
	before.retained(t)
}

type vacuumPauseTask struct {
	pause     *util.ExecutionPause
	op        *session.Operation
	source    context.Context
	cancel    context.CancelCauseFunc
	done      chan error
	joined    bool
	goroutine uint64
}

func startVacuumPause(t *testing.T, e *core.Engine, worker *session.Session, name string) *vacuumPauseTask {
	t.Helper()
	pause := util.NewExecutionPause("vacuum.inspect")
	waitCanceled, stopWaiting := context.WithCancel(context.Background())
	stopWaiting()
	if err := pause.Wait(waitCanceled); err != context.Canceled {
		t.Fatal(err)
	}
	request, stopRequest := context.WithDeadline(util.WithExecutionPause(context.Background(), pause), time.Now().Add(time.Hour))
	source, cancel := context.WithCancelCause(request)
	op, _, err := worker.AcquireCommand(source)
	if err != nil {
		cancel(nil)
		stopRequest()
		t.Fatal(err)
	}
	task := &vacuumPauseTask{pause: pause, op: op, source: source, cancel: cancel, done: make(chan error, 1)}
	originalContext := op.Context()
	started := make(chan uint64, 1)
	statement := mustLockStatement("VACUUM (FREEZE) " + name)
	t.Cleanup(func() {
		cancel(nil)
		pause.Resume()
		stopRequest()
		if !task.joined {
			select {
			case <-task.done:
				task.joined = true
			case <-time.After(5 * time.Second):
				t.Error("paused VACUUM did not join")
			}
		}
	})
	go func() {
		var identity [96]byte
		fields := strings.Fields(string(identity[:runtime.Stack(identity[:], false)]))
		id, _ := strconv.ParseUint(fields[1], 10, 64)
		started <- id
		result, err := e.Execute(op, statement, 0, nil)
		if result.Rows != nil {
			result.Rows.Close()
		}
		err = op.CommandError(err)
		op.Done()
		task.done <- err
	}()
	task.goroutine = <-started
	wait, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	if err := pause.Wait(wait); err != nil {
		t.Fatal("VACUUM did not enter real inspection", err)
	}
	sourceDeadline, sourceHasDeadline := source.Deadline()
	childDeadline, childHasDeadline := op.Context().Deadline()
	if op.Context() != originalContext || !sourceHasDeadline || !childHasDeadline || !sourceDeadline.Equal(childDeadline) {
		t.Fatal("VACUUM replaced effective context/deadline authority")
	}
	return task
}

func (task *vacuumPauseTask) join(t *testing.T) error {
	t.Helper()
	select {
	case err := <-task.done:
		task.joined = true
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("VACUUM failed to unwind without Resume")
		return nil
	}
}

func vacuumPauseFrame(t *testing.T, task *vacuumPauseTask) {
	t.Helper()
	buffer := make([]byte, 1<<20)
	n := runtime.Stack(buffer, true)
	prefix := fmt.Sprintf("goroutine %d ", task.goroutine)
	for _, stack := range strings.Split(string(buffer[:n]), "\n\n") {
		if strings.HasPrefix(stack, prefix) && strings.Contains(stack, "storage.InspectVacuum(") && strings.Contains(stack, "util.ExecutionCheckpoint(") {
			return
		}
	}
	t.Fatal("exact admitted worker lacks live storage inspection frame", task.goroutine)
}

func vacuumPauseClean(t *testing.T, e *core.Engine, worker *session.Session, observer *session.Session) {
	t.Helper()
	if rows := vacuumProgressCapture(t, e, observer); len(rows) != 0 {
		t.Fatal("terminal progress", rows)
	}
	lockEmpty(t, e)
	worker.Mu.Lock()
	leases, operations := worker.Leases, len(worker.Operations)
	worker.Mu.Unlock()
	if leases != 0 || operations != 0 {
		t.Fatal("terminal operation ownership", leases, operations)
	}
	if !e.ReplacementMu.TryLock() {
		t.Fatal("terminal replacement reservation")
	}
	e.ReplacementMu.Unlock()
}

func TestVacuumExecutionPause(t *testing.T) {
	for _, mode := range []string{"source", "operation", "session_cancel", "session_end", "terminate", "release"} {
		t.Run(mode, func(t *testing.T) {
			e := engine.New()
			owner, worker, observer := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
			before := vacuumPauseFixture(t, e, owner, "paused")
			task := startVacuumPause(t, e, worker, "paused")
			before.unchanged(t, e)
			vacuumPauseFrame(t, task)
			rows := vacuumProgressCapture(t, e, observer)
			if len(rows) != 1 {
				t.Fatal(rows)
			}
			phase, err := rows[0][4].Text()
			if err != nil || phase != "scanning heap" {
				t.Fatal(phase, err)
			}
			vacuumProgressInteger(t, rows[0], 0, int64(worker.SessionToken.ID()))
			vacuumProgressInteger(t, rows[0], 5, 2)
			vacuumProgressInteger(t, rows[0], 6, 1)
			vacuumProgressInteger(t, rows[0], 7, 0)
			vacuumProgressInteger(t, rows[0], 9, 129*8)
			vacuumProgressInteger(t, rows[0], 10, 64*8)
			vacuumProgressInteger(t, rows[0], 11, 64)
			if err := task.pause.Wait(context.Background()); err != nil {
				t.Fatal(err)
			}
			cause := errors.New("vacuum owner cause")
			switch mode {
			case "source":
				task.cancel(cause)
			case "operation":
				task.op.Cancel(cause)
			case "session_cancel":
				worker.Mu.Lock()
				worker.CancelQueryLocked()
				worker.Mu.Unlock()
			case "session_end":
				worker.RequestEnd(session.CleanClose)
			case "terminate":
				worker.Mu.Lock()
				worker.TerminateLocked()
				worker.Mu.Unlock()
			case "release":
				task.pause.Resume()
				task.pause.Resume()
			}
			err = task.join(t)
			if mode == "release" {
				if err != nil {
					t.Fatal(err)
				}
				vacuumPauseSuccess(t, e, before)
				task.cancel(cause)
				task.pause.Resume()
				if before.store.Maintenance.VacuumCount != 1 {
					t.Fatal("late cancel retracted completion")
				}
			} else {
				if mode == "session_cancel" || mode == "terminate" {
					var diagnostic *sqlstate.Error
					want := *session.UserCancelCause
					if mode == "terminate" {
						want = sqlstate.Error{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: "57P01", Message: "terminating connection due to administrator command"}
					}
					if !errors.As(err, &diagnostic) || !reflect.DeepEqual(*diagnostic, want) {
						t.Fatal("wrong session diagnostic", err)
					}
				} else if err != context.Canceled {
					t.Fatal("wrong ordinary cancellation", err)
				}
				if (mode == "source" || mode == "operation") && context.Cause(task.op.Context()) != cause {
					t.Fatal("lost owner cause", context.Cause(task.op.Context()))
				}
				if mode != "source" && task.source.Err() != nil {
					t.Fatal("child canceled source", task.source.Err())
				}
				before.unchanged(t, e)
			}
			vacuumPauseClean(t, e, worker, observer)
			before.retained(t)
			if mode != "session_end" && mode != "terminate" {
				transactionRows(t, worker, "SELECT id FROM paused WHERE id=1", 1)
			}
			if mode == "session_end" || mode == "terminate" {
				wait, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := worker.WaitEnd(wait); err != nil {
					t.Fatal("session cleanup", err)
				}
			}
		})
	}
	t.Run("independent_holds", func(t *testing.T) {
		e := engine.New()
		owner, a, b, observer := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
		beforeA, beforeB := vacuumPauseFixture(t, e, owner, "paused_a"), vacuumPauseFixture(t, e, owner, "paused_b")
		// B's creation legitimately changes catalog state before A starts.
		beforeA.catalog = e.Catalog.State
		ta, tb := startVacuumPause(t, e, a, "paused_a"), startVacuumPause(t, e, b, "paused_b")
		vacuumPauseFrame(t, ta)
		vacuumPauseFrame(t, tb)
		if rows := vacuumProgressCapture(t, e, observer); len(rows) != 2 {
			t.Fatal(rows)
		}
		ta.cancel(context.Canceled)
		if err := ta.join(t); err != context.Canceled {
			t.Fatal(err)
		}
		beforeA.unchanged(t, e)
		beforeB.unchanged(t, e)
		select {
		case err := <-tb.done:
			tb.joined = true
			t.Fatal("A released B", err)
		default:
		}
		rows := vacuumProgressCapture(t, e, observer)
		if len(rows) != 1 {
			t.Fatal(rows)
		}
		vacuumProgressInteger(t, rows[0], 0, int64(b.SessionToken.ID()))
		if tb.source.Err() != nil {
			t.Fatal("A canceled B source")
		}
		tb.pause.Resume()
		if err := tb.join(t); err != nil {
			t.Fatal(err)
		}
		vacuumPauseSuccess(t, e, beforeB)
		vacuumPauseClean(t, e, a, observer)
		vacuumPauseClean(t, e, b, observer)
	})
}

func vacuumPauseSuccess(t *testing.T, e *core.Engine, before vacuumPauseState) {
	t.Helper()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	store := e.Storage.Relations[before.ref.Address]
	if store != before.store || storage.SameVersionRoot(store.Versions, before.rows) || store.Ends == before.ends || len(store.Versions) != 64 || len(store.Ends.Rows) != 0 || store.Generation != before.generation+1 {
		t.Fatal("wrong retirement/root/generation")
	}
	if store.Retained != nil {
		t.Fatal("successful quiescent vacuum did not retire predecessor chain")
	}
	for i, row := range store.Versions {
		id, err := row.Values[0].Integer()
		if err != nil || id != int64(2*i+1) || !row.Frozen || row.Row != before.rows[2*i+1].Row || row.Version != before.rows[2*i+1].Version {
			t.Fatal("wrong frozen survivor/remap", i, row, err)
		}
	}
	want := before.maintenance
	want.Scanned, want.Live, want.Dead, want.Tuples = true, 64, 0, 64
	want.InsertsSince, want.VacuumCount = 0, 1
	cutoff, err := e.Visibility.VacuumCutoffLocked()
	if err != nil {
		t.Fatal(err)
	}
	want.FrozenXID = cutoff
	if store.Maintenance.LastVacuum.Before(before.captured) || store.Maintenance.LastVacuum.After(time.Now().UTC()) {
		t.Fatal("completion timestamp outside execution")
	}
	want.LastVacuum = store.Maintenance.LastVacuum
	if store.Maintenance != want {
		t.Fatal("wrong maintenance completion", store.Maintenance, want)
	}
	if e.Storage.RelationMetadata[before.ref.Address] == before.metadata || e.Storage.RelationMetadata[before.ref.Address].Imported != nil {
		t.Fatal("successful vacuum failed imported-stat invalidation")
	}
	for address, old := range before.indexes {
		root, definition := e.Storage.IndexRoots[address], e.Catalog.State.MustRelation(address)
		if root == old || len(root.Entries) != 64 || !definition.SQLIndex.Valid || !definition.SQLIndex.Ready {
			t.Fatal("wrong index publication", address)
		}
		for i, entry := range root.Entries {
			value, err := entry.Keys[0].Integer()
			if err != nil || value != int64(2*i+1) || entry.Position != i || entry.Row != store.Versions[i].Row || entry.Version != store.Versions[i].Version {
				t.Fatal("wrong exact index remap", address, i, entry)
			}
		}
	}
	before.retained(t)
}
