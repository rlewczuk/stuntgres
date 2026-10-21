package role

import (
	"context"
	"strings"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

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
	if notice, ok := authorization.GrantWarning(d.Name(), "", requested, available, all, s.Revoke); ok {
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

// GrantRoutineArgumentDisplay renders the argument types of a missing or
// mismatched GRANT routine target, qualifying user types the path hides.
func GrantRoutineArgumentDisplay(view access.View, env access.Environment, inputs []ids.OID) string {
	path, _ := env.ReferencePath()
	var display strings.Builder
	for i, id := range inputs {
		if i != 0 {
			display.WriteString(", ")
		}
		typ := view.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: id})
		if typ.SQLType.IsUserDefined() {
			display.WriteString(catalog.TypeDiagnosticNameAt(view.Catalog, typ, path))
		} else {
			display.WriteString(typ.SQLType.DiagnosticName())
		}
	}
	return display.String()
}
