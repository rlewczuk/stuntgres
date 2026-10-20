package routine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// Commutator and negator are reciprocal links, deliberately not dependencies.
func OperatorDependencyAddresses(view catalog.Read, d *catalog.Operator) []ids.ObjectAddress {
	refs := make([]ids.ObjectAddress, 0, 7)
	add := func(a ids.ObjectAddress) {
		if a.ObjectID == 0 {
			return
		}
		for _, old := range refs {
			if old == a {
				return
			}
		}
		refs = append(refs, a)
	}
	add(d.Namespace())
	for _, id := range [...]ids.OID{d.Left, d.Right, d.Result} {
		add(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: id})
	}
	for _, id := range [...]ids.OID{d.Function, d.Restriction, d.Join} {
		a := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: id}
		if _, ok := view.Find(a); ok {
			add(a)
		}
	}
	return refs
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
