package engine

import (
	"context"
	"errors"
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Source analysis and parameter import precede this destination-only phase.
// Engine.mu is held. A skipped destination never enters executor admission.
func (e *Engine) prepareQueryTableLocked(op *Operation, auth AuthorizationView, s *parser.CreateQueryTable, b binding) (*CatalogStage, *relationDescriptor, bool, error) {
	ns, err := e.creationNamespace(auth, s.Name)
	if err != nil {
		if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "0A000" {
			err = creationError(err, s.Name)
		}
		return nil, nil, false, err
	}
	if err = checkPersistentCreationNamespace(auth.catalog, ns.Address); err != nil {
		return nil, nil, false, err
	}
	name := s.Name.Parts[len(s.Name.Parts)-1].Text
	key := NameKey{ClassID: RelationClassID, Namespace: ns.Address, Name: name}
	if _, exists := auth.catalog.state.names[key]; exists {
		if s.IfNotExists {
			op.existingRelationNotice(name)
			return nil, nil, true, nil
		}
		return nil, nil, false, sqlError("42P07", fmt.Sprintf(`relation "%s" already exists`, name))
	}
	if err = auth.CheckCreate(ns); err != nil {
		return nil, nil, false, err
	}
	stage := e.BeginCatalogStage(auth.catalog)
	stage.authorizeLocked(auth, op.session)
	available, err := e.displaceAssociatedArrayLocked(stage, ns.Address, name)
	if err != nil {
		return nil, nil, false, err
	}
	if !available {
		return nil, nil, false, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42710", Message: fmt.Sprintf(`type "%s" already exists`, name), Hint: "A relation has an associated type of the same name, so you must use a name that doesn't conflict with any existing type."}
	}
	projection, ok := b.query.Root.(*projectNode)
	if !ok {
		return nil, nil, false, errors.New("stuntgres: query table lacks final projection")
	}
	schema := projection.Schema()
	if len(s.Columns) > len(schema) {
		return nil, nil, false, sqlError("42601", "too many column names were specified")
	}
	if len(schema) > 1600 {
		return nil, nil, false, sqlError("54011", "tables can have at most 1600 columns")
	}
	columns := make([]ColumnDescriptor, len(schema))
	names := make(map[string]int, len(schema))
	for i, column := range schema {
		column.Nullable, column.NullabilityKnown = true, true
		column.OriginTable, column.OriginAttribute = 0, 0
		if i < len(s.Columns) {
			column.Name = s.Columns[i].Text
		}
		if _, exists := names[column.Name]; exists {
			return nil, nil, false, sqlError("42701", fmt.Sprintf(`column "%s" specified more than once`, column.Name))
		}
		if isSystemColumn(column.Name) {
			return nil, nil, false, sqlError("42701", fmt.Sprintf(`column name "%s" conflicts with a system column name`, column.Name))
		}
		names[column.Name] = i
		typ := column.Usage.Type
		if typ == nil || !typ.Concrete() || typ.ID() == sqltype.Void {
			typeName := "unknown"
			if typ != nil {
				typeName = typ.Name()
			}
			return nil, nil, false, sqlError("42P16", fmt.Sprintf(`column "%s" has pseudo-type %s`, column.Name, typeName))
		}
		if err = validateQueryTableOutputType(typ); err != nil {
			return nil, nil, false, err
		}
		if projection.expressions[i].CollationIndeterminate() {
			return nil, nil, false, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P22", Message: fmt.Sprintf(`no collation was derived for column "%s" with collatable type %s`, column.Name, typ.Name()), Hint: "Use the COLLATE clause to set the collation explicitly."}
		}
		if !column.Usage.Valid() {
			return nil, nil, false, errors.New("stuntgres: invalid query table usage")
		}
		dependencyType := typ
		for dependencyType.IsArray() {
			dependencyType = dependencyType.Element()
		}
		if dependencyType.IsUserDefined() {
			address := ObjectAddress{ClassID: TypeClassID, ObjectID: OID(dependencyType.ID())}
			if err = auth.CanUse(ObjectRef{owner: e.owner, Epoch: auth.catalog.Epoch(), Address: address}); err != nil {
				return nil, nil, false, err
			}
			descriptor := auth.catalog.state.descriptors[address]
			if descriptor == nil {
				return nil, nil, false, errors.New("stuntgres: query table type definition is missing")
			}
			stage.domainBases = append(stage.domainBases, descriptor)
		}
		columns[i] = column
	}
	// Source definitions and namespace lookup authority must still be current at
	// publication, including for NO DATA. Stored data has no dependency on them.
	for _, leaf := range b.querySources {
		stage.querySources = append(stage.querySources, leaf.relation)
		if descriptor := auth.catalog.state.descriptors[leaf.relation.namespace]; descriptor != nil {
			stage.querySources = append(stage.querySources, descriptor)
		}
	}
	owner := auth.state.CurrentUser
	if s.Form == parser.CreateMaterializedView && op.restoreTableDestination {
		directive, err := e.authorizationLocked(op)
		if err != nil {
			return nil, nil, false, err
		}
		owner = directive.state.CurrentUser
	}
	d, err := e.newTableDescriptorLocked(ns.Address, name, owner, columns, names)
	if err != nil {
		return nil, nil, false, err
	}
	if s.Form == parser.CreateMaterializedView {
		definition, err := e.freezeViewDefinitionLocked(auth, s.Query, b, false, false, 0, materializedViewRelation, false)
		if err != nil {
			return nil, nil, false, err
		}
		d.relationKind, d.view, d.populated = materializedViewRelation, definition, !s.NoData
	}
	d.acl = stage.creationACL(owner, ns.Address, authorization.DefaultTables)
	if err = e.stageRowTypeLocked(stage, d); err != nil {
		return nil, nil, false, err
	}
	if err = stage.Create(d); err != nil {
		return nil, nil, false, err
	}
	if len(stage.oldNames) != 0 {
		if err = e.reownStagedTypeGenerationsLocked(stage); err != nil {
			return nil, nil, false, err
		}
		d = stage.descriptors[d.address].(*relationDescriptor)
	}
	return stage, d, false, nil
}

// Rows have already been finalized, typed and detached from query scratch. Only
// this private stage owns them; identity gaps are allowed if publication fails.
func (e *Engine) stageQueryTableRows(ctx context.Context, stage *CatalogStage, d *relationDescriptor, rows [][]sqltype.Value, ref TxRef) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if stage == nil || d == nil || stage.owner != e || stage.invalid != nil || stage.lifecycle != catalogOpen || stage.descriptors[d.address] != d || stage.base.state.descriptors[d.address] != nil || stage.initialTables != nil {
		return errors.New("stuntgres: invalid initial table stage")
	}
	if stage.base.Epoch() != e.state.epoch {
		return ErrCatalogReplaced
	}
	// A demanded producer can enroll reads even when every row is filtered out.
	// NO DATA and existing-target skips never attach a producer transaction.
	if len(rows) == 0 {
		stage.initialTx = ref
		return nil
	}
	for _, row := range rows {
		for i, value := range row {
			if target := d.columns[i].Usage.Type; target != value.Type() {
				reowned, err := reownDomainDatum(value, target, nil, nil, nil)
				if err != nil {
					return err
				}
				row[i] = reowned
			}
		}
	}
	versions, err := e.allocateRowVersionsLocked(rows, nil)
	if err != nil {
		return err
	}
	stage.initialTables = map[ObjectAddress]*relationStorage{d.address: {versions: versions}}
	stage.initialTx = ref
	return nil
}

