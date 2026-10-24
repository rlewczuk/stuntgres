package typedef

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Utility lookup diagnostics do not carry an expression's type-name position.
func domainUtilityPath(h StageHost, auth ddl.Authority, name parser.ObjectName) ([]ids.ObjectAddress, error) {
	if len(name.Parts) == 2 {
		parts := [2]parser.Identifier{name.Parts[0], name.Parts[1]}
		parts[0].Pos = parser.Position{}
		name.Parts = parts[:]
	}
	return h.SearchNamespaces(auth, name, ids.TypeClassID)
}

func utilityType(h StageHost, auth ddl.Authority, name parser.ObjectName) (*catalog.Type, error) {
	path, err := domainUtilityPath(h, auth, name)
	if err != nil {
		return nil, err
	}
	v := h.AccessView(auth).Catalog
	for _, ns := range path {
		if a, ok := v.LookupName(ids.NameKey{ClassID: ids.TypeClassID, Namespace: ns, Name: name.Parts[len(name.Parts)-1].Text}); ok {
			return v.MustType(a), nil
		}
	}
	return nil, sqlError("42704", fmt.Sprintf(`type "%s" does not exist`, catalog.ObjectNameText(name)))
}

func StageAlterDomainIdentityLocked(h StageHost, auth ddl.Authority, s *parser.AlterDomain) error {
	return stageTypeIdentity(h, auth, s.Name, s.Target, s.Action == parser.DomainSetSchema, true, s.Pos)
}

func StageAlterTypeIdentityLocked(h StageHost, auth ddl.Authority, s *parser.AlterType) error {
	return stageTypeIdentity(h, auth, s.Name, s.Target, s.SetSchema, false, s.Pos)
}

