package catalog

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// IndexEqualityCallback retains the selected hash-family strategy, not a
// fictitious equality support slot. It is immutable operation metadata.
type IndexEqualityCallback struct {
	class    OperatorClass
	family   OperatorFamily
	member   OperatorFamilyOperator
	operator Operator
	routine  orderingRoutineStamp
}

func CaptureIndexEqualityCallback(view Read, usage sqltype.Usage, class sqltype.IndexClass) (GroupingRoutineCallback, error) {
	fact, ok := view.OperatorClass(ids.OID(class.OID()))
	if !ok || !class.IsHash() || fact.Family != ids.OID(class.FamilyOID()) {
		return GroupingRoutineCallback{}, ids.ErrCatalogReplaced
	}
	family, ok := view.OperatorFamily(ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: fact.Family})
	if !ok || family.Method != fact.Method {
		return GroupingRoutineCallback{}, ids.ErrCatalogReplaced
	}
	member, ok := view.FamilyOperator(fact.Family, fact.InputType, fact.InputType, 1, 's')
	if !ok {
		return GroupingRoutineCallback{}, ids.ErrCatalogReplaced
	}
	operator, ok := view.Operator(ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: member.Operator})
	if !ok {
		return GroupingRoutineCallback{}, ids.ErrCatalogReplaced
	}
	helper, ok := view.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: operator.Function})
	if !ok {
		return GroupingRoutineCallback{}, ids.ErrCatalogReplaced
	}
	for _, descriptor := range [5]Descriptor{fact, family, member, operator, helper} {
		provider := view.Owner().Provider(descriptor.Address().ClassID)
		if provider == nil {
			return GroupingRoutineCallback{}, ids.ErrCatalogReplaced
		}
		if err := provider.Validate(view, descriptor); err != nil {
			return GroupingRoutineCallback{}, err
		}
	}
	return GroupingRoutineCallback{
		Type: usage.Type, Procedure: helper, Reference: view.Domain().Ref(view.Epoch(), helper.Address()),
		Class:         view.Domain().Ref(view.Epoch(), fact.Address()),
		IndexEquality: &IndexEqualityCallback{class: *fact, family: *family, member: *member, operator: *operator, routine: orderingRoutineOf(helper)},
	}, nil
}

func (c *IndexEqualityCallback) validate(view Read, callback GroupingRoutineCallback) error {
	class, ok := view.IndexClass(callback.Class.Address.ObjectID)
	if !ok {
		return ids.ErrCatalogReplaced
	}
	current, err := CaptureIndexEqualityCallback(view, callback.Type.Usage(), class)
	if err != nil {
		return err
	}
	if current.Reference != callback.Reference || current.Class != callback.Class || *current.IndexEquality != *c {
		return ids.ErrCatalogReplaced
	}
	return nil
}
