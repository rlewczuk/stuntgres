package engine

import (
	"context"
	"reflect"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
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
			if err := s.acquireInheritedMutationTargets(&parser.Update{}); err != nil {
				t.Fatal(err)
			}
			refs := s.mutationTargetRefs(s.lockedRef)
			e.mu.Lock()
			view, err := s.acquireDataViewsLocked(e, refs)
			e.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			retained := view.Retain()
			if retained == nil {
				t.Fatal("sole statement view was released")
			}
			defer retained.Release()
			before := make(map[ids.ObjectAddress][]storage.RowVersion)
			frontier := e.visibility.CommitSeq()
			for _, ref := range refs {
				store := s.mutationStorage(ref)
				before[ref.Address] = append([]storage.RowVersion(nil), store.Versions...)
				old := store.Versions[0]
				if err = s.acquireRow(visibility.DeleteCandidate{Ref: ref, Storage: store, Row: old.Row, Version: old.Version, Base: old.Base, Ordinal: 0}); err != nil {
					t.Fatal(err)
				}
				if err = s.Writes().replace(ref, []storage.RowVersion{old}, index.WriteBatch{Values: [][]sqltype.Value{{inheritanceQueryInt(e, 30)}}}); err != nil {
					t.Fatal(err)
				}
			}
			for _, ref := range refs {
				if !reflect.DeepEqual(e.storage.Relations[ref.Address].Versions, before[ref.Address]) {
					t.Fatal("staging published one physical store", ref)
				}
			}
			if reject {
				// Private test-owned provenance is invalidated after both real producers
				// staged data, selecting the final all-store veto rather than an early row
				// validation failure. No manager prevention is bypassed in public tests.
				s.stage.rows[c.Address()][0].Base++
			}
			err = s.Finish(nil)
			if reject {
				if err == nil {
					t.Fatal("stale later child predecessor published")
				}
				if e.visibility.CommitSeq() != frontier {
					t.Fatal("failed publication consumed a frontier")
				}
				for _, ref := range refs {
					if !reflect.DeepEqual(e.storage.Relations[ref.Address].Versions, before[ref.Address]) {
						t.Fatal("later child veto partially published", ref)
					}
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if e.visibility.CommitSeq() != frontier+1 {
					t.Fatal("one statement published multiple frontiers", e.visibility.CommitSeq(), frontier)
				}
				for _, ref := range refs {
					current := e.storage.Relations[ref.Address].Versions[0]
					if current.Born != e.visibility.CommitSeq() || !reflect.DeepEqual(current.Values, []sqltype.Value{inheritanceQueryInt(e, 30)}) {
						t.Fatal("physical replacement missed shared frontier", current)
					}
				}
			}
			if before[p.Address()][0].Values[0] != inheritanceQueryInt(e, 10) || before[c.Address()][0].Values[0] != inheritanceQueryInt(e, 20) {
				t.Fatal("immutable predecessor payload changed")
			}
			lockEmpty(t, e)
		})
	}
}
