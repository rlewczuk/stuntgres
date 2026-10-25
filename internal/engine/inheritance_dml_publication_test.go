package engine

import (
	"context"
	"reflect"
	"stuntgres/internal/engine/dml"
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
				store := s.mutationStorage(ref)
				before[ref.Address] = append([]storage.RowVersion(nil), store.Versions...)
				old := store.Versions[0]
				if err = s.acquireRow(visibility.DeleteCandidate{Ref: ref, Storage: store, Row: old.Row, Version: old.Version, Base: old.Base, Ordinal: 0}); err != nil {
					t.Fatal(err)
				}
				if err = dml.ReplaceRows(s.WriteHost(), ref, []storage.RowVersion{old}, index.WriteBatch{Values: [][]sqltype.Value{{inheritanceQueryInt(e, 30)}}}, dml.ReplaceMerge); err != nil {
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
			lockEmpty(t, e)
		})
	}
}
