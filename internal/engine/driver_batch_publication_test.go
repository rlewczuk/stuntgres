package engine

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

// Context.Err is the canonical cooperative publication seam. With no peer
// mutators, both mutexes are held only at the actual final live-operation check;
// this observes that boundary without depending on intermediate check counts.
type batchPublicationContext struct {
	context.Context
	e                *Engine
	s                *session.Session
	entered, release chan struct{}
	armed            atomic.Bool
	late             context.CancelFunc
}

func (c *batchPublicationContext) Err() error {
	if c.e.mu.TryLock() {
		c.e.mu.Unlock()
		return c.Context.Err()
	}
	if c.s.Mu.TryLock() {
		c.s.Mu.Unlock()
		return c.Context.Err()
	}
	if c.armed.CompareAndSwap(false, true) {
		close(c.entered)
		<-c.release
		if c.late != nil {
			c.late()
			return nil
		}
	}
	return c.Context.Err()
}
func TestDriverBatchPublicationBoundary(t *testing.T) {
	for _, boundary := range []string{"success", "cancel_before", "cancel_after", "epoch_before", "counter_before", "ref_before", "terminal_before"} {
		t.Run(boundary, func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE TABLE t(v INT)")
			table := catalogRef(t, e, e.CatalogRead(), "t")
			store := e.storage.Relations[table.Address]
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			m := batchUnitStart(t, s, ctx)
			t.Cleanup(func() { _ = m.end(context.Canceled) })
			if _, err := m.step(statement(t, "INSERT INTO t VALUES(1)")); err != nil {
				t.Fatal(err)
			}
			result, err := m.step(statement(t, "SELECT v FROM t"))
			if err != nil {
				t.Fatal(err)
			}
			defer result.Rows.Close()
			state, frontier := e.catalog.state, e.visibility.CommitSeq()
			switch boundary {
			case "cancel_before", "cancel_after":
				barrier := &batchPublicationContext{Context: m.op.Ctx, e: e, s: s, entered: make(chan struct{}), release: make(chan struct{})}
				if boundary == "cancel_after" {
					barrier.late = cancel
				}
				m.op.Ctx = barrier
				var release sync.Once
				unblock := func() { release.Do(func() { close(barrier.release) }) }
				done := make(chan struct{})
				finished := make(chan error, 1)
				t.Cleanup(func() { cancel(); unblock(); <-done })
				go func() { defer close(done); finished <- m.end(nil) }()
				select {
				case <-barrier.entered:
				case <-time.After(5 * time.Second):
					t.Fatal("final publication boundary not reached")
				}
				if e.catalog.state != state || e.visibility.CommitSeq() != frontier || len(store.Versions) != 0 {
					t.Fatal("prefix escaped before publication")
				}
				if boundary == "cancel_before" {
					cancel()
				}
				unblock()
				select {
				case err = <-finished:
				case <-time.After(5 * time.Second):
					t.Fatal("publication worker not joined")
				}
				<-done
			case "epoch_before":
				e.mu.Lock()
				next := *e.catalog.state
				next.SetEpoch(next.Epoch() + 1)
				e.catalog.state = &next
				state = e.catalog.state
				e.mu.Unlock()
				err = m.end(nil)
			case "counter_before":
				e.mu.Lock()
				e.visibility.SetCommitSeqLocked(ids.CommitSeq(^uint64(0)))
				frontier = e.visibility.CommitSeq()
				e.mu.Unlock()
				err = m.end(nil)
			case "ref_before":
				stale := s.Transactions.Ref
				stale = withTxID(stale, stale.ID()+1)
				err = e.publishWrites(m.op, stale, s.Transactions.Writes, s.Transactions.Deletes, nil)
				if err == nil {
					t.Fatal("stale publication ref admitted")
				}
				if end := m.end(err); end != err {
					t.Fatal("stale-ref primary replaced", end)
				}
			case "terminal_before":
				s.RequestEnd(session.Terminated)
				err = m.end(nil)
				wait, stop := context.WithTimeout(context.Background(), 5*time.Second)
				defer stop()
				if end := s.WaitEnd(wait); end != nil {
					t.Fatal(end)
				}
			default:
				err = m.end(nil)
			}
			committed := boundary == "success" || boundary == "cancel_after"
			if committed {
				if err != nil || e.visibility.CommitSeq() != frontier+1 || len(store.Versions) != 1 {
					t.Fatal("complete publication lost", err)
				}
			} else {
				if err == nil || e.visibility.CommitSeq() != frontier || len(store.Versions) != 0 || e.catalog.state != state {
					t.Fatal("rejected publication changed roots", err)
				}
				if boundary == "cancel_before" && !errors.Is(err, context.Canceled) {
					t.Fatal("cancellation primary", err)
				}
				if boundary == "epoch_before" && !errors.Is(err, ids.ErrCatalogReplaced) {
					t.Fatal("epoch primary", err)
				}
				var diagnostic interface{ SQLState() string }
				if boundary == "counter_before" && (err.Error() != "stuntgres: commit sequence exhausted" || errors.As(err, &diagnostic)) {
					t.Fatal("counter primary", err)
				}
			}
			if got := tuples(result); len(got) != 1 || got[0][0] != int64(1) {
				t.Fatal("completed output changed at disposition", got)
			}
			if s.Transactions.RetainedMessage {
				t.Fatal("message admission flag retained")
			}
		})
	}
}
func TestDriverBatchRoleACLConflictKeepsPeerRoot(t *testing.T) {
	for _, roleChange := range []bool{false, true} {
		t.Run(map[bool]string{false: "acl_authority", true: "roles_and_acl"}[roleChange], func(t *testing.T) {
			e := New()
			s, peer := sessionStart(t, e, nil), sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE TABLE t(v INT)")
			if !roleChange {
				sessionSQL(t, s, "CREATE ROLE pending LOGIN")
			}
			state := e.catalog.state
			m := batchUnitStart(t, s, context.Background())
			defer m.end(context.Canceled)
			if roleChange {
				if _, err := m.step(statement(t, "CREATE ROLE pending LOGIN")); err != nil {
					t.Fatal(err)
				}
			}
			for _, q := range []string{"GRANT SELECT ON t TO pending", "INSERT INTO t VALUES(1)"} {
				if _, err := m.step(statement(t, q)); err != nil {
					t.Fatal(q, err)
				}
			}
			c := s.Transactions.Catalog
			if c == nil || c.RolesBase == nil || c.Roles == nil {
				t.Fatal("missing pending role/ACL revision authority")
			}
			sessionSQL(t, peer, "CREATE ROLE peer LOGIN")
			roles, frontier := e.roles.published, e.visibility.CommitSeq()
			if err := m.end(nil); err == nil {
				t.Fatal("role authority conflict committed")
			}
			if e.catalog.state != state || e.roles.published != roles || e.visibility.CommitSeq() != frontier {
				t.Fatal("private ACL or stale role root overwrote peer")
			}
			if _, ok := e.Roles().Lookup("peer"); !ok {
				t.Fatal("peer role lost")
			}
			if _, ok := e.Roles().Lookup("pending"); ok == roleChange {
				t.Fatal("pending role disposition")
			}
			transactionRows(t, peer, "SELECT v FROM t")
		})
	}
}
