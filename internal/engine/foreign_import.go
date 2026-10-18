package engine

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

func foreignMappingFor(auth AuthorizationView, server *catalog.ForeignObject) (*catalog.ForeignObject, error) {
	for _, role := range []authorization.PrincipalRef{auth.state.CurrentUser, {}} {
		key := ids.NameKey{ClassID: ids.ForeignMappingClassID, Name: catalog.ForeignMappingName(server.Address(), role)}
		if d, ok := auth.catalog.Foreign(auth.catalog.NameAddress(key)); ok {
			return d, nil
		}
	}
	role, err := auth.roles.Descriptor(auth.state.CurrentUser)
	if err != nil {
		return nil, err
	}
	return nil, sqlError("42704", fmt.Sprintf(`user mapping not found for user "%s", server "%s"`, role.Name, server.Name()))
}
func foreignHandlerFor(catalog catalog.Read, wrapper *catalog.ForeignObject) (*catalog.ForeignProcedure, error) {
	if wrapper.Handler == (ids.ObjectAddress{}) {
		return nil, sqlError("55000", fmt.Sprintf(`foreign-data wrapper "%s" has no handler`, wrapper.Name()))
	}
	p, ok := catalog.Procedure(wrapper.Handler)
	if !ok || p.Foreign == nil || p.Foreign.Wrapper == nil {
		return nil, sqlError("55000", fmt.Sprintf(`foreign-data wrapper "%s" has no registered handler`, wrapper.Name()))
	}
	return p.Foreign, nil
}

func (e *Engine) executeForeignImport(scope *StatementScope, s *parser.ForeignDDL) (Result, error) {
	e.mu.Lock()
	auth, err := e.authorizationLocked(scope.op)
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	server, err := findForeign(auth.catalog, ids.ForeignServerClassID, s.Server.Text)
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	if err = auth.Check(AccessRequirement{Object: foreignRef(auth, server), Privileges: authorization.UsagePrivilege}); err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	ns, err := e.creationNamespace(auth, parser.ObjectName{Parts: []parser.Identifier{s.LocalSchema, {Text: "imported"}}})
	if err == nil {
		err = auth.CheckCreate(ns)
	}
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	wrapper, ok := auth.catalog.Foreign(server.Wrapper)
	if !ok {
		e.mu.Unlock()
		return Result{}, ids.ErrCatalogReplaced
	}
	handler, err := foreignHandlerFor(auth.catalog, wrapper)
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	importer, ok := handler.Wrapper.(ForeignSchemaImporter)
	if !ok {
		e.mu.Unlock()
		return Result{}, sqlError("HV00P", fmt.Sprintf(`foreign-data wrapper "%s" does not support IMPORT FOREIGN SCHEMA`, wrapper.Name()))
	}
	mapping, err := foreignMappingFor(auth, server)
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	options, err := transformForeignOptions(nil, s.Options)
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	role, err := auth.roles.Descriptor(auth.state.CurrentUser)
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	request := ForeignImportRequest{RemoteSchema: s.RemoteSchema.Text, LocalSchema: s.LocalSchema.Text, Server: server.Name(), Role: role.Name, WrapperOptions: slices.Clone(wrapper.Options), ServerOptions: slices.Clone(server.Options), MappingOptions: slices.Clone(mapping.Options), Options: options, Except: s.ImportExcept}
	for _, table := range s.ImportFilter {
		request.Tables = append(request.Tables, table.Text)
	}
	stage := e.BeginCatalogStage(auth.catalog)
	stage.authorizeLocked(auth, scope.op.session)
	stage.domainBases = append(stage.domainBases, server, wrapper, mapping, auth.catalog.Get(wrapper.Handler), auth.catalog.Get(ns.Address))
	e.mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if scope.replacementRead {
		e.replacementMu.RUnlock()
		scope.replacementRead = false
	}
	definitions, err := importer.ImportForeignSchema(scope.op.Context(), request)
	if err != nil {
		return Result{}, err
	}
	var jobs []foreignValidation
	for _, definition := range definitions {
		if err := scope.op.Context().Err(); err != nil {
			return Result{}, err
		}
		selected := len(s.ImportFilter) == 0
		if len(s.ImportFilter) != 0 {
			selected = false
			for _, filter := range s.ImportFilter {
				if filter.Text == definition.Name {
					selected = true
					break
				}
			}
			if s.ImportExcept {
				selected = !selected
			}
		}
		if !selected {
			continue
		}
		table, err := foreignImportedTable(scope, definition, s)
		if err != nil {
			return Result{}, foreignImportError(err, definition.Name)
		}
		e.mu.Lock()
		_, currentErr := e.authorizationLocked(scope.op)
		if currentErr == nil && e.catalog.state.Epoch() != stage.Base.Epoch() {
			currentErr = ids.ErrCatalogReplaced
		}
		var validations []foreignValidation
		if currentErr == nil {
			validations, _, currentErr = e.stageForeignTableLocked(scope, auth, stage, table)
		}
		e.mu.Unlock()
		if currentErr != nil {
			return Result{}, foreignImportError(currentErr, definition.Name)
		}
		for i := range validations {
			validations[i].importTable = definition.Name
		}
		jobs = append(jobs, validations...)
	}
	if err = runForeignValidations(scope.op.Context(), jobs); err != nil {
		return Result{}, err
	}
	if err = scope.acquireDefinitionProviders(stage); err != nil {
		return Result{}, err
	}
	prepared, err := e.ValidateCatalogStage(scope.op.Context(), stage)
	if err != nil {
		return Result{}, err
	}
	_, err = e.publishCatalogOperation(scope.op, scope, prepared, PublicationToken{Epoch: stage.Base.Epoch()})
	return Result{}, err
}

