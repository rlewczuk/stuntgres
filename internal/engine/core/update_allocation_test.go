package core_test

import (
	"context"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"unsafe"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// The consumer invariant is auxiliary memory proportional to matches, not to
// scanned rows. A nonempty replacement still owns one immutable root copy.
func TestUpdateCoreSparseAllocation(t *testing.T) {
	const small = 32
	const large = 4096
	const repetitions = 16
	allocated := func(run func()) uint64 {
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		for range repetitions {
			run()
		}
		runtime.ReadMemStats(&after)
		return (after.TotalAlloc - before.TotalAlloc) / repetitions
	}
	exercise := func(n int) (zero, sparse, private uint64) {
		e := engine.New()
		s := sessionStart(t, e, nil)
		sessionSQL(t, s, "CREATE TABLE t(id INT,v INT)")
		var q strings.Builder
		q.WriteString("INSERT INTO t VALUES")
		for i := range n {
			if i != 0 {
				q.WriteByte(',')
			}
			q.WriteString("(")
			q.WriteString(strconv.Itoa(i))
			q.WriteString(",1)")
		}
		sessionSQL(t, s, q.String())
		// Measure UPDATE execution, as the benchmark does, not GC-evictable
		// parser receiver checkouts inside these small byte envelopes.
		noMatchFalse := enginetest.Statement(t, "UPDATE t SET v=2 WHERE FALSE")
		noMatchPredicate := enginetest.Statement(t, "UPDATE t SET v=2 WHERE id=-1")
		oneMatch := enginetest.Statement(t, "UPDATE t SET v=2 WHERE id=0")
		execute := func(statement parser.Statement, want int64) {
			op, err := s.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			result, err := e.Execute(op, statement, 0, nil)
			op.Done()
			if result.Rows != nil {
				result.Rows.Close()
				t.Fatal("UPDATE without RETURNING exposed rows")
			}
			if err != nil || result.Count != want {
				t.Fatal("UPDATE affected count", result.Count, want, err)
			}
		}
		ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
		before := e.Storage.Relations[ref.Address].Versions
		zero = allocated(func() { execute(noMatchFalse, 0) })
		absent := allocated(func() { execute(noMatchPredicate, 0) })
		if absent > zero {
			zero = absent
		}
		if &before[0] != &e.Storage.Relations[ref.Address].Versions[0] {
			t.Fatal("zero-match UPDATE replaced root")
		}
		sparse = allocated(func() { execute(oneMatch, 1) })
		if len(e.Storage.Relations[ref.Address].Versions) != n || enginetest.Export(before[0].Values[1]) != int64(1) {
			t.Fatal("root retention/cardinality")
		}
		old := e.Storage.Relations[ref.Address].Versions
		stage := []storage.RowVersion{old[n-1]}
		stage[0].Version++
		private = allocated(func() {
			next := visibility.MergePrivateRows(old, stage, true)
			if len(next) != n || next[n-1].Version != stage[0].Version || old[n-1].Version == stage[0].Version {
				t.Fatal("private merge changed predecessor")
			}
		})
		return
	}
	zeroSmall, sparseSmall, privateSmall := exercise(small)
	zeroLarge, sparseLarge, privateLarge := exercise(large)
	rootGrowth := uint64(large-small) * uint64(unsafe.Sizeof(storage.RowVersion{}))
	// Broad structural byte envelopes tolerate allocator size classes and fixed
	// command overhead, but reject a table-sized candidate array or identity map.
	if zeroLarge > zeroSmall+rootGrowth/8 {
		t.Fatal("no-match auxiliary storage scales with table", zeroSmall, zeroLarge)
	}
	if sparseLarge > sparseSmall+rootGrowth+rootGrowth/4 {
		t.Fatal("sparse publication allocates beyond one necessary root", sparseSmall, sparseLarge, rootGrowth)
	}
	if privateLarge > privateSmall+rootGrowth+rootGrowth/4 {
		t.Fatal("single private replacement indexes every prior head", privateSmall, privateLarge, rootGrowth)
	}
}

func BenchmarkUpdateCoreSparse(b *testing.B) {
	for _, n := range []int{512, 8192} {
		for _, match := range []bool{false, true} {
			name := strconv.Itoa(n) + map[bool]string{false: "/no_match", true: "/one_match"}[match]
			b.Run(name, func(b *testing.B) {
				e := engine.New()
				ctx := context.Background()
				s, err := e.StartSession(ctx, session.SessionStart{ID: ids.SessionID(testSessionID.Add(1))})
				if err != nil {
					b.Fatal(err)
				}
				b.Cleanup(func() {
					s.RequestEnd(session.CleanClose)
					if err := s.WaitEnd(ctx); err != nil {
						b.Error(err)
					}
				})
				execute := func(q string) {
					op, err := s.Acquire(ctx)
					if err != nil {
						b.Fatal(err)
					}
					_, err = e.Execute(op, mustLockStatement(q), 0, nil)
					op.Done()
					if err != nil {
						b.Fatal(err)
					}
				}
				execute("CREATE TABLE t(id INT,v INT)")
				var q strings.Builder
				q.WriteString("INSERT INTO t VALUES")
				for i := range n {
					if i != 0 {
						q.WriteByte(',')
					}
					q.WriteString("(" + strconv.Itoa(i) + ",1)")
				}
				execute(q.String())
				query := "UPDATE t SET v=2 WHERE FALSE"
				if match {
					query = "UPDATE t SET v=2 WHERE id=0"
				}
				ast := mustLockStatement(query)
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					op, err := s.Acquire(ctx)
					if err != nil {
						b.Fatal(err)
					}
					result, err := e.Execute(op, ast, 0, nil)
					op.Done()
					want := int64(0)
					if match {
						want = 1
					}
					if err != nil || result.Count != want {
						b.Fatal(result, err)
					}
				}
			})
		}
	}
}

