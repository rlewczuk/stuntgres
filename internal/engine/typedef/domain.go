package typedef

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Domain declarations use the same private catalog publication as schema DDL.
func StageCreateDomainLocked(h StageHost, auth ddl.Authority, s *parser.CreateDomain, input sqltype.InputEnvironment, output sqltype.OutputEnvironment) error {
	ns, err := h.ExistingCreationNamespace(auth, s.Name)
	if err != nil {
		return err
	}
	trustedDestination, err := h.TrustedRestoreDestination(auth, "domain destination")
	if err != nil {
		return err
	}
	view := h.AccessView(auth)
	if err = view.CheckCreate(auth, ns); err != nil {
		denied, ok := err.(*sqlstate.Error)
		if !trustedDestination || !ok || denied.Code != "42501" {
			return err
		}
	}
	if err = access.CheckPersistentCreationNamespace(view.Catalog, ns.Address); err != nil {
		return err
	}
	name := s.Name.Parts[len(s.Name.Parts)-1].Text
	available, err := h.DisplaceAssociatedArray(ns.Address, name)
	if err != nil {
		return err
	}
	if !available || RelationTypeNameOccupied(view.Catalog, ns.Address, name) {
		return sqlError("42710", fmt.Sprintf(`type "%s" already exists`, name))
	}
	var defaultRaw parser.Expr
	seenNull, ownNotNull := false, false
	for _, c := range s.Constraints {
		declarationPos := c.Pos
		if c.NamedDeclarationPos.Character > 0 {
			declarationPos = c.NamedDeclarationPos
		}
		if c.Unique != nil {
			return expr.ErrorAt("42601", "unique constraints not possible for domains", c.Unique.Pos)
		}
		if c.ForeignKey != nil {
			return expr.ErrorAt("42601", "foreign key constraints not possible for domains", c.Pos)
		}
		if c.Identity != nil || c.Generation != nil {
			return expr.ErrorAt("0A000", "domain constraints and defaults are unsupported", c.Pos)
		}
		if c.Default != nil {
			if defaultRaw != nil {
				return expr.ErrorAt("42601", "multiple default expressions", declarationPos)
			}
			defaultRaw = c.Default
		} else if c.Check == nil && c.Unique == nil && c.ForeignKey == nil {
			if seenNull && ownNotNull != c.NotNull {
				return expr.ErrorAt("42601", "conflicting NULL/NOT NULL constraints", declarationPos)
			}
			if seenNull && c.NotNull {
				return expr.ErrorAt("42P17", "redundant NOT NULL constraint definition", declarationPos)
			}
			seenNull, ownNotNull = true, c.NotNull
		}
	}
	base, err := h.ResolveDomainBase(s.Base)
	if err != nil {
		return err
	}
	if replacement, ok := h.Stage().Get(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(base.Type.ID())}).(*catalog.Type); ok {
		base.Type = replacement.SQLType
	}
	base, err = h.ResolveColumnCollation(base, s.Collation)
	if err != nil {
		return err
	}
	ref := ddl.ObjectRef(h, auth, ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(base.Type.ID())})
	h.AddDomainBase(view.Catalog.Get(ref.Address))
	if err = view.CanUse(auth, ref); err != nil {
		return err
	}
	oid, err := h.AllocateOID()
	if err != nil {
		return err
	}
	revision, err := h.Revision()
	if err != nil {
		return err
	}
	namespace := view.Catalog.Get(ns.Address).Name()
	declaration := catalog.DependencyIdentifier(namespace) + "." + catalog.DependencyIdentifier(name)
	hasChecks := false
	for _, constraint := range s.Constraints {
		hasChecks = hasChecks || constraint.Check != nil
	}
	var typ *sqltype.Descriptor
	if base.Type.Concrete() {
		typ, err = sqltype.Domain(sqltype.ID(oid), name, declaration, base, hasChecks, ownNotNull)
	} else {
		typ = sqltype.LookupDomain(sqltype.ID(oid), name, declaration, base, hasChecks, ownNotNull)
	}
	if err != nil {
		return err
	}
	acl, synthetic := h.CreationTypeACL(view.State.CurrentUser, ns.Address)
	arrayOID, err := h.AllocateOID()
	if err != nil {
		return err
	}
	arrayName := h.AssociatedArrayName(ns.Address, name)
	var array *sqltype.Descriptor
	if !typ.Concrete() || typ.BaseType().IsHstore() || typ.BaseType().IsEan13() || typ.BaseType().IsIsbn() || typ.BaseType().IsIsbn13() || typ.BaseType().IsIsmn() || typ.BaseType().IsIsmn13() || typ.BaseType().IsIssn() || typ.BaseType().IsIssn13() || typ.BaseType().IsUpc() || typ.BaseType().IsCube() || typ.BaseType().IsQueryInt() || typ.BaseType().IsCitext() || sqltype.IsNetwork(typ.BaseType().ID()) || sqltype.IsOperatorAlias(typ.BaseType().ID()) || sqltype.IsRegTS(typ.BaseType().ID()) {
		array = sqltype.LookupArray(sqltype.ID(arrayOID), arrayName, typ)
	} else {
		array, err = sqltype.Array(sqltype.ID(arrayOID), arrayName, typ)
		if err != nil {
			return err
		}
	}
	domain := &catalog.Type{SQLType: typ, ObjectNamespace: ns.Address, ObjectRevision: revision, Owner: view.State.CurrentUser, ACL: acl, ACLSyntheticDefault: synthetic, Dimensions: int32(len(s.Base.ArrayBounds)), Array: array.ID()}
	if err := h.BindDomainDefault(auth, domain, defaultRaw, input, output); err != nil {
		return err
	}
	if err := h.BindDomainConstraints(auth, domain, s.Constraints, input); err != nil {
		return err
	}
	if err = h.Stage().Create(domain); err != nil {
		return err
	}
	return h.Stage().Create(&catalog.Type{SQLType: array, ObjectNamespace: ns.Address, ObjectRevision: revision, Owner: view.State.CurrentUser, ACL: authorization.DefaultBaseline(view.State.CurrentUser, authorization.DefaultTypes, false), ACLSyntheticDefault: true})
}

func StageDropDomainLocked(h StageHost, auth ddl.Authority, s *parser.DropDomain) error {
	return stageDropTypes(h, auth, s.Names, s.IfExists, true)
}
