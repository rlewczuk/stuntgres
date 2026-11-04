package analyze

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/sqltype"
)

// Capture only callbacks whose kernels Grouping actually executes. Cube's
// dimension fingerprint has no PostgreSQL hash procedure and gets no callback.
func (b *Binder) captureGroupingCallbacks(group sqltype.Grouping, usage sqltype.Usage, source expr.Info) (sqltype.Grouping, error) {
	if owner := b.BindingCatalog().Owner(); owner == nil || owner.Sepgsql == nil {
		return group, nil
	}
	if element, ok := group.ElementGrouping(); ok {
		child := usage.Type.BaseType().Element().Usage()
		child.Collation = usage.Collation
		captured, err := b.captureGroupingCallbacks(element, child, source)
		if err != nil {
			return sqltype.Grouping{}, err
		}
		return group.WithElementGrouping(captured)
	}
	base := usage.Type.BaseType()
	if !base.IsHstore() && !base.IsLtree() && !base.IsCube() && !base.IsCitext() && !base.IsISN() {
		return group, nil
	}
	view := b.BindingCatalog()
	ref, p, private, err := b.groupingRoutineReference(usage)
	if err != nil {
		return sqltype.Grouping{}, err
	}
	if private || p == nil {
		return group, nil
	}
	var equal, hash sqltype.GroupingCallbackReference
	if !routine.FmgrBuiltin(p) {
		equal = catalog.GroupingRoutineCallback{Type: usage.Type, Procedure: p, Reference: ref, Occurrence: source.Occurrence, ExpressionSource: source.Source}
	}
	if base.IsHstore() || base.IsLtree() {
		class, ok, err := view.DefaultIndexClass(405, base)
		if err != nil {
			return sqltype.Grouping{}, err
		}
		if !ok {
			return sqltype.Grouping{}, fmt.Errorf("stuntgres: missing grouping hash class")
		}
		class, err = routine.BindClassSupport(view, class)
		if err != nil {
			return sqltype.Grouping{}, err
		}
		support := class.Support(2)
		helper, ok := view.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(support.Routine)})
		if !ok {
			return sqltype.Grouping{}, fmt.Errorf("stuntgres: missing grouping hash callback")
		}
		callback := catalog.GroupingRoutineCallback{Type: usage.Type, Procedure: helper, Reference: view.Domain().Ref(view.Epoch(), helper.Address()), Class: view.Domain().Ref(view.Epoch(), ids.ObjectAddress{ClassID: ids.OperatorClassCatalogID, ObjectID: ids.OID(class.OID())}), Support: support, Hash: true, Occurrence: source.Occurrence, ExpressionSource: source.Source}
		if err := callback.Validate(view); err != nil {
			return sqltype.Grouping{}, err
		}
		hash = callback
	}
	return group.WithCallbacks(equal, hash)
}
