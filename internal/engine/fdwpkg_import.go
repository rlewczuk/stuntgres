package engine

import (
	"context"
	"fmt"
	"slices"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// planForeignImport decides IMPORT FOREIGN SCHEMA up to the importer
// callback: the server and its USAGE, the local schema and CREATE on it, the
// wrapper's handler and its importer capability, the user mapping, the
// options and the request. It records the server, wrapper, mapping, handler
// procedure and local schema among the stage's observed dependencies. The
// engine runs the importer after releasing Engine.mu and replacementMu.
func planForeignImport(h fdw.Host, auth ddl.Authority, s *parser.ForeignDDL) (fdw.SchemaImporter, fdw.ImportRequest, error) {
	view := h.AccessView(auth)
	server, err := fdw.Find(view.Catalog, ids.ForeignServerClassID, s.Server.Text)
	if err != nil {
		return nil, fdw.ImportRequest{}, err
	}
	if err = ddl.Check(h, auth, access.Requirement{Object: fdw.ObjectRef(view, server), Privileges: authorization.UsagePrivilege}); err != nil {
		return nil, fdw.ImportRequest{}, err
	}
	ns, err := h.ExistingCreationNamespace(auth, parser.ObjectName{Parts: []parser.Identifier{s.LocalSchema, {Text: "imported"}}})
	if err == nil {
		err = view.CheckCreate(auth, ns)
	}
	if err != nil {
		return nil, fdw.ImportRequest{}, err
	}
	wrapper, ok := view.Catalog.Foreign(server.Wrapper)
	if !ok {
		return nil, fdw.ImportRequest{}, ids.ErrCatalogReplaced
	}
	handler, err := fdw.HandlerFor(view.Catalog, wrapper)
	if err != nil {
		return nil, fdw.ImportRequest{}, err
	}
	importer, ok := handler.Wrapper.(fdw.SchemaImporter)
	if !ok {
		return nil, fdw.ImportRequest{}, sqlError("HV00P", fmt.Sprintf(`foreign-data wrapper "%s" does not support IMPORT FOREIGN SCHEMA`, wrapper.Name()))
	}
	mapping, err := fdw.MappingFor(view, server)
	if err != nil {
		return nil, fdw.ImportRequest{}, err
	}
	options, err := fdw.TransformOptions(nil, s.Options)
	if err != nil {
		return nil, fdw.ImportRequest{}, err
	}
	role, err := view.Roles.Descriptor(view.State.CurrentUser)
	if err != nil {
		return nil, fdw.ImportRequest{}, err
	}
	request := fdw.ImportRequest{RemoteSchema: s.RemoteSchema.Text, LocalSchema: s.LocalSchema.Text, Server: server.Name(), Role: role.Name, WrapperOptions: slices.Clone(wrapper.Options), ServerOptions: slices.Clone(server.Options), MappingOptions: slices.Clone(mapping.Options), Options: options, Except: s.ImportExcept}
	for _, table := range s.ImportFilter {
		request.Tables = append(request.Tables, table.Text)
	}
	for _, base := range []catalog.Descriptor{server, wrapper, mapping, view.Catalog.Get(wrapper.Handler), view.Catalog.Get(ns.Address)} {
		h.AddDomainBase(base)
	}
	return importer, request, nil
}

// foreignImportSelected reports whether LIMIT TO or EXCEPT keeps the remote
// table name; without either every table is kept.
func foreignImportSelected(s *parser.ForeignDDL, name string) bool {
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

// foreignImportedTable is the CREATE FOREIGN TABLE statement for one table
// the importer returned, in the local schema on the statement's server.
func foreignImportedTable(ctx context.Context, definition fdw.TableDefinition, s *parser.ForeignDDL) (*parser.ForeignDDL, error) {
	name, err := fdw.FunctionName(catalog.ForeignFunctionName{Schema: s.LocalSchema.Text, Name: definition.Name})
	if err != nil {
		return nil, err
	}
	qualified := parser.ObjectName{Parts: []parser.Identifier{{Text: name.Schema}, {Text: name.Name}}}
	table := &parser.CreateTable{Name: qualified, Persistence: "logged"}
	result := &parser.ForeignDDL{Action: "create", Kind: "table", Name: qualified, Table: table, Server: s.Server, Options: fdw.ImportedOptions(definition.Options), ColumnOptions: make(map[string][]parser.ForeignOption)}
	for _, column := range definition.Columns {
		if _, err := fdw.FunctionName(catalog.ForeignFunctionName{Name: column.Name}); err != nil {
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
			expression, err := fdw.ImportExpression(column.Default)
			if err != nil {
				return nil, err
			}
			c.Constraints = append(c.Constraints, parser.ColumnConstraint{Default: expression})
		}
		if column.Check != "" {
			expression, err := fdw.ImportExpression(column.Check)
			if err != nil {
				return nil, err
			}
			c.Constraints = append(c.Constraints, parser.ColumnConstraint{Check: &parser.CheckConstraint{Expression: expression}})
		}
		table.Elements = append(table.Elements, parser.TableElement{Column: c})
		result.ColumnOptions[column.Name] = fdw.ImportedOptions(column.Options)
	}
	for _, check := range definition.Checks {
		expression, err := fdw.ImportExpression(check)
		if err != nil {
			return nil, err
		}
		table.Elements = append(table.Elements, parser.TableElement{Check: &parser.CheckConstraint{Expression: expression}})
	}
	return result, nil
}