func (s *CatalogStage) validateInitialTables() error {
	for address, storage := range s.initialTables {
		d, ok := s.descriptors[address].(*relationDescriptor)
		if !ok || !d.relationKind.stored() || d.relationKind == materializedViewRelation && !d.populated || s.base.state.descriptors[address] != nil || storage == nil || len(storage.versions) == 0 || storage.ends != nil || storage.generation != 0 {
			return errors.New("stuntgres: invalid initial table storage")
		}
		for _, row := range storage.versions {
			if row.row == 0 || row.version == 0 || row.born != 0 || len(row.values) != len(d.columns) {
				return errors.New("stuntgres: invalid initial table row")
			}
			for i, value := range row.values {
				if value.Type() != d.columns[i].Usage.Type {
					return errors.New("stuntgres: invalid initial table value type")
				}
			}
		}
	}
	return nil
}

func (e *Engine) publishQueryTable(scope *StatementScope, stage *CatalogStage) error {
	op := scope.op
	if err := scope.acquireDefinitionProviders(stage); err != nil {
		return err
	}
	prepared, err := e.ValidateCatalogStage(op.Context(), stage)
	if err == nil {
		_, err = e.publishCatalogOperation(op, prepared, PublicationToken{Epoch: stage.base.Epoch()})
	}
	if err == nil {
		scope.completed = true
	}
	return err
}

// The common SELECT pipeline has already planned and admitted this producer.
// Engine.mu is held on entry; every outcome releases it. The caller owns discard.
func (e *Engine) executeQueryTableLocked(scope *StatementScope, s *parser.CreateQueryTable, b binding, root LogicalNode, parameters []sqltype.Value, stage *CatalogStage, d *relationDescriptor) (Result, error) {
	ctx := scope.op.Context()
	source, err := e.captureQuerySourcesLocked(scope, b.querySources, s.Query.From == nil && s.Query.Values == nil)
	e.mu.Unlock()
	if err != nil {
		return Result{}, err
	}
	if err = selectQuerySources(ctx, source); err != nil {
		return Result{}, err
	}
	result, count, err := finalizeQuery(ctx, root, source, parameters)
	if err != nil {
		return Result{}, err
	}
	rows, err := result.(*finalRows).takeTableRows(ctx)
	if err != nil {
		return Result{}, err
	}
	if err = e.stageQueryTableRows(ctx, stage, d, rows, scope.txRef); err != nil {
		return Result{}, err
	}
	if err = e.publishQueryTable(scope, stage); err != nil {
		return Result{}, err
	}
	return Result{Count: int64(count)}, nil
}

// Concrete catalog carriers are not necessarily physical SQL storage values.
// Resolve existing domain/array wrappers without adding a public type framework.
func validateQueryTableOutputType(typ *sqltype.Descriptor) error {
	if typ.ContainsRegproc() {
		return sqlError("0A000", "query table output type regproc is unsupported")
	}
	for {
		physical := typ.BaseType()
		if !physical.IsArray() {
			if physical.ID() == sqltype.PgNodeTree {
				return sqlError("0A000", "query table output type pg_node_tree is unsupported")
			}
			return nil
		}
		// Reject direct enum elements, not the supported domain-over-enum
		// element arrays. Outer physical wrappers still reach this boundary.
		if physical.Element().IsEnum() {
			return sqlError("0A000", "unsupported array element type")
		}
		typ = physical.Element()
	}
}
