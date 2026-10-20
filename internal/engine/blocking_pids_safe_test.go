package engine

import (
	"math"
	"reflect"
	"sort"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/visibility"
)

func blockingUnitSafeWant(t *testing.T, e *Engine, pid ids.SessionID, want ...int64) {
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
	r := &visibility.SerializableTx{Ref: rt.Ref(), Captured: true, ReadOnly: true, Deferrable: true, Ready: make(chan struct{})}
	w := &visibility.SerializableTx{Ref: wt.Ref(), Captured: true}
	w2 := &visibility.SerializableTx{Ref: second.Ref(), Captured: true}
	r.Possible = map[*visibility.SerializableTx]struct{}{w: {}, w2: {}}
	e.mu.Lock()
	e.visibility.Serializable.Nodes = map[ids.TxRef]*visibility.SerializableTx{r.Ref: r, w.Ref: w, w2.Ref: w2}
	e.mu.Unlock()
	blockingUnitSafeWant(t, e, reader.SessionToken.ID(), int64(writer.SessionToken.ID()), int64(second.Session().ID()))
	blockingUnitWant(t, e, reader.SessionToken.ID()) // SSI must never invent ordinary locks.
	for _, mutate := range []func(){
		func() { r.Deferrable = false }, func() { r.ReadOnly = false }, func() { r.Captured = false },
		func() { r.Resolved = true }, func() { r.Aborted = true }, func() { r.Ended = true },
		func() { r.Committed = true }, func() { r.Prepared = true }, func() { r.Ready = nil },
	} {
		e.mu.Lock()
		original := *r
		mutate()
		e.mu.Unlock()
		blockingUnitSafeWant(t, e, reader.SessionToken.ID())
		e.mu.Lock()
		*r = original
		e.mu.Unlock()
	}
	// Each retained prepared SSI owner is a distinct candidate, even when
	// both projections are zero. Preparing does not resolve safe acquisition.
	e.mu.Lock()
	w.Prepared, w2.Prepared = true, true
	e.mu.Unlock()
	blockingUnitSafeWant(t, e, reader.SessionToken.ID(), 0, 0)
	e.mu.Lock()
	e.endSerializableLocked(w.Ref, false)
	e.mu.Unlock()
	blockingUnitSafeWant(t, e, reader.SessionToken.ID(), 0)
	e.mu.Lock()
	e.endSerializableLocked(w2.Ref, false)
	e.mu.Unlock()
	blockingUnitSafeWant(t, e, reader.SessionToken.ID())
	select {
	case <-r.Ready:
	default:
		t.Fatal("resolved acquisition not woken")
	}
	// A retired node retained in a possible map is not a current owner.
	e.mu.Lock()
	r.Resolved = false
	r.Ready = make(chan struct{})
	r.Possible = map[*visibility.SerializableTx]struct{}{w: {}}
	e.visibility.Serializable.Nodes[r.Ref] = r
	e.mu.Unlock()
	blockingUnitSafeWant(t, e, reader.SessionToken.ID())
	// Full-width session identity must not alias an int4 target on lookup.
	e.mu.Lock()
	delete(e.visibility.Serializable.Nodes, r.Ref)
	r.Ref = withTxSession(r.Ref, withSessionID(r.Ref.Session(), r.Ref.Session().ID()+ids.SessionID(uint64(1)<<32)))
	e.visibility.Serializable.Nodes[r.Ref] = r
	e.mu.Unlock()
	blockingUnitSafeWant(t, e, reader.SessionToken.ID())
	// Exercise native overflow separately from prepared zero classification.
	e.mu.Lock()
	w.Aborted, w.Ended, w.Prepared = false, false, false
	delete(e.visibility.Serializable.Nodes, w.Ref)
	w.Ref = withTxSession(w.Ref, withSessionID(w.Ref.Session(), math.MaxInt32+1))
	e.visibility.Serializable.Nodes[w.Ref] = w
	delete(e.visibility.Serializable.Nodes, r.Ref)
	r.Ref = withTxSession(r.Ref, reader.SessionToken)
	e.visibility.Serializable.Nodes[r.Ref] = r
	r.Possible = map[*visibility.SerializableTx]struct{}{w: {}}
	_, err := e.safeSnapshotBlockingPidsLocked(reader.SessionToken.ID(), typ)
	e.mu.Unlock()
	if err == nil || err.Error() != "stuntgres: blocking session identity exceeds integer range" {
		t.Fatal("safe PID overflow", err)
	}
	e.mu.Lock()
	e.fenceSerializableLocked()
	e.mu.Unlock()
	blockingUnitSafeWant(t, e, reader.SessionToken.ID())
}
