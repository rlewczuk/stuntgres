package catalog

import (
	"strconv"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
)

// OperatorFamily and its members are immutable catalog descriptors. Associated
// types are independent of the referenced routine's formal argument types.
type OperatorFamily struct {
	ObjectAddress, ObjectNamespace ids.ObjectAddress
	Extension                      ids.ObjectAddress
	ObjectRevision                 ids.DefinitionRevision
	ObjectName                     string
	Method                         ids.OID
	Owner                          authorization.PrincipalRef
}

func (d *OperatorFamily) Address() ids.ObjectAddress       { return d.ObjectAddress }
func (d *OperatorFamily) Namespace() ids.ObjectAddress     { return d.ObjectNamespace }
func (d *OperatorFamily) Revision() ids.DefinitionRevision { return d.ObjectRevision }
func (d *OperatorFamily) Name() string                     { return d.ObjectName }
func (*OperatorFamily) Kind() ids.ObjectKind               { return ids.OperatorFamilyObject }

type OperatorFamilyOperator struct {
	ObjectAddress                                     ids.ObjectAddress
	ObjectRevision                                    ids.DefinitionRevision
	Family, Left, Right, Operator, Method, SortFamily ids.OID
	// OwningClass is the insertion-time INTERNAL dependency, or zero for loose members.
	OwningClass ids.OID
	Number      int16
	Purpose     byte
}

func (d *OperatorFamilyOperator) Address() ids.ObjectAddress       { return d.ObjectAddress }
func (*OperatorFamilyOperator) Namespace() ids.ObjectAddress       { return ids.ObjectAddress{} }
func (d *OperatorFamilyOperator) Revision() ids.DefinitionRevision { return d.ObjectRevision }
func (d *OperatorFamilyOperator) Name() string {
	return familyMemberName(d.Family, d.Left, d.Right, d.Number, 0)
}
func (*OperatorFamilyOperator) Kind() ids.ObjectKind { return ids.OperatorFamilyOperatorObject }

type OperatorFamilySupport struct {
	ObjectAddress                 ids.ObjectAddress
	ObjectRevision                ids.DefinitionRevision
	Family, Left, Right, Function ids.OID
	OwningClass                   ids.OID
	Number                        int16
}

func (d *OperatorFamilySupport) Address() ids.ObjectAddress       { return d.ObjectAddress }
func (*OperatorFamilySupport) Namespace() ids.ObjectAddress       { return ids.ObjectAddress{} }
func (d *OperatorFamilySupport) Revision() ids.DefinitionRevision { return d.ObjectRevision }
func (d *OperatorFamilySupport) Name() string {
	return familyMemberName(d.Family, d.Left, d.Right, d.Number, 0)
}
func (*OperatorFamilySupport) Kind() ids.ObjectKind { return ids.OperatorFamilySupportObject }

func familyMemberName(family, left, right ids.OID, number int16, purpose byte) string {
	return strconv.FormatUint(uint64(family), 10) + "," + strconv.FormatUint(uint64(left), 10) + "," + strconv.FormatUint(uint64(right), 10) + "," + strconv.Itoa(int(number)) + "," + strconv.Itoa(int(purpose))
}
func FamilyNameKey(namespace ids.ObjectAddress, method ids.OID, name string) ids.NameKey {
	return ids.NameKey{ClassID: ids.OperatorFamilyClassID, Namespace: namespace, Name: name, Signature: strconv.FormatUint(uint64(method), 10)}
}
func (r Read) OperatorFamily(a ids.ObjectAddress) (*OperatorFamily, bool) {
	return descriptorAs[*OperatorFamily](r.state, a)
}
func (r Read) FamilySupport(family, left, right ids.OID, slot int16) (*OperatorFamilySupport, bool) {
	a := r.NameAddress(ids.NameKey{ClassID: ids.AccessMethodProcedureClassID, Name: familyMemberName(family, left, right, slot, 0)})
	return descriptorAs[*OperatorFamilySupport](r.state, a)
}
func (r Read) FamilyOperator(family, left, right ids.OID, strategy int16, purpose byte) (*OperatorFamilyOperator, bool) {
	a := r.NameAddress(ids.NameKey{ClassID: ids.AccessMethodOperatorClassID, Name: familyMemberName(family, left, right, strategy, 0)})
	d, ok := descriptorAs[*OperatorFamilyOperator](r.state, a)
	if !ok || d.Purpose != purpose {
		return nil, false
	}
	return d, true
}

// OperatorFamilyNamespace follows member ownership for lifetime projection;
// members have no independent SQL namespace or namespace-name binding.
func OperatorFamilyNamespace(view Read, d Descriptor) ids.ObjectAddress {
	var family ids.OID
	switch d := d.(type) {
	case *OperatorFamily:
		return d.Namespace()
	case *OperatorFamilyOperator:
		family = d.Family
	case *OperatorFamilySupport:
		family = d.Family
	default:
		return ids.ObjectAddress{}
	}
	if owner, ok := view.OperatorFamily(ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: family}); ok {
		return owner.Namespace()
	}
	return ids.ObjectAddress{}
}
