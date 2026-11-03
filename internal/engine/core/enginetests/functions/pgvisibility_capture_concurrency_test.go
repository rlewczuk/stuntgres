package functions_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
)

// pausedVisibilityCapture is an ordinary summary capture held at its detached
// layout work: the first cohort window has sampled facts, horizon and root,
// and no owner/Engine/manager mutex is held until it is released.
type pausedVisibilityCapture struct {
	gate    *visibilityCheckBarrier
	done    chan visibilityCaptureOutcome
	release sync.Once
	joined  bool
}

func pauseVisibilityCapture(t *testing.T, e *core.Engine, s *session.Session, n plan.InstalledPgvisibilityExpr, ref ids.ObjectRef) *pausedVisibilityCapture {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	p := &pausedVisibilityCapture{gate: &visibilityCheckBarrier{Context: ctx, entered: make(chan struct{}), release: make(chan struct{})}, done: make(chan visibilityCaptureOutcome, 1)}
	go func() {
		capture, err := visibilityCapture(p.gate, e, s, n, ref)
		p.done <- visibilityCaptureOutcome{capture, err}
	}()
	t.Cleanup(func() {
		cancel()
		p.unblock()
		if !p.joined {
			select {
			case <-p.done:
			case <-time.After(30 * time.Second):
				t.Error("paused visibility capture failed to join")
			}
		}
	})
	select {
	case <-p.gate.entered:
	case out := <-p.done:
		p.joined = true
		t.Fatal("capture finished before detached layout", out.err)
	case <-time.After(30 * time.Second):
		t.Fatal("detached layout was not reached")
	}
	return p
}
func (p *pausedVisibilityCapture) unblock() { p.release.Do(func() { close(p.gate.release) }) }
func (p *pausedVisibilityCapture) finish(t *testing.T) visibilityCaptureOutcome {
	t.Helper()
	p.unblock()
	select {
	case out := <-p.done:
		p.joined = true
		return out
	case <-time.After(30 * time.Second):
		t.Fatal("released visibility capture did not finish")
	}
	return visibilityCaptureOutcome{}
}

func visibilityPeerSnapshot(e *core.Engine, s *session.Session) bool {
	e.Mu.Lock()
	defer e.Mu.Unlock()
	return e.Visibility.Entries[s.Transactions.Ref].Snapshot
}
func visibilityRootOf(e *core.Engine, ref ids.ObjectRef) *storage.VisibilityRoot {
	e.Mu.Lock()
	defer e.Mu.Unlock()
	return e.Storage.Relations[ref.Address].Visibility
}
func visibilityWant(t *testing.T, label string, out visibilityCaptureOutcome, visible, frozen int64) {
	t.Helper()
	if errors.Is(out.err, ids.ErrCatalogReplaced) {
		t.Fatal(label, "lawful peer activity fenced as replacement:", out.err)
	}
	if out.err != nil || out.capture.Blocks != 1 || out.capture.AllVisible != visible || out.capture.AllFrozen != frozen {
		t.Fatalf("%s: capture %+v (%v), want %d/%d", label, out.capture, out.err, visible, frozen)
	}
}

