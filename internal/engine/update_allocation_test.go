package engine

import (
	"context"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"unsafe"
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
		e := New()
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
		ref := catalogRef(t, e, e.CatalogRead(), "t")
		before := e.storage[ref.Address].versions
		zero = allocated(func() { sessionSQL(t, s, "UPDATE t SET v=2 WHERE FALSE") })
		absent := allocated(func() { sessionSQL(t, s, "UPDATE t SET v=2 WHERE id=-1") })
		if absent > zero {
			zero = absent
		}
		if &before[0] != &e.storage[ref.Address].versions[0] {
			t.Fatal("zero-match UPDATE replaced root")
		}
		sparse = allocated(func() { sessionSQL(t, s, "UPDATE t SET v=2 WHERE id=0") })
		if len(e.storage[ref.Address].versions) != n || testExport(before[0].values[1]) != int64(1) {
			t.Fatal("root retention/cardinality")
		}
		old := e.storage[ref.Address].versions
		stage := []rowVersion{old[n-1]}
		stage[0].version++
		private = allocated(func() {
			next := mergePrivateRows(old, stage, true)
			if len(next) != n || next[n-1].version != stage[0].version || old[n-1].version == stage[0].version {
				t.Fatal("private merge changed predecessor")
			}
		})
		return
	}
	zeroSmall, sparseSmall, privateSmall := exercise(small)
	zeroLarge, sparseLarge, privateLarge := exercise(large)
	rootGrowth := uint64(large-small) * uint64(unsafe.Sizeof(rowVersion{}))
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
				e := New()
				ctx := context.Background()
				s, err := e.StartSession(ctx, SessionStart{ID: SessionID(testSessionID.Add(1))})
				if err != nil {
					b.Fatal(err)
				}
				b.Cleanup(func() {
					s.RequestEnd(CleanClose)
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
