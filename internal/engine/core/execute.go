package core

import (
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (e *Engine) executeStatement(scope *StatementScope, stmt parser.Statement, count int, importFrame FrameImporter) (Result, error) {
	op := scope.Op
	if err := e.validateOperation(op); err != nil {
		return Result{}, err
	}
	if _, ok := stmt.(*parser.AlterSystem); ok {
		return scope.executeAlterSystem()
	}
	if err := scope.parallelExecutionEligibility(stmt); err != nil {
		return Result{}, err
	}
	if err := restrictedStatement(op, stmt); err != nil {
		return Result{}, err
	}
	if op.Restricted != nil || scope.activeSQLRoutineDepth != 0 {
		store := op.EffectiveSettings()
		store.Mu.Lock()
		scope.PlannerMethods = store.PlannerMethodsLocked()
		store.Mu.Unlock()
		if err := scope.refreshRestrictedPresentation(); err != nil {
			return Result{}, err
		}
	}
	CaptureEnumOperationSafety(op, &scope.Input)
	scope.Input.LockedDomainInput, scope.Input.DomainChecks = scope, scope
	if !op.CommandAdmission {
		op.SnapshotNoticeThreshold()
	}
	ctx := scope.executionContext()
	switch stmt.(type) {
	case *parser.Explain:
		return e.executeExplain(scope, stmt.(*parser.Explain), count, importFrame)
	case *parser.LockTable:
		// AcquireRelation already performed the actual relation-lock admission,
		// including nested routine commands. Only completion belongs here.
		return Result{}, op.Context().Err()
	case *parser.Listen, *parser.Unlisten, *parser.Notify:
		return scope.executeNotification(stmt, count)
	case *parser.Truncate:
		return Result{}, scope.stageTruncate()
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
			if !scope.Owner.InBlock() && !declaration.Hold {
				return Result{}, SQLError("25P01", "DECLARE CURSOR can only be used in transaction blocks")
			}
			name := declaration.Name.Text
			if PayloadOf(op.Session).Cursors.Entries[name] != nil || protocolPortalExists(op.Session, name) {
				return Result{}, SQLError("42P03", fmt.Sprintf("cursor %q already exists", name))
			}
		}
	case *parser.TransactionCharacteristics:
		return scope.executeCharacteristics(stmt.(*parser.TransactionCharacteristics), count)
	case *parser.Set, *parser.Show, *parser.Reset:
		result, err := e.executeSetting(ctx, op, stmt, count, !scope.Owner.InBlock())
		if err == nil {
			scope.CompleteUtility()
		}
		return result, err
	default:
		if result, handled, err := e.Statements.Execute(e, scope, stmt, count); handled {
			return result, err
		}
	}
	e.Mu.Lock()
	if err := scope.RefreshRelationGrantsLocked(e); err != nil {
		e.Mu.Unlock()
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		e.Mu.Unlock()
		return Result{}, err
	}
	auth, err := scope.statementAuthorizationLocked(stmt)
	if err != nil {
		e.Mu.Unlock()
		return Result{}, err
	}
	scope.RoutineAuth = auth
	// Trusted Restore insertion elevates permissions, not session identity.
	// Defaults and CHECK providers must observe the actual owner directive.
	identityAuth := auth
	if _, inserting := stmt.(*parser.Insert); inserting && OperationReplayOrZero(op).InsertAuthority {
		identityAuth, err = e.AuthorizationLocked(op)
		if err != nil {
			e.Mu.Unlock()
			return Result{}, err
		}
	}
	if err := scope.CaptureIdentityLocked(identityAuth); err != nil {
		e.Mu.Unlock()
		return Result{}, err
	}
	if err := captureEnumOperationInput(auth, &scope.Input); err != nil {
		e.Mu.Unlock()
		return Result{}, err
	}
	if result, handled, err := e.Statements.ExecuteLocked(e, scope, auth, stmt, count); handled {
		return result, err
	}
	expected := scope.Expected
	var candidate Description
	foldPlanning, err := e.prepareFoldAttemptLocked(scope, stmt, auth, scope.Expected)
	if err != nil {
		e.Mu.Unlock()
		return Result{}, err
	}
	valid := true
	var rebindTypes map[analyze.PreparedTypeKey]analyze.TypeOccurrence
	var rebindTemporals []analyze.TemporalConstant
	installedScalarAuthorityRebind := false
	portal := scope.protocolOrZero().execute
	acceptedPortal := portal != nil && expected == &portal.Entry.Description && portal.Folds != nil
	replan := !acceptedPortal && (scope.prepared.invalid || expected != nil && expected.PlanOwner == op.Session.SessionToken && expected.PlanGeneration != op.Session.DiscardPlansGeneration)
	if expected != nil && expected.Analysis != nil {
		path, er := e.sequenceEnvironmentLocked(auth)
		if er != nil {
			e.Mu.Unlock()
			return Result{}, er
		}
		// Evolution during a wait can invalidate captures, but admission has
		// already fixed this attempt's identities. Never resolve them again.
		current := acceptedPortal || expected.Analysis.Valid(auth.Catalog, e.preparedEnvironmentAtPathLocked(auth, path, scope.RowSecurity))
		if !current && !scope.prepared.invalid {
			scope.prepared.changedDuringAdmission = true
		}
		replan = replan || !current
		installedScalarAuthorityRebind = replan && e.preparedInstalledScalarAuthorityRebindLocked(auth, expected, path)
		rebindTemporals = e.preparedInstalledScalarLiteralTemporalsLocked(auth, expected, path)
		if replan {
			rebindTemporals = e.retirePreparedInstalledScalarLiteralTemporalsLocked(auth, expected, path, rebindTemporals)
		}
	}
	if expected != nil {
		if replan {
			rebindTypes = expected.RetireAnalysisTypeCastsForRebind(installedScalarAuthorityRebind)
		}
		valid, err = e.sequenceAnalysisValidLocked(auth, expected.Sequences)
		if err != nil {
			e.Mu.Unlock()
			return Result{}, err
		}
		candidate = *expected
		if rebindTemporals != nil {
			candidate.Temporals = rebindTemporals
		}
		if replan {
			candidate.Types = rebindTypes
		}
		candidate.FoldPlanning = foldPlanning
		if !valid {
			candidate.Sequences = nil
		}
		regtypeValid, er := e.regtypeAnalysisValidLocked(auth, expected.Regtypes)
		if er != nil {
			e.Mu.Unlock()
			return Result{}, er
		}
		if !regtypeValid || replan {
			projection, er := e.regtypeReanalysisLocked(auth, expected)
			if er != nil {
				e.Mu.Unlock()
				return Result{}, er
			}
			// Genuine alias invalidation remains sticky, but an unrelated
			// replan must not mutate a valid group's publication state.
			if !regtypeValid && expected.Regtypes != nil {
				expected.Regtypes.Invalidate()
			}
			candidate.Regtypes = projection
			valid = false
		}
		if replan {
			candidate.RefreshTemporals = true
			candidate.Sequences = nil
			valid = false
		}
		expected = &candidate
	}
	b, err := e.describeScoped(scope, stmt, auth, expected)
	if err != nil {
		e.Mu.Unlock()
		if scope.sqlRoutine.lexical != nil && scope.sqlRoutine.lexical.Program.Invocation != proc.OrdinaryInvocation {
			return Result{}, &proc.PLExpressionBindingError{Err: err}
		}
		return Result{}, err
	}
	if scope.sqlRoutine.terminal {
		scope.sqlRoutine.resultPlan, err = ValidateSQLRoutineTerminal(stmt, b, scope.sqlRoutine.result, auth, scope.sqlRoutine.definition)
		if err != nil {
			e.Mu.Unlock()
			return Result{}, err
		}
	}
	// Preserve the raw caller's capture group before attaching stored recipes.
	// Rewrite metadata must not become caller-owned native lifetime provenance.
	callerSequences, callerRegtypes := b.Prepared.SequenceConstants, b.Prepared.RegtypeConstants
	if b.ViewWrite != nil {
		e.Mu.Unlock()
		w := view.ViewWriteOf(b.ViewWrite)
		if w.Instead || w.BaseRelation.RelationKind != catalog.ViewRelation {
			if err := scope.acquireViewWriteTarget(w); err != nil {
				return Result{}, err
			}
		}
		e.Mu.Lock()
		auth, err = scope.statementAuthorizationLocked(stmt)
		if err != nil {
			e.Mu.Unlock()
			return Result{}, err
		}
		scope.RoutineAuth = auth
		if err := scope.ValidateAdmissionNamesLocked(e, auth); err != nil {
			e.Mu.Unlock()
			return Result{}, err
		}
		b.Storage = scope.LockedStorage
		if err := e.attachViewWriteSourcesLocked(scope, &b); err != nil {
			e.Mu.Unlock()
			return Result{}, err
		}
	}
	if err := e.attachCTEViewTargetsLocked(scope, &b, true); err != nil {
		e.Mu.Unlock()
		return Result{}, err
	}
	if b.Resolvers.Regclass != nil {
		scope.Input.Regclass = b.Resolvers.Regclass
	}
	if b.Resolvers.Regtype != nil {
		scope.Input.Regtype = b.Resolvers.Regtype
	}
	if b.Resolvers.Regrole != nil {
		scope.Input.Regrole = b.Resolvers.Regrole
	}
	if b.Resolvers.Operator != nil {
		scope.Input.Operator = b.Resolvers.Operator
	}
	if b.Resolvers.Regproc != nil {
		scope.Input.Regproc = b.Resolvers.Regproc
	}
	if b.Resolvers.Regcollation != nil {
		scope.Input.Regcollation = b.Resolvers.Regcollation
	}
	if b.Resolvers.Regnamespace != nil {
		scope.Input.Regnamespace = b.Resolvers.Regnamespace
	}
	if b.Resolvers.RegTS != nil {
		scope.Input.RegTS = b.Resolvers.RegTS
	}
	if b.Resolvers.ACLItem != nil {
		scope.Input.ACLItem = b.Resolvers.ACLItem
	}
	if b.Resolvers.FTS != nil {
		scope.Input.FTS = b.Resolvers.FTS
	}
	if b.Resolvers.TypeDiagnosticName != nil {
		scope.Input.TypeDiagnosticName = b.Resolvers.TypeDiagnosticName
	}
	scope.Input.EnumMemberSafe = b.Resolvers.EnumMemberSafe
	if err := scope.captureFormalInputLocked(auth, b.Parameters.Types); err != nil {
		e.Mu.Unlock()
		return Result{}, err
	}
	if err := scope.ValidateQuerySources(b.QuerySources); err != nil {
		e.Mu.Unlock()
		return Result{}, err
	}
	if err := view.NormalizeRuleCTEsLocked(&e.Catalog.Owner, auth, stmt, &b); err != nil {
		e.Mu.Unlock()
		return Result{}, err
	}
	if scope.Expected != nil {
		var results []catalog.Column
		if _, call := stmt.(*parser.Call); call {
			results = analyze.RoutineCallSchema(b)
		}
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
		if err := CheckStatementSignature(*scope.Expected, Description{Parameters: b.Parameters, Results: results, CatalogOwner: auth.Catalog.Owner(), CatalogEpoch: auth.Catalog.Epoch()}); err != nil {
			e.Mu.Unlock()
			return Result{}, err
		}
		if err = sequence.PinReplacement(auth.Catalog.Epoch(), auth.Session.SessionToken, scope.Expected.Sequences, callerSequences); err != nil {
			e.Mu.Unlock()
			return Result{}, err
		}
		if !valid || scope.Expected.Regtypes != nil && candidate.Regtypes == nil {
			analysis, er := e.sequenceAnalysisLocked(auth, callerSequences)
			if er != nil {
				e.Mu.Unlock()
				return Result{}, er
			}
			regtypes, er := e.regtypeAnalysisLocked(auth, callerRegtypes)
			if er != nil {
				e.Mu.Unlock()
				return Result{}, er
			}
			var prepared *Analysis
			if replan && session.PreparableBody(stmt) {
				prepared, er = e.preparedAnalysisLocked(scope, auth, b)
				if er != nil {
					e.Mu.Unlock()
					return Result{}, er
				}
			}
			var refreshed *Description
			if replan {
				next := Description{Temporals: b.Prepared.Temporals, Types: b.Prepared.Types, Analysis: scope.Expected.Analysis, PlanGeneration: op.Session.DiscardPlansGeneration}
				if session.PreparableBody(stmt) {
					next.Analysis = prepared
				}
				refreshed = &next
			}
			if er := scope.Expected.AcceptAnalysis(ctx, analysis, regtypes, refreshed); er != nil {
				e.Mu.Unlock()
				return Result{}, er
			}
		}
	}
	if foldPlanning != nil {
		// All admission/analysis fences have now accepted this exact root.
		// Keep its next-use analysis environment separate from the environment
		// that must remain unchanged during unlocked fold planning.
		foldPlanning.Environment.Path, err = e.sequenceEnvironmentLocked(auth)
		if err != nil {
			e.Mu.Unlock()
			return Result{}, err
		}
		foldPlanning.Environment.Namespace = auth.Catalog.State().NamespaceGeneration()
		if foldPlanning.Portal {
			foldPlanning.Environment.Private = auth.Session.Transactions.Catalog
		}
	}
	if count != b.Parameters.Count {
		e.Mu.Unlock()
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want %d", count, b.Parameters.Count)
	}
	canonical := make([]sqltype.Value, count)
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, Parameters: canonical, Host: HostOf(scope), Catalog: auth.Catalog, Input: scope.Input, Output: scope.Output}}
	scope.Domains.InputCatalog = auth.Catalog
	scope.RoutineAuth = auth
	scope.RoutineDependencies = b.Dependencies
	for _, usage := range b.Parameters.Types {
		if sqltype.ContainsDomainConstraints(usage.Type) {
			evaluation.Input.DomainChecks = &evaluation
			break
		}
	}
	if count != 0 || scope.protocolOrZero().bind != nil {
		if importFrame == nil {
			e.Mu.Unlock()
			return Result{}, fmt.Errorf("stuntgres: missing parameter importer")
		}
		// Complete each parameter/member input before decoding the next one.
		// CHECK recipes use this captured authority and run without Engine.mu.
		captured := auth.Catalog
		e.Mu.Unlock()
		runtimeContext := scope.activateWork(ctx)
		evaluation.Context = runtimeContext
		err = importFrame(runtimeContext, b.Parameters, canonical, evaluation.Input)
		evaluation.Context = ctx
		e.Mu.Lock()
		if err != nil {
			e.Mu.Unlock()
			return Result{}, err
		}
		if err = scope.ValidateLockedRelation(e); err != nil {
			e.Mu.Unlock()
			return Result{}, err
		}
		auth, err = scope.statementAuthorizationLocked(stmt)
		if err != nil {
			e.Mu.Unlock()
			return Result{}, err
		}
		scope.RoutineAuth = auth
		if err := typedef.ValidateCapturedInputBinding(captured, auth.Catalog, b.Dependencies); err != nil {
			e.Mu.Unlock()
			return Result{}, err
		}
		for i, v := range canonical {
			if v.Type() != b.Parameters.Types[i].Type {
				e.Mu.Unlock()
				return Result{}, fmt.Errorf("stuntgres: invalid parameter frame")
			}
		}
	}
	if p := scope.protocolOrZero().bind; p != nil {
		p.values = canonical
		if _, call := stmt.(*parser.Call); call {
			p.columns = analyze.RoutineCallSchema(b)
		}
		if b.Returning != nil {
			p.columns = b.Returning.Schema
		}
		if b.Query != nil && !b.CommandQuery {
			p.columns = b.Query.Root.Schema()
		}
	}
	if b.Insert.Conflict != nil && b.Insert.Conflict.Update != nil {
		if boundary := partition.WriteExtensionBoundary(stmt, view.PhysicalBinding(b).Relation); boundary != nil {
			// Hierarchies have no conflict-update executor. Complete analysis
			// and target/source authority before rejecting that capability,
			// without demanding arbiters, defaults or routed row effects.
			if err := analyze.CheckBinding(auth, b, stmt); err != nil {
				e.Mu.Unlock()
				return Result{}, err
			}
			e.Mu.Unlock()
			return Result{}, boundary
		}
	}
	if relation := view.BindingRuleRelation(b); relation != nil && len(stored.RulesOf(relation)) != 0 {
		return e.executeRuleProgramLocked(scope, stmt, b, auth, canonical, candidate)
	}
	return e.executeBoundLocked(scope, stmt, b, auth, canonical, candidate)
}

