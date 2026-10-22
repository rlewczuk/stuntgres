package fdw

import (
	"context"
	"fmt"
	"slices"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
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

// PlanImport decides IMPORT FOREIGN SCHEMA up to the importer
// callback: the server and its USAGE, the local schema and CREATE on it, the
// wrapper's handler and its importer capability, the user mapping, the
// options and the request. It records the server, wrapper, mapping, handler
// procedure and local schema among the stage's observed dependencies. The
// engine runs the importer after releasing Engine.mu and replacementMu.
func PlanImport(h Host, auth ddl.Authority, s *parser.ForeignDDL) (SchemaImporter, ImportRequest, error) {
	view := h.AccessView(auth)
	server, err := Find(view.Catalog, ids.ForeignServerClassID, s.Server.Text)
	if err != nil {
		return nil, ImportRequest{}, err
	}
	if err = ddl.Check(h, auth, access.Requirement{Object: ObjectRef(view, server), Privileges: authorization.UsagePrivilege}); err != nil {
		return nil, ImportRequest{}, err
	}
	ns, err := h.ExistingCreationNamespace(auth, parser.ObjectName{Parts: []parser.Identifier{s.LocalSchema, {Text: "imported"}}})
	if err == nil {
		err = view.CheckCreate(auth, ns)
	}
	if err != nil {
		return nil, ImportRequest{}, err
	}
	wrapper, ok := view.Catalog.Foreign(server.Wrapper)
	if !ok {
		return nil, ImportRequest{}, ids.ErrCatalogReplaced
	}
	handler, err := HandlerFor(view.Catalog, wrapper)
	if err != nil {
		return nil, ImportRequest{}, err
	}
	importer, ok := handler.Wrapper.(SchemaImporter)
	if !ok {
		return nil, ImportRequest{}, sqlError("HV00P", fmt.Sprintf(`foreign-data wrapper "%s" does not support IMPORT FOREIGN SCHEMA`, wrapper.Name()))
	}
	mapping, err := MappingFor(view, server)
	if err != nil {
		return nil, ImportRequest{}, err
	}
	// IMPORT options belong to the provider, not stored catalog metadata.
	// Preserve ordered occurrences (including duplicates) for its semantics.
	options := make([]catalog.ForeignOption, len(s.Options))
	for i, option := range s.Options {
		options[i] = catalog.ForeignOption{Name: option.Name.Text, Value: option.Value}
	}
	role, err := view.Roles.Descriptor(view.State.CurrentUser)
	if err != nil {
		return nil, ImportRequest{}, err
	}
	request := ImportRequest{RemoteSchema: s.RemoteSchema.Text, LocalSchema: s.LocalSchema.Text, Server: server.Name(), Role: role.Name, WrapperOptions: slices.Clone(wrapper.Options), ServerOptions: slices.Clone(server.Options), MappingOptions: slices.Clone(mapping.Options), Options: options, Except: s.ImportExcept}
	for _, table := range s.ImportFilter {
		request.Tables = append(request.Tables, table.Text)
	}
	for _, base := range []catalog.Descriptor{server, wrapper, mapping, view.Catalog.Get(wrapper.Handler), view.Catalog.Get(ns.Address)} {
		h.AddDomainBase(base)
	}
	return importer, request, nil
}

// ImportSelected reports whether LIMIT TO or EXCEPT keeps the remote
// table name; without either every table is kept.
func ImportSelected(s *parser.ForeignDDL, name string) bool {
	if len(s.ImportFilter) == 0 {
		return true
	}
	selected := false
	for _, filter := range s.ImportFilter {
		if filter.Text == name {
			selected = true
			break
		}
	}
	if s.ImportExcept {
		return !selected
	}
	return selected
}

// ImportedTable is the CREATE FOREIGN TABLE statement for one table
// the importer returned, in the local schema on the statement's server.
func ImportedTable(ctx context.Context, definition TableDefinition, s *parser.ForeignDDL) (*parser.ForeignDDL, error) {
	name, err := FunctionName(catalog.ForeignFunctionName{Schema: s.LocalSchema.Text, Name: definition.Name})
	if err != nil {
		return nil, err
	}
	qualified := parser.ObjectName{Parts: []parser.Identifier{{Text: name.Schema}, {Text: name.Name}}}
	table := &parser.CreateTable{Name: qualified, Persistence: "logged"}
	result := &parser.ForeignDDL{Action: "create", Kind: "table", Name: qualified, Table: table, Server: s.Server, Options: ImportedOptions(definition.Options), ColumnOptions: make(map[string][]parser.ForeignOption)}
	for _, column := range definition.Columns {
		if _, err := FunctionName(catalog.ForeignFunctionName{Name: column.Name}); err != nil {
			return nil, err
		}
		typ, err := parser.ParseTypeName(ctx, column.Type, nil)
		if err != nil {
			return nil, err
		}
		c := &parser.Column{Name: column.Name, TypeName: typ}
		if column.NotNull {
			c.Constraints = append(c.Constraints, parser.ColumnConstraint{NotNull: true})
		}
		if column.Default != "" {
			expression, err := ImportExpression(column.Default)
			if err != nil {
				return nil, err
			}
			c.Constraints = append(c.Constraints, parser.ColumnConstraint{Default: expression})
		}
		if column.Check != "" {
			expression, err := ImportExpression(column.Check)
			if err != nil {
				return nil, err
			}
			c.Constraints = append(c.Constraints, parser.ColumnConstraint{Check: &parser.CheckConstraint{Expression: expression}})
		}
		table.Elements = append(table.Elements, parser.TableElement{Column: c})
		result.ColumnOptions[column.Name] = ImportedOptions(column.Options)
	}
	for _, check := range definition.Checks {
		expression, err := ImportExpression(check)
		if err != nil {
			return nil, err
		}
		table.Elements = append(table.Elements, parser.TableElement{Check: &parser.CheckConstraint{Expression: expression}})
	}
	return result, nil
}
