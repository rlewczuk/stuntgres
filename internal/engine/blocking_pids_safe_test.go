package engine

import (
	"math"
	"reflect"
	"sort"
	"testing"
)

func blockingUnitSafeWant(t *testing.T, e *Engine, pid SessionID, want ...int64) {
	t.Helper()
	typ := blockingUnitType(e)
	e.mu.Lock()
	values, err := e.safeSnapshotBlockingPidsLocked(pid, typ)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	got := make([]int64, len(values))
	for i, v := range values {
		got[i], err = v.Integer()
		if err != nil {
			t.Fatal(err)
		}
	}
	sort.Slice(got, func(i, j int) bool { return got[i] < got[j] })
	sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })
	if len(got) != len(want) || len(want) != 0 && !reflect.DeepEqual(got, want) {
		t.Fatal("safe blocker bag", got, want)
	}
}

func TestBlockingPidsSafeAuthority(t *testing.T) {
	e := New()
	reader, rt := blockingUnitOwner(t, e)
	writer, wt := blockingUnitOwner(t, e)
	_, second := blockingUnitOwner(t, e)
	typ := blockingUnitType(e)
	r := &serializableTransaction{ref: rt.ref, captured: true, readOnly: true, deferrable: true, ready: make(chan struct{})}
	w := &serializableTransaction{ref: wt.ref, captured: true}
	w2 := &serializableTransaction{ref: second.ref, captured: true}
	r.possible = map[*serializableTransaction]struct{}{w: {}, w2: {}}
	e.mu.Lock()
	e.serializable.nodes = map[TxRef]*serializableTransaction{r.ref: r, w.ref: w, w2.ref: w2}
	e.mu.Unlock()
	blockingUnitSafeWant(t, e, reader.token.ID(), int64(writer.token.ID()), int64(second.session.ID()))
	blockingUnitWant(t, e, reader.token.ID()) // SSI must never invent ordinary locks.
	for _, mutate := range []func(){
		func() { r.deferrable = false }, func() { r.readOnly = false }, func() { r.captured = false },
		func() { r.resolved = true }, func() { r.aborted = true }, func() { r.ended = true },
		func() { r.committed = true }, func() { r.prepared = true }, func() { r.ready = nil },
	} {
		e.mu.Lock()
		original := *r
		mutate()
		e.mu.Unlock()
		blockingUnitSafeWant(t, e, reader.token.ID())
		e.mu.Lock()
		*r = original
		e.mu.Unlock()
	}
	// Each retained prepared SSI owner is a distinct candidate, even when
	// both projections are zero. Preparing does not resolve safe acquisition.
	e.mu.Lock()
	w.prepared, w2.prepared = true, true
	e.mu.Unlock()
	blockingUnitSafeWant(t, e, reader.token.ID(), 0, 0)
	e.mu.Lock()
	e.endSerializableLocked(w.ref, false)
	e.mu.Unlock()
	blockingUnitSafeWant(t, e, reader.token.ID(), 0)
	e.mu.Lock()
	e.endSerializableLocked(w2.ref, false)
	e.mu.Unlock()
	blockingUnitSafeWant(t, e, reader.token.ID())
	select {
	case <-r.ready:
	default:
		t.Fatal("resolved acquisition not woken")
	}
	// A retired node retained in a possible map is not a current owner.
	e.mu.Lock()
	r.resolved = false
	r.ready = make(chan struct{})
	r.possible = map[*serializableTransaction]struct{}{w: {}}
	e.serializable.nodes[r.ref] = r
	e.mu.Unlock()
	blockingUnitSafeWant(t, e, reader.token.ID())
	// Full-width session identity must not alias an int4 target on lookup.
	e.mu.Lock()
	delete(e.serializable.nodes, r.ref)
	r.ref = withTxSession(r.ref, withSessionID(r.ref.Session(), r.ref.Session().ID()+SessionID(uint64(1)<<32)))
	e.serializable.nodes[r.ref] = r
	e.mu.Unlock()
	blockingUnitSafeWant(t, e, reader.token.ID())
	// Exercise native overflow separately from prepared zero classification.
	e.mu.Lock()
	w.aborted, w.ended, w.prepared = false, false, false
	delete(e.serializable.nodes, w.ref)
	w.ref = withTxSession(w.ref, withSessionID(w.ref.Session(), math.MaxInt32+1))
	e.serializable.nodes[w.ref] = w
	delete(e.serializable.nodes, r.ref)
	r.ref = withTxSession(r.ref, reader.token)
	e.serializable.nodes[r.ref] = r
	r.possible = map[*serializableTransaction]struct{}{w: {}}
	_, err := e.safeSnapshotBlockingPidsLocked(reader.token.ID(), typ)
	e.mu.Unlock()
	if err == nil || err.Error() != "stuntgres: blocking session identity exceeds integer range" {
		t.Fatal("safe PID overflow", err)
	}
	e.mu.Lock()
	e.fenceSerializableLocked()
	e.mu.Unlock()
	blockingUnitSafeWant(t, e, reader.token.ID())
}
