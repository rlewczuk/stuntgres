package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"testing"
)

// The actual selected unique-provider admission path runs over an entire
// committed duplicate batch. Opposite storage/key orders exercise the checked
// physical position, not a coincidentally correct sorted ordinal.
func BenchmarkInsertConflictCommittedOmissions(b *testing.B) {
	for _, isolation := range []Isolation{ReadCommitted, RepeatableRead} {
		for _, size := range []int{128, 4096, 65536} {
			b.Run(fmt.Sprintf("isolation=%d/rows=%d", isolation, size), func(b *testing.B) {
				f := insertConflictUniqueFixture(b)
				f.store.Versions = make([]storage.RowVersion, size)
				for i := range f.store.Versions {
					f.store.Versions[i] = storage.RowVersion{Row: ids.RowID(i + 1), Version: ids.VersionID(i + 1), Born: 1, Values: insertConflictUnitRow(b, f, int64(size-i))}
				}
				f.e.storage.SetFrontierLocked(uint64(size), uint64(size))
				f.e.visibility.SetCommitSeqLocked(1)
				root, err := buildIndexRoot(&EvalContext{Context: context.Background(), Scope: 1, Frame: 1}, f.parent, f.index.SQLIndex, f.store, nil)
				if err != nil {
					b.Fatal(err)
				}
				f.e.storage.IndexRoots[f.index.Address()] = root
				session, err := f.e.StartSession(context.Background(), SessionStart{ID: ids.SessionID(testSessionID.Add(1)), Authenticated: f.parent.Owner})
				if err != nil {
					b.Fatal(err)
				}
				op, err := session.Acquire(context.Background())
				if err != nil {
					session.RequestEnd(CleanClose)
					b.Fatal(err)
				}
				defer func() {
					op.Done()
					session.RequestEnd(CleanClose)
					if err := session.WaitEnd(context.Background()); err != nil {
						b.Error(err)
					}
				}()
				scope, err := f.e.StartStatement(op)
				if err != nil {
					b.Fatal(err)
				}
				defer func() {
					if !scope.finished {
						_ = scope.Finish(context.Canceled)
					}
				}()
				if err := scope.gate(&parser.Insert{}); err != nil {
					b.Fatal(err)
				}
				scope.owner.isolation, scope.owner.frontier = isolation, 1
				ref := f.e.catalog.Domain.Ref(f.e.catalog.state.Epoch(), f.parent.Address())
				action := &boundInsertConflict{keys: []conflictKey{{attribute: 10}}, predicate: f.predicate, reads: []int{0, 1}, targets: []int{0, 1}}
				evaluation := EvalContext{Context: op.Context(), Scope: 1, Frame: 1, Input: scope.input, Output: scope.output}
				if err := action.selectArbiters(&evaluation, f.parent, f.e.CatalogRead()); err != nil {
					b.Fatal(err)
				}
				scope.insertConflict = action
				if admitted, err := scope.admitInsertRow(ref, &indexWriteCandidate{values: f.store.Versions[0].Values}, indexWriteBatch{}, nil); admitted || err != nil {
					b.Fatal("warmup", admitted, err)
				}
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					for i := range f.store.Versions {
						if admitted, err := scope.admitInsertRow(ref, &indexWriteCandidate{values: f.store.Versions[i].Values}, indexWriteBatch{}, nil); admitted || err != nil {
							b.Fatal("committed omission", admitted, err)
						}
					}
				}
				b.StopTimer()
				b.ReportMetric(float64(size), "proposals/op")
				b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/float64(size), "ns/proposal")
			})
		}
	}
}
