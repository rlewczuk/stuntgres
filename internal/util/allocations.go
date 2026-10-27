package util

import "runtime"

// AllocationsPerCall reports the heap allocations and bytes of one call of f,
// for allocation-budget tests. Like testing.AllocsPerRun it calls f once to
// warm up and then calls times with GOMAXPROCS(1), and like
// testing.BenchmarkResult it divides the runtime.MemStats deltas with integer
// division. It keeps the smallest of three samples, so an unrelated runtime
// allocation during one sample cannot fail an exact budget.
func AllocationsPerCall(calls int, f func()) (allocations, bytes int64) {
	if calls <= 0 {
		panic("util: AllocationsPerCall needs a positive call count")
	}
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	f()
	var before, after runtime.MemStats
	for sample := range 3 {
		runtime.ReadMemStats(&before)
		for range calls {
			f()
		}
		runtime.ReadMemStats(&after)
		a := int64(after.Mallocs-before.Mallocs) / int64(calls)
		b := int64(after.TotalAlloc-before.TotalAlloc) / int64(calls)
		if sample == 0 || a < allocations {
			allocations = a
		}
		if sample == 0 || b < bytes {
			bytes = b
		}
	}
	return allocations, bytes
}
