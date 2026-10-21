package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func (e *Engine) resolveGrantObject(v AuthorizationView, name parser.ObjectName, kind string) (ids.ObjectRef, error) {
	if kind == "" || kind == "sequence" {
		return e.resolveRelationReference(v, name, true)
	}
	if kind == "tablespace" {
		return resolveGrantTablespace(v, catalog.ObjectNameText(name))
	}
	var address ids.ObjectAddress
	if kind == "schema" {
		if len(name.Parts) == 1 {
			address = v.Catalog.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: name.Parts[0].Text})
			if name.Parts[0].Text == "pg_temp" {
				address = temporaryNamespace(v)
			}
		}
		if address == (ids.ObjectAddress{}) {
			return ids.ObjectRef{}, sqlError("3F000", fmt.Sprintf(`schema "%s" does not exist`, catalog.ObjectNameText(name)))
		}
	} else {
		path, err := e.lookupNamespaces(v, name, analyze.TypeLookup)
		if err != nil {
			return ids.ObjectRef{}, err
		}
		for i := range path.Count() {
			found := v.Catalog.Lookup(ids.NameKey{ClassID: ids.TypeClassID, Namespace: path.At(i), Name: name.Parts[len(name.Parts)-1].Text})
			if len(found) == 1 {
				address = found[0]
				break
			}
		}
		if address == (ids.ObjectAddress{}) {
			return ids.ObjectRef{}, sqlError("42704", fmt.Sprintf(`type "%s" does not exist`, catalog.ObjectNameText(name)))
		}
		t := v.Catalog.MustType(address)
		if kind == "domain" && !t.SQLType.IsDomain() {
			return ids.ObjectRef{}, sqlError("42809", fmt.Sprintf(`"%s" is not a domain`, catalog.ObjectNameText(name)))
		}
		if t.SQLType.IsArray() {
			diagnostic := sqlError("0LP01", "cannot set privileges of array types").(*sqlstate.Error)
			diagnostic.Hint = "Set the privileges of the element type instead."
			return ids.ObjectRef{}, diagnostic
		}
	}
	return e.catalog.Domain.Ref(v.Catalog.Epoch(), address), nil
}

// Nonrelation classes share the same source selection, ACL mutation and final
// catalog publication as relation grants, without materializing table defaults.
// It returns the changed descriptor (nil when nothing changes) and the
// statement's warning for d, which is set even when an error follows it.
func changeNonRelationGrant(ctx context.Context, view access.View, d catalog.Descriptor, s *parser.ObjectGrant, grantees []authorization.ACLGrantee, principals []authorization.PrincipalRef, super bool, requested authorization.Privileges, all bool) (catalog.Descriptor, *sqlstate.Error, error) {
	if err := access.ValidateACL(view.Roles, d); err != nil {
		return nil, nil, err
	}
	owner, old, _ := access.DescriptorACL(d)
	if typ, ok := d.(*catalog.Type); ok && typ.ACLSyntheticDefault {
		old = authorization.ACLValue{} // Effective baseline is still raw NULL.
	}
	class := authorization.DefaultTypes
	noun := "type"
	switch d.Kind() {
	case ids.NamespaceObject:
		class, noun = authorization.DefaultSchemas, "schema"
	case ids.ProcedureObject:
		class, noun = authorization.DefaultRoutines, "function"
	case ids.ParameterObject:
		class, noun = authorization.DefaultParameters, "parameter"
	case ids.TablespaceObject:
		class, noun = authorization.DefaultTablespaces, "tablespace"
	}
	baseline := old
	if !baseline.Explicit {
		baseline = authorization.DefaultBaseline(owner, class, false)
	}
	source, available, err := authorization.ObjectGrantSource(ctx, owner, old, authorization.ACLValue{}, principals, super, requested)
	if err != nil {
		return nil, nil, err
	}
	if available == 0 && authorization.ACLRights(baseline, owner, principals, access.ClassPrivileges(d), false) == 0 {
		// Routine target kinds all share the function ACL object class.
		return nil, nil, sqlError("42501", "permission denied for "+noun+" "+d.Name())
	}
	var warning *sqlstate.Error
	if notice, ok := grantWarning(d.Name(), "", requested, available, all, s.Revoke); ok {
		warning = &notice
	}
	if available == 0 {
		return nil, warning, nil
	}
	nextACL, changed, err := authorization.ChangeObjectACL(ctx, view.Roles, baseline, owner, source, grantees, available, s.GrantOption, s.Revoke, s.OptionOnly, s.Cascade)
	if err != nil {
		return nil, warning, err
	}
	if !changed && (old.Explicit || d.Kind() == ids.ParameterObject) {
		return nil, warning, nil
	}
	var next catalog.Descriptor
	switch d := d.(type) {
	case *catalog.Namespace:
		n := *d
		n.ACL = nextACL
		next = &n
	case *catalog.Type:
		n := *d
		n.ACL = nextACL
		n.ACLSyntheticDefault = false
		next = &n
	case *catalog.Procedure:
		n := *d
		n.ACL = nextACL
		next = &n
	case *catalog.Parameter:
		n := *d
		n.ACL = nextACL
		next = &n
	case *catalog.Tablespace:
		n := *d
		n.ACL = nextACL
		next = &n
	}
	if err := access.ValidateACL(view.Roles, next); err != nil {
		return nil, warning, err
	}
	return next, warning, nil
}
