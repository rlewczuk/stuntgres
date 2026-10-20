package engine

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (e *Engine) executeStatement(scope *StatementScope, stmt parser.Statement, count int, importFrame FrameImporter) (Result, error) {
	op := scope.op
	if err := e.validateOperation(op); err != nil {
		return Result{}, err
	}
	captureEnumOperationSafety(op, &scope.input)
	scope.input.LockedDomainInput, scope.input.DomainChecks = scope, scope
	if !op.commandAdmission {
		op.snapshotNoticeThreshold()
	}
	ctx := op.Context()
	switch stmt.(type) {
	case *parser.CreateFunction:
		return e.executeCreateFunction(scope, stmt.(*parser.CreateFunction), count)
	case *parser.Listen, *parser.Unlisten, *parser.Notify:
		return scope.executeNotification(stmt, count)
	case *parser.ForeignDDL:
		result, err := e.executeForeignDDL(scope, stmt.(*parser.ForeignDDL), count)
		if err == nil {
			scope.completed = true
		}
		return result, err
	case *parser.CreateTablespace:
		return e.executeCreateTablespace(op, stmt.(*parser.CreateTablespace), count)
	case *parser.Truncate:
		return Result{}, scope.stageTruncate()
	case *parser.CreateExtension:
		return e.executeExtension(scope, stmt.(*parser.CreateExtension), count)
	case *parser.DropExtension:
		return e.executeDropExtension(scope, stmt.(*parser.DropExtension), count)
	case *parser.AlterExtensionUpdate:
		return e.executeExtensionUpdate(scope, stmt.(*parser.AlterExtensionUpdate), count)
	case *parser.Discard:
		return scope.executeDiscard(stmt.(*parser.Discard), count)
	case *parser.PrepareSQL:
		return scope.executePrepareSQL(stmt.(*parser.PrepareSQL), count)
	case *parser.DeallocateSQL:
		return scope.executeDeallocateSQL(stmt.(*parser.DeallocateSQL), count)
	case *parser.FetchCursor:
		return scope.executeFetchCursor(stmt.(*parser.FetchCursor), count)
	case *parser.CloseCursor:
		return scope.executeCloseCursor(stmt.(*parser.CloseCursor), count)
	case *parser.DeclareCursor:
		declaration := stmt.(*parser.DeclareCursor)
		// A modifying WITH is an analysis error, before cursor installation
		// checks. Ordinary declarations keep their existing execution order.
		if !parser.HasModifyingCTE(declaration.Query) {
			if !scope.owner.inBlock() && !declaration.Hold {
				return Result{}, sqlError("25P01", "DECLARE CURSOR can only be used in transaction blocks")
			}
			name := declaration.Name.Text
			if op.session.cursors.entries[name] != nil || protocolPortalExists(op.session, name) {
				return Result{}, sqlError("42P03", fmt.Sprintf("cursor %q already exists", name))
			}
		}
	case *parser.CreateRule, *parser.AlterRule, *parser.DropRule:
		return e.executeRule(scope, stmt, count)
	case *parser.CreatePolicy, *parser.AlterPolicy, *parser.DropPolicy:
		return e.executePolicy(scope, stmt, count)
	case *parser.CreateIndex:
		return e.executeCreateIndex(scope, stmt.(*parser.CreateIndex), count)
	case *parser.DropIndex, *parser.AlterIndex, *parser.Reindex:
		return e.executeIndexMaintenance(scope, stmt, count)
	case *parser.CreateView:
		return e.executeCreateView(scope, stmt.(*parser.CreateView), count)
	case *parser.AlterOwner:
		return e.executeOwnership(scope, stmt.(*parser.AlterOwner), count)
	case *parser.ReassignOwned, *parser.DropOwned:
		return e.executeOwned(scope, stmt, count)
	case *parser.AlterView:
		if owner := stmt.(*parser.AlterView); owner.Action == "owner" {
			kind := "view"
			if owner.Materialized {
				kind = "materialized view"
			}
			return e.executeRelationOwnership(scope, owner.Name, owner.Target.Text, owner.OwnerIdentity, kind, owner.IfExists, count)
		}
		return e.executeAlterView(scope, stmt.(*parser.AlterView), count)
	case *parser.DropView:
		return e.executeDropView(scope, stmt.(*parser.DropView), count)
	case *parser.CreateSequence, *parser.AlterSequence, *parser.DropSequence:
		return e.executeSequenceDDL(scope, stmt, count)
	case *parser.CreateEnum, *parser.AlterEnum:
		return e.executeEnumDDL(scope, stmt, count)
	case *parser.AlterDomain:
		result, err := e.executeAlterDomain(scope, stmt.(*parser.AlterDomain), count)
		if err == nil {
			scope.completed = true
		}
		return result, err
	case *parser.AlterComposite:
		return e.executeAlterComposite(scope, op, stmt.(*parser.AlterComposite), count)
	case *parser.Comment:
		if c := stmt.(*parser.Comment); c.Kind == parser.CommentSequence {
			return e.executeSequenceComment(scope, c, count)
		}
		result, err := e.executeComment(scope, stmt.(*parser.Comment), count)
		if err == nil {
			scope.completed = true
		}
		return result, err
	case *parser.CreateSchema, *parser.AlterSchema, *parser.DropSchema, *parser.CreateComposite, *parser.DropComposite, *parser.CreateDomain, *parser.DropDomain, *parser.CreateCollation, *parser.AlterCollation, *parser.DropCollation, *parser.CreateConversion, *parser.AlterConversion, *parser.DropConversion:
		result, err := e.executeSchema(scope, op, stmt, count, scope.input)
		if err == nil {
			scope.completed = true
		}
		return result, err
	case *parser.CreateOperator, *parser.AlterOperator, *parser.DropOperator:
		result, err := e.executeSchema(scope, op, stmt, count, scope.input)
		if err == nil {
			scope.completed = true
		}
		return result, err
	case *parser.CreateRole, *parser.AlterRole:
		result, err := e.executeRoleAttributes(scope, stmt, count)
		if err == nil {
			scope.completed = true
		}
		return result, err
	case *parser.DropRole, *parser.SessionAuthorization, *parser.RoleSelection, *parser.RoleMembership, *parser.ObjectGrant, *parser.AlterDefaultPrivileges:
		if grant, ok := stmt.(*parser.ObjectGrant); ok && foreignGrantKind(grant.Kind) {
			return e.executeForeignGrant(scope, grant, count)
		}
		if grant, ok := stmt.(*parser.ObjectGrant); ok && grant.Kind == "database" {
			return e.executeDatabaseGrant(scope, grant, count)
		}
		result, err := e.executeRole(op, stmt, count, !scope.owner.inBlock())
		if err == nil {
			scope.completed = true
		}
		return result, err
	case *parser.Set, *parser.Show, *parser.Reset:
		result, err := e.executeSetting(op, stmt, count, !scope.owner.inBlock())
		if err == nil {
			scope.completed = true
		}
		return result, err
	case *parser.SettingCall:
		result, err := e.executeSettingCall(op, stmt.(*parser.SettingCall), count, importFrame, scope.input, scope.output)
		if err == nil {
			scope.completed = true
		}
		return result, err
	}
	e.mu.Lock()
	if err := scope.refreshRelationGrantsLocked(e); err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	auth, err := e.statementAuthorizationLocked(op, stmt)
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	scope.routineAuth = auth
	// Trusted Restore insertion elevates permissions, not session identity.
	// Defaults and CHECK providers must observe the actual owner directive.
	identityAuth := auth
	if _, inserting := stmt.(*parser.Insert); inserting && op.replayOrZero().insertAuthority {
		identityAuth, err = e.authorizationLocked(op)
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
	}
	if err := scope.captureIdentityLocked(identityAuth); err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	if err := e.captureEnumOperationInput(auth, &scope.input); err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	if s, ok := stmt.(*parser.AlterTable); ok {
		return e.alterTableLocked(scope, op, auth, s, count)
	}
	if s, ok := stmt.(*parser.AlterTableDefault); ok {
		return e.alterDefaultLocked(scope, op, auth, s, count)
	}
	switch s := stmt.(type) {
	case *parser.AlterTableAllInTablespace:
		return e.alterAllTablespaceLocked(scope, auth, s, count)
	case *parser.RefreshMaterializedView:
		return e.refreshMaterializedLocked(scope, auth, s, count)
	case *parser.AlterTableOf:
		return e.alterTableOfLocked(scope, op, auth, s, count)
	case *parser.AlterTableAttachPartition:
		return e.attachPartitionLocked(scope, op, auth, s, count)
	case *parser.AlterTableDetachPartition:
		return e.detachPartitionLocked(scope, op, auth, s, count)
	case *parser.AlterTableRenameConstraint:
		return e.renameConstraintLocked(scope, op, auth, s, count)
	case *parser.AlterTableSetSchema:
		return e.alterSchemaLocked(scope, op, auth, s, count)
	case *parser.AlterTableRenameColumn:
		return e.renameRelationLocked(scope, op, auth, s.Name, s.Column.Text, s.NewName.Text, s.IfExists, true, false, s.Only, count)
	case *parser.AlterTableRenameTable:
		return e.renameRelationLocked(scope, op, auth, s.Name, "", s.NewName.Text, s.IfExists, false, false, false, count)
	}
	if s, ok := stmt.(*parser.DropTable); ok {
		return e.dropTableLocked(scope, op, auth, s, count)
	}
	if s, ok := stmt.(*parser.CreateTable); ok {
		return e.createTableLocked(scope, op, auth, s, count)
	}
	expected := scope.Expected
	var candidate StatementDescription
	quoteCapturesChanged := false
	valid := true
	replan := scope.prepared.invalid || expected != nil && expected.planOwner == op.session.token && expected.planGeneration != op.session.discardPlansGeneration
	if expected != nil && expected.analysis != nil {
		path, er := e.sequenceEnvironmentLocked(auth)
		if er != nil {
			e.mu.Unlock()
			return Result{}, er
		}
		// Evolution during a wait can invalidate captures, but admission has
		// already fixed this attempt's identities. Never resolve them again.
		current := e.preparedAnalysisValidLocked(auth, expected.analysis, path)
		if !current && !scope.prepared.invalid {
			scope.prepared.changedDuringAdmission = true
		}
		replan = replan || !current
	}
	if expected != nil {
		valid, err = e.sequenceAnalysisValidLocked(auth, expected.sequences)
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		candidate = *expected
		if expected.Parameters.Count != 0 {
			// Parameterized executions never borrow a retained quote group.
			candidate.quotes = nil
		} else if expected.quoteGeneration != op.session.discardPlansGeneration {
			candidate.quotes = nil
			quoteCapturesChanged = true
		}
		if !valid {
			candidate.sequences = nil
		}
		regtypeValid, er := e.regtypeAnalysisValidLocked(auth, expected.regtypes)
		if er != nil {
			e.mu.Unlock()
			return Result{}, er
		}
		if !regtypeValid {
			// Invalidation survives failed analysis. Restoring the old path
			// must not make its retired literal captures reusable again.
			expected.regtypes.invalidated = true
			candidate.regtypes = nil
		}
		if replan {
			candidate.refreshTemporals = true
			candidate.sequences, candidate.regtypes = nil, nil
			// Analysis changes retire statement-root captures, not unchanged
			// stored definitions. DISCARD already cleared the complete group.
			for i, saved := range candidate.quotes {
				if saved.Origin.Owner != (ids.ObjectRef{}) {
					continue
				}
				quotes := make([]quoteConstant, 0, len(candidate.quotes)-1)
				quotes = append(quotes, candidate.quotes[:i]...)
				for _, remaining := range candidate.quotes[i+1:] {
					if remaining.Origin.Owner != (ids.ObjectRef{}) {
						quotes = append(quotes, remaining)
					}
				}
				candidate.quotes = quotes
				quoteCapturesChanged = true
				break
			}
			valid = false
		}
		expected = &candidate
	}
	b, err := e.describeScoped(scope, stmt, auth, expected)
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	if scope.sqlRoutine.scalar {
		scope.sqlRoutine.resultPlan, err = validateSQLRoutineResult(b.query.Root.Schema(), scope.sqlRoutine.result)
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
	}
	// Preserve the raw caller's capture group before attaching stored recipes.
	// Rewrite metadata must not become caller-owned native lifetime provenance.
	callerSequences, callerRegtypes := b.prepared.sequenceConstants, b.prepared.regtypeConstants
	if b.viewWrite != nil {
		e.mu.Unlock()
		if b.viewWrite.base.RelationKind != catalog.ViewRelation {
			if err := scope.acquireViewWriteTarget(b.viewWrite); err != nil {
				return Result{}, err
			}
		}
		e.mu.Lock()
		auth, err = e.statementAuthorizationLocked(op, stmt)
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		scope.routineAuth = auth
		if err := scope.validateAdmissionNamesLocked(e, auth); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		b.storage = scope.lockedStorage
		if err := e.attachViewWriteSourcesLocked(scope, &b); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
	}
	if err := e.attachCTEViewTargetsLocked(scope, &b, true); err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	if b.resolvers.regclass != nil {
		scope.input.Regclass = b.resolvers.regclass
	}
	if b.resolvers.regtype != nil {
		scope.input.Regtype = b.resolvers.regtype
	}
	if b.resolvers.regrole != nil {
		scope.input.Regrole = b.resolvers.regrole
	}
	if b.resolvers.operator != nil {
		scope.input.Operator = b.resolvers.operator
	}
	if b.resolvers.regproc != nil {
		scope.input.Regproc = b.resolvers.regproc
	}
	if b.resolvers.regcollation != nil {
		scope.input.Regcollation = b.resolvers.regcollation
	}
	if b.resolvers.regnamespace != nil {
		scope.input.Regnamespace = b.resolvers.regnamespace
	}
	if b.resolvers.regTS != nil {
		scope.input.RegTS = b.resolvers.regTS
	}
	if b.resolvers.aclitem != nil {
		scope.input.ACLItem = b.resolvers.aclitem
	}
	if b.resolvers.fts != nil {
		scope.input.FTS = b.resolvers.fts
	}
	if b.resolvers.typeDiagnosticName != nil {
		scope.input.TypeDiagnosticName = b.resolvers.typeDiagnosticName
	}
	scope.input.EnumMemberSafe = b.resolvers.enumMemberSafe
	if err := scope.captureFormalInputLocked(auth, b.parameters.Types); err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	if err := scope.validateQuerySources(b.querySources); err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	if err := e.normalizeRuleCTEsLocked(scope, auth, stmt, &b); err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	if scope.Expected != nil {
		var results []catalog.Column
		if b.query != nil && !b.commandQuery {
			results = b.query.Root.Schema()
		}
		if b.returning != nil {
			results = b.returning.schema
		}
		if scope.sqlRoutine.scalar {
			// The caller freezes the declared scalar output. A SQL body's raw
			// output may change through a valid assignment conversion on reanalysis.
			results = scope.Expected.Results
		}
		if err := checkStatementSignature(*scope.Expected, StatementDescription{Parameters: b.parameters, Results: results, catalogOwner: auth.Catalog.Owner(), catalogEpoch: auth.Catalog.Epoch()}); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		if err = e.pinSequenceReplacementLocked(auth, scope.Expected.sequences, callerSequences); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		if !valid || scope.Expected.regtypes != nil && candidate.regtypes == nil {
			analysis, er := e.sequenceAnalysisLocked(auth, callerSequences)
			if er != nil {
				e.mu.Unlock()
				return Result{}, er
			}
			regtypes, er := e.regtypeAnalysisLocked(auth, callerRegtypes)
			if er != nil {
				e.mu.Unlock()
				return Result{}, er
			}
			var prepared *preparedAnalysis
			if replan && preparableBody(stmt) {
				prepared, er = e.preparedAnalysisLocked(scope, auth, b)
				if er != nil {
					e.mu.Unlock()
					return Result{}, er
				}
			}
			if er := ctx.Err(); er != nil {
				e.mu.Unlock()
				return Result{}, er
			}
			if scope.Expected.sequences != nil {
				*scope.Expected.sequences = analysis
			} else if len(analysis.Constants) != 0 {
				scope.Expected.sequences = &analysis
			}
			if scope.Expected.regtypes != nil && regtypes != nil {
				*scope.Expected.regtypes = *regtypes
			} else {
				scope.Expected.regtypes = regtypes
			}
			if replan {
				scope.Expected.temporals = b.prepared.temporals
				if preparableBody(stmt) {
					scope.Expected.analysis = prepared
				}
				scope.Expected.planGeneration = op.session.discardPlansGeneration
			}
		}
	}
	if count != b.parameters.Count {
		e.mu.Unlock()
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want %d", count, b.parameters.Count)
	}
	canonical := make([]sqltype.Value, count)
	evaluation := EvalContext{evalStatement: evalStatement{Context: ctx, Parameters: canonical, Host: hostOf(scope), Catalog: auth.Catalog, Input: scope.input, Output: scope.output}}
	scope.domains.inputCatalog = auth.Catalog
	scope.routineAuth = auth
	scope.routineDependencies = b.dependencies
	for _, usage := range b.parameters.Types {
		if sqltype.ContainsDomainConstraints(usage.Type) {
			evaluation.Input.DomainChecks = &evaluation
			break
		}
	}
	if count != 0 || scope.protocolOrZero().bind != nil {
		if importFrame == nil {
			e.mu.Unlock()
			return Result{}, fmt.Errorf("stuntgres: missing parameter importer")
		}
		// Complete each parameter/member input before decoding the next one.
		// CHECK recipes use this captured authority and run without Engine.mu.
		captured := auth.Catalog
		e.mu.Unlock()
		err = importFrame(ctx, b.parameters, canonical, evaluation.Input)
		e.mu.Lock()
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		if err = scope.validateLockedRelation(e); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		auth, err = e.statementAuthorizationLocked(op, stmt)
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		scope.routineAuth = auth
		if err := typedef.ValidateCapturedInputBinding(captured, auth.Catalog, b.dependencies); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		for i, v := range canonical {
			if v.Type() != b.parameters.Types[i].Type {
				e.mu.Unlock()
				return Result{}, fmt.Errorf("stuntgres: invalid parameter frame")
			}
		}
	}
	if p := scope.protocolOrZero().bind; p != nil {
		p.values = canonical
		if b.returning != nil {
			p.columns = b.returning.schema
		}
		if b.query != nil && !b.commandQuery {
			p.columns = b.query.Root.Schema()
		}
		if !protocolPlainSelect(stmt) {
			e.mu.Unlock()
			return Result{}, nil
		}
	}
	if b.insert.conflict != nil && b.insert.conflict.update != nil {
		if boundary := partitionWriteExtensionBoundary(stmt, b.physicalBinding().relation); boundary != nil {
			// Hierarchies have no conflict-update executor. Complete analysis
			// and target/source authority before rejecting that capability,
			// without demanding arbiters, defaults or routed row effects.
			if err := auth.checkBinding(b, stmt); err != nil {
				e.mu.Unlock()
				return Result{}, err
			}
			e.mu.Unlock()
			return Result{}, boundary
		}
	}
	if relation := bindingRuleRelation(b); relation != nil && len(rulesOf(relation)) != 0 {
		return e.executeRuleProgramLocked(scope, stmt, b, auth, canonical, candidate, quoteCapturesChanged)
	}
	return e.executeBoundLocked(scope, stmt, b, auth, canonical, candidate, quoteCapturesChanged)
}

