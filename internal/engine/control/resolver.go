package control

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/table"
)

// Resolver is Dump's dump.Resolver: it maps a dependency to the dump event
// that must precede its consumer. It reads only its arguments.
type Resolver struct{}

var _ dump.Resolver = Resolver{}

// Predecessor returns the OID of the dump event address waits for, or 0.
func (Resolver) Predecessor(view catalog.Read, address, consumer ids.ObjectAddress) (ids.OID, error) {
	return predecessor(view, address, consumer)
}

// Standalone sequences are emitted before this graph. An identity sequence is
// instead created by its owning table's inline declaration, so dependencies on
// it must wait for that exact reciprocal owner, even after address remapping.
func predecessor(view catalog.Read, address, consumer ids.ObjectAddress) (ids.OID, error) {
	address.SubID = 0
	if address.ClassID == ids.ProcedureClassID {
		if _, builtin := catalog.BuiltinRoutineIdentityByOID(address.ObjectID); builtin {
			return 0, nil
		}
	}
	object, exists := view.Descriptor(address)
	if !exists {
		return 0, fmt.Errorf("stuntgres: missing dump dependency")
	}
	if foreign, ok := object.(*catalog.ForeignObject); ok {
		if err := (catalog.ForeignCatalogProvider{Class: address.ClassID}).Validate(view, foreign); err != nil {
			return 0, err
		}
		return address.ObjectID, nil
	}
	if extension, ok := object.(*catalog.Extension); ok {
		namespace, ok := view.Namespace(extension.TargetNamespace)
		if !ok || namespace.Temporary != (ids.SessionToken{}) {
			return 0, fmt.Errorf("stuntgres: invalid extension dump dependency lifetime")
		}
		return extension.Address().ObjectID, nil
	}
	if method, ok := object.(*catalog.AccessMethod); ok {
		if catalog.AccessMethodSeeded(method) {
			return 0, nil
		}
		return method.Address().ObjectID, nil
	}
	if class, ok := object.(*catalog.OperatorClass); ok {
		if catalog.OperatorClassSeeded(class) && !catalog.OperatorClassSeedModified(class) {
			return 0, nil
		}
		return class.Address().ObjectID, nil
	}
	if _, ok := object.(*catalog.OperatorFamily); ok {
		if catalog.OperatorFamilySeeded(object) {
			return 0, nil
		}
		return object.Address().ObjectID, nil
	}
	if operator, ok := object.(*catalog.Operator); ok {
		if routine.OperatorSeeded(operator) {
			return 0, nil
		}
		if operator.Function != 0 {
			return operator.Address().ObjectID, nil
		}
		if source := routine.OperatorShellGenerator(view, operator); source != nil {
			return source.Address().ObjectID, nil
		}
		return operator.Address().ObjectID, nil
	}
	if proc, ok := object.(*catalog.Procedure); ok {
		if proc.Foreign != nil {
			if _, err := fdw.DumpProcedure(view, address, proc.Signature); err != nil {
				return 0, err
			}
			return 0, nil
		}
		if proc.DynamicCallable() {
			return proc.Address().ObjectID, nil
		}
		ext, ok := view.Extension(proc.Extension)
		if !ok {
			return 0, fmt.Errorf("stuntgres: missing installed function dump predecessor")
		}
		return ext.Address().ObjectID, nil
	}
	if dictionary, ok := object.(*catalog.TSDictionary); ok {
		return predecessor(view, dictionary.Extension, consumer)
	}
	if typ, ok := object.(*catalog.Type); ok {
		if typ.SQLType.IsArray() {
			return predecessor(view, ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.SQLType.Element().ID())}, consumer)
		}
		if typ.Extension != (ids.ObjectAddress{}) {
			return predecessor(view, typ.Extension, consumer)
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
		if !index.BackingConstraintPresent(parent, source) {
			return 0, fmt.Errorf("stuntgres: orphan unique index dump dependency")
		}
		if producer, moved, err := table.PhysicalCompetingIndexSource(view, source); err != nil {
			return 0, err
		} else if moved {
			source = producer
		}
		if root := index.PartitionReplayVirtualUniqueRoot(view, source); root != nil {
			source = root
		} else if !index.PartitionReplayUsingIndex(view, source) {
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

// ColumnPredecessors adds the predecessors of relation's row type and column
// types to predecessors.
func ColumnPredecessors(view catalog.Read, relation *catalog.Relation, predecessors []ids.OID) ([]ids.OID, error) {
	if relation.OfType != (ids.ObjectAddress{}) {
		id, err := predecessor(view, relation.OfType, relation.Address())
		if err != nil {
			return nil, err
		}
		if id != 0 && !slices.Contains(predecessors, id) {
			predecessors = append(predecessors, id)
		}
	}
	for _, column := range relation.Columns {
		id, err := predecessor(view, ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(column.Usage.Type.ID())}, relation.Address())
		if err != nil {
			return nil, err
		}
		if id != 0 && !slices.Contains(predecessors, id) {
			predecessors = append(predecessors, id)
		}
	}
	return predecessors, nil
}
