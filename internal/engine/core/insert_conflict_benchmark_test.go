package core_test

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/enginetest"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
)

// The actual selected unique-provider admission path runs over an entire
// committed duplicate batch. Opposite storage/key orders exercise the checked
// physical position, not a coincidentally correct sorted ordinal.
func BenchmarkInsertConflictCommittedOmissions(b *testing.B) {
	for _, isolation := range []session.Isolation{session.ReadCommitted, session.RepeatableRead} {
		for _, size := range []int{128, 4096, 65536} {
			b.Run(fmt.Sprintf("isolation=%d/rows=%d", isolation, size), func(b *testing.B) {
				f := insertConflictUniqueFixture(b)
				f.store.Versions = make([]storage.RowVersion, size)
				for i := range f.store.Versions {
					f.store.Versions[i] = storage.RowVersion{Row: ids.RowID(i + 1), Version: ids.VersionID(i + 1), Born: 1, Values: insertConflictUnitRow(b, f, int64(size-i))}
				}
				f.e.Storage.SetFrontierLocked(uint64(size), uint64(size))
				f.e.Visibility.SetCommitSeqLocked(1)
				root, err := index.BuildIndexRoot(&exec.Context{EvalStatement: exec.EvalStatement{Catalog: operatorFamilyTestRead(), Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, f.parent, f.index.SQLIndex, f.store, nil)
				if err != nil {
					b.Fatal(err)
				}
				f.e.Mu.Lock()
				f.e.Storage.InstallIndexRootLocked(f.index.Address(), root)
				f.e.Mu.Unlock()
				sess, err := enginetest.OpenSession(context.Background(), f.e, session.SessionStart{ID: ids.SessionID(testSessionID.Add(1))}, f.parent.Owner)
				if err != nil {
					b.Fatal(err)
				}
				op, err := sess.Acquire(context.Background())
				if err != nil {
					sess.RequestEnd(session.CleanClose)
					b.Fatal(err)
				}
				defer func() {
					op.Done()
					sess.RequestEnd(session.CleanClose)
					if err := sess.WaitEnd(context.Background()); err != nil {
						b.Error(err)
					}
				}()
				scope, err := f.e.StartStatement(op)
				if err != nil {
					b.Fatal(err)
				}
				defer func() {
					if !scope.Finished {
						_ = scope.Finish(context.Canceled)
					}
				}()
				if err := scope.Gate(&parser.Insert{}); err != nil {
					b.Fatal(err)
				}
				scope.Owner.Isolation, scope.Owner.Frontier = isolation, 1
				ref := f.e.Catalog.Domain.Ref(f.e.Catalog.State.Epoch(), f.parent.Address())
				action := dml.NewInsertConflictState(&analyze.InsertConflict{Keys: []analyze.ConflictKey{{Attribute: 10}}, Predicate: f.predicate, Reads: []int{0, 1}, Targets: []int{0, 1}})
				evaluation := exec.Context{EvalStatement: exec.EvalStatement{Catalog: operatorFamilyTestRead(), Context: op.Context(), Input: scope.Input, Output: scope.Output}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
				if err := action.SelectArbiters(&evaluation, f.parent, f.e.CatalogRead()); err != nil {
					b.Fatal(err)
				}
				scope.InsertConflict = action
				if admitted, err := dml.AdmitInsertRow(scope.WriteHost(), ref, &index.WriteCandidate{Values: f.store.Versions[0].Values}, index.WriteBatch{}, nil); admitted || err != nil {
					b.Fatal("warmup", admitted, err)
				}
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					for i := range f.store.Versions {
						if admitted, err := dml.AdmitInsertRow(scope.WriteHost(), ref, &index.WriteCandidate{Values: f.store.Versions[i].Values}, index.WriteBatch{}, nil); admitted || err != nil {
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