// The caller owns Engine.mu. Every return releases it. Rule programs reuse this
// executor without importing parameters again or opening another statement.
func (e *Engine) executeBoundLocked(scope *StatementScope, stmt parser.Statement, b binding, auth AuthorizationView, canonical []sqltype.Value, candidate StatementDescription, quoteCapturesChanged bool) (Result, error) {
	op := scope.op
	ctx := op.Context()
	evaluation := EvalContext{evalStatement: evalStatement{Context: ctx, Parameters: canonical, Host: hostOf(scope), Catalog: auth.Catalog, Input: scope.input, Output: scope.output}}
	var err error
	var creationStage *CatalogStage
	var creationDescriptor *catalog.Relation
	if creation, ok := stmt.(*parser.CreateQueryTable); ok {
		// CTAS is a utility after canonical analysis/import. NoData and an
		// existing destination skip the internal SELECT planner entirely.
		if scope.protocolOrZero().simple && !scope.protocolOrZero().simpleCreated {
			e.mu.Unlock()
			if err := scope.beginProtocolSimplePortal(); err != nil {
				return Result{}, err
			}
			e.mu.Lock()
		}
		var skipped bool
		creationStage, creationDescriptor, skipped, err = e.prepareQueryTableLocked(op, auth, creation, b)
		if err != nil || skipped {
			e.mu.Unlock()
			if err == nil {
				return Result{CommandTag: queryCreationUtilityTag(creation.Form)}, nil
			}
			return Result{}, err
		}
		defer e.DiscardCatalogStage(creationStage)
		if creation.NoData {
			e.mu.Unlock()
			err = e.publishQueryTable(scope, creationStage)
			return Result{}, err
		}
		if creation.Form == parser.CreateMaterializedView {
			op.replay().materializedPopulation = true
			defer func() { op.replay().materializedPopulation = false }()
		}
	}
	// Native has no cost-based custom/generic planner. Parameterized consumers
	// fold into a fresh operation-local group, including stored origins.
	quoteCaptures := quoteCaptureCandidate{}
	if b.parameters.Count == 0 {
		quoteCaptures = quoteCaptureCandidate{Entries: candidate.quotes, Private: quoteCapturesChanged}
		pruneQuoteCaptures(&quoteCaptures, auth.Catalog)
	}
	evaluation.QuoteCaptures = &quoteCaptures
	// Planning-owned physical conversions and CHECK recipe initialization
	// precede executor ACLs, without Engine.mu or a data view. Checked
	// domain truth remains runtime consumer demand, not a folded value.
	return e.executeCTECommandsLocked(scope, stmt, b, auth, canonical, evaluation, creationStage, creationDescriptor)
}

