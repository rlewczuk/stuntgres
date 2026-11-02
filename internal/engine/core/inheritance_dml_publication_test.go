package core_test

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestInheritanceDMLAtomicPublication(t *testing.T) {
	for _, reject := range []bool{false, true} {
		name := "publish"
		if reject {
			name = "late_predecessor_veto"
		}
		t.Run(name, func(t *testing.T) {
			e, p, c := inheritanceDMLPublishedGraph(t)
			s, done := inheritanceDMLScope(t, e, p, context.Background())
			defer done()
			if err := core.ScopeAcquireInheritedMutationTargets(s, &parser.Update{}); err != nil {
				t.Fatal(err)
			}
			refs := s.Command.TargetRefs(s.LockedRef)
			e.Mu.Lock()
			view, err := s.AcquireDataViewsLocked(e, refs)
			e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			retained := view.Retain()
			if retained == nil {
				t.Fatal("sole statement view was released")
			}
			defer retained.Release()
			before := make(map[ids.ObjectAddress][]storage.RowVersion)
			frontier := e.Visibility.CommitSeq()
			for _, ref := range refs {
				store := core.ScopeMutationStorage(s, ref)
				before[ref.Address] = append([]storage.RowVersion(nil), store.Versions...)
				old := store.Versions[0]
				if err = core.ScopeAcquireRow(s, visibility.DeleteCandidate{Ref: ref, Storage: store, Row: old.Row, Version: old.Version, Base: old.Base, Ordinal: 0}); err != nil {
					t.Fatal(err)
				}
				if _, err = dml.ReplaceRows(s.ActivateRuntime(s.Op.Context()), s.WriteHost(), ref, []storage.RowVersion{old}, index.WriteBatch{Values: [][]sqltype.Value{{inheritanceQueryInt(e, 30)}}}, dml.ReplaceMerge); err != nil {
					t.Fatal(err)
				}
			}
			for _, ref := range refs {
				if !reflect.DeepEqual(e.Storage.Relations[ref.Address].Versions, before[ref.Address]) {
					t.Fatal("staging published one physical store", ref)
				}
			}
			if reject {
				// Private test-owned provenance is invalidated after both real producers
				// staged data, selecting the final all-store veto rather than an early row
				// validation failure. No manager prevention is bypassed in public tests.
				s.Stage.Rows[c.Address()][0].Base++
			}
			err = s.Finish(nil)
			if reject {
				if err == nil {
					t.Fatal("stale later child predecessor published")
				}
				if e.Visibility.CommitSeq() != frontier {
					t.Fatal("failed publication consumed a frontier")
				}
				for _, ref := range refs {
					if !reflect.DeepEqual(e.Storage.Relations[ref.Address].Versions, before[ref.Address]) {
						t.Fatal("later child veto partially published", ref)
					}
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if e.Visibility.CommitSeq() != frontier+1 {
					t.Fatal("one statement published multiple frontiers", e.Visibility.CommitSeq(), frontier)
				}
				for _, ref := range refs {
					current := e.Storage.Relations[ref.Address].Versions[0]
					if current.Born != e.Visibility.CommitSeq() || !reflect.DeepEqual(current.Values, []sqltype.Value{inheritanceQueryInt(e, 30)}) {
						t.Fatal("physical replacement missed shared frontier", current)
					}
				}
			}
			if before[p.Address()][0].Values[0] != inheritanceQueryInt(e, 10) || before[c.Address()][0].Values[0] != inheritanceQueryInt(e, 20) {
				t.Fatal("immutable predecessor payload changed")
			}
			enginetest.LockEmpty(t, e)
		})
	}
}

// Published heads deliberately have no PrivateHistory. Descendant authority
// must follow actual immutable successor links from the captured physical
// candidate, while rejecting both a later insertion and a reset same-RowID link.
func TestInheritanceDMLCommittedSuccessorAuthority(t *testing.T) {
	for _, reset := range []bool{false, true} {
		name := "committed_chain"
		if reset {
			name = "repaired_same_rowid_sibling"
		}
		t.Run(name, func(t *testing.T) {
			e := engine.New()
			peer := enginetest.StartSession(t, e, nil)
			for _, q := range []string{
				"CREATE TABLE p(v INT)",
				"CREATE TABLE c() INHERITS(p)",
				"INSERT INTO c VALUES(20)",
			} {
				enginetest.SessionSQL(t, peer, q)
			}
			p := e.CatalogRead().MustRelation(enginetest.CatalogRef(t, e, e.CatalogRead(), "p").Address)
			scope, done := inheritanceDMLScope(t, e, p, context.Background())
			defer done()
			if err := core.ScopeAcquireInheritedMutationTargets(scope, &parser.Update{}); err != nil {
				t.Fatal(err)
			}
			child := scope.InheritedTargets[0]
			refs := scope.Command.TargetRefs(scope.LockedRef)
			e.Mu.Lock()
			view, err := scope.AcquireDataViewsLocked(e, refs)
			e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			captured := view.Data().Committed[child.Ref.Address][0]
			enginetest.SessionSQL(t, peer, "UPDATE ONLY c SET v=30")
			enginetest.SessionSQL(t, peer, "UPDATE ONLY c SET v=40")
			enginetest.SessionSQL(t, peer, "INSERT INTO c VALUES(90)")
			e.Mu.Lock()
			store := child.Storage
			current, later := store.Versions[0], store.Versions[1]
			headers, retained := store.Headers, store.Retained
			e.Mu.Unlock()
			if current.PrivateHistory != nil || current.Version == captured.Version || current.Row != captured.Row || retained == nil {
				t.Fatal("fixture did not publish a real detached committed predecessor chain", current, captured)
			}
			version := captured.Version
			for version < current.Version {
				next := headers.Header(version).Successor
				if next <= version {
					break
				}
				version = next
			}
			if version != current.Version {
				t.Fatal("fixture lost actual committed successor links")
			}
			if reset {
				freezeTupleRepair(t, e, store, captured, child.Ref)
			}
			host := scope.WriteHost()
			if err := host.AcquireUpdateRow(child.Ref, current, lock.ForNoKeyUpdate); err != nil {
				t.Fatal(err)
			}
			err = host.PrepareReplacement(child.Ref, []storage.RowVersion{current})
			if reset {
				if err == nil {
					t.Fatal("reset original link admitted an unlinked same-RowID sibling")
				}
			} else if err != nil {
				t.Fatal("real committed descendant lost captured authority", err)
			}
			if err := host.AcquireUpdateRow(child.Ref, later, lock.ForNoKeyUpdate); err != nil {
				t.Fatal(err)
			}
			if err := host.PrepareReplacement(child.Ref, []storage.RowVersion{later}); err == nil {
				t.Fatal("later unrelated insertion acquired captured candidate authority")
			}
			if view.Data().Committed[child.Ref.Address][0].Version != captured.Version {
				t.Fatal("authority validation mutated the frozen candidate root")
			}
		})
	}
}
