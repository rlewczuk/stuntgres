package engine

import (
	"fmt"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (e *Engine) executeStatement(scope *StatementScope, stmt parser.Statement, count int, importFrame FrameImporter) (Result, error) {
	op := scope.op
	if err := e.validateOperation(op); err != nil {
		return Result{}, err
	}
	captureEnumOperationSafety(op, &scope.input)
	if !op.commandAdmission {
		op.snapshotNoticeThreshold()
	}
	ctx := op.Context()
	switch stmt.(type) {
	case *parser.Truncate:
		return Result{}, scope.stageTruncate()
	case *parser.CreateExtension:
		return e.executeExtension(scope, stmt.(*parser.CreateExtension), count)
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
		if !scope.owner.inBlock() && !stmt.(*parser.DeclareCursor).Hold {
			return Result{}, sqlError("25P01", "DECLARE CURSOR can only be used in transaction blocks")
		}
		name := stmt.(*parser.DeclareCursor).Name.Text
		if op.session.cursors.entries[name] != nil {
			return Result{}, sqlError("42P03", fmt.Sprintf("cursor %q already exists", name))
		}
	case *parser.CreatePolicy, *parser.AlterPolicy, *parser.DropPolicy:
		return e.executePolicy(scope, stmt, count)
	case *parser.CreateIndex:
		return e.executeCreateIndex(scope, stmt.(*parser.CreateIndex), count)
	case *parser.DropIndex, *parser.AlterIndex, *parser.Reindex:
		return e.executeIndexMaintenance(scope, stmt, count)
	case *parser.CreateView:
		return e.executeCreateView(scope, stmt.(*parser.CreateView), count)
	case *parser.AlterView:
		return e.executeAlterView(scope, stmt.(*parser.AlterView), count)
	case *parser.DropView:
		return e.executeDropView(scope, stmt.(*parser.DropView), count)
	case *parser.CreateSequence, *parser.AlterSequence, *parser.DropSequence:
		return e.executeSequenceDDL(scope, stmt, count)
	case *parser.CreateEnum, *parser.AlterEnum:
		return e.executeEnumDDL(scope, stmt, count)
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
	case *parser.CreateSchema, *parser.AlterSchema, *parser.DropSchema, *parser.CreateComposite, *parser.DropComposite, *parser.CreateDomain, *parser.DropDomain, *parser.CreateCollation, *parser.AlterCollation, *parser.DropCollation:
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
	// Trusted Restore insertion elevates permissions, not session identity.
	// Defaults and CHECK providers must observe the actual owner directive.
	identityAuth := auth
	if _, inserting := stmt.(*parser.Insert); inserting && op.restoreInsertAuthority {
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
	case *parser.RefreshMaterializedView:
		return e.refreshMaterializedLocked(scope, auth, s, count)
	case *parser.AlterTableOf:
		return e.alterTableOfLocked(scope, op, auth, s, count)
	case *parser.AlterTableRenameConstraint:
		return e.renameConstraintLocked(scope, op, auth, s, count)
	case *parser.AlterTableSetSchema:
		return e.alterSchemaLocked(scope, op, auth, s, count)
	case *parser.AlterTableRenameColumn:
		return e.renameRelationLocked(scope, op, auth, s.Name, s.Column.Text, s.NewName.Text, s.IfExists, true, false, count)
	case *parser.AlterTableRenameTable:
		return e.renameRelationLocked(scope, op, auth, s.Name, "", s.NewName.Text, s.IfExists, false, false, count)
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
	replan := scope.preparedInvalid || expected != nil && expected.planOwner == op.session.token && expected.planGeneration != op.session.discardPlansGeneration
	if expected != nil && expected.analysis != nil {
		path, er := e.sequenceEnvironmentLocked(auth)
		if er != nil {
			e.mu.Unlock()
			return Result{}, er
		}
		// Evolution during a wait can invalidate captures, but admission has
		// already fixed this attempt's identities. Never resolve them again.
		current := e.preparedAnalysisValidLocked(auth, expected.analysis, path)
		if !current && !scope.preparedInvalid {
			scope.preparedChangedDuringAdmission = true
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
			candidate.regtypes = nil
		}
		if replan {
			candidate.refreshTemporals = true
			candidate.sequences, candidate.regtypes = nil, nil
			// Analysis changes retire statement-root captures, not unchanged
			// stored definitions. DISCARD already cleared the complete group.
			for i, saved := range candidate.quotes {
				if saved.origin.owner != (ObjectRef{}) {
					continue
				}
				quotes := make([]quoteConstant, 0, len(candidate.quotes)-1)
				quotes = append(quotes, candidate.quotes[:i]...)
				for _, remaining := range candidate.quotes[i+1:] {
					if remaining.origin.owner != (ObjectRef{}) {
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
	b, err := e.describe(ctx, stmt, auth, scope.lockedRef, scope.input, op, expected, &scope.queryLockAdmission)
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	if b.viewWrite != nil {
		e.mu.Unlock()
		if err := scope.acquireViewWriteTarget(b.viewWrite); err != nil {
			return Result{}, err
		}
		e.mu.Lock()
		auth, err = e.statementAuthorizationLocked(op, stmt)
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
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
	if b.regclassInput != nil {
		scope.input.Regclass = b.regclassInput
	}
	if b.regtypeInput != nil {
		scope.input.Regtype = b.regtypeInput
	}
	if b.typeDiagnosticName != nil {
		scope.input.TypeDiagnosticName = b.typeDiagnosticName
	}
	scope.input.EnumMemberSafe = b.enumMemberSafe
	for _, usage := range b.parameters.Types {
		if usage.Type.ContainsRegclass() && scope.input.Regclass == nil {
			scope.input.Regclass, err = e.captureRegclassInput(auth)
			if err != nil {
				e.mu.Unlock()
				return Result{}, err
			}
		}
		if usage.Type.ContainsRegtype() && scope.input.Regtype == nil {
			scope.input.Regtype, err = e.captureRegtypeInput(auth, op)
			if err != nil {
				e.mu.Unlock()
				return Result{}, err
			}
		}
	}
	if err := scope.validateQuerySources(b.querySources); err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	if scope.Expected != nil {
		var results []ColumnDescriptor
		if b.query != nil && !b.commandQuery {
			results = b.query.Root.Schema()
		}
		if b.returning != nil {
			results = b.returning.schema
		}
		if err := checkStatementSignature(*scope.Expected, StatementDescription{Parameters: b.parameters, Results: results, catalogOwner: auth.catalog.owner, catalogEpoch: auth.catalog.Epoch()}); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		if err = e.pinSequenceReplacementLocked(auth, scope.Expected.sequences, b.sequenceConstants); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		if !valid || scope.Expected.regtypes != nil && candidate.regtypes == nil {
			analysis, er := e.sequenceAnalysisLocked(auth, b.sequenceConstants)
			if er != nil {
				e.mu.Unlock()
				return Result{}, er
			}
			regtypes, er := e.regtypeAnalysisLocked(auth, b.regtypeConstants)
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
			} else if len(analysis.constants) != 0 {
				scope.Expected.sequences = &analysis
			}
			if scope.Expected.regtypes != nil && regtypes != nil {
				*scope.Expected.regtypes = *regtypes
			} else {
				scope.Expected.regtypes = regtypes
			}
			if replan {
				scope.Expected.temporals = b.temporals
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
	if count != 0 {
		if importFrame == nil {
			e.mu.Unlock()
			return Result{}, fmt.Errorf("stuntgres: missing parameter importer")
		}
		if err = importFrame(ctx, b.parameters, canonical, scope.input); err != nil {
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
	if b.conflict != nil && b.conflict.update != nil {
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
	var creationStage *CatalogStage
	var creationDescriptor *relationDescriptor
	if creation, ok := stmt.(*parser.CreateQueryTable); ok {
		var skipped bool
		creationStage, creationDescriptor, skipped, err = e.prepareQueryTableLocked(op, auth, creation, b)
		if err != nil || skipped {
			e.mu.Unlock()
			return Result{}, err
		}
		defer e.DiscardCatalogStage(creationStage)
		if creation.NoData {
			e.mu.Unlock()
			err = e.publishQueryTable(scope, creationStage)
			return Result{}, err
		}
		if creation.Form == parser.CreateMaterializedView {
			op.materializedPopulation = true
			defer func() { op.materializedPopulation = false }()
		}
	}
	expressions := b.expressions
	expressionsChanged := false
	evaluation := EvalContext{Context: ctx, Parameters: canonical, statementScope: scope, catalog: auth.catalog, Input: scope.input, Output: scope.output}
	// Native has no cost-based custom/generic planner. Parameterized consumers
	// fold into a fresh operation-local group, including stored origins.
	quoteCaptures := quoteCaptureCandidate{}
	if b.parameters.Count == 0 {
		quoteCaptures = quoteCaptureCandidate{entries: candidate.quotes, private: quoteCapturesChanged}
		quoteCaptures.prune(auth.catalog)
	}
	evaluation.quoteCaptures = &quoteCaptures
	_, inserting := stmt.(*parser.Insert)
	var queryRoot LogicalNode
	_, deleting := stmt.(*parser.Delete)
	if inserting || b.merge != nil || b.update != nil || b.query != nil || (deleting && (b.deleteUsing != nil || b.predicate != nil || b.returning != nil)) || len(b.defaults) != 0 {
		// Planning-owned conversions precede executor ACLs, without retaining
		// Engine.mu or acquiring a data view. Analysis/namespace checks remain
		// earlier, and only this execution's imported frame is materialized.
		e.mu.Unlock()
		if b.conflict != nil {
			if b.conflict.predicate != nil {
				b.conflict.predicate, err = normalizeConflictPredicate(&evaluation, b.conflict.predicate)
				if err != nil {
					return Result{}, err
				}
			}
			if err := b.conflict.selectArbiters(&evaluation, b.physicalBinding().relation, auth.catalog); err != nil {
				return Result{}, err
			}
			scope.insertConflict = b.conflict
			if err := b.conflict.preflightUpdate(&evaluation); err != nil {
				return Result{}, err
			}
		}
		if inserting {
			b.arrayTargets, err = preflightArrayTargets(&evaluation, b.arrayTargets)
			if err != nil {
				return Result{}, err
			}
			// Supplied values retain priority over defaults and executor ACLs.
			// Constant planning and frame conversions both run unlocked; only
			// changed rows and the outer slice need execution-private copies.
			destination, destinationTargets := b.relation, b.targets
			if b.viewWrite != nil {
				destination, destinationTargets = b.viewWrite.logical.relation, b.viewWrite.logical.targets
			}
			for i, row := range b.expressions {
				var privateRow []BoundExpr
				for j, expression := range row {
					if len(b.expressions) == 1 && len(b.identityDiscard) != 0 && b.identityDiscard[b.targets[j]] {
						continue
					}
					folded, changed, foldErr := preflightCoercions(&evaluation, expression)
					if foldErr != nil {
						return Result{}, insertValueError(auth.catalog, foldErr, destination.columns[destinationTargets[j]], stmt.(*parser.Insert).Rows[i][j], nil)
					}
					if changed {
						if privateRow == nil {
							privateRow = append([]BoundExpr(nil), row...)
						}
						privateRow[j] = folded
					}
				}
				if privateRow != nil {
					if !expressionsChanged {
						expressions = append([][]BoundExpr(nil), b.expressions...)
						expressionsChanged = true
					}
					expressions[i] = privateRow
				}
			}
		}
		if b.merge != nil {
			err = preflightMerge(&evaluation, b.merge, auth.catalog)
		} else if b.update != nil {
			b.update, err = preflightUpdate(&evaluation, b.update)
			if err == nil && b.update.from != nil {
				err = validateQueryJoinExecution(&evaluation, b.update.from, auth.catalog, 0)
			}
		} else if b.query != nil {
			queryRoot, err = preflightQuery(&evaluation, b.query.Root)
			if err == nil {
				err = validateQueryJoinExecution(&evaluation, queryRoot, auth.catalog, 0)
			}
		} else if deleting {
			if b.deleteUsing != nil {
				b.deleteUsing, err = preflightQuery(&evaluation, b.deleteUsing)
				if err == nil {
					err = validateQueryJoinExecution(&evaluation, b.deleteUsing, auth.catalog, 0)
				}
			}
			if err == nil && b.predicate != nil {
				b.predicate, _, err = preflightCoercions(&evaluation, b.predicate)
			}
		}
		if err == nil && b.returning != nil {
			b.returning, err = preflightReturning(&evaluation, b.returning)
		}
		if err == nil && b.subqueryDemand && (inserting || b.update != nil || deleting) {
			err = validateMutationJoinExecution(&evaluation, &b, expressions, auth.catalog)
		}
		if err != nil {
			return Result{}, err
		}
		if b.viewWrite != nil {
			var checks []viewCheckGroup
			for i, group := range b.viewWrite.checks {
				var predicates []BoundExpr
				for j, expression := range group.predicates {
					folded, changed, foldErr := preflightCoercions(&evaluation, expression)
					if foldErr != nil {
						return Result{}, foldErr
					}
					if changed {
						if predicates == nil {
							predicates = append([]BoundExpr(nil), group.predicates...)
						}
						predicates[j] = folded
					}
				}
				if predicates != nil {
					if checks == nil {
						checks = append([]viewCheckGroup(nil), b.viewWrite.checks...)
					}
					checks[i].predicates = predicates
				}
			}
			if checks != nil {
				b.viewWrite.checks = checks
			}
		}
		// Both INSERT paths plan only defaults this binding actually consumes.
		// Demand immutable conversions even for an empty query source, while
		// keeping the stored definition and any runtime-only producer unfolded.
		var privateDefaults []BoundExpr
		for i, expression := range b.defaults {
			if expression == nil {
				continue
			}
			defaultEvaluation := evaluation
			physical := b.physicalBinding()
			attribute := physical.relation.attributes[i]
			defaultEvaluation.quoteOrigin = quoteOrigin{owner: physical.ref, attribute: attribute, definition: physical.relation.defaults[attribute].OID}
			folded, changed, foldErr := preflightCoercionsWithPolicy(&defaultEvaluation, expression, true)
			if foldErr != nil {
				return Result{}, foldErr
			}
			if changed {
				if privateDefaults == nil {
					privateDefaults = append([]BoundExpr(nil), b.defaults...)
				}
				privateDefaults[i] = folded
			}
		}
		if privateDefaults != nil {
			b.defaults = privateDefaults
		}
		e.mu.Lock()
		if err = ctx.Err(); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		if err = scope.validateLockedRelation(e); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		// Do not authorize source capture with the pre-unlock authority view.
		// Keep the original binding's actual-column manifest and exact ref.
		auth, err = e.statementAuthorizationLocked(op, stmt)
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
	}
	switch stmt.(type) {
	case *parser.Insert, *parser.Update, *parser.Delete:
		if b.viewWrite == nil && b.relation.relationKind != materializedViewRelation {
			if err := tableKind(b.relation, "change"); err != nil {
				e.mu.Unlock()
				return Result{}, err
			}
		}
	}
	// Released MERGE checks read-only after analysis/planning conversions, but
	// before executor column/source ACLs. Other statements keep their ordering.
	if b.merge != nil && scope.owner.readOnly {
		e.mu.Unlock()
		return Result{}, sqlError("25006", "cannot execute MERGE in a read-only transaction")
	}
	if err = auth.checkBinding(b, stmt); err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	if scope.Expected != nil && b.parameters.Count == 0 && quoteCaptures.private {
		scope.Expected.quotes = quoteCaptures.entries
		scope.Expected.quoteGeneration = op.session.discardPlansGeneration
	}
	quoteCaptures.sealed = true
	scope.quoteCaptures = &quoteCaptures
	b = b.physicalBinding()
	if err = partitionWriteExtensionBoundary(stmt, b.relation); err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	tag := modifyingTag(stmt)
	selection, selecting := stmt.(*parser.Select)
	if declaration, ok := stmt.(*parser.DeclareCursor); ok {
		selection, selecting = declaration.Query, true
		tag = modifyingTag(selection)
	}
	// PostgreSQL reports the rewritten SELECT command for a marked view,
	// including constant views without physical lock leaves. Physical table
	// rowmarks retain their original SELECT FOR ... diagnostic.
	if scope.owner.readOnly {
		if selecting {
			for _, leaf := range b.querySources {
				if leaf.mark.mode != 0 && leaf.relation.relationKind == viewRelation {
					tag = "SELECT"
					break
				}
			}
		}
	}
	if selecting && tag == "" && scope.owner.readOnly && b.query != nil {
		mode, modeErr := e.firstQueryRowModeLocked(scope, b.query.Root, b.querySources)
		if modeErr != nil {
			e.mu.Unlock()
			return Result{}, modeErr
		}
		if mode != 0 {
			tag = "SELECT"
		}
	}
	if scope.owner.readOnly && tag != "" && !temporaryDataTarget(auth, &b, stmt) {
		e.mu.Unlock()
		return Result{}, sqlError("25006", "cannot execute "+tag+" in a read-only transaction")
	}
	if b.relation != nil && b.relation.relationKind == materializedViewRelation {
		switch stmt.(type) {
		case *parser.Insert, *parser.Update, *parser.Delete:
			e.mu.Unlock()
			return Result{}, tableKind(b.relation, "change")
		}
	}
	if b.query != nil || b.subqueryDemand || len(b.querySources) != 0 {
		if err = e.initializeQueryLocksLocked(scope, queryRoot, b.querySources); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
	}
	scope.visibilityDemand = scope.visibilityDemand || b.visibilitySnapshot
	if b.query != nil {
		scope.visibilityDemand = scope.visibilityDemand || b.query.visibilitySnapshot
	}
	if b.returning != nil {
		scope.visibilityDemand = scope.visibilityDemand || b.returning.visibilitySnapshot
	}
	if scope.visibilityDemand {
		err = e.captureVisibilitySnapshotLocked(scope)
	}
	if err == nil && b.relation != nil {
		err = e.captureCheckVisibilitySnapshotLocked(scope, b.relation)
	}
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	if b.regclassConcat && b.query == nil {
		if err = e.captureCommentSnapshotLocked(scope); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
	}
	if err := e.configureRowSecurityLocked(scope, b, stmt); err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	switch s := stmt.(type) {
	case *parser.Merge:
		if err := e.captureCheckCommentSnapshotLocked(scope, b.relation); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		refs, err := e.queryTableRefsLocked(scope.inheritanceRead(auth.catalog), b.querySources, []ObjectRef{b.ref}, scope.op.session.token)
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		view, err := scope.acquireDataViewsLocked(e, refs)
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		var target *querySource
		if scope.rlsTargetDenied {
			target, err = captureDeniedQuerySource(view, b.ref)
		} else {
			target, err = captureQuerySource(ctx, view, b.ref)
		}
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		inputs, err := e.captureQuerySourcesLocked(scope, b.querySources, false)
		if err != nil {
			target.close()
			e.mu.Unlock()
			return Result{}, err
		}
		schema := auth.catalog.state.descriptors[b.relation.namespace].Name()
		e.mu.Unlock()
		return e.executeMerge(ctx, scope, b, target, inputs, canonical, schema)
	case *parser.Update:
		err = e.captureCheckCommentSnapshotLocked(scope, b.relation)
		if err == nil && b.returning != nil {
			err = e.captureCommentSnapshotLocked(scope)
		}
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		var view *VisibilityView
		if len(b.querySources) != 0 {
			refs, collectErr := e.queryTableRefsLocked(scope.inheritanceRead(auth.catalog), b.querySources, []ObjectRef{b.ref}, scope.op.session.token)
			if collectErr != nil {
				e.mu.Unlock()
				return Result{}, collectErr
			}
			view, err = scope.acquireDataViewsLocked(e, refs)
		} else {
			view, err = scope.acquireDataViewLocked(e, b.ref)
		}
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		var source *querySource
		if scope.rlsTargetDenied {
			source, err = captureDeniedQuerySource(view, b.ref)
		} else {
			source, err = captureQuerySource(ctx, view, b.ref)
		}
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		var from *querySource
		if b.update.from != nil || b.subqueryDemand {
			from, err = e.captureQuerySourcesLocked(scope, b.querySources, false)
			if err != nil {
				source.close()
				e.mu.Unlock()
				return Result{}, err
			}
		}
		schema := auth.catalog.state.descriptors[b.relation.namespace].Name()
		e.mu.Unlock()
		return e.executeUpdate(ctx, scope, b, source, from, canonical, schema)
	case *parser.Insert:
		if err := e.captureCheckCommentSnapshotLocked(scope, b.relation); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		if err := scope.capturePartitionInsertLocked(e, auth, b.relation); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		scope.beginInsertReturning(b)
		keepReturning := false
		if scope.insertReturning != nil {
			defer func() {
				if !keepReturning {
					scope.insertReturning.rows.Close()
				}
			}()
		}
		if b.commandQuery {
			source, sourceErr := e.captureQuerySourcesLocked(scope, b.querySources, len(b.querySources) == 0)
			if sourceErr != nil {
				e.mu.Unlock()
				return Result{}, sourceErr
			}
			schema := auth.catalog.state.descriptors[b.relation.namespace].Name()
			e.mu.Unlock()
			if err := selectQuerySources(ctx, source); err != nil {
				return Result{}, err
			}
			if b.viewWrite != nil {
				source = freezeViewWriteSources(scope, source, b.querySources)
			}
			b.query = &BoundQuery{Root: queryRoot}
			rows, queryErr := evaluateInsertQuery(ctx, b, source, canonical, schema, scope.input, scope.output, scope)
			if queryErr != nil {
				return Result{}, queryErr
			}
			if err := ctx.Err(); err != nil {
				return Result{}, err
			}
			if scope.insertReturning != nil {
				result, err := scope.finishInsertReturning()
				keepReturning = err == nil
				return result, err
			}
			count, err := scope.applyInsertValidated(b.ref, rows)
			if err != nil {
				return Result{}, err
			}
			return Result{Count: int64(count)}, nil
		}
		if b.subqueryDemand {
			inputs, sourceErr := e.captureQuerySourcesLocked(scope, b.querySources, false)
			if sourceErr != nil {
				e.mu.Unlock()
				return Result{}, sourceErr
			}
			state := &queryState{source: inputs, parameters: canonical, input: scope.input, output: scope.output, locking: inputs.locks != nil}
			defer state.releaseMemberships()
			evaluation.query = state
			evaluation.Scope, evaluation.Frame = 1, 1
		}
		e.mu.Unlock()
		if evaluation.query != nil {
			if err := selectQuerySources(ctx, evaluation.query.source); err != nil {
				return Result{}, err
			}
			if b.viewWrite != nil {
				evaluation.query.source = freezeViewWriteSources(scope, evaluation.query.source, b.querySources)
			}
			defer evaluation.query.source.close()
		}
		var rows [][]sqltype.Value
		if scope.insertReturning == nil {
			rows = make([][]sqltype.Value, 0, len(s.Rows))
		}
		for i, source := range s.Rows {
			if err = ctx.Err(); err != nil {
				return Result{}, err
			}
			row := make([]sqltype.Value, len(b.relation.columns))
			for j, c := range b.relation.columns {
				row[j] = sqltype.Null(c.Usage.Type)
			}
			if len(b.insertProjection) != 0 {
				if err = evaluateIdentityValuesRow(auth.catalog, &b, expressions[i], source, row, len(s.Rows) > 1, &evaluation); err != nil {
					return Result{}, err
				}
				schema := auth.catalog.state.descriptors[b.relation.namespace].Name()
				if err = e.validateInsertRow(scope, b, schema, row, &evaluation); err != nil {
					return Result{}, err
				}
				admitted, admitErr := scope.admitInsertProducer(b, row, rows, &evaluation)
				if admitErr != nil {
					return Result{}, admitErr
				}
				if admitted {
					if err := e.checkViewRow(scope, &b, row, &evaluation); err != nil {
						return Result{}, err
					}
					rows = append(rows, row)
				}
				continue
			}
			if len(b.defaults) != 0 {
				supplied := b.expressions[i]
				for j, target := range b.targets {
					if j < len(supplied) && supplied[j] != nil && (len(b.arrayTargets) == 0 || b.arrayTargets[j] == nil) {
						row[target] = sqltype.Value{}
					}
				}
				for j, expression := range b.defaults {
					if expression != nil && row[j].Type() != nil {
						row[j], err = expression.Eval(&evaluation)
						if err != nil {
							return Result{}, err
						}
					}
				}
			}
			for j, v := range source {
				if b.expressions[i][j] == nil {
					continue
				}
				target := b.targets[j]
				c := b.relation.columns[target]
				var value sqltype.Value
				if len(b.arrayTargets) != 0 && b.arrayTargets[j] != nil {
					value, err = b.arrayTargets[j].applyExpression(&evaluation, row[target], expressions[i][j])
				} else {
					value, err = expressions[i][j].Eval(&evaluation)
				}
				if err == nil {
					row[target] = value
				}
				if err != nil {
					return Result{}, insertValueError(auth.catalog, err, c, v, nil)
				}
			}
			schema := auth.catalog.state.descriptors[b.relation.namespace].Name()
			if err = e.validateInsertRow(scope, b, schema, row, &evaluation); err != nil {
				return Result{}, err
			}
			admitted, admitErr := scope.admitInsertProducer(b, row, rows, &evaluation)
			if admitErr != nil {
				return Result{}, admitErr
			}
			if admitted {
				if err := e.checkViewRow(scope, &b, row, &evaluation); err != nil {
					return Result{}, err
				}
				rows = append(rows, row)
			}
		}
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		if scope.insertReturning != nil {
			result, err := scope.finishInsertReturning()
			keepReturning = err == nil
			return result, err
		}
		count, err := scope.applyInsertValidated(b.ref, rows)
		if err != nil {
			return Result{}, err
		}
		return Result{Count: int64(count)}, nil
	case *parser.Delete:
		var view *VisibilityView
		var viewErr error
		if len(b.querySources) != 0 {
			refs, err := e.queryTableRefsLocked(scope.inheritanceRead(auth.catalog), b.querySources, []ObjectRef{b.ref}, scope.op.session.token)
			if err != nil {
				e.mu.Unlock()
				return Result{}, err
			}
			view, viewErr = scope.acquireDataViewsLocked(e, refs)
		} else {
			view, viewErr = scope.acquireDataViewLocked(e, b.ref)
		}
		var inputs *querySource
		if viewErr == nil && (b.deleteUsing != nil || b.subqueryDemand) {
			inputs, viewErr = e.captureQuerySourcesLocked(scope, b.querySources, false)
		} else if viewErr == nil && b.returning != nil {
			viewErr = e.captureCommentSnapshotLocked(scope)
		}
		e.mu.Unlock()
		if viewErr != nil {
			view.Release()
			return Result{}, viewErr
		}
		return scope.executeDelete(b, view, inputs, canonical)
	case *parser.CreateQueryTable:
		return e.executeQueryTableLocked(scope, s, b, queryRoot, canonical, creationStage, creationDescriptor)
	case *parser.DeclareCursor:
		// Even an unlocked cursor retains physical lineage through derived/view
		// projections. Zero-mode entries capture identity, never row grants.
		if scope.queryLocks == nil {
			scope.queryLocks = &queryLockContext{identityOnly: true}
		}
		source, sourceErr := e.captureQuerySourcesLocked(scope, b.querySources, s.Query.From == nil && s.Query.Values == nil)
		e.mu.Unlock()
		if sourceErr != nil {
			return Result{}, sourceErr
		}
		if err := selectQuerySources(ctx, source); err != nil {
			source.close()
			return Result{}, err
		}
		return scope.installSQLCursor(s, queryRoot, source, canonical)
	case *parser.Select:
		source, sourceErr := e.captureQuerySourcesLocked(scope, b.querySources, s.From == nil && s.Values == nil)
		e.mu.Unlock()
		if sourceErr != nil {
			return Result{}, sourceErr
		}
		if err := selectQuerySources(ctx, source); err != nil {
			return Result{}, err
		}
		rows, count, queryErr := finalizeQuery(ctx, queryRoot, source, canonical)
		if queryErr != nil {
			return Result{}, queryErr
		}
		if b.query.regclassOutput && count != 0 {
			if err := e.captureRegclassOutput(op, rows.(*finalRows)); err != nil {
				rows.Close()
				return Result{}, err
			}
		}
		return Result{Count: int64(count), Rows: rows}, nil
	}
	e.mu.Unlock()
	return Result{}, sqlError("0A000", "unsupported statement")
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
	if err := rowSecurityProposal(scope.rlsProposal, scope.rlsProposalTable); err != nil {
		return err
	}
	for j, column := range b.relation.columns {
		if column.Nullable {
			continue
		}
		value, err := logicalColumnValue(b.relation, j, row, evaluation)
		if err != nil {
			return err
		}
		if value.IsNull() {
			detail, err := e.rowConstraintDetail(scope, named, row)
			if err != nil {
				return err
			}
			return notNullDetailError(schema, b.relation.name, column.Name, detail)
		}
	}
	if len(b.relation.checkConstraints) != 0 {
		constraint, err := validateCheckRow(b.relation, row, evaluation)
		if err != nil {
			return err
		}
		if constraint != nil {
			detail, err := e.rowConstraintDetail(scope, named, row)
			if err != nil {
				return err
			}
			return checkDetailError(schema, b.relation.name, constraint.Name, detail)
		}
	}
	return nil
}
