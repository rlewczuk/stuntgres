package catalog

import (
	"strconv"
	"strings"
	"stuntgres/internal/engine/ids"
)

// FamilyDependency retains the released dependency kind. Loose members have
// AUTO edges to their family and implementation. Explicit type dependencies
// omit types already covered by the implementation's formals. Loose type and
// sort-family edges are AUTO too; original pinning is independent.
type FamilyDependency struct {
	Address ids.ObjectAddress
	Kind    byte
}

func OperatorFamilyDependencies(view Read, d Descriptor) []FamilyDependency {
	var family, left, right, implementation, sort, owning ids.OID
	var class ids.OID
	switch d := d.(type) {
	case *OperatorFamily:
		return []FamilyDependency{{d.Namespace(), 'n'}, {ids.ObjectAddress{ClassID: ids.AccessMethodClassID, ObjectID: d.Method}, 'a'}}
	case *OperatorClass:
		deps := []FamilyDependency{{d.Namespace(), 'n'}, {ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: d.InputType}, 'n'}, {ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: d.Family}, 'a'}}
		if d.StorageType != 0 && d.StorageType != d.InputType {
			deps = append(deps, FamilyDependency{ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: d.StorageType}, 'n'})
		}
		return deps
	case *OperatorFamilyOperator:
		family, left, right, implementation, sort, class, owning = d.Family, d.Left, d.Right, d.Operator, d.SortFamily, ids.OperatorClassID, d.OwningClass
	case *OperatorFamilySupport:
		family, left, right, implementation, class, owning = d.Family, d.Left, d.Right, d.Function, ids.ProcedureClassID, d.OwningClass
	default:
		return nil
	}
	flavor := byte('a')
	if owning != 0 {
		flavor = 'n'
	}
	deps := []FamilyDependency{{ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: family}, 'a'}, {ids.ObjectAddress{ClassID: class, ObjectID: implementation}, flavor}}
	if owning != 0 {
		deps[0] = FamilyDependency{ids.ObjectAddress{ClassID: ids.OperatorClassCatalogID, ObjectID: owning}, 'i'}
	}
	if class == ids.ProcedureClassID {
		identity, _ := BuiltinRoutineIdentityByOID(implementation)
		if procedure, ok := view.Procedure(ids.ObjectAddress{ClassID: class, ObjectID: implementation}); ok {
			identity.Signature = procedure.Signature
		}
		hasFormal := func(id ids.OID) bool {
			if typ, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: id}); ok && TypePinned(view, nil, typ) {
				return true
			}
			spelling := strconv.FormatUint(uint64(id), 10)
			for formal := range strings.SplitSeq(string(identity.Signature), ",") {
				if formal == spelling {
					return true
				}
			}
			return false
		}
		if !hasFormal(left) {
			deps = append(deps, FamilyDependency{ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: left}, flavor})
		}
		if right != left && !hasFormal(right) {
			deps = append(deps, FamilyDependency{ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: right}, flavor})
		}
	}
	if sort != 0 {
		deps = append(deps, FamilyDependency{ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: sort}, flavor})
	}
	return deps
}

// OperatorFamilyMemberOwner returns only the insertion-time class owner.
// Loose members, including classless family entries, have no INTERNAL owner.
func OperatorFamilyMemberOwner(d Descriptor) ids.ObjectAddress {
	var owning ids.OID
	switch d := d.(type) {
	case *OperatorFamilyOperator:
		owning = d.OwningClass
	case *OperatorFamilySupport:
		owning = d.OwningClass
	}
	if owning == 0 {
		return ids.ObjectAddress{}
	}
	return ids.ObjectAddress{ClassID: ids.OperatorClassCatalogID, ObjectID: owning}
}

// FamilyMemberClass applies btadjustmembers/hashadjustmembers at insertion.
// Optional and cross-type rows are loose even when a matching class exists.
// CREATE CLASS prefers its own descriptor for matching input; other eligible
// inputs and ALTER FAMILY use the existing family/datatype lookup.
func FamilyMemberClass(view Read, method, family, left, right ids.OID, number int16, function bool, creating *OperatorClass) ids.OID {
	fact, ok := view.AccessMethod(method)
	if !ok || left != right || fact.Native != BtreeAccessMethod && fact.Native != HashAccessMethod || function && number != 1 {
		return 0
	}
	if creating != nil && creating.Method == method && creating.Family == family && creating.InputType == left {
		return creating.Address().ObjectID
	}
	for _, row := range view.OperatorClasses() {
		if row.Method == method && row.Family == family && row.InputType == left {
			return row.Address().ObjectID
		}
	}
	return 0
}
