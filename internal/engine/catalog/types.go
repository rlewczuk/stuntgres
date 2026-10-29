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
	// DomainPlanStamp separates domain/companion lexical identity edits from
	// executable/authority revisions. Zero uses Revision(); ordinary revised
	// replacement clears it.
	DomainPlanStamp ids.DefinitionRevision
	Owner           authorization.PrincipalRef
	Extension       ids.ObjectAddress
	Origin          *InstalledOrigin
	ACL             authorization.ACLValue
	// ACLSyntheticDefault distinguishes effective baseline USAGE from raw NULL
	// typacl. Copies/owner remaps retain it; genuine ACL replacement clears it.
	ACLSyntheticDefault bool
	Dimensions          int32
	Array               sqltype.ID
	OwningRelation      ids.ObjectAddress
	DomainChecks        []DomainCheckConstraint
	DomainNotNull       *DomainNotNullConstraint
	// DomainDefault is the immutable effective recipe copied at creation.
	// A nonnil NULL recipe masks an inherited default; it has no pg_attrdef OID.
	DomainDefault    *ColumnDefault
	DomainDefaultOwn bool
	// DomainDefaultText is creation-time presentation, copied with inheritance.
	// It is not executable SQL or a provider/visibility capability.
	DomainDefaultText string
}

// DomainNotNullConstraint is a genuine domain-owned definition. Its namespace
// is always the current namespace of its domain; identity edits retain the OID.
type DomainNotNullConstraint struct {
	OID  ids.OID
	Name string
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

// UsesDomainPlanStamp selects domains and immediate domain-element companions,
// not unrelated arrays. Annotation reconstruction checks reciprocal companion
// authority against the live catalog.
func (d *Type) UsesDomainPlanStamp() bool {
	return d.SQLType.IsDomain() || d.SQLType.IsArray() && d.SQLType.Element().IsDomain()
}

func (d *Type) EffectiveDomainPlanStamp() ids.DefinitionRevision {
	if d.DomainPlanStamp != 0 {
		return d.DomainPlanStamp
	}
	return d.Revision()
}

// NormalizeTypeReplacement is shared by staged DDL and ACL publication. Revised
// ordinary domain/companion replacements invalidate analysis by default. Only certified
// name/namespace edits preserve it; same-revision generation refresh retains the
// already normalized private predecessor rather than reviving the base stamp.
func NormalizeTypeReplacement(previous, next Descriptor, preserveIdentity bool) Descriptor {
	d, ok := next.(*Type)
	if !ok || !d.UsesDomainPlanStamp() {
		return next
	}
	old, ok := previous.(*Type)
	if !ok {
		return next
	}
	var stamp ids.DefinitionRevision
	if d.Revision() == old.Revision() {
		stamp = old.DomainPlanStamp
	} else if preserveIdentity {
		stamp = old.EffectiveDomainPlanStamp()
	}
	if d.DomainPlanStamp == stamp {
		return next
	}
	copy := *d
	copy.DomainPlanStamp = stamp
	return &copy
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
