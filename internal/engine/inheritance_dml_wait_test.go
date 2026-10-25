package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
	"time"
)

func inheritanceDMLPublishedGraph(t *testing.T) (*Engine, *catalog.Relation, *catalog.Relation) {
	t.Helper()
	e := New()
	ps, p := queryCreateStage(t, e, "p", 1)
	if err := e.StageQueryTableRows(context.Background(), ps, p, [][]sqltype.Value{{inheritanceQueryInt(e, 10)}}, ids.TxRef{}); err != nil {
		t.Fatal(err)
	}
	queryCreatePublish(t, e, ps)
	cs, c := queryCreateStage(t, e, "c", 1)
	c.Inheritance = &catalog.TableInheritance{Parents: inheritanceUnitParents(p.Address()), Columns: map[int32]catalog.InheritedColumn{c.Attributes[0]: {Origins: []catalog.InheritanceOrigin{{Parent: p.Address(), Attribute: p.Attributes[0]}}}}, NotNull: map[int32]catalog.InheritedConstraint{}, Checks: map[string]catalog.InheritedConstraint{}}
	if err := e.StageQueryTableRows(context.Background(), cs, c, [][]sqltype.Value{{inheritanceQueryInt(e, 20)}}, ids.TxRef{}); err != nil {
		t.Fatal(err)
	}
	queryCreatePublish(t, e, cs)
	// Child publication may issue the parent's real subclass-marker revision.
	// Return the actual published identities, not pre-publication candidates.
	published := e.CatalogRead()
	return e, published.MustRelation(p.Address()), published.MustRelation(c.Address())
}

func TestInheritanceDMLContention(t *testing.T)  { inheritanceDMLQueued(t, false) }
func TestInheritanceDMLReplacement(t *testing.T) { inheritanceDMLQueued(t, true) }

func inheritanceDMLQueued(t *testing.T, replace bool) {
	t.Helper()
	e, p, c := inheritanceDMLPublishedGraph(t)
	op, opDone, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer opDone()
	holder, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Finish(nil)
	if err = holder.Gate(&parser.Select{}); err != nil {
		t.Fatal(err)
	}
	old := e.Catalog.Domain.Ref(e.CatalogRead().Epoch(), c.Address())
	if err = e.Locks.Acquire(op.Context(), holder.LockTicket, lock.Resource{ObjectRef: old}, lock.AccessExclusive); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, done := inheritanceDMLScope(t, e, p, ctx)
	defer done()
	result := make(chan error, 1)
	joined := false
	go func() { result <- s.acquireInheritedMutationTargets(&parser.Update{}) }()
	defer func() {
		cancel()
		holder.Finish(nil)
		if !joined {
			select {
			case <-result:
			case <-time.After(5 * time.Second):
				t.Error("mutation admission worker did not join")
			}
		}
	}()
	lockPending(t, e, 1)
	lockState := e.Locks.Inspect()
	entry := lockState.Resources[lock.Resource{ObjectRef: old}]
	correct := entry != nil && len(entry.Pending) == 1 && entry.Pending[0].Ticket == s.LockTicket && entry.Pending[0].Mode == lock.RowExclusive
	if !correct {
		t.Fatal("descendant did not queue an actual physical RX request")
	}
	// Observing the real queued request is the synchronization point; no sleeps
	// or synthetic wait hooks decide when replacement/cancellation happens.
	if replace {
		remove := e.BeginCatalogStage(e.CatalogRead())
		if err = remove.Remove(old); err != nil {
			t.Fatal(err)
		}
		queryCreatePublish(t, e, remove)
		stage, next := queryCreateStage(t, e, "c", 1)
		next.Inheritance = catalog.CloneInheritance(c.Inheritance)
		queryCreatePublish(t, e, stage)
		if next.Address() == old.Address || e.Storage.Relations[next.Address()] == e.Storage.Relations[old.Address] {
			t.Fatal("replacement did not mint a fresh physical identity")
		}
		if err = holder.Finish(nil); err != nil {
			t.Fatal(err)
		}
	} else {
		cancel()
	}
	select {
	case err = <-result:
		joined = true
	case <-time.After(5 * time.Second):
		t.Fatal("mutation admission did not resume")
	}
	if replace && !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("queued mutation rebound to same-name replacement", err)
	}
	if !replace && !errors.Is(err, context.Canceled) {
		t.Fatal("physical wait ignored cancellation", err)
	}
	if len(s.InheritedTargets) != 0 {
		t.Fatal("failed wait admitted a descendant", s.InheritedTargets)
	}
	s.Finish(err)
	holder.Finish(nil)
	lockEmpty(t, e)
}