// Measure actual distinct-target accumulation, not allocation counts: repeatedly
// copying a growing storage.RowVersion prefix allocates few objects but quadratic bytes.
func TestUpdateCoreMultirowAllocation(t *testing.T) {
	var previous uint64
	for _, n := range []int{128, 512, 2048} {
		e := engine.New()
		s := sessionStart(t, e, nil)
		sessionSQL(t, s, "CREATE TABLE t(id INT,v INT)")
		var q strings.Builder
		q.WriteString("INSERT INTO t VALUES")
		for i := range n {
			if i != 0 {
				q.WriteByte(',')
			}
			q.WriteString("(")
			q.WriteString(strconv.Itoa(i))
			q.WriteString(",1)")
		}
		sessionSQL(t, s, q.String())
		op, scope, bound, source := updateBoundSource(t, e, s, context.Background(), "UPDATE t SET v=v+1")
		retained := source.View.Retain()
		old := retained.Data().Committed[bound.Ref.Address]
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		result, err := core.ExecuteUpdate(op.Context(), scope, bound, source, nil, nil, "public")
		finishErr := scope.Finish(err)
		op.Done()
		runtime.ReadMemStats(&after)
		used := after.TotalAlloc - before.TotalAlloc
		if result.Rows != nil {
			result.Rows.Close()
		}
		if err != nil || finishErr != nil || result.Count != int64(n) || result.Rows != nil {
			retained.Release()
			t.Fatal("multirow UPDATE", n, result, err, finishErr)
		}
		current := e.Storage.Relations[bound.Ref.Address].Versions
		if len(current) != n || len(old) != n {
			retained.Release()
			t.Fatal("replacement cardinality", n, len(current), len(old))
		}
		for i := range old {
			if old[i].Row != current[i].Row || old[i].Version == current[i].Version || enginetest.Export(old[i].Values[1]) != int64(1) || enginetest.Export(current[i].Values[1]) != int64(2) {
				retained.Release()
				t.Fatal("retained view or replacement lineage", n, i)
			}
		}
		retained.Release()
		t.Logf("rows=%d allocated_bytes=%d", n, used)
		// Four times as many rows permits six times the bytes plus fixed
		// allocator/GC noise. This admits linear growth, not prefix copying.
		if previous != 0 && used > previous*6+(1<<20) {
			t.Fatal("distinct UPDATE allocation grows beyond linear envelope", n, previous, used)
		}
		previous = used
	}
}

// A legitimate existing action prefix is not covered by UPDATE's source-row
// uniqueness proof. Overlapping heads must merge without mutating old views;
// other accepted targets may still append.
func TestUpdateCoreStagedPrefixOverlap(t *testing.T) {
	e := engine.New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE t(id INT,v INT)")
	sessionSQL(t, s, "INSERT INTO t VALUES(1,1),(2,1)")
	op, scope, bound, source := updateBoundSource(t, e, s, context.Background(), "UPDATE t SET v=v+1")
	committed := source.Committed
	payload := append([]sqltype.Value(nil), committed[0].Values...)
	var err error
	payload[1], err = sqltype.Integer(payload[1].Type(), 11)
	if err == nil {
		err = dml.ReplaceRows(scope.WriteHost(), bound.Ref, committed[:1], index.WriteBatch{Values: [][]sqltype.Value{payload}}, dml.ReplaceMerge)
	}
	if err != nil {
		scope.Finish(err)
		op.Done()
		t.Fatal("existing action stage", err)
	}
	earlier := scope.Stage.Rows[bound.Ref.Address]
	result, err := core.ExecuteUpdate(op.Context(), scope, bound, source, nil, nil, "public")
	finishErr := scope.Finish(err)
	op.Done()
	if result.Rows != nil {
		result.Rows.Close()
	}
	if err != nil || finishErr != nil || result.Count != 2 || result.Rows != nil {
		t.Fatal("overlapping UPDATE", result, err, finishErr)
	}
	if len(earlier) != 1 || enginetest.Export(earlier[0].Values[1]) != int64(11) || enginetest.Export(committed[0].Values[1]) != int64(1) {
		t.Fatal("replacement mutated retained action/source prefix")
	}
	current := e.Storage.Relations[bound.Ref.Address].Versions
	if len(current) != 2 {
		t.Fatal("overlap appended duplicate physical heads", len(current))
	}
	for i := range current {
		if current[i].Row != committed[i].Row || enginetest.Export(current[i].Values[1]) != int64(2) {
			t.Fatal("frozen-source replacement result", i, current[i])
		}
	}
	enginetest.LockEmpty(t, e)
}