// Peer READ COMMITTED statements advertise and retire snapshots while a
// capture is detached; peer captures publish their own roots. Neither replaces
// the catalog, so neither may fence a capture whose facts are still current.
func TestPgvisibilityCaptureConcurrentPeers(t *testing.T) {
	for _, phase := range []string{"peer_statement_begins", "peer_statement_ends"} {
		t.Run(phase, func(t *testing.T) {
			e, ref, n := visibilityCaptureFixture(t)
			reader, peer := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, peer, "BEGIN")
			// Cleanups run in reverse: open scopes and paused captures end first.
			t.Cleanup(func() { enginetest.SessionSQL(t, peer, "ROLLBACK") })
			if visibilityRootOf(e, ref) != nil || visibilityPeerSnapshot(e, peer) {
				t.Fatal("fixture precondition: no root and an idle peer snapshot")
			}
			var finishPeer func(error) error
			if phase == "peer_statement_ends" {
				_, finishPeer = visibilityScope(t, e, peer)
			}
			before := visibilityPeerSnapshot(e, peer)
			capture := pauseVisibilityCapture(t, e, reader, n, ref)
			if phase == "peer_statement_begins" {
				_, finishPeer = visibilityScope(t, e, peer)
			} else if err := finishPeer(nil); err != nil {
				t.Fatal(err)
			}
			if visibilityPeerSnapshot(e, peer) == before {
				t.Fatal("peer snapshot advertisement did not change while the capture was detached")
			}
			out := capture.finish(t)
			// The result is exact for the horizon sampled with its facts: a peer
			// statement already running at that point keeps the claim conservative.
			if phase == "peer_statement_begins" {
				visibilityWant(t, phase, out, 1, 1)
			} else {
				visibilityWant(t, phase, out, 0, 0)
			}
			if visibilityRootOf(e, ref) != nil {
				t.Fatal("capture published a root with a stale READ proof")
			}
			if phase == "peer_statement_begins" {
				if err := finishPeer(nil); err != nil {
					t.Fatal(err)
				}
			}
			later, err := visibilityCapture(t.Context(), e, reader, n, ref)
			visibilityWant(t, "later capture", visibilityCaptureOutcome{later, err}, 1, 1)
			if root := visibilityRootOf(e, ref); root == nil || root.Invalidated || root.Map.AllVisible != 1 {
				t.Fatal("undisturbed later capture did not materialize its root", root)
			}
		})
	}
	t.Run("truncated_root_fences", func(t *testing.T) {
		e, ref, n := visibilityCaptureFixture(t)
		reader := enginetest.StartSession(t, e, nil)
		capture, err := visibilityCapture(t.Context(), e, reader, n, ref)
		visibilityWant(t, "materializing capture", visibilityCaptureOutcome{capture, err}, 1, 1)
		prior := visibilityRootOf(e, ref)
		paused := pauseVisibilityCapture(t, e, reader, n, ref)
		// Public truncation waits behind the capture's AccessShare; install
		// its explicit empty root directly, as the CHECK lifecycle does.
		e.Mu.Lock()
		e.Storage.Relations[ref.Address].Visibility = storage.EmptyVisibilityRoot()
		e.Mu.Unlock()
		out := paused.finish(t)
		if !errors.Is(out.err, ids.ErrCatalogReplaced) || out.capture.Blocks != 0 || out.capture.Runs != nil {
			t.Fatal("truncation between windows was not fenced", out.capture, out.err)
		}
		if root := visibilityRootOf(e, ref); !root.Invalidated || prior.Invalidated || prior.Map.AllVisible != 1 {
			t.Fatal("fenced capture republished positives or mutated the captured root", root)
		}
		capture, err = visibilityCapture(t.Context(), e, reader, n, ref)
		visibilityWant(t, "suppressed capture", visibilityCaptureOutcome{capture, err}, 0, 0)
	})
	for _, phase := range []string{"established_root_replaced", "first_root_published"} {
		t.Run(phase, func(t *testing.T) {
			e, ref, n := visibilityCaptureFixture(t)
			var prior *storage.VisibilityRoot
			if phase == "established_root_replaced" {
				capture, err := visibilityCapture(t.Context(), e, enginetest.StartSession(t, e, nil), n, ref)
				visibilityWant(t, "materializing capture", visibilityCaptureOutcome{capture, err}, 1, 1)
				if prior = visibilityRootOf(e, ref); prior == nil || prior.Invalidated {
					t.Fatal("missing materialized prior root")
				}
			}
			// A held peer RC statement keeps both captures' READ horizons
			// unknown throughout, so only root publication differs between them.
			holder := enginetest.StartSession(t, e, nil)
			_, finishHolder := visibilityScope(t, e, holder)
			a, b := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
			for i, s := range []*session.Session{a, b} {
				enginetest.SessionSQL(t, s, "BEGIN")
				enginetest.SessionSQL(t, s, fmt.Sprintf("INSERT INTO visibility_heap VALUES(%d)", i+2))
			}
			t.Cleanup(func() {
				for _, s := range []*session.Session{a, b} {
					if s.TransactionState() != session.TxIdle {
						enginetest.SessionSQL(t, s, "ROLLBACK")
					}
				}
			})
			first, second := pauseVisibilityCapture(t, e, a, n, ref), pauseVisibilityCapture(t, e, b, n, ref)
			visibilityWant(t, "first capture", first.finish(t), 0, 0)
			published := visibilityRootOf(e, ref)
			if published == nil || published == prior || published.Invalidated {
				t.Fatal("first capture did not publish its root", published)
			}
			visibilityWant(t, "second capture", second.finish(t), 0, 0)
			if visibilityRootOf(e, ref) != published {
				t.Fatal("superseded capture overwrote the newer root")
			}
			enginetest.SessionSQL(t, a, "COMMIT")
			enginetest.SessionSQL(t, b, "ROLLBACK")
			if err := finishHolder(nil); err != nil {
				t.Fatal(err)
			}
			enginetest.Run(t, e, "VACUUM (FREEZE) visibility_heap")
			capture, err := visibilityCapture(t.Context(), e, a, n, ref)
			visibilityWant(t, "frozen continuation", visibilityCaptureOutcome{capture, err}, 1, 1)
		})
	}
}
