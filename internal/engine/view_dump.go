package engine

import (
	"container/heap"
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
)

// Standalone sequences are emitted before this graph. An identity sequence is
// instead created by its owning table's inline declaration, so dependencies on
// it must wait for that exact reciprocal owner, even after address remapping.
func dumpPredecessor(view catalog.Read, address, consumer ids.ObjectAddress) (ids.OID, error) {
	address.SubID = 0
	if address.ClassID == ids.ProcedureClassID {
		if _, builtin := routineByOID[address.ObjectID]; builtin {
			return 0, nil
		}
	}
	object, exists := view.Descriptor(address)
	if !exists {
		return 0, fmt.Errorf("stuntgres: missing dump dependency")
	}
	if extension, ok := object.(*catalog.Extension); ok {
		namespace, ok := view.Namespace(extension.TargetNamespace)
		if !ok || namespace.Temporary != (ids.SessionToken{}) {
			return 0, fmt.Errorf("stuntgres: invalid extension dump dependency lifetime")
		}
		return extension.Address().ObjectID, nil
	}
	if operator, ok := object.(*catalog.Operator); ok {
		if routine.OperatorSeeded(operator) {
			return 0, nil
		}
		if operator.Function != 0 {
			return operator.Address().ObjectID, nil
		}
		if source := operatorShellGenerator(view, operator); source != nil {
			return source.Address().ObjectID, nil
		}
		return operator.Address().ObjectID, nil
	}
	if proc, ok := object.(*catalog.Procedure); ok {
		if proc.SQL != nil {
			return proc.Address().ObjectID, nil
		}
		ext, ok := view.Extension(proc.Extension)
		if !ok {
			return 0, fmt.Errorf("stuntgres: missing installed function dump predecessor")
		}
		return ext.Address().ObjectID, nil
	}
	if dictionary, ok := object.(*catalog.TSDictionary); ok {
		return dumpPredecessor(view, dictionary.Extension, consumer)
	}
	if typ, ok := object.(*catalog.Type); ok {
		if typ.SQLType.IsArray() {
			return dumpPredecessor(view, ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.SQLType.Element().ID())}, consumer)
		}
		if typ.Extension != (ids.ObjectAddress{}) {
			return dumpPredecessor(view, typ.Extension, consumer)
		}
		if typ.OwningRelation == (ids.ObjectAddress{}) && typ.SQLType.IsUserDefined() {
			return typ.Address().ObjectID, nil
		}
	}
	if typ, ok := object.(*catalog.Type); ok && typ.OwningRelation != (ids.ObjectAddress{}) {
		object, exists = view.Descriptor(typ.OwningRelation)
		if !exists {
			return 0, fmt.Errorf("stuntgres: missing row type dump owner")
		}
	}
	source, relation := object.(*catalog.Relation)
	if !relation || source.RelationKind == catalog.CatalogRelation {
		return 0, nil
	}
	// Inline attachments collapse to their parent. INCLUDE UNIQUE/PK indexes
	// instead have a real CREATE + attachment graph node of their own.
	if source.RelationKind == catalog.IndexRelation && source.SQLIndex != nil && source.SQLIndex.Constraint != nil {
		unique := source.SQLIndex.Constraint
		parent, ok := view.Relation(source.SQLIndex.Parent)
		if !ok || (parent.RelationKind != catalog.TableRelation && parent.RelationKind != catalog.PartitionedRelation) || parent.Namespace() != source.Namespace() || parent.Owner != source.Owner || unique.Index != source.Address() {
			return 0, fmt.Errorf("stuntgres: invalid unique index dump dependency")
		}
		if !backingConstraintPresent(parent, source) {
			return 0, fmt.Errorf("stuntgres: orphan unique index dump dependency")
		}
		if producer, moved, err := physicalCompetingIndexSource(view, source); err != nil {
			return 0, err
		} else if moved {
			source = producer
		}
		if root := partitionReplayVirtualUniqueRoot(view, source); root != nil {
			source = root
		} else if !partitionReplayUsingIndex(view, source) {
			source = parent
		}
	}
	if source.RelationKind == catalog.SequenceRelation {
		if !source.Sequence.Identity {
			return 0, nil
		}
		owned := source.Sequence.Owned
		ownerAddress := owned
		ownerAddress.SubID = 0
		table, ok := view.Relation(ownerAddress)
		if !ok || table.RelationKind != catalog.TableRelation || !table.HasAttribute(owned.SubID) || table.ColumnMeta.Identities[owned.SubID].Sequence != address || table.Namespace() != source.Namespace() {
			return 0, fmt.Errorf("stuntgres: invalid identity dump dependency")
		}
		source = table
	}
	namespace, exists := view.Namespace(source.Namespace())
	if !exists || namespace.Temporary != (ids.SessionToken{}) {
		return 0, fmt.Errorf("stuntgres: invalid dump dependency lifetime")
	}
	if source.Address() == consumer && source.RelationKind != catalog.ViewRelation {
		return 0, nil
	}
	return source.Address().ObjectID, nil
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
	dumpInvalidIndexPreparation
	dumpInvalidIndexCompletion
	dumpInheritanceRecipes
	dumpInheritanceChecks
	dumpRetiredConstraints
	dumpInheritedForeignKeys
	dumpDomainChecks
	dumpOperatorLinks
)

type dumpEventKey struct {
	oid   ids.OID
	phase dumpPhase
}

func dumpDeclarationEvents(ids []ids.OID) []dumpEventKey {
	events := make([]dumpEventKey, len(ids))
	for i, oid := range ids {
		events[i] = dumpEventKey{oid: oid}
	}
	return events
}

func dumpDeclarationIDs(events []dumpEventKey) []ids.OID {
	ids := make([]ids.OID, 0, len(events))
	for _, event := range events {
		if event.phase == dumpDeclaration {
			ids = append(ids, event.oid)
		}
	}
	return ids
}

type viewDumpReady struct {
	ids        []dumpEventKey
	priorities map[ids.OID]ids.OID
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
func viewDumpOrder(ctx context.Context, predecessors map[dumpEventKey][]dumpEventKey, priorities map[ids.OID]ids.OID) ([]dumpEventKey, error) {
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
