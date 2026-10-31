package extension

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// LoProvider installs logical large-object references and the opt-in lo_manage
// trigger body. Numeric coercion neither looks up objects nor attaches triggers.
type LoProvider struct{ control catalog.ExtensionControl }

var StockLo = &LoProvider{control: catalog.ExtensionControl{Version: "1.2", Superuser: true, Trusted: true, Relocatable: true, Comment: new("Large Object maintenance")}}

func (p *LoProvider) Control() catalog.ExtensionControl { return p.control.Detached() }
func (*LoProvider) Versions() catalog.ExtensionVersions {
	return catalog.ExtensionVersions{Bases: []string{"1.2"}}
}

func (p *LoProvider) Stage(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef) error {
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
	if err := stage.Create(&catalog.Type{SQLType: array, ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, Owner: owner, ACL: authorization.DefaultBaseline(owner, authorization.DefaultTypes, false), ACLSyntheticDefault: true}); err != nil {
		return err
	}
	procedureOID, err := stage.AllocateOID()
	if err != nil {
		return err
	}
	procedureRevision, err := stage.Revision()
	if err != nil {
		return err
	}
	procedure := &catalog.Procedure{
		ObjectAddress:   ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: procedureOID},
		ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: procedureRevision,
		ObjectName: "lo_manage", RoutineKind: catalog.FunctionRoutine, Owner: owner,
		Result: 2279, Volatility: expr.Volatile,
		Installed:      catalog.InstalledRoutineDeclaration{Parallel: 'u'},
		Display:        catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: "lo_manage", Binary: "$libdir/lo", HasBinary: true},
		Implementation: p,
		ACL:            stage.CreationRoutineACL(owner, ext.TargetNamespace),
	}
	if stage.LookupName(catalog.DescriptorNameKey(procedure)) != (ids.ObjectAddress{}) {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: `function "lo_manage" already exists with same argument types`, Context: "SQL statement \"CREATE FUNCTION lo_manage()\nRETURNS pg_catalog.trigger\nAS '$libdir/lo'\nLANGUAGE C\"\nextension script file \"lo--1.1.sql\", near line 22"}
	}
	if err := stage.Create(procedure); err != nil {
		return err
	}
	ext.Members = append(ext.Members, procedure.Address())
	return nil
}

func (p *LoProvider) validateRecipe(v catalog.Read, ext *catalog.Extension) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid lo manifest") }
	if p != StockLo || ext == nil || ext.Name() != "lo" || ext.Version != "1.2" || len(ext.Members) != 3 || v.Owner() == nil || v.Owner().Extensions["lo"] != p {
		return invalid()
	}
	if err := ValidateProviderHeader(v, ext, ids.ExtensionClassID); err != nil {
		return err
	}
	if !currentManifest(v, ext) {
		return invalid()
	}
	base, ok := v.LookupType(sqltype.Oid)
	if !ok || ext.Members[0] == ext.Members[1] {
		return invalid()
	}
	for _, address := range ext.Members[:2] {
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
	procedure, ok := v.Procedure(ext.Members[2])
	if !ok || procedure.Extension != ext.Address() || procedure.Address().SubID != 0 || procedure.ObjectRevision == 0 {
		return invalid()
	}
	return p.ValidateProcedure(v, procedure)
}

func (p *LoProvider) ValidateProcedure(v catalog.Read, procedure *catalog.Procedure) error {
	return ValidateProcedureOrigin(v, procedure, p)
}

// SupportsDMLTrigger checks the immutable installed body, not mutable SQL names
// or membership. Current catalog and execution authority are checked by callers.
func (p *LoProvider) SupportsDMLTrigger(procedure *catalog.Procedure) bool {
	if p != StockLo || procedure == nil || procedure.Implementation != p || !procedure.Origin.Valid(procedure.Address()) {
		return false
	}
	installation := procedure.Origin.Installation
	if installation.Implementation != p || installation.Name() != "lo" || installation.Version != "1.2" || len(installation.Members) != 3 || procedure.Origin.Declaration != 2 {
		return false
	}
	declaration, ok := installation.Declarations[2].(*catalog.Procedure)
	return ok && procedure.RoutineKind == catalog.FunctionRoutine && procedure.Signature == "" && procedure.Result == 2279 && !procedure.Strict && !procedure.SecurityDefiner && procedure.Volatility == expr.Volatile && procedure.Display == declaration.Display && procedure.Installed == declaration.Installed && procedure.SQL == nil && procedure.Builtin == nil && procedure.Foreign == nil
}
