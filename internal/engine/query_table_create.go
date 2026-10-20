package engine

import (
	"context"
	"errors"
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Source analysis and parameter import precede this destination-only phase.
// Engine.mu is held. A skipped destination never enters executor admission.
func (e *Engine) prepareQueryTableLocked(op *Operation, auth AuthorizationView, s *parser.CreateQueryTable, b analyze.Binding) (*CatalogStage, *catalog.Relation, bool, error) {
	ns, err := e.creationNamespace(auth, s.Name)
	if err != nil {
		if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "0A000" {
			err = creationError(err, s.Name)
		}
		return nil, nil, false, err
	}
	if err = checkPersistentCreationNamespace(auth.Catalog, ns.Address); err != nil {
		return nil, nil, false, err
	}
	name := s.Name.Parts[len(s.Name.Parts)-1].Text
	key := ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns.Address, Name: name}
	if _, exists := auth.Catalog.LookupName(key); exists {
		if s.IfNotExists {
			op.existingRelationNotice(name)
			return nil, nil, true, nil
		}
		return nil, nil, false, sqlError("42P07", fmt.Sprintf(`relation "%s" already exists`, name))
	}
	if err = auth.CheckCreate(ns); err != nil {
		return nil, nil, false, err
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.authorizeLocked(auth, op.session)
	available, err := e.displaceAssociatedArrayLocked(stage, ns.Address, name)
	if err != nil {
		return nil, nil, false, err
	}
	if !available {
		return nil, nil, false, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42710", Message: fmt.Sprintf(`type "%s" already exists`, name), Hint: "A relation has an associated type of the same name, so you must use a name that doesn't conflict with any existing type."}
	}
	projection, ok := b.Query.Root.(*plan.ProjectNode)
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
	columns := make([]catalog.Column, len(schema))
	names := make(map[string]int, len(schema))
	for i, column := range schema {
		column.Nullable, column.NullabilityKnown = true, true
		column.OriginTable, column.OriginAttribute = 0, 0
		column.DeclaredDimensions = 0
		if i < len(s.Columns) {
			column.Name = s.Columns[i].Text
		}
		if _, exists := names[column.Name]; exists {
			return nil, nil, false, sqlError("42701", fmt.Sprintf(`column "%s" specified more than once`, column.Name))
		}
		if catalog.IsSystemColumn(column.Name) {
			return nil, nil, false, sqlError("42701", fmt.Sprintf(`column name "%s" conflicts with a system column name`, column.Name))
		}
		names[column.Name] = i
		typ := column.Usage.Type
		if sqltype.OccurrenceRecordArray(typ) || typ != nil && typ.ID() == 2249 && typ.IsComposite() {
			typeName := "record"
			if sqltype.OccurrenceRecordArray(typ) {
				typeName = "record[]"
			}
			return nil, nil, false, sqlError("42P16", fmt.Sprintf(`column "%s" has pseudo-type %s`, column.Name, typeName))
		}
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
		if projection.Expressions[i].CollationIndeterminate() {
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
			address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(dependencyType.ID())}
			if err = auth.CanUse(e.catalog.Domain.Ref(auth.Catalog.Epoch(), address)); err != nil {
				return nil, nil, false, err
			}
			descriptor := auth.Catalog.Get(address)
			if descriptor == nil {
				return nil, nil, false, errors.New("stuntgres: query table type definition is missing")
			}
			stage.domainBases = append(stage.domainBases, descriptor)
		}
		columns[i] = column
	}
	// Source definitions and namespace lookup authority must still be current at
	// publication, including for NO DATA. Stored data has no dependency on them.
	for _, leaf := range b.QuerySources {
		stage.querySources = append(stage.querySources, leaf.Relation)
		if descriptor := auth.Catalog.Get(leaf.Relation.Namespace()); descriptor != nil {
			stage.querySources = append(stage.querySources, descriptor)
		}
	}
	owner := auth.State.CurrentUser
	if s.Form == parser.CreateMaterializedView && op.replayOrZero().tableDestination {
		directive, err := e.authorizationLocked(op)
		if err != nil {
			return nil, nil, false, err
		}
		owner = directive.State.CurrentUser
	}
	d, err := e.newTableDescriptorLocked(ns.Address, name, owner, columns, names)
	if err != nil {
		return nil, nil, false, err
	}
	if s.Form == parser.CreateMaterializedView {
		definition, err := e.freezeViewDefinitionLocked(auth, s.Query, b, false, false, 0, catalog.MaterializedViewRelation, false)
		if err != nil {
			return nil, nil, false, err
		}
		d.RelationKind, d.View, d.Populated = catalog.MaterializedViewRelation, viewPayloadOf(definition), !s.NoData
	}
	d.Security.ACL = stage.creationACL(owner, ns.Address, authorization.DefaultTables)
	if err = e.stageRowTypeLocked(stage, d); err != nil {
		return nil, nil, false, err
	}
	if err = stage.Create(d); err != nil {
		return nil, nil, false, err
	}
	if stage.OldNameCount() != 0 {
		if err = e.reownStagedTypeGenerationsLocked(stage); err != nil {
			return nil, nil, false, err
		}
		d = stage.Get(d.Address()).(*catalog.Relation)
	}
	return stage, d, false, nil
}

