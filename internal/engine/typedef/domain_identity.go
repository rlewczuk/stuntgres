package typedef

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
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

func StageAlterDomainIdentityLocked(h StageHost, auth ddl.Authority, s *parser.AlterDomain) error {
	path, err := domainUtilityPath(h, auth, s.Name)
	if err != nil {
		return err
	}
	view := h.AccessView(auth)
	var old *catalog.Type
	for _, namespace := range path {
		found := view.Catalog.Lookup(ids.NameKey{ClassID: ids.TypeClassID, Namespace: namespace, Name: s.Name.Parts[len(s.Name.Parts)-1].Text})
		if len(found) == 1 {
			old = view.Catalog.MustType(found[0])
			break
		}
	}
	if old == nil {
		return sqlError("42704", fmt.Sprintf(`type "%s" does not exist`, catalog.ObjectNameText(s.Name)))
	}
	ref := ddl.ObjectRef(h, auth, old.Address())
	if s.Action == parser.DomainRename {
		if err := ddl.Check(h, auth, access.Requirement{Object: ref, OwnerOnly: true}); err != nil {
			return err
		}
	}
	if !old.SQLType.IsDomain() {
		return sqlError("42809", old.SQLType.DiagnosticName()+" is not a domain")
	}
	namespace := old.Namespace()
	name := old.Name()
	switch s.Action {
	case parser.DomainRename:
		name = s.Target.Text
		available, err := h.DisplaceAssociatedArray(namespace, name)
		if err != nil {
			return err
		}
		if !available || RelationTypeNameOccupied(h.StageRead(), namespace, name) {
			return sqlError("42710", fmt.Sprintf(`type "%s" already exists`, name))
		}
	case parser.DomainSetSchema:
		address, exists := view.Catalog.LookupName(ids.NameKey{ClassID: ids.NamespaceClassID, Name: s.Target.Text})
		var destination ids.ObjectRef
		if s.Target.Text == "pg_temp" {
			address = auth.TemporaryNamespace()
			if address == (ids.ObjectAddress{}) {
				return sqlError("0A000", "cannot move objects into or out of temporary schemas")
			}
			destination = ddl.ObjectRef(h, auth, address)
		} else {
			if !exists {
				return sqlError("3F000", fmt.Sprintf(`schema "%s" does not exist`, s.Target.Text))
			}
			destination = ddl.ObjectRef(h, auth, address)
		}
		if err := view.CheckCreate(auth, destination); err != nil {
			return err
		}
		if err := ddl.Check(h, auth, access.Requirement{Object: ref, OwnerOnly: true}); err != nil {
			return err
		}
		namespace = destination.Address
		if namespace == old.Namespace() {
			return nil
		}
		source := view.Catalog.MustNamespace(old.Namespace())
		target := view.Catalog.MustNamespace(namespace)
		if source.Temporary != (ids.SessionToken{}) || target.Temporary != (ids.SessionToken{}) {
			return sqlError("0A000", "cannot move objects into or out of temporary schemas")
		}
		if source.Name() == "pg_toast" || target.Name() == "pg_toast" {
			return sqlError("0A000", "cannot move objects into or out of TOAST schema")
		}
	default:
		return fmt.Errorf("stuntgres: invalid domain identity action")
	}
	current := h.StageRead()
	// Array displacement may already have reowned the domain generation.
	old = current.MustType(old.Address())
	array := current.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(old.Array)})
	if !array.SQLType.IsArray() || array.SQLType.Element() != old.SQLType || array.Namespace() != old.Namespace() {
		return fmt.Errorf("stuntgres: invalid domain companion array")
	}
	if s.Action == parser.DomainSetSchema {
		for _, member := range []*catalog.Type{old, array} {
			if h.Stage().LookupName(ids.NameKey{ClassID: ids.TypeClassID, Namespace: namespace, Name: member.Name()}) != (ids.ObjectAddress{}) || RelationTypeNameOccupied(current, namespace, member.Name()) {
				return sqlError("42710", fmt.Sprintf(`type "%s" already exists in schema "%s"`, member.Name(), current.MustNamespace(namespace).Name()))
			}
		}
	}
	next := *old
	next.ObjectNamespace = namespace
	declaration := catalog.DependencyIdentifier(current.MustNamespace(namespace).Name()) + "." + catalog.DependencyIdentifier(name)
	if old.SQLType.Concrete() {
		next.SQLType, err = old.SQLType.RebindDomain(old.SQLType.DomainBase(), name, declaration)
	} else {
		next.SQLType = sqltype.LookupDomain(old.SQLType.ID(), name, declaration, old.SQLType.DomainBase())
	}
	if err != nil {
		return err
	}
	next.ObjectRevision, err = h.Revision()
	if err != nil {
		return err
	}
	if err = h.ReplaceDomainIdentity(&next); err != nil {
		return err
	}
	companion := *array
	companion.ObjectNamespace = namespace
	arrayName := array.Name()
	if s.Action == parser.DomainRename {
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
	return h.ReplaceDomainIdentity(&companion)
}