func foreignImportExpression(text string) (parser.Expr, error) {
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
func importedOptions(options []ForeignOption) []parser.ForeignOption {
	out := make([]parser.ForeignOption, len(options))
	for i, o := range options {
		out[i] = parser.ForeignOption{Name: parser.Identifier{Text: o.Name}, Value: o.Value}
	}
	return out
}
func foreignImportedTable(scope *StatementScope, definition ForeignTableDefinition, s *parser.ForeignDDL) (*parser.ForeignDDL, error) {
	name, err := foreignFunctionName(ForeignFunctionName{Schema: s.LocalSchema.Text, Name: definition.Name})
	if err != nil {
		return nil, err
	}
	qualified := parser.ObjectName{Parts: []parser.Identifier{{Text: name.Schema}, {Text: name.Name}}}
	table := &parser.CreateTable{Name: qualified, Persistence: "logged"}
	result := &parser.ForeignDDL{Action: "create", Kind: "table", Name: qualified, Table: table, Server: s.Server, Options: importedOptions(definition.Options), ColumnOptions: make(map[string][]parser.ForeignOption)}
	for _, column := range definition.Columns {
		if _, err := foreignFunctionName(ForeignFunctionName{Name: column.Name}); err != nil {
			return nil, err
		}
		typ, err := parser.ParseTypeName(scope.op.Context(), column.Type, nil)
		if err != nil {
			return nil, err
		}
		c := &parser.Column{Name: column.Name, TypeName: typ}
		if column.NotNull {
			c.Constraints = append(c.Constraints, parser.ColumnConstraint{NotNull: true})
		}
		if column.Default != "" {
			expression, err := foreignImportExpression(column.Default)
			if err != nil {
				return nil, err
			}
			c.Constraints = append(c.Constraints, parser.ColumnConstraint{Default: expression})
		}
		if column.Check != "" {
			expression, err := foreignImportExpression(column.Check)
			if err != nil {
				return nil, err
			}
			c.Constraints = append(c.Constraints, parser.ColumnConstraint{Check: &parser.CheckConstraint{Expression: expression}})
		}
		table.Elements = append(table.Elements, parser.TableElement{Column: c})
		result.ColumnOptions[column.Name] = importedOptions(column.Options)
	}
	for _, check := range definition.Checks {
		expression, err := foreignImportExpression(check)
		if err != nil {
			return nil, err
		}
		table.Elements = append(table.Elements, parser.TableElement{Check: &parser.CheckConstraint{Expression: expression}})
	}
	return result, nil
}
