package fdw

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

func MappingFor(view access.View, server *catalog.ForeignObject) (*catalog.ForeignObject, error) {
	for _, role := range []authorization.PrincipalRef{view.State.CurrentUser, {}} {
		key := ids.NameKey{ClassID: ids.ForeignMappingClassID, Name: catalog.ForeignMappingName(server.Address(), role)}
		if d, ok := view.Catalog.Foreign(view.Catalog.NameAddress(key)); ok {
			return d, nil
		}
	}
	role, err := view.Roles.Descriptor(view.State.CurrentUser)
	if err != nil {
		return nil, err
	}
	return nil, sqlError("42704", fmt.Sprintf(`user mapping not found for user "%s", server "%s"`, role.Name, server.Name()))
}

func HandlerFor(catalog catalog.Read, wrapper *catalog.ForeignObject) (*catalog.ForeignProcedure, error) {
	if wrapper.Handler == (ids.ObjectAddress{}) {
		return nil, sqlError("55000", fmt.Sprintf(`foreign-data wrapper "%s" has no handler`, wrapper.Name()))
	}
	p, ok := catalog.Procedure(wrapper.Handler)
	if !ok || p.Foreign == nil || p.Foreign.Wrapper == nil {
		return nil, sqlError("55000", fmt.Sprintf(`foreign-data wrapper "%s" has no registered handler`, wrapper.Name()))
	}
	return p.Foreign, nil
}

func ImportExpression(text string) (parser.Expr, error) {
	statements, err := parser.Parse("SELECT (" + text + ")")
	if err != nil {
		return nil, err
	}
	if len(statements) != 1 {
		return nil, fmt.Errorf("stuntgres: invalid imported expression")
	}
	selectStmt, ok := statements[0].(*parser.Select)
	if !ok || len(selectStmt.Targets) != 1 || selectStmt.From != nil || selectStmt.Where != nil || selectStmt.With != nil || selectStmt.Set != nil || selectStmt.Into != nil || selectStmt.Order != nil || selectStmt.Bounds != nil || selectStmt.GroupBy != nil || selectStmt.Having != nil || len(selectStmt.Locking) != 0 || len(selectStmt.Windows) != 0 {
		return nil, fmt.Errorf("stuntgres: invalid imported expression")
	}
	return selectStmt.Targets[0], nil
}

func ImportedOptions(options []catalog.ForeignOption) []parser.ForeignOption {
	out := make([]parser.ForeignOption, len(options))
	for i, o := range options {
		out[i] = parser.ForeignOption{Name: parser.Identifier{Text: o.Name}, Value: o.Value}
	}
	return out
}
