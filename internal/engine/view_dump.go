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
	if address.ClassID == ProcedureClassID {
		if _, builtin := routineByOID[address.ObjectID]; builtin {
			return 0, nil
		}
	}
	object, exists := view.Descriptor(address)
	if !exists {
		return 0, fmt.Errorf("stuntgres: missing dump dependency")
	}
	if extension, ok := object.(*extensionDescriptor); ok {
		namespace, ok := view.state.descriptors[extension.targetNamespace].(*namespaceDescriptor)
		if !ok || namespace.temporary != (SessionToken{}) {
			return 0, fmt.Errorf("stuntgres: invalid extension dump dependency lifetime")
		}
		return extension.address.ObjectID, nil
	}
	if proc, ok := object.(*procedureDescriptor); ok {
		ext, ok := view.state.descriptors[proc.extension].(*extensionDescriptor)
		if !ok {
			return 0, fmt.Errorf("stuntgres: missing installed function dump predecessor")
		}
		return ext.address.ObjectID, nil
	}
	if dictionary, ok := object.(*tsDictionaryDescriptor); ok {
		return dumpPredecessor(view, dictionary.extension, consumer)
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
	// Inline attachments collapse to their parent. INCLUDE UNIQUE/PK indexes
	// instead have a real CREATE + attachment graph node of their own.
	if source.relationKind == indexRelation && source.sqlIndex != nil && source.sqlIndex.constraint != nil {
		unique := source.sqlIndex.constraint
		parent, ok := view.state.descriptors[source.sqlIndex.parent].(*relationDescriptor)
		if !ok || (parent.relationKind != tableRelation && parent.relationKind != partitionedRelation) || parent.namespace != source.namespace || parent.owner != source.owner || unique.Index != source.address {
			return 0, fmt.Errorf("stuntgres: invalid unique index dump dependency")
		}
		if !backingConstraintPresent(parent, source) {
			return 0, fmt.Errorf("stuntgres: orphan unique index dump dependency")
		}
		if !usingIndexDump(source) {
			source = parent
		}
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

// Ready declarations retain source allocation order. Only collision-remapped
// imports need captured priorities; other declarations keep their actual OIDs.

type dumpPhase uint8

const (
	dumpDeclaration dumpPhase = iota
	dumpRename
	dumpDefaults
	dumpRows
	dumpInvalidConstraints
	dumpIndexAttachment
	dumpTableAttachment
	dumpTableCompletion
)

type dumpEventKey struct {
	oid   OID
	phase dumpPhase
}

func dumpDeclarationEvents(ids []OID) []dumpEventKey {
	events := make([]dumpEventKey, len(ids))
	for i, oid := range ids {
		events[i] = dumpEventKey{oid: oid}
	}
	return events
}

func dumpDeclarationIDs(events []dumpEventKey) []OID {
	ids := make([]OID, 0, len(events))
	for _, event := range events {
		if event.phase == dumpDeclaration {
			ids = append(ids, event.oid)
		}
	}
	return ids
}

type viewDumpReady struct {
	ids        []dumpEventKey
	priorities map[OID]OID
}

func (h viewDumpReady) Len() int { return len(h.ids) }
func (h viewDumpReady) Less(i, j int) bool {
	left, right := h.ids[i], h.ids[j]
	a, b := left.oid, right.oid
	if priority := h.priorities[a]; priority != 0 {
		a = priority
	}
	if priority := h.priorities[b]; priority != 0 {
		b = priority
	}
	if a != b {
		return a < b
	}
	if left.oid != right.oid {
		return left.oid < right.oid
	}
	return left.phase < right.phase
}
func (h viewDumpReady) Swap(i, j int)   { h.ids[i], h.ids[j] = h.ids[j], h.ids[i] }
func (h *viewDumpReady) Push(value any) { h.ids = append(h.ids, value.(dumpEventKey)) }
func (h *viewDumpReady) Pop() any {
	last := len(h.ids) - 1
	value := h.ids[last]
	h.ids = h.ids[:last]
	return value
}

// This sorts the captured dump graph, without reading a possibly replaced
// catalog after unlock. Cyclic replacement graphs, including self edges, fail.
func viewDumpOrder(ctx context.Context, predecessors map[dumpEventKey][]dumpEventKey, priorities map[OID]OID) ([]dumpEventKey, error) {
	indegree := make(map[dumpEventKey]int, len(predecessors))
	dependents := make(map[dumpEventKey][]dumpEventKey, len(predecessors))
	ready := viewDumpReady{ids: make([]dumpEventKey, 0, len(predecessors)), priorities: priorities}
	for oid, sources := range predecessors {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		indegree[oid] = len(sources)
		if len(sources) == 0 {
			ready.ids = append(ready.ids, oid)
		}
		for _, source := range sources {
			if _, exists := predecessors[source]; !exists {
				return nil, fmt.Errorf("stuntgres: missing view dump predecessor")
			}
			dependents[source] = append(dependents[source], oid)
		}
	}
	heap.Init(&ready)
	order := make([]dumpEventKey, 0, len(predecessors))
	for ready.Len() > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		oid := heap.Pop(&ready).(dumpEventKey)
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