// Rows have already been finalized, typed and detached from query scratch. Only
// this private stage owns them; identity gaps are allowed if publication fails.
func (e *Engine) stageQueryTableRows(ctx context.Context, stage *CatalogStage, d *catalog.Relation, rows [][]sqltype.Value, ref ids.TxRef) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if stage == nil || d == nil || stage.owner != e || stage.invalid != nil || stage.lifecycle != catalogOpen || stage.Get(d.Address()) != d || stage.Base.Get(d.Address()) != nil || stage.initialTables != nil {
		return errors.New("stuntgres: invalid initial table stage")
	}
	if stage.Base.Epoch() != e.catalog.state.Epoch() {
		return ids.ErrCatalogReplaced
	}
	// A demanded producer can enroll reads even when every row is filtered out.
	// NO DATA and existing-target skips never attach a producer transaction.
	if len(rows) == 0 {
		stage.initialTx = ref
		return nil
	}
	for _, row := range rows {
		for i, value := range row {
			if target := d.Columns[i].Usage.Type; target != value.Type() {
				reowned, err := typedef.ReownDomainDatum(value, target, nil, nil, nil)
				if err != nil {
					return err
				}
				row[i] = reowned
			}
		}
	}
	versions, err := e.storage.AllocateRowVersionsLocked(rows, nil)
	if err != nil {
		return err
	}
	stage.initialTables = map[ids.ObjectAddress]*storage.Relation{d.Address(): {Versions: versions}}
	stage.initialTx = ref
	return nil
}

func (s *CatalogStage) validateInitialTables() error {
	for address, storage := range s.initialTables {
		d, ok := s.Get(address).(*catalog.Relation)
		if !ok || !d.RelationKind.Stored() || d.RelationKind == catalog.MaterializedViewRelation && !d.Populated || s.Base.Get(address) != nil || storage == nil || len(storage.Versions) == 0 || storage.Ends != nil || storage.Generation != 0 {
			return errors.New("stuntgres: invalid initial table storage")
		}
		for _, row := range storage.Versions {
			if row.Row == 0 || row.Version == 0 || row.Born != 0 || len(row.Values) != len(d.Columns) {
				return errors.New("stuntgres: invalid initial table row")
			}
			for i, value := range row.Values {
				if value.Type() != d.Columns[i].Usage.Type {
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
	if scope.foreignTablesOrZero().owner != nil {
		if err := scope.finishForeignOwner(scope.foreignTablesOrZero().owner, nil); err != nil {
			return err
		}
	}
	prepared, err := e.ValidateCatalogStage(op.Context(), stage)
	if err == nil {
		if scope.cteInvocation != nil {
			err = e.stageCatalogOverlay(scope, stage, scope.owner.catalog.Clone())
		} else {
			_, err = e.publishCatalogOperation(op, scope, prepared, PublicationToken{Epoch: stage.Base.Epoch()})
		}
	}
	if err == nil && scope.cteInvocation == nil {
		scope.completed = true
	}
	return err
}

// The common SELECT pipeline has already planned and admitted this producer.
// Engine.mu is held on entry; every outcome releases it. The caller owns discard.
func (e *Engine) executeQueryTableLocked(scope *StatementScope, s *parser.CreateQueryTable, b analyze.Binding, root plan.Node, parameters []sqltype.Value, stage *CatalogStage, d *catalog.Relation) (Result, error) {
	ctx := scope.op.Context()
	source, err := e.captureQuerySourcesLocked(scope, b.QuerySources, s.Query.From == nil && s.Query.Values == nil)
	e.mu.Unlock()
	if err != nil {
		return Result{}, err
	}
	if err = exec.SelectQuerySources(ctx, source); err != nil {
		return Result{}, err
	}
	result, count, err := exec.Finalize(ctx, root, source, parameters)
	if err != nil {
		return Result{}, err
	}
	rows, err := result.(*exec.FinalRows).TakeTableRows(ctx)
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