func stageTypeIdentity(h StageHost, auth ddl.Authority, source parser.ObjectName, target parser.Identifier, moving, domainOnly bool, pos parser.Position) error {
	old, err := utilityType(h, auth, source)
	if err != nil {
		return err
	}
	h.AddDomainBase(old)
	view := h.AccessView(auth)
	path, _ := auth.ReferencePath()
	address := old.Address()
	if !domainOnly && old.SQLType.IsArray() {
		// Ownership diagnostics identify the actual internal element owner.
		// Protected-kind validation remains after action-specific admission.
		address.ObjectID = ids.OID(old.SQLType.Element().ID())
	}
	ref := ddl.ObjectRef(h, auth, address)
	if !moving {
		if err := ddl.Check(h, auth, access.Requirement{Object: ref, OwnerOnly: true}); err != nil {
			return err
		}
		if !domainOnly {
			if err := catalog.TypeAlterProtection(view.Catalog, old, path); err != nil {
				return err
			}
		}
	}
	if domainOnly && !old.SQLType.IsDomain() {
		return sqlError("42809", old.SQLType.DiagnosticName()+" is not a domain")
	}
	if (domainOnly || !moving) && !old.SQLType.IsUserDefined() {
		return expr.ErrorAt("0A000", "alteration of builtin types is unsupported", pos)
	}
	namespace, name := old.Namespace(), old.Name()
	if moving {
		address, exists := view.Catalog.LookupName(ids.NameKey{ClassID: ids.NamespaceClassID, Name: target.Text})
		if target.Text == "pg_temp" {
			address = auth.TemporaryNamespace()
			if address == (ids.ObjectAddress{}) {
				return sqlError("0A000", "cannot move objects into or out of temporary schemas")
			}
		} else if !exists {
			return sqlError("3F000", fmt.Sprintf(`schema "%s" does not exist`, target.Text))
		}
		destination := ddl.ObjectRef(h, auth, address)
		if err := view.CheckCreate(auth, destination); err != nil {
			return err
		}
		if err := ddl.Check(h, auth, access.Requirement{Object: ref, OwnerOnly: true}); err != nil {
			return err
		}
		if !domainOnly {
			if err := catalog.TypeAlterArrayProtection(view.Catalog, old, path); err != nil {
				return err
			}
			if !old.SQLType.IsUserDefined() {
				return expr.ErrorAt("0A000", "alteration of builtin types is unsupported", pos)
			}
		}
		namespace = address
		if namespace == old.Namespace() {
			if !domainOnly {
				if err := catalog.TypeAlterRowProtection(view.Catalog, old, path); err != nil {
					return err
				}
			}
			return nil
		}
		from, to := view.Catalog.MustNamespace(old.Namespace()), view.Catalog.MustNamespace(namespace)
		if from.Temporary != (ids.SessionToken{}) || to.Temporary != (ids.SessionToken{}) {
			return sqlError("0A000", "cannot move objects into or out of temporary schemas")
		}
		if from.Name() == "pg_toast" || to.Name() == "pg_toast" {
			return sqlError("0A000", "cannot move objects into or out of TOAST schema")
		}
	} else {
		name = target.Text
		if old.SQLType.IsComposite() && h.Stage().LookupName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: name}) != (ids.ObjectAddress{}) {
			return sqlError("42P07", fmt.Sprintf(`relation "%s" already exists`, name))
		}
		available, err := h.DisplaceAssociatedArray(namespace, name)
		if err != nil {
			return err
		}
		if !available || RelationTypeNameOccupied(h.StageRead(), namespace, name) {
			return sqlError("42710", fmt.Sprintf(`type "%s" already exists`, name))
		}
	}
	current := h.StageRead()
	// Displacement may already have rebuilt this exact generation.
	old = current.MustType(old.Address())
	array := current.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(old.Array)})
	if !array.SQLType.IsArray() || array.SQLType.Element() != old.SQLType || array.Namespace() != old.Namespace() {
		return fmt.Errorf("stuntgres: invalid type companion array")
	}
	if moving {
		if h.Stage().LookupName(ids.NameKey{ClassID: ids.TypeClassID, Namespace: namespace, Name: old.Name()}) != (ids.ObjectAddress{}) || RelationTypeNameOccupied(current, namespace, old.Name()) {
			return sqlError("42710", fmt.Sprintf(`type "%s" already exists in schema "%s"`, old.Name(), current.MustNamespace(namespace).Name()))
		}
		if !domainOnly {
			if err := catalog.TypeAlterRowProtection(current, old, path); err != nil {
				return err
			}
		}
		if h.Stage().LookupName(ids.NameKey{ClassID: ids.TypeClassID, Namespace: namespace, Name: array.Name()}) != (ids.ObjectAddress{}) || RelationTypeNameOccupied(current, namespace, array.Name()) {
			return sqlError("42710", fmt.Sprintf(`type "%s" already exists in schema "%s"`, array.Name(), current.MustNamespace(namespace).Name()))
		}
	}
	if old.SQLType.IsComposite() {
		relation := *current.MustRelation(old.OwningRelation)
		relation.ObjectName, relation.ObjectNamespace = name, namespace
		relation.ObjectRevision, err = h.Revision()
		if err != nil {
			return err
		}
		// The shared row-type synchronizer owns both reciprocal descriptors.
		return h.Stage().Replace(&relation)
	}
	next := *old
	next.ObjectNamespace = namespace
	declaration := catalog.DependencyIdentifier(current.MustNamespace(namespace).Name()) + "." + catalog.DependencyIdentifier(name)
	switch {
	case old.SQLType.IsDomain():
		if old.SQLType.Concrete() {
			next.SQLType, err = old.SQLType.RebindDomain(old.SQLType.DomainBase(), name, declaration)
		} else {
			next.SQLType = sqltype.LookupDomain(old.SQLType.ID(), name, declaration, old.SQLType.DomainBase(), len(old.DomainChecks) != 0, old.SQLType.OwnDomainNotNull())
		}
	case old.SQLType.IsEnum():
		next.SQLType, err = sqltype.EnumWithMembers(old.SQLType.ID(), name, declaration, old.SQLType.EnumMembers())
	case old.SQLType.IsCube():
		next.SQLType, err = sqltype.Cube(old.SQLType.ID(), name, declaration)
	case old.SQLType.IsCitext():
		next.SQLType, err = sqltype.Citext(old.SQLType.ID(), name, declaration)
	case old.SQLType.IsQueryInt():
		next.SQLType, err = sqltype.QueryInt(old.SQLType.ID(), name, declaration)
	case old.SQLType.IsEan13():
		next.SQLType, err = sqltype.Ean13(old.SQLType.ID(), name, declaration)
	case old.SQLType.IsIsbn():
		next.SQLType, err = sqltype.Isbn(old.SQLType.ID(), name, declaration)
	default:
		return fmt.Errorf("stuntgres: missing user type identity provider")
	}
	if err != nil {
		return err
	}
	next.ObjectRevision, err = h.Revision()
	if err != nil {
		return err
	}
	replace := func(d *catalog.Type) error {
		if old.SQLType.IsDomain() {
			return h.ReplaceDomainIdentity(d)
		}
		return h.ReplaceUnsynchronized(d)
	}
	if err := replace(&next); err != nil {
		return err
	}
	companion := *array
	companion.ObjectNamespace = namespace
	arrayName := array.Name()
	if !moving {
		arrayName = h.AssociatedArrayName(namespace, name)
	}
	if array.SQLType.Concrete() {
		companion.SQLType, err = sqltype.Array(array.SQLType.ID(), arrayName, next.SQLType)
	} else {
		companion.SQLType = sqltype.LookupArray(array.SQLType.ID(), arrayName, next.SQLType)
	}
	if err != nil {
		return err
	}
	companion.ObjectRevision, err = h.Revision()
	if err != nil {
		return err
	}
	return replace(&companion)
}
