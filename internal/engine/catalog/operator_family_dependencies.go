package catalog

import (
	"strconv"
	"strings"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
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
		return []FamilyDependency{{d.Namespace(), 'n'}}
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
		deps[0] = FamilyDependency{ids.ObjectAddress{ClassID: 2616, ObjectID: owning}, 'i'}
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

// FamilyMemberClass applies btadjustmembers/hashadjustmembers at insertion.
// Optional and cross-type rows are loose even when a matching class exists.
func FamilyMemberClass(method, family, left, right ids.OID, number int16, function bool) ids.OID {
	if left != right || method != 403 && method != 405 || function && number != 1 {
		return 0
	}
	for i := range sqltype.IndexClassMetadataCount() {
		row, ok := sqltype.IndexClassMetadataAt(i)
		if ok && row.Method == uint32(method) && row.Family == uint32(family) && row.Input == uint32(left) {
			return ids.OID(row.OID)
		}
	}
	return 0
}
