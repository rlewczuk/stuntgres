package routine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// Commutator and negator are reciprocal links, deliberately not dependencies.
func OperatorDependencyAddresses(view catalog.Read, d *catalog.Operator) []ids.ObjectAddress {
	refs := make([]ids.ObjectAddress, 0, 7)
	VisitOperatorDependencyAddresses(view, d, func(address ids.ObjectAddress) bool {
		refs = append(refs, address)
		return true
	})
	return refs
}

// VisitOperatorDependencyAddresses visits the same deduplicated hard edges
// without allocating a list for definition admission.
func VisitOperatorDependencyAddresses(view catalog.Read, d *catalog.Operator, yield func(ids.ObjectAddress) bool) {
	var seen [7]ids.ObjectAddress
	count := 0
	add := func(address ids.ObjectAddress) bool {
		if address.ObjectID == 0 {
			return true
		}
		for _, old := range seen[:count] {
			if old == address {
				return true
			}
		}
		seen[count] = address
		count++
		return yield(address)
	}
	if !add(d.Namespace()) {
		return
	}
	for _, id := range [...]ids.OID{d.Left, d.Right, d.Result} {
		if !add(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: id}) {
			return
		}
	}
	for _, id := range [...]ids.OID{d.Function, d.Restriction, d.Join} {
		address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: id}
		if _, ok := view.Find(address); ok && !add(address) {
			return
		}
	}
}

// OperatorDependsOn reports whether target is one of
// OperatorDependencyAddresses(view, d) without building that list.
func OperatorDependsOn(view catalog.Read, d *catalog.Operator, target ids.ObjectAddress) bool {
	if target.ObjectID == 0 {
		return false
	}
	if target == d.Namespace() {
		return true
	}
	if target.SubID != 0 {
		return false
	}
	switch target.ClassID {
	case ids.TypeClassID:
		return target.ObjectID == d.Left || target.ObjectID == d.Right || target.ObjectID == d.Result
	case ids.ProcedureClassID:
		if target.ObjectID == d.Function || target.ObjectID == d.Restriction || target.ObjectID == d.Join {
			_, ok := view.Find(target)
			return ok
		}
	}
	return false
}
