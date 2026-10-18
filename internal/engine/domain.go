package engine

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
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
			return expr.ErrorAt("42601", "unique constraints not possible for domains", c.Unique.Pos)
		}
		if c.ForeignKey != nil {
			return expr.ErrorAt("42601", "foreign key constraints not possible for domains", c.Pos)
		}
		if c.NotNull || c.Default != nil || c.Identity != nil || c.Generation != nil || c.Name.Text != "" && c.Check == nil {
			return expr.ErrorAt("0A000", "domain constraints and defaults are unsupported", c.Pos)
		}
	}
	base, err := e.resolveAnnotationTypeMode(s.Base, op, false, true)
	if err != nil {
		return err
	}
	if replacement, ok := stage.Descriptors[ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(base.Type.ID())}].(*catalog.Type); ok {
		base.Type = replacement.SQLType
	}
	base, err = e.resolveColumnCollation(base, s.Collation, op)
	if err != nil {
		return err
	}
	ref := e.catalog.Domain.Ref(auth.catalog.Epoch(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(base.Type.ID())})
	stage.domainBases = append(stage.domainBases, auth.catalog.Get(ref.Address))
	if err = auth.CanUse(ref); err != nil {
		return err
	}
	oid, err := e.catalog.allocateOIDLocked()
	if err != nil {
		return err
	}
	revision, err := e.catalog.revisionLocked()
	if err != nil {
		return err
	}
	namespace := auth.catalog.Get(ns.Address).Name()
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
	arrayOID, err := e.catalog.allocateOIDLocked()
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
	domain := &catalog.Type{SQLType: typ, ObjectNamespace: ns.Address, ObjectRevision: revision, Owner: auth.state.CurrentUser, ACL: acl, ACLSyntheticDefault: synthetic, Dimensions: int32(len(s.Base.ArrayBounds)), Array: array.ID()}
	if err := e.bindDomainChecks(op, auth, stage, domain, s.Constraints, input); err != nil {
		return err
	}
	if err = stage.Create(domain); err != nil {
		return err
	}
	return stage.Create(&catalog.Type{SQLType: array, ObjectNamespace: ns.Address, ObjectRevision: revision, Owner: auth.state.CurrentUser, ACL: authorization.DefaultBaseline(auth.state.CurrentUser, authorization.DefaultTypes, false), ACLSyntheticDefault: true})
}

func (e *Engine) stageDropDomainLocked(op *Operation, auth AuthorizationView, stage *CatalogStage, s *parser.DropDomain) error {
	seen := make(map[ids.ObjectAddress]bool, len(s.Names))
	for _, name := range s.Names {
		path, err := e.lookupNamespaces(auth, name, typeLookup)
		if err != nil {
			return err
		}
		var found []ids.ObjectAddress
		for i := range path.count() {
			found = auth.catalog.Lookup(ids.NameKey{ClassID: ids.TypeClassID, Namespace: path.at(i), Name: name.Parts[len(name.Parts)-1].Text})
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
		target := auth.catalog.MustType(found[0])
		if !target.SQLType.IsDomain() {
			return sqlError("42809", fmt.Sprintf(`"%s" is not a domain`, objectNameText(name)))
		}
		ref := e.catalog.Domain.Ref(auth.catalog.Epoch(), target.Address())
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

func typeDiagnosticNameAt(view catalog.Read, d *catalog.Type, path []ids.ObjectAddress) string {
	if d.SQLType.IsArray() {
		element, ok := view.Descriptor(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(d.SQLType.Element().ID())})
		if ok {
			return typeDiagnosticNameAt(view, element.(*catalog.Type), path) + "[]"
		}
	}
	name := dependencyIdentifier(d.Name())
	if path == nil {
		return name
	}
	for _, ns := range path {
		if address, ok := view.LookupName(ids.NameKey{ClassID: ids.TypeClassID, Namespace: ns, Name: d.Name()}); ok {
			if address == d.Address() {
				return name
			}
			break
		}
	}
	return dependencyIdentifier(view.Get(d.Namespace()).Name()) + "." + name
}

func validateTypeProviders(view catalog.Read, d catalog.Descriptor) error {
	switch d := d.(type) {
	case *catalog.Type:
		if d.SQLType.IsDomain() {
			base := d.SQLType.DomainBase()
			if err := validateUsageCollation(view, base); err != nil {
				return err
			}
			actual, ok := view.LookupType(base.Type.ID())
			if !ok || actual != base.Type {
				return catalog.Conflict{}
			}
		}
		if d.SQLType.IsArray() {
			actual, ok := view.LookupType(d.SQLType.Element().ID())
			if !ok || actual != d.SQLType.Element() {
				return catalog.Conflict{}
			}
		}
	case *catalog.Relation:
		for _, c := range d.Columns {
			if err := validateUsageCollation(view, c.Usage); err != nil {
				return err
			}
			actual, ok := view.LookupType(c.Usage.Type.ID())
			if !ok || actual != c.Usage.Type {
				return catalog.Conflict{}
			}
		}
	}
	return nil
}
