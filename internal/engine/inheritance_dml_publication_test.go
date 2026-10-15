package engine

import (
	"context"
	"reflect"
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
			before := make(map[ObjectAddress][]rowVersion)
			frontier := e.commitSeq
			for _, ref := range refs {
				store := s.mutationStorage(ref)
				before[ref.Address] = append([]rowVersion(nil), store.versions...)
				old := store.versions[0]
				if err = s.acquireRow(deleteCandidate{ref: ref, storage: store, row: old.row, version: old.version, base: old.base, ordinal: 0}); err != nil {
					t.Fatal(err)
				}
				if err = s.Writes().replace(ref, []rowVersion{old}, indexWriteBatch{values: [][]sqltype.Value{{inheritanceQueryInt(e, 30)}}}); err != nil {
					t.Fatal(err)
				}
			}
			for _, ref := range refs {
				if !reflect.DeepEqual(e.storage[ref.Address].versions, before[ref.Address]) {
					t.Fatal("staging published one physical store", ref)
				}
			}
			if reject {
				// Private test-owned provenance is invalidated after both real producers
				// staged data, selecting the final all-store veto rather than an early row
				// validation failure. No manager prevention is bypassed in public tests.
				s.stage.rows[c.address][0].base++
			}
			err = s.Finish(nil)
			if reject {
				if err == nil {
					t.Fatal("stale later child predecessor published")
				}
				if e.commitSeq != frontier {
					t.Fatal("failed publication consumed a frontier")
				}
				for _, ref := range refs {
					if !reflect.DeepEqual(e.storage[ref.Address].versions, before[ref.Address]) {
						t.Fatal("later child veto partially published", ref)
					}
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if e.commitSeq != frontier+1 {
					t.Fatal("one statement published multiple frontiers", e.commitSeq, frontier)
				}
				for _, ref := range refs {
					current := e.storage[ref.Address].versions[0]
					if current.born != e.commitSeq || !reflect.DeepEqual(current.values, []sqltype.Value{inheritanceQueryInt(e, 30)}) {
						t.Fatal("physical replacement missed shared frontier", current)
					}
				}
			}
			if before[p.address][0].values[0] != inheritanceQueryInt(e, 10) || before[c.address][0].values[0] != inheritanceQueryInt(e, 20) {
				t.Fatal("immutable predecessor payload changed")
			}
			lockEmpty(t, e)
		})
	}
}
