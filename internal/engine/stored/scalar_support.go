package stored

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqltype"
)

func (r ScalarRenderer) descriptor(view catalog.Read, address ids.ObjectAddress) catalog.Descriptor {
	descriptor, _ := catalog.DescriptorAt(view, r.Definition, address)
	return descriptor
}

func (r ScalarRenderer) sameRoutineDescriptor(view catalog.Read, address ids.ObjectAddress, captured *catalog.Procedure) bool {
	declaration, ok := r.descriptor(view, address).(*catalog.Procedure)
	return ok && exec.SameRoutineDeclaration(declaration, captured)
}

// Both stored-expression capture and provider validation require the same live
// stable member of the actual named rowtype/backing-relation pair. This is
// metadata authority only; it does not authorize reading another table's rows.
func CompositeAttributeProvider(typ *catalog.Type, relation *catalog.Relation, attribute int32) bool {
	if typ == nil || typ.SQLType == nil || relation == nil || attribute <= 0 || relation.Address().ClassID != ids.RelationClassID || typ.OwningRelation != relation.Address() || relation.RowType != typ.SQLType.ID() || !typ.SQLType.IsComposite() {
		return false
	}
	index, exists := typ.SQLType.CompositeAttributeIndex(attribute)
	slot := relation.IndexForAttribute(attribute)
	if !exists || slot < 0 {
		return false
	}
	member := typ.SQLType.CompositeMember(index)
	return relation.Columns[slot].Name == member.Name && relation.Columns[slot].Usage == member.Usage
}

// Only meaningful NULL masks or deferred domain conversions survive ordinary
// column-default elision. Plain builtin DEFAULT NULL retains its old absence.
func OmitColumnDefault(view catalog.Read, node expr.Expr, usage sqltype.Usage) bool {
	return plan.StructuralNull(node) && typedef.TypeDefault(view, usage.Type) == nil && !node.Properties().RuntimeDomainConversion
}
