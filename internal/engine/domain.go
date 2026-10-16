package engine

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Domain declarations use the same private catalog publication as schema DDL.
func (e *Engine) stageCreateDomainLocked(op *Operation, auth AuthorizationView, stage *CatalogStage, s *parser.CreateDomain, input sqltype.InputEnvironment) error {
	ns, err := e.creationNamespace(auth, s.Name)
	if err != nil {
		return err
	}
	trustedDestination := op != nil && op.restoreTableDestination
	if trustedDestination {
		bootstrap, guardErr := auth.roles.Descriptor(auth.state.Authenticated)
		if guardErr != nil || !bootstrap.Superuser || e.importTargets == nil {
			return fmt.Errorf("stuntgres: invalid trusted restore domain destination authority")
		}
	}
	if err = auth.CheckCreate(ns); err != nil {
		denied, ok := err.(*sqlstate.Error)
		if !trustedDestination || !ok || denied.Code != "42501" {
			return err
		}
	}
	if err = checkPersistentCreationNamespace(auth.catalog, ns.Address); err != nil {
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
		if c.ForeignKey != nil {
			return expressionError("42601", "foreign key constraints not possible for domains", c.Pos)
		}
		if c.NotNull || c.Default != nil || c.Identity != nil || c.Generation != nil || c.Name.Text != "" && c.Check == nil {
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
	ref := ObjectRef{owner: e.owner, Epoch: auth.catalog.Epoch(), Address: ObjectAddress{ClassID: TypeClassID, ObjectID: OID(base.Type.ID())}}
	stage.domainBases = append(stage.domainBases, auth.catalog.state.descriptors[ref.Address])
	if err = auth.CanUse(ref); err != nil {
		return err
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
	hasChecks := false
	for _, constraint := range s.Constraints {
		hasChecks = hasChecks || constraint.Check != nil
	}
	typ, err := sqltype.Domain(sqltype.ID(oid), name, declaration, base, hasChecks)
	if err != nil {
		return err
	}
	acl, synthetic := stage.creationTypeACL(auth.state.CurrentUser, ns.Address)
	arrayOID, err := e.allocateOIDLocked()
	if err != nil {
		return err
	}
	arrayName := associatedArrayName(stage, ns.Address, name)
	var array *sqltype.Descriptor
	if typ.BaseType().IsQueryInt() || sqltype.IsNetwork(typ.BaseType().ID()) || sqltype.IsOperatorAlias(typ.BaseType().ID()) || sqltype.IsRegTS(typ.BaseType().ID()) {
		array = sqltype.LookupArray(sqltype.ID(arrayOID), arrayName, typ)
	} else {
		array, err = sqltype.Array(sqltype.ID(arrayOID), arrayName, typ)
		if err != nil {
			return err
		}
	}
	domain := &typeDescriptor{typ: typ, namespace: ns.Address, revision: revision, owner: auth.state.CurrentUser, acl: acl, aclSyntheticDefault: synthetic, dimensions: int32(len(s.Base.ArrayBounds)), array: array.ID()}
	if err := e.bindDomainChecks(op, auth, stage, domain, s.Constraints, input); err != nil {
		return err
	}
	if err = stage.Create(domain); err != nil {
		return err
	}
	return stage.Create(&typeDescriptor{typ: array, namespace: ns.Address, revision: revision, owner: auth.state.CurrentUser, acl: authorization.DefaultBaseline(auth.state.CurrentUser, authorization.DefaultTypes, false), aclSyntheticDefault: true})
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
			if err := validateUsageCollation(view, base); err != nil {
				return err
			}
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
			if err := validateUsageCollation(view, c.Usage); err != nil {
				return err
			}
			actual, ok := view.LookupType(c.Usage.Type.ID())
			if !ok || actual != c.Usage.Type {
				return CatalogConflict{}
			}
		}
	}
	return nil
}
