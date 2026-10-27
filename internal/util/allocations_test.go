package util

import "testing"

var allocationSink []byte

func TestAllocationsPerCall(t *testing.T) {
	allocations, bytes := AllocationsPerCall(100, func() { allocationSink = make([]byte, 1024) })
	if allocations != 1 || bytes != 1024 {
		t.Fatal("one 1 KiB allocation per call", allocations, bytes)
	}
	allocations, bytes = AllocationsPerCall(100, func() { allocationSink = allocationSink[:0] })
	if allocations != 0 || bytes != 0 {
		t.Fatal("no allocation", allocations, bytes)
	}
	calls := 0
	AllocationsPerCall(5, func() { calls++ })
	if calls != 1+3*5 {
		t.Fatal("one warm-up call and three samples", calls)
	}
}
