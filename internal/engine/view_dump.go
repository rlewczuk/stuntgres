package engine

import (
	"container/heap"
	"context"
	"fmt"
)

// OIDs break ties among tables/views whose persistent predecessors are ready.
// Restore may remap a source above its dependent, so topology takes precedence
// over creation-address order in that exceptional case.
type viewDumpReady []OID

func (h viewDumpReady) Len() int           { return len(h) }
func (h viewDumpReady) Less(i, j int) bool { return h[i] < h[j] }
func (h viewDumpReady) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *viewDumpReady) Push(value any)    { *h = append(*h, value.(OID)) }
func (h *viewDumpReady) Pop() any {
	last := len(*h) - 1
	value := (*h)[last]
	*h = (*h)[:last]
	return value
}

// The catalog already validates acyclic view sources. This sorts only the
// captured dump graph, without reading a possibly replaced catalog after unlock.
func viewDumpOrder(ctx context.Context, predecessors map[OID][]OID) ([]OID, error) {
	indegree := make(map[OID]int, len(predecessors))
	dependents := make(map[OID][]OID, len(predecessors))
	ready := make(viewDumpReady, 0, len(predecessors))
	for oid, sources := range predecessors {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		indegree[oid] = len(sources)
		if len(sources) == 0 {
			ready = append(ready, oid)
		}
		for _, source := range sources {
			if _, exists := predecessors[source]; !exists {
				return nil, fmt.Errorf("stuntgres: missing view dump predecessor")
			}
			dependents[source] = append(dependents[source], oid)
		}
	}
	heap.Init(&ready)
	order := make([]OID, 0, len(predecessors))
	for ready.Len() > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		oid := heap.Pop(&ready).(OID)
		order = append(order, oid)
		for _, dependent := range dependents[oid] {
			indegree[dependent]--
			if indegree[dependent] == 0 {
				heap.Push(&ready, dependent)
			}
		}
	}
	if len(order) != len(predecessors) {
		return nil, fmt.Errorf("stuntgres: cyclic view dump dependencies")
	}
	return order, nil
}
