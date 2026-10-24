package extension

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// LoProvider installs logical large-object reference identity over ordinary OID
// values. It neither looks up objects nor attaches reference-management triggers.
type LoProvider struct{ control catalog.ExtensionControl }

var StockLo = &LoProvider{control: catalog.ExtensionControl{Version: "1.2", Superuser: true, Trusted: true, Relocatable: true, Comment: new("Large Object maintenance")}}

func (p *LoProvider) Control() catalog.ExtensionControl { return p.control.Detached() }
func (*LoProvider) Versions() catalog.ExtensionVersions {
	return catalog.ExtensionVersions{Bases: []string{"1.2"}}
}

func (*LoProvider) Stage(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef) error {
	base, ok := stage.Read().LookupType(sqltype.Oid)
	if !ok {
		return fmt.Errorf("stuntgres: missing lo OID base")
	}
	available, err := stage.DisplaceAssociatedArray(ext.TargetNamespace, "lo")
	if err != nil {
		return err
	}
	if !available || typedef.RelationTypeNameOccupied(stage.Read(), ext.TargetNamespace, "lo") {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42710", Message: `type "lo" already exists`, Context: "SQL statement \"CREATE DOMAIN lo AS pg_catalog.oid\"\nextension script file \"lo--1.1.sql\", near line 10"}
	}
	oid, err := stage.AllocateOID()
	if err != nil {
		return err
	}
	arrayOID, err := stage.AllocateOID()
	if err != nil {
		return err
	}
	declaration := catalog.DependencyIdentifier(stage.Read().Get(ext.TargetNamespace).Name()) + ".lo"
	typ, err := sqltype.Domain(sqltype.ID(oid), "lo", declaration, base.Usage())
	if err != nil {
		return err
	}
	array := sqltype.LookupArray(sqltype.ID(arrayOID), stage.AssociatedArrayName(ext.TargetNamespace, "lo"), typ)
	revision, err := stage.Revision()
	if err != nil {
		return err
	}
	acl, synthetic := stage.CreationTypeACL(owner, ext.TargetNamespace)
	domain := &catalog.Type{SQLType: typ, ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, Owner: owner, ACL: acl, ACLSyntheticDefault: synthetic, Array: array.ID()}
	ext.Members = []ids.ObjectAddress{domain.Address(), {ClassID: ids.TypeClassID, ObjectID: arrayOID}}
	if err := stage.Create(domain); err != nil {
		return err
	}
	return stage.Create(&catalog.Type{SQLType: array, ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, Owner: owner, ACL: authorization.DefaultBaseline(owner, authorization.DefaultTypes, false), ACLSyntheticDefault: true})
}

func (p *LoProvider) ValidateExtension(v catalog.Read, ext *catalog.Extension) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid lo manifest") }
	if p != StockLo || ext == nil || ext.Name() != "lo" || ext.Version != "1.2" || len(ext.Members) != 2 || v.Owner() == nil || v.Owner().Extensions["lo"] != p {
		return invalid()
	}
	if err := ValidateProviderHeader(v, ext, ids.ExtensionClassID); err != nil {
		return err
	}
	current, ok := v.Extension(ext.Address())
	if !ok || current.Revision() != ext.Revision() || current.Name() != ext.Name() || current.Version != ext.Version || current.Owner != ext.Owner || current.TargetNamespace != ext.TargetNamespace || len(current.Members) != 2 || current.Members[0] != ext.Members[0] || current.Members[1] != ext.Members[1] {
		return invalid()
	}
	base, ok := v.LookupType(sqltype.Oid)
	if !ok || ext.Members[0] == ext.Members[1] {
		return invalid()
	}
	for _, address := range ext.Members {
		if address.ClassID != ids.TypeClassID || address.ObjectID == 0 || address.SubID != 0 {
			return invalid()
		}
	}
	d, ok := v.Type(ext.Members[0])
	if !ok || d.SQLType == nil || d.Address() != ext.Members[0] || !d.SQLType.IsDomain() || !d.SQLType.Concrete() || d.SQLType.DomainBase() != base.Usage() || d.SQLType.HasDomainConstraints() || len(d.DomainChecks) != 0 || d.DomainNotNull != nil || d.DomainDefault != nil || d.DomainDefaultOwn || d.DomainDefaultText != "" || d.Dimensions != 0 || d.OwningRelation != (ids.ObjectAddress{}) || d.Extension != ext.Address() {
		return invalid()
	}
	a, ok := v.Type(ext.Members[1])
	if !ok || a.SQLType == nil || a.Address() != ext.Members[1] || a.Address().ObjectID != ids.OID(d.Array) || !a.SQLType.IsArray() || a.SQLType.Concrete() || a.SQLType.Element() != d.SQLType || a.Array != 0 || a.Owner != d.Owner || a.Namespace() != d.Namespace() || a.Extension != ext.Address() || a.OwningRelation != (ids.ObjectAddress{}) || a.Dimensions != 0 || len(a.DomainChecks) != 0 || a.DomainNotNull != nil || a.DomainDefault != nil || a.DomainDefaultOwn || a.DomainDefaultText != "" {
		return invalid()
	}
	for _, member := range []*catalog.Type{d, a} {
		// Type.Revision supplies a bootstrap fallback for zero; installed
		// members must carry their own allocated catalog revision.
		if member.ObjectRevision == 0 {
			return invalid()
		}
		if err := ValidateProviderHeader(v, member, ids.TypeClassID); err != nil {
			return err
		}
	}
	return nil
}
