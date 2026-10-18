package catalog

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

type Column struct {
	Name                       string
	Usage                      sqltype.Usage
	DeclaredDimensions         int16
	Nullable, NullabilityKnown bool
	OriginTable                uint32
	OriginAttribute            int16
}

// Catalog wrappers retain the existing class/namespace/revision/epoch ownership
// seam. A sqltype ID is a projection, not an authorization token.
type Type struct {
	SQLType         *sqltype.Descriptor
	Comment         string
	ObjectNamespace ids.ObjectAddress
	ObjectRevision  ids.DefinitionRevision
	Owner           authorization.PrincipalRef
	Extension       ids.ObjectAddress
	ACL             authorization.ACLValue
	// ACLSyntheticDefault distinguishes effective baseline USAGE from raw NULL
	// typacl. Copies/owner remaps retain it; genuine ACL replacement clears it.
	ACLSyntheticDefault bool
	Dimensions          int32
	Array               sqltype.ID
	OwningRelation      ids.ObjectAddress
	DomainChecks        []DomainCheckConstraint
}

func (d *Type) Address() ids.ObjectAddress {
	return ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(d.SQLType.ID())}
}

func (d *Type) Revision() ids.DefinitionRevision {
	if d.ObjectRevision == 0 {
		return 1
	}
	return d.ObjectRevision
}

func (d *Type) Namespace() ids.ObjectAddress {
	if d.ObjectNamespace != (ids.ObjectAddress{}) {
		return d.ObjectNamespace
	}
	return ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
}

func (d *Type) Name() string { return d.SQLType.Name() }

func (*Type) Kind() ids.ObjectKind { return ids.TypeObject }

func (r Read) LookupType(id sqltype.ID) (*sqltype.Descriptor, bool) {
	d, ok := r.Descriptor(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(id)})
	if !ok {
		return nil, false
	}
	t, ok := d.(*Type)
	if !ok {
		return nil, false
	}
	return t.SQLType, true
}

func (r Read) LookupArray(element *sqltype.Descriptor) (*sqltype.Descriptor, bool) {
	if element == nil {
		return nil, false
	}
	owner, ok := r.Descriptor(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(element.ID())})
	if !ok || owner.(*Type).SQLType != element {
		return nil, false
	}
	array, ok := r.LookupType(owner.(*Type).Array)
	return array, ok && array.IsArray() && array.Element() == element
}
