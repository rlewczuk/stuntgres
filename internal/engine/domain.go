package engine

import (
	"fmt"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Domain declarations use the same private catalog publication as schema DDL.
func (e *Engine) stageCreateDomainLocked(op *Operation, auth AuthorizationView, stage *CatalogStage, s *parser.CreateDomain) error {
	ns, err := e.creationNamespace(auth, s.Name)
	if err != nil {
		return err
	}
	if err = auth.CheckCreate(ns); err != nil {
		return err
	}
	if err = checkTableCreationNamespace(auth.catalog, ns.Address); err != nil {
		return err
	}
	name := s.Name.Parts[len(s.Name.Parts)-1].Text
	available, err := e.displaceAssociatedArrayLocked(stage, ns.Address, name)
	if err != nil {
		return err
	}
	if !available || relationTypeNameOccupied(auth.catalog, ns.Address, name) {
		return sqlError("42710", fmt.Sprintf(`type "%s" already exists`, name))
	}
	for _, c := range s.Constraints {
		if c.Unique != nil {
			return expressionError("42601", "unique constraints not possible for domains", c.Unique.Pos)
		}
		if c.NotNull || c.Default != nil || c.Check != nil || c.Identity != nil || c.Name.Text != "" {
			return expressionError("0A000", "domain constraints and defaults are unsupported", c.Pos)
		}
	}
	base, err := e.resolveAnnotationTypeMode(s.Base, op, false, true)
	if err != nil {
		return err
	}
	if replacement, ok := stage.descriptors[ObjectAddress{ClassID: TypeClassID, ObjectID: OID(base.Type.ID())}].(*typeDescriptor); ok {
		base.Type = replacement.typ
	}
	base, err = e.resolveColumnCollation(base, s.Collation, op)
	if err != nil {
		return err
	}
	if base.Type.IsUserDefined() || base.Type.IsArray() && base.Type.Element().IsDomain() {
		ref := ObjectRef{owner: e.owner, Epoch: auth.catalog.Epoch(), Address: ObjectAddress{ClassID: TypeClassID, ObjectID: OID(base.Type.ID())}}
		stage.domainBases = append(stage.domainBases, auth.catalog.state.descriptors[ref.Address])
		if err = auth.CanUse(ref); err != nil {
			return err
		}
	}
	oid, err := e.allocateOIDLocked()
	if err != nil {
		return err
	}
	revision, err := e.revisionLocked()
	if err != nil {
		return err
	}
	namespace := auth.catalog.state.descriptors[ns.Address].Name()
	declaration := dependencyIdentifier(namespace) + "." + dependencyIdentifier(name)
	typ, err := sqltype.Domain(sqltype.ID(oid), name, declaration, base)
	if err != nil {
		return err
	}
	acl := ACLValue{Explicit: true, Entries: []ACLEntry{{Grantor: auth.state.CurrentUser, Grantee: ACLGrantee{Public: true}, Privileges: UsagePrivilege}, {Grantor: auth.state.CurrentUser, Grantee: ACLGrantee{Role: auth.state.CurrentUser}, Privileges: UsagePrivilege, GrantOptions: UsagePrivilege}}}
	arrayOID, err := e.allocateOIDLocked()
	if err != nil {
		return err
	}
	arrayName := associatedArrayName(stage, ns.Address, name)
	array, err := sqltype.Array(sqltype.ID(arrayOID), arrayName, typ)
	if err != nil {
		return err
	}
	if err = stage.Create(&typeDescriptor{typ: typ, namespace: ns.Address, revision: revision, owner: auth.state.CurrentUser, acl: acl, dimensions: int32(len(s.Base.ArrayBounds)), array: array.ID()}); err != nil {
		return err
	}
	return stage.Create(&typeDescriptor{typ: array, namespace: ns.Address, revision: revision, owner: auth.state.CurrentUser, acl: acl})
}

func (e *Engine) stageDropDomainLocked(op *Operation, auth AuthorizationView, stage *CatalogStage, s *parser.DropDomain) error {
	seen := make(map[ObjectAddress]bool, len(s.Names))
	for _, name := range s.Names {
		path, err := e.lookupNamespaces(auth, name, typeLookup)
		if err != nil {
			return err
		}
		var found []ObjectAddress
		for i := range path.count() {
			found = auth.catalog.Lookup(NameKey{ClassID: TypeClassID, Namespace: path.at(i), Name: name.Parts[len(name.Parts)-1].Text})
			if len(found) != 0 {
				break
			}
		}
		if len(found) != 1 {
			if s.IfExists {
				schemaNotice(op, "00000", fmt.Sprintf(`type "%s" does not exist, skipping`, objectNameText(name)))
				continue
			}
			return sqlError("42704", fmt.Sprintf(`type "%s" does not exist`, objectNameText(name)))
		}
		target := auth.catalog.state.descriptors[found[0]].(*typeDescriptor)
		if !target.typ.IsDomain() {
			return sqlError("42809", fmt.Sprintf(`"%s" is not a domain`, objectNameText(name)))
		}
		ref := ObjectRef{owner: e.owner, Epoch: auth.catalog.Epoch(), Address: target.Address()}
		if err = auth.Check(AccessRequirement{Object: ref, OwnerOnly: true}); err != nil {
			return err
		}
		if seen[ref.Address] {
			continue
		}
		seen[ref.Address] = true
		stage.domainTargets = append(stage.domainTargets, ref.Address)
	}
	stage.dependencyPath, _ = e.referencePath(auth)
	return nil
}

func typeDiagnosticNameAt(catalog CatalogRead, d *typeDescriptor, path []ObjectAddress) string {
	if d.typ.IsArray() {
		element, ok := catalog.Descriptor(ObjectAddress{ClassID: TypeClassID, ObjectID: OID(d.typ.Element().ID())})
		if ok {
			return typeDiagnosticNameAt(catalog, element.(*typeDescriptor), path) + "[]"
		}
	}
	name := dependencyIdentifier(d.Name())
	if path == nil {
		return name
	}
	for _, ns := range path {
		if address, ok := catalog.state.names[NameKey{ClassID: TypeClassID, Namespace: ns, Name: d.Name()}]; ok {
			if address == d.Address() {
				return name
			}
			break
		}
	}
	return dependencyIdentifier(catalog.state.descriptors[d.Namespace()].Name()) + "." + name
}

func validateTypeProviders(view CatalogRead, d Descriptor) error {
	switch d := d.(type) {
	case *typeDescriptor:
		if d.typ.IsDomain() {
			base := d.typ.DomainBase()
			actual, ok := view.LookupType(base.Type.ID())
			if !ok || actual != base.Type {
				return CatalogConflict{}
			}
		}
		if d.typ.IsArray() {
			actual, ok := view.LookupType(d.typ.Element().ID())
			if !ok || actual != d.typ.Element() {
				return CatalogConflict{}
			}
		}
	case *relationDescriptor:
		for _, c := range d.columns {
			actual, ok := view.LookupType(c.Usage.Type.ID())
			if !ok || actual != c.Usage.Type {
				return CatalogConflict{}
			}
		}
	}
	return nil
}
