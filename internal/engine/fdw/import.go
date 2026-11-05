package fdw

import (
	"context"
	"fmt"
	"slices"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/parser"
)

// FindMapping selects the current user's mapping before PUBLIC from the
// captured authorization catalog; absence is valid optional IMPORT metadata.
func FindMapping(view access.View, server *catalog.ForeignObject) (*catalog.ForeignObject, bool) {
	for _, role := range []authorization.PrincipalRef{view.State.CurrentUser, {}} {
		key := ids.NameKey{ClassID: ids.ForeignMappingClassID, Name: catalog.ForeignMappingName(server.Address(), role)}
		if d, ok := view.Catalog.Foreign(view.Catalog.NameAddress(key)); ok {
			return d, true
		}
	}
	return nil, false
}

func MappingFor(view access.View, server *catalog.ForeignObject) (*catalog.ForeignObject, error) {
	if mapping, ok := FindMapping(view, server); ok {
		return mapping, nil
	}
	role, err := view.Roles.Descriptor(view.State.CurrentUser)
	if err != nil {
		return nil, err
	}
	return nil, sqlError("42704", fmt.Sprintf(`user mapping not found for user "%s", server "%s"`, role.Name, server.Name()))
}

func HandlerFor(catalog catalog.Read, wrapper *catalog.ForeignObject, restricted settings.RestrictedRelationKinds) (*catalog.ForeignProcedure, error) {
	if restricted&settings.RestrictForeignTable != 0 {
		return nil, sqlError("55000", "access to non-system foreign table is restricted")
	}
	if wrapper.Handler == (ids.ObjectAddress{}) {
		return nil, sqlError("55000", fmt.Sprintf(`foreign-data wrapper "%s" has no handler`, wrapper.Name()))
	}
	p, ok := catalog.Procedure(wrapper.Handler)
	if !ok || p.Foreign == nil || p.Foreign.Wrapper == nil {
		return nil, sqlError("55000", fmt.Sprintf(`foreign-data wrapper "%s" has no registered handler`, wrapper.Name()))
	}
	if p.Implementation == extension.StockFileFDW || p.Foreign.Wrapper == extension.StockFileFDW {
		if err := extension.StockFileFDW.ValidateProcedure(catalog, p); err != nil { return nil, err }
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

// PlanImport decides IMPORT FOREIGN SCHEMA up to actual handler invocation:
// server USAGE, local schema CREATE, selected handler metadata, mapping,
// options and request. The captured SQL wrapper name is diagnostic text only.
// Execution obtains the real methods before testing optional import support.
// The stage observes the server, wrapper, selected mapping, handler and schema.
// The engine runs the importer after releasing Engine.mu and replacementMu.
func PlanImport(h Host, auth ddl.Authority, s *parser.ForeignDDL, restricted settings.RestrictedRelationKinds) (exec.ForeignHandlerSelection, ImportRequest, string, error) {
	view := h.AccessView(auth)
	server, err := Find(view.Catalog, ids.ForeignServerClassID, s.Server.Text)
	if err != nil {
		return exec.ForeignHandlerSelection{}, ImportRequest{}, "", err
	}
	if err = ddl.Check(h, auth, access.Requirement{Object: ObjectRef(view, server), Privileges: authorization.UsagePrivilege}); err != nil {
		return exec.ForeignHandlerSelection{}, ImportRequest{}, "", err
	}
	ns, err := h.ExistingCreationNamespace(auth, parser.ObjectName{Parts: []parser.Identifier{s.LocalSchema, {Text: "imported"}}})
	if err == nil {
		err = view.CheckCreate(auth, ns)
	}
	if err != nil {
		return exec.ForeignHandlerSelection{}, ImportRequest{}, "", err
	}
	wrapper, ok := view.Catalog.Foreign(server.Wrapper)
	if !ok {
		return exec.ForeignHandlerSelection{}, ImportRequest{}, "", ids.ErrCatalogReplaced
	}
	_, err = HandlerFor(view.Catalog, wrapper, restricted)
	if err != nil {
		return exec.ForeignHandlerSelection{}, ImportRequest{}, "", err
	}
	mapping, mappingPresent := FindMapping(view, server)
	// IMPORT options belong to the provider, not stored catalog metadata.
	// Preserve ordered occurrences (including duplicates) for its semantics.
	options := make([]catalog.ForeignOption, len(s.Options))
	for i, option := range s.Options {
		options[i] = catalog.ForeignOption{Name: option.Name.Text, Value: option.Value}
	}
	role, err := view.Roles.Descriptor(view.State.CurrentUser)
	if err != nil {
		return exec.ForeignHandlerSelection{}, ImportRequest{}, "", err
	}
	request := ImportRequest{RemoteSchema: s.RemoteSchema.Text, LocalSchema: s.LocalSchema.Text, Server: server.Name(), Role: role.Name, WrapperOptions: slices.Clone(wrapper.Options), ServerOptions: slices.Clone(server.Options), MappingPresent: mappingPresent, Options: options, Except: s.ImportExcept}
	if mappingPresent {
		request.MappingOptions = slices.Clone(mapping.Options)
		h.AddDomainBase(mapping)
	}
	for _, table := range s.ImportFilter {
		request.Tables = append(request.Tables, table.Text)
	}
	for _, base := range []catalog.Descriptor{server, wrapper, view.Catalog.Get(wrapper.Handler), view.Catalog.Get(ns.Address)} {
		h.AddDomainBase(base)
	}
	return exec.SelectForeignHandler(view.Catalog, wrapper.Handler), request, wrapper.Name(), nil
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