// The caller owns Engine.mu. Every return releases it. Rule programs reuse this
// executor without importing parameters again or opening another statement.
func (e *Engine) executeBoundLocked(scope *StatementScope, stmt parser.Statement, b analyze.Binding, auth AuthorizationView, canonical []sqltype.Value, candidate Description) (Result, error) {
	op := scope.Op
	ctx := scope.executionContext()
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, Parameters: canonical, Host: HostOf(scope), Catalog: auth.Catalog, Input: scope.Input, Output: scope.Output}}
	if scope.sqlRoutine.binding != nil {
		evaluation.PlanStaticOperators, evaluation.SpecializeRuleParameters = true, true
		// This binding is the validated frozen SQL-standard action, not newly
		// analyzed source. Retain its original call lifetimes through planning.
		evaluation.RoutineRecipe = true
		evaluation.RoutineChecks = scope.sqlRoutine.binding.Dependencies
	}
	var err error
	var creationStage *CatalogStage
	var creationDescriptor *catalog.Relation
	if creation, ok := stmt.(*parser.CreateQueryTable); ok {
		// Source names, expressions and types have gone through the canonical
		// binder. Read-only utility execution follows that analysis, but precedes
		// destination construction and every planner/producer demand.
		captured := auth.Catalog
		e.Mu.Unlock()
		if err := scope.readOnlyUtilityEligibility(creation); err != nil {
			return Result{}, err
		}
		e.Mu.Lock()
		if err := e.VisibilityWriteLocked(scope.TxRef); err != nil {
			e.Mu.Unlock()
			return Result{}, err
		}
		auth, err = e.StatementAuthorizationLocked(op, stmt)
		if err == nil {
			err = typedef.ValidateCapturedInputBinding(captured, auth.Catalog, b.Dependencies)
		}
		if err != nil {
			e.Mu.Unlock()
			return Result{}, err
		}
		evaluation.Catalog, scope.RoutineAuth = auth.Catalog, auth
		// CTAS is a utility after canonical analysis/import. NoData and an
		// existing destination skip the internal SELECT planner entirely.
		if scope.protocolOrZero().simple && !scope.protocolOrZero().simpleCreated {
			e.Mu.Unlock()
			if err := scope.beginProtocolSimplePortal(); err != nil {
				return Result{}, err
			}
			e.Mu.Lock()
		}
		var skipped bool
		creationStage, creationDescriptor, skipped, err = e.prepareQueryTableLocked(op, auth, creation, b)
		if err != nil || skipped {
			e.Mu.Unlock()
			if err == nil {
				return Result{CommandTag: session.QueryCreationUtilityTag(creation.Form)}, nil
			}
			return Result{}, err
		}
		defer e.DiscardCatalogStage(creationStage)
		e.Mu.Unlock()
		if err := scope.acquireCatalogNames(creationStage); err != nil {
			if creation.IfNotExists {
				key := ids.NameKey{ClassID: ids.RelationClassID, Namespace: creationDescriptor.Namespace(), Name: creationDescriptor.Name()}
				skipped, failure := scope.SkipConcurrentRelation(creationStage.Base, key, creation.Name, err)
				if skipped {
					return Result{CommandTag: session.QueryCreationUtilityTag(creation.Form)}, nil
				}
				return Result{}, failure
			}
			return Result{}, err
		}
		e.Mu.Lock()
		if e.Catalog.State.Epoch() != scope.TxRef.Epoch() {
			e.Mu.Unlock()
			return Result{}, ids.ErrCatalogReplaced
		}
		if creation.NoData {
			e.Mu.Unlock()
			err = e.publishQueryTable(scope, creationStage)
			return Result{}, err
		}
		if creation.Form == parser.CreateMaterializedView {
			OperationReplay(op).MaterializedPopulation = true
			defer func() { OperationReplay(op).MaterializedPopulation = false }()
		}
	}
	// Native has no cost-based custom/generic planner. Parameterized consumers
	// fold into a fresh operation-local group, including stored origins.
	evaluation.FoldCaptures = &exec.FoldCaptureCandidate{}
	if candidate.FoldPlanning != nil {
		evaluation.FoldCaptures = candidate.FoldPlanning.Group
	}
	// Planning-owned physical conversions and CHECK recipe initialization
	// precede executor ACLs, without Engine.mu or a data view. Checked
	// domain truth remains runtime consumer demand, not a folded value.
	return e.executeCTECommandsLocked(scope, stmt, b, auth, canonical, evaluation, creationStage, creationDescriptor)
}
