package engine

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func (e *Engine) resolveGrantObject(v AuthorizationView, name parser.ObjectName, kind string) (ids.ObjectRef, error) {
	if kind == "" || kind == "sequence" {
		return e.resolveRelationReference(v, name, true)
	}
	if kind == "tablespace" {
		return resolveGrantTablespace(v, objectNameText(name))
	}
	var address ids.ObjectAddress
	if kind == "schema" {
		if len(name.Parts) == 1 {
			address = v.catalog.state.names[ids.NameKey{ClassID: ids.NamespaceClassID, Name: name.Parts[0].Text}]
			if name.Parts[0].Text == "pg_temp" {
				address = temporaryNamespace(v)
			}
		}
		if address == (ids.ObjectAddress{}) {
			return ids.ObjectRef{}, sqlError("3F000", fmt.Sprintf(`schema "%s" does not exist`, objectNameText(name)))
		}
	} else {
		path, err := e.lookupNamespaces(v, name, typeLookup)
		if err != nil {
			return ids.ObjectRef{}, err
		}
		for i := range path.count() {
			found := v.catalog.Lookup(ids.NameKey{ClassID: ids.TypeClassID, Namespace: path.at(i), Name: name.Parts[len(name.Parts)-1].Text})
			if len(found) == 1 {
				address = found[0]
				break
			}
		}
		if address == (ids.ObjectAddress{}) {
			return ids.ObjectRef{}, sqlError("42704", fmt.Sprintf(`type "%s" does not exist`, objectNameText(name)))
		}
		t := v.catalog.state.descriptors[address].(*typeDescriptor)
		if kind == "domain" && !t.typ.IsDomain() {
			return ids.ObjectRef{}, sqlError("42809", fmt.Sprintf(`"%s" is not a domain`, objectNameText(name)))
		}
		if t.typ.IsArray() {
			diagnostic := sqlError("0LP01", "cannot set privileges of array types").(*sqlstate.Error)
			diagnostic.Hint = "Set the privileges of the element type instead."
			return ids.ObjectRef{}, diagnostic
		}
	}
	return e.domain.Ref(v.catalog.Epoch(), address), nil
}

// Nonrelation classes share the same source selection, ACL mutation and final
// catalog publication as relation grants, without materializing table defaults.
func changeNonRelationGrant(op *Operation, v AuthorizationView, d Descriptor, s *parser.ObjectGrant, grantees []authorization.ACLGrantee, principals []authorization.PrincipalRef, super bool, requested authorization.Privileges, all bool) (Descriptor, error) {
	if err := validateACL(v.roles, d); err != nil {
		return nil, err
	}
	owner, old, _ := descriptorACL(d)
	if typ, ok := d.(*typeDescriptor); ok && typ.aclSyntheticDefault {
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
	source, available, err := authorization.ObjectGrantSource(op.Context(), owner, old, authorization.ACLValue{}, principals, super, requested)
	if err != nil {
		return nil, err
	}
	if available == 0 && authorization.ACLRights(baseline, owner, principals, classPrivileges(d), false) == 0 {
		// Routine target kinds all share the function ACL object class.
		return nil, sqlError("42501", "permission denied for "+noun+" "+d.Name())
	}
	grantWarning(op, d.Name(), "", requested, available, all, s.Revoke)
	if available == 0 {
		return nil, nil
	}
	nextACL, changed, err := authorization.ChangeObjectACL(op.Context(), v.roles, baseline, owner, source, grantees, available, s.GrantOption, s.Revoke, s.OptionOnly, s.Cascade)
	if err != nil {
		return nil, err
	}
	if !changed && (old.Explicit || d.Kind() == ids.ParameterObject) {
		return nil, nil
	}
	var next Descriptor
	switch d := d.(type) {
	case *namespaceDescriptor:
		n := *d
		n.acl = nextACL
		next = &n
	case *typeDescriptor:
		n := *d
		n.acl = nextACL
		n.aclSyntheticDefault = false
		next = &n
	case *procedureDescriptor:
		n := *d
		n.acl = nextACL
		next = &n
	case *parameterDescriptor:
		n := *d
		n.acl = nextACL
		next = &n
	case *tablespaceDescriptor:
		n := *d
		n.acl = nextACL
		next = &n
	}
	if err := validateACL(v.roles, next); err != nil {
		return nil, err
	}
	return next, nil
}