// A complete projected row is constrained before another identity source row
// is demanded. This does not publish rows: the caller still stages one batch.
func (e *Engine) validateInsertRow(scope *StatementScope, b binding, schema string, row []sqltype.Value, evaluation *EvalContext) error {
	if scope.partitionInsert != nil {
		return scope.validatePartitionInsert(b, row, evaluation)
	}
	return e.validatePhysicalInsertRow(scope, b, &b, schema, row, evaluation)
}

// Physical constraints own evaluation and error identity; the original named
// target owns disclosure authority. The named binding is read-only.
func (e *Engine) validatePhysicalInsertRow(scope *StatementScope, b binding, named *binding, schema string, row []sqltype.Value, evaluation *EvalContext) error {
	if err := evaluation.Context.Err(); err != nil {
		return err
	}
	if err := evaluateGenerations(evaluation.Context, b.relation, row, evaluation); err != nil {
		return err
	}
	if err := access.RowSecurityProposal(scope.rlsProposal, scope.rlsProposalTable); err != nil {
		return err
	}
	return validatePhysicalRowConstraints(b.relation, schema, row, evaluation, func() (string, error) { return e.rowConstraintDetail(scope, named, row) })
}

// Shared by ordinary insertion and isolated native replacement. Validation does
// not mutate row values; each caller supplies its existing disclosure authority.
func validatePhysicalRowConstraints(relation *catalog.Relation, schema string, row []sqltype.Value, evaluation *EvalContext, detail func() (string, error)) error {
	for j, column := range relation.Columns {
		if column.Nullable {
			continue
		}
		value, err := logicalColumnValue(relation, j, row, evaluation)
		if err != nil {
			return err
		}
		if value.IsNull() {
			detail, err := detail()
			if err != nil {
				return err
			}
			return notNullDetailError(schema, relation.Name(), column.Name, detail)
		}
	}
	if len(relation.Constraints.Check) != 0 {
		constraint, err := validateCheckRow(relation, row, evaluation)
		if err != nil {
			return err
		}
		if constraint != nil {
			detail, err := detail()
			if err != nil {
				return err
			}
			return checkDetailError(schema, relation.Name(), constraint.Name, detail)
		}
	}
	return nil
}
