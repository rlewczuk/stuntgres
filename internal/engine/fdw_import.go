package engine

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

func (e *Engine) executeForeignImport(scope *StatementScope, s *parser.ForeignDDL) (Result, error) {
	e.mu.Lock()
	auth, err := e.authorizationLocked(scope.op)
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	server, err := fdw.Find(auth.Catalog, ids.ForeignServerClassID, s.Server.Text)
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	if err = auth.Check(access.Requirement{Object: fdw.ObjectRef(auth.View, server), Privileges: authorization.UsagePrivilege}); err != nil {
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
	wrapper, ok := auth.Catalog.Foreign(server.Wrapper)
	if !ok {
		e.mu.Unlock()
		return Result{}, ids.ErrCatalogReplaced
	}
	handler, err := fdw.HandlerFor(auth.Catalog, wrapper)
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	importer, ok := handler.Wrapper.(fdw.SchemaImporter)
	if !ok {
		e.mu.Unlock()
		return Result{}, sqlError("HV00P", fmt.Sprintf(`foreign-data wrapper "%s" does not support IMPORT FOREIGN SCHEMA`, wrapper.Name()))
	}
	mapping, err := fdw.MappingFor(auth.View, server)
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	options, err := fdw.TransformOptions(nil, s.Options)
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	role, err := auth.Roles.Descriptor(auth.State.CurrentUser)
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	request := fdw.ImportRequest{RemoteSchema: s.RemoteSchema.Text, LocalSchema: s.LocalSchema.Text, Server: server.Name(), Role: role.Name, WrapperOptions: slices.Clone(wrapper.Options), ServerOptions: slices.Clone(server.Options), MappingOptions: slices.Clone(mapping.Options), Options: options, Except: s.ImportExcept}
	for _, table := range s.ImportFilter {
		request.Tables = append(request.Tables, table.Text)
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.authorizeLocked(auth, scope.op.Session)
	stage.domainBases = append(stage.domainBases, server, wrapper, mapping, auth.Catalog.Get(wrapper.Handler), auth.Catalog.Get(ns.Address))
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
	var jobs []fdw.Validation
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
			return Result{}, fdw.ImportError(err, definition.Name)
		}
		e.mu.Lock()
		_, currentErr := e.authorizationLocked(scope.op)
		if currentErr == nil && e.catalog.state.Epoch() != stage.Base.Epoch() {
			currentErr = ids.ErrCatalogReplaced
		}
		var validations []fdw.Validation
		if currentErr == nil {
			validations, _, currentErr = e.stageForeignTableLocked(scope.op, scope.input, auth, stage, table)
		}
		e.mu.Unlock()
		if currentErr != nil {
			return Result{}, fdw.ImportError(currentErr, definition.Name)
		}
		for i := range validations {
			validations[i].ImportTable = definition.Name
		}
		jobs = append(jobs, validations...)
	}
	if err = fdw.RunValidations(scope.op.Context(), jobs); err != nil {
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

func foreignImportedTable(scope *StatementScope, definition fdw.TableDefinition, s *parser.ForeignDDL) (*parser.ForeignDDL, error) {
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
		typ, err := parser.ParseTypeName(scope.op.Context(), column.Type, nil)
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
