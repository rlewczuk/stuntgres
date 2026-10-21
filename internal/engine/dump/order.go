package dump

import (
	"container/heap"
	"context"
	"fmt"

	"stuntgres/internal/engine/ids"
)

// Ready declarations retain source allocation order. Only collision-remapped
// imports need captured priorities; other declarations keep their actual OIDs.
type ready struct {
	ids        []Event
	priorities map[ids.OID]ids.OID
}

func (h ready) Len() int { return len(h.ids) }
func (h ready) Less(i, j int) bool {
	left, right := h.ids[i], h.ids[j]
	a, b := left.OID, right.OID
	if priority := h.priorities[a]; priority != 0 {
		a = priority
	}
	if priority := h.priorities[b]; priority != 0 {
		b = priority
	}
	if a != b {
		return a < b
	}
	if left.OID != right.OID {
		return left.OID < right.OID
	}
	return left.Phase < right.Phase
}
func (h ready) Swap(i, j int)   { h.ids[i], h.ids[j] = h.ids[j], h.ids[i] }
func (h *ready) Push(value any) { h.ids = append(h.ids, value.(Event)) }
func (h *ready) Pop() any {
	last := len(h.ids) - 1
	value := h.ids[last]
	h.ids = h.ids[:last]
	return value
}

// Order sorts the captured dump graph, without reading a possibly replaced
// catalog after unlock. Ties go to the lower priority (the OID when none is
// captured), then the lower OID, then the earlier phase. Cyclic replacement
// graphs, including self edges, fail.
func Order(ctx context.Context, predecessors Graph, priorities map[ids.OID]ids.OID) ([]Event, error) {
	indegree := make(map[Event]int, len(predecessors))
	dependents := make(map[Event][]Event, len(predecessors))
	queue := ready{ids: make([]Event, 0, len(predecessors)), priorities: priorities}
	for oid, sources := range predecessors {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		indegree[oid] = len(sources)
		if len(sources) == 0 {
			queue.ids = append(queue.ids, oid)
		}
		for _, source := range sources {
			if _, exists := predecessors[source]; !exists {
				return nil, fmt.Errorf("stuntgres: missing view dump predecessor")
			}
			dependents[source] = append(dependents[source], oid)
		}
	}
	heap.Init(&queue)
	order := make([]Event, 0, len(predecessors))
	for queue.Len() > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		oid := heap.Pop(&queue).(Event)
		order = append(order, oid)
		for _, dependent := range dependents[oid] {
			indegree[dependent]--
			if indegree[dependent] == 0 {
				heap.Push(&queue, dependent)
			}
		}
	}
	if len(order) != len(predecessors) {
		return nil, fmt.Errorf("stuntgres: cyclic view dump dependencies")
	}
	return order, nil
}
