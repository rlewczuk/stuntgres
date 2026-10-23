package engine

import (
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/engine/view"
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
	if !op.CommandAdmission {
		op.SnapshotNoticeThreshold()
	}
	ctx := op.Context()
	switch stmt.(type) {
	case *parser.CreateFunction, *parser.CreateAggregate:
		return e.executeSQLRoutineDDL(scope, stmt, count)
	case *parser.Listen, *parser.Unlisten, *parser.Notify:
		return scope.executeNotification(stmt, count)
	case *parser.ForeignDDL:
		result, err := e.executeForeignDDL(scope, stmt.(*parser.ForeignDDL), count)
		if err == nil {
			scope.completed = true
		}
		return result, err
	case *parser.CreateDatabaseStmt:
		return e.executeCreateDatabase(scope, stmt.(*parser.CreateDatabaseStmt), count)
	case *parser.CreateTablespace:
		return e.executeCreateTablespace(op, stmt.(*parser.CreateTablespace), count)
	case *parser.Load:
		return e.executeLoad(op, stmt.(*parser.Load), count)
	case *parser.Vacuum:
		return e.executeVacuum(scope, stmt.(*parser.Vacuum), count)
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
	case *parser.Do:
		return e.executeDO(scope, stmt.(*parser.Do), count)
	case *parser.DeclareCursor:
		declaration := stmt.(*parser.DeclareCursor)
		// A modifying WITH is an analysis error, before cursor installation
		// checks. Ordinary declarations keep their existing execution order.
		if !parser.HasModifyingCTE(declaration.Query) {
			if !scope.owner.InBlock() && !declaration.Hold {
				return Result{}, sqlError("25P01", "DECLARE CURSOR can only be used in transaction blocks")
			}
			name := declaration.Name.Text
			if payloadOf(op.Session).cursors.entries[name] != nil || protocolPortalExists(op.Session, name) {
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
		domain := stmt.(*parser.AlterDomain)
		var result Result
		var err error
		if domain.Action == parser.DomainRename || domain.Action == parser.DomainSetSchema {
			result, err = e.executeSchema(scope, op, stmt, count, scope.input)
		} else {
			result, err = e.executeAlterDomain(scope, domain, count)
		}
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
	case *parser.CreateSchema, *parser.CreateLanguage, *parser.AlterSchema, *parser.DropSchema, *parser.CreateComposite, *parser.AlterType, *parser.DropType, *parser.CreateDomain, *parser.DropDomain, *parser.CreateCollation, *parser.AlterCollation, *parser.DropCollation, *parser.CreateConversion, *parser.AlterConversion, *parser.DropConversion:
		result, err := e.executeSchema(scope, op, stmt, count, scope.input)
		if err == nil {
			scope.completed = true
		}
		return result, err
	case *parser.CreateAccessMethod, *parser.DropAccessMethod:
		result, err := e.executeSchema(scope, op, stmt, count, scope.input)
		if err == nil {
			scope.completed = true
		}
		return result, err
	case *parser.CreateCast, *parser.DropCast, *parser.CreateOperator, *parser.AlterOperator, *parser.DropOperator, *parser.DropRoutine, *parser.CreateOperatorFamily, *parser.AlterOperatorFamily, *parser.DropOperatorFamily, *parser.CreateOperatorClass, *parser.AlterOperatorClass, *parser.DropOperatorClass:
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
		if grant, ok := stmt.(*parser.ObjectGrant); ok && session.ForeignGrantKind(grant.Kind) {
			return e.executeForeignGrant(scope, grant, count)
		}
		if grant, ok := stmt.(*parser.ObjectGrant); ok && grant.Kind == "database" {
			return e.executeDatabaseGrant(scope, grant, count)
		}
		result, err := e.executeRole(op, stmt, count, !scope.owner.InBlock())
		if err == nil {
			scope.completed = true
		}
		return result, err
	case *parser.Set, *parser.Show, *parser.Reset:
		result, err := e.executeSetting(op, stmt, count, !scope.owner.InBlock())
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
	if _, inserting := stmt.(*parser.Insert); inserting && operationReplayOrZero(op).insertAuthority {
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
	replan := scope.prepared.invalid || expected != nil && expected.planOwner == op.Session.SessionToken && expected.planGeneration != op.Session.DiscardPlansGeneration
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
		if replan {
			expected.retireAnalysisTypeCasts()
		}
		valid, err = e.sequenceAnalysisValidLocked(auth, expected.sequences)
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		candidate = *expected
		if expected.Parameters.Count != 0 {
			// Parameterized executions never borrow a retained quote group.
			candidate.quotes = nil
		} else if expected.quoteGeneration != op.Session.DiscardPlansGeneration {
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
			expected.regtypes.Invalidate()
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
				quotes := make([]exec.QuoteConstant, 0, len(candidate.quotes)-1)
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
	if scope.sqlRoutine.terminal {
		scope.sqlRoutine.resultPlan, err = validateSQLRoutineTerminal(stmt, b, scope.sqlRoutine.result)
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
	}
	// Preserve the raw caller's capture group before attaching stored recipes.
	// Rewrite metadata must not become caller-owned native lifetime provenance.
	callerSequences, callerRegtypes := b.Prepared.SequenceConstants, b.Prepared.RegtypeConstants
	if b.ViewWrite != nil {
		e.mu.Unlock()
		if view.ViewWriteOf(b.ViewWrite).BaseRelation.RelationKind != catalog.ViewRelation {
			if err := scope.acquireViewWriteTarget(view.ViewWriteOf(b.ViewWrite)); err != nil {
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
		b.Storage = scope.lockedStorage
		if err := e.attachViewWriteSourcesLocked(scope, &b); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
	}
	if err := e.attachCTEViewTargetsLocked(scope, &b, true); err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	if b.Resolvers.Regclass != nil {
		scope.input.Regclass = b.Resolvers.Regclass
	}
	if b.Resolvers.Regtype != nil {
		scope.input.Regtype = b.Resolvers.Regtype
	}
	if b.Resolvers.Regrole != nil {
		scope.input.Regrole = b.Resolvers.Regrole
	}
	if b.Resolvers.Operator != nil {
		scope.input.Operator = b.Resolvers.Operator
	}
	if b.Resolvers.Regproc != nil {
		scope.input.Regproc = b.Resolvers.Regproc
	}
	if b.Resolvers.Regcollation != nil {
		scope.input.Regcollation = b.Resolvers.Regcollation
	}
	if b.Resolvers.Regnamespace != nil {
		scope.input.Regnamespace = b.Resolvers.Regnamespace
	}
	if b.Resolvers.RegTS != nil {
		scope.input.RegTS = b.Resolvers.RegTS
	}
	if b.Resolvers.ACLItem != nil {
		scope.input.ACLItem = b.Resolvers.ACLItem
	}
	if b.Resolvers.FTS != nil {
		scope.input.FTS = b.Resolvers.FTS
	}
	if b.Resolvers.TypeDiagnosticName != nil {
		scope.input.TypeDiagnosticName = b.Resolvers.TypeDiagnosticName
	}
	scope.input.EnumMemberSafe = b.Resolvers.EnumMemberSafe
	if err := scope.captureFormalInputLocked(auth, b.Parameters.Types); err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	if err := scope.validateQuerySources(b.QuerySources); err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	if err := view.NormalizeRuleCTEsLocked(e.catalog.owner(), auth, stmt, &b); err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	if scope.Expected != nil {
		var results []catalog.Column
		if b.Query != nil && !b.CommandQuery {
			results = b.Query.Root.Schema()
		}
		if b.Returning != nil {
			results = b.Returning.Schema
		}
		if scope.sqlRoutine.definition != nil {
			// The caller freezes the declared scalar output. A SQL body's raw
			// output may change through a valid assignment conversion on reanalysis.
			results = scope.Expected.Results
		}
		if err := checkStatementSignature(*scope.Expected, StatementDescription{Parameters: b.Parameters, Results: results, catalogOwner: auth.Catalog.Owner(), catalogEpoch: auth.Catalog.Epoch()}); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		if err = sequence.PinReplacement(auth.Catalog.Epoch(), auth.session.SessionToken, scope.Expected.sequences, callerSequences); err != nil {
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
				scope.Expected.temporals = b.Prepared.Temporals
				scope.Expected.types = analyze.PublishDomainCasts(scope.Expected.types, b.Prepared.Types)
				if preparableBody(stmt) {
					scope.Expected.analysis = prepared
				}
				scope.Expected.planGeneration = op.Session.DiscardPlansGeneration
			}
		}
	}
	if count != b.Parameters.Count {
		e.mu.Unlock()
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want %d", count, b.Parameters.Count)
	}
	canonical := make([]sqltype.Value, count)
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, Parameters: canonical, Host: hostOf(scope), Catalog: auth.Catalog, Input: scope.input, Output: scope.output}}
	scope.domains.inputCatalog = auth.Catalog
	scope.routineAuth = auth
	scope.routineDependencies = b.Dependencies
	for _, usage := range b.Parameters.Types {
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
		err = importFrame(ctx, b.Parameters, canonical, evaluation.Input)
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
		if err := typedef.ValidateCapturedInputBinding(captured, auth.Catalog, b.Dependencies); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		for i, v := range canonical {
			if v.Type() != b.Parameters.Types[i].Type {
				e.mu.Unlock()
				return Result{}, fmt.Errorf("stuntgres: invalid parameter frame")
			}
		}
	}
	if p := scope.protocolOrZero().bind; p != nil {
		p.values = canonical
		if b.Returning != nil {
			p.columns = b.Returning.Schema
		}
		if b.Query != nil && !b.CommandQuery {
			p.columns = b.Query.Root.Schema()
		}
		if !protocolPlainSelect(stmt) {
			e.mu.Unlock()
			return Result{}, nil
		}
	}
	if b.Insert.Conflict != nil && b.Insert.Conflict.Update != nil {
		if boundary := partition.WriteExtensionBoundary(stmt, view.PhysicalBinding(b).Relation); boundary != nil {
			// Hierarchies have no conflict-update executor. Complete analysis
			// and target/source authority before rejecting that capability,
			// without demanding arbiters, defaults or routed row effects.
			if err := analyze.CheckBinding(auth, b, stmt); err != nil {
				e.mu.Unlock()
				return Result{}, err
			}
			e.mu.Unlock()
			return Result{}, boundary
		}
	}
	if relation := view.BindingRuleRelation(b); relation != nil && len(stored.RulesOf(relation)) != 0 {
		return e.executeRuleProgramLocked(scope, stmt, b, auth, canonical, candidate, quoteCapturesChanged)
	}
	return e.executeBoundLocked(scope, stmt, b, auth, canonical, candidate, quoteCapturesChanged)
}

// The caller owns Engine.mu. Every return releases it. Rule programs reuse this
// executor without importing parameters again or opening another statement.
func (e *Engine) executeBoundLocked(scope *StatementScope, stmt parser.Statement, b analyze.Binding, auth AuthorizationView, canonical []sqltype.Value, candidate StatementDescription, quoteCapturesChanged bool) (Result, error) {
	op := scope.op
	ctx := op.Context()
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, Parameters: canonical, Host: hostOf(scope), Catalog: auth.Catalog, Input: scope.input, Output: scope.output}}
	if scope.sqlRoutine.binding != nil {
		evaluation.PlanStaticOperators, evaluation.SpecializeRuleParameters = true, true
	}
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
				return Result{CommandTag: session.QueryCreationUtilityTag(creation.Form)}, nil
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
			operationReplay(op).materializedPopulation = true
			defer func() { operationReplay(op).materializedPopulation = false }()
		}
	}
	// Native has no cost-based custom/generic planner. Parameterized consumers
	// fold into a fresh operation-local group, including stored origins.
	quoteCaptures := exec.QuoteCaptureCandidate{}
	if b.Parameters.Count == 0 {
		quoteCaptures = exec.QuoteCaptureCandidate{Entries: candidate.quotes, Private: quoteCapturesChanged}
		stored.PruneQuoteCaptures(&quoteCaptures, auth.Catalog)
	}
	evaluation.QuoteCaptures = &quoteCaptures
	// Planning-owned physical conversions and CHECK recipe initialization
	// precede executor ACLs, without Engine.mu or a data view. Checked
	// domain truth remains runtime consumer demand, not a folded value.
	return e.executeCTECommandsLocked(scope, stmt, b, auth, canonical, evaluation, creationStage, creationDescriptor)
}
