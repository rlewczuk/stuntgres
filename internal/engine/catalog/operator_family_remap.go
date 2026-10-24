package catalog

import "stuntgres/internal/engine/ids"

// RemapOperatorFamily copies only descriptors whose identities actually change.
// No source member or operation is adopted by a destination preparation.
func RemapOperatorFamily(d Descriptor, mapping map[ids.ObjectAddress]ids.ObjectAddress) Descriptor {
	move := func(a ids.ObjectAddress) ids.ObjectAddress {
		if n, ok := mapping[a]; ok {
			return n
		}
		return a
	}
	oid := func(class, id ids.OID) ids.OID { return move(ids.ObjectAddress{ClassID: class, ObjectID: id}).ObjectID }
	switch d := d.(type) {
	case *OperatorFamily:
		n := *d
		n.ObjectAddress = move(d.Address())
		n.ObjectNamespace = move(d.Namespace())
		n.Extension = move(d.Extension)
		n.Method = oid(2601, d.Method)
		if n == *d {
			return d
		}
		return &n
	case *OperatorFamilyOperator:
		n := *d
		n.ObjectAddress = move(d.Address())
		n.Family = oid(ids.OperatorFamilyClassID, d.Family)
		n.SortFamily = oid(ids.OperatorFamilyClassID, d.SortFamily)
		n.Left = oid(ids.TypeClassID, d.Left)
		n.Right = oid(ids.TypeClassID, d.Right)
		n.Operator = oid(ids.OperatorClassID, d.Operator)
		n.Method = oid(2601, d.Method)
		n.InternalOwner = move(d.InternalOwner)
		if n == *d {
			return d
		}
		return &n
	case *OperatorFamilySupport:
		n := *d
		n.ObjectAddress = move(d.Address())
		n.Family = oid(ids.OperatorFamilyClassID, d.Family)
		n.Left = oid(ids.TypeClassID, d.Left)
		n.Right = oid(ids.TypeClassID, d.Right)
		n.Function = oid(ids.ProcedureClassID, d.Function)
		n.InternalOwner = move(d.InternalOwner)
		if n == *d {
			return d
		}
		return &n
	}
	return d
}
