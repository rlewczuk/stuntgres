package engine

import (
	"container/heap"
	"context"
	"fmt"
)

// Standalone sequences are emitted before this graph. An identity sequence is
// instead created by its owning table's inline declaration, so dependencies on
// it must wait for that exact reciprocal owner, even after address remapping.
func dumpPredecessor(view CatalogRead, address, consumer ObjectAddress) (OID, error) {
	address.SubID = 0
	object, exists := view.Descriptor(address)
	if !exists {
		return 0, fmt.Errorf("stuntgres: missing dump dependency")
	}
	if typ, ok := object.(*typeDescriptor); ok {
		if typ.typ.IsArray() {
			return dumpPredecessor(view, ObjectAddress{ClassID: TypeClassID, ObjectID: OID(typ.typ.Element().ID())}, consumer)
		}
		if typ.owningRelation == (ObjectAddress{}) && typ.typ.IsUserDefined() {
			return typ.Address().ObjectID, nil
		}
	}
	if typ, ok := object.(*typeDescriptor); ok && typ.owningRelation != (ObjectAddress{}) {
		object, exists = view.Descriptor(typ.owningRelation)
		if !exists {
			return 0, fmt.Errorf("stuntgres: missing row type dump owner")
		}
	}
	source, relation := object.(*relationDescriptor)
	if !relation || source.relationKind == catalogRelation {
		return 0, nil
	}
	// Only constraint attachments are created inline with their parent.
	// Standalone indexes retain their own exact predecessor graph node.
	if source.relationKind == indexRelation && source.sqlIndex != nil && source.sqlIndex.constraint != nil {
		unique := source.sqlIndex.constraint
		parent, ok := view.state.descriptors[source.sqlIndex.parent].(*relationDescriptor)
		if !ok || parent.relationKind != tableRelation || parent.namespace != source.namespace || parent.owner != source.owner || unique.Index != source.address {
			return 0, fmt.Errorf("stuntgres: invalid unique index dump dependency")
		}
		if !backingConstraintPresent(parent, source) {
			return 0, fmt.Errorf("stuntgres: orphan unique index dump dependency")
		}
		source = parent
	}
	if source.relationKind == sequenceRelation {
		if !source.sequence.identity {
			return 0, nil
		}
		owned := source.sequence.owned
		ownerAddress := owned
		ownerAddress.SubID = 0
		table, ok := view.state.descriptors[ownerAddress].(*relationDescriptor)
		if !ok || table.relationKind != tableRelation || !table.hasAttribute(owned.SubID) || table.identities[owned.SubID].sequence != address || table.namespace != source.namespace {
			return 0, fmt.Errorf("stuntgres: invalid identity dump dependency")
		}
		source = table
	}
	namespace, exists := view.state.descriptors[source.namespace].(*namespaceDescriptor)
	if !exists || namespace.temporary != (SessionToken{}) {
		return 0, fmt.Errorf("stuntgres: invalid dump dependency lifetime")
	}
	if source.address == consumer && source.relationKind != viewRelation {
		return 0, nil
	}
	return source.address.ObjectID, nil
}

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

// This sorts the captured dump graph, without reading a possibly replaced
// catalog after unlock. Cyclic replacement graphs, including self edges, fail.
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
