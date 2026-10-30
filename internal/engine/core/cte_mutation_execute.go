package core

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type cteCommandExecution struct {
	definition *plan.CTEDefinition
	execution  preparedCommandExecution
	mutation   int
	statement  parser.Statement
	ruleOwner  bool
	scratch    ruleCommandScratch
}

type PreparedCTEProgram struct {
	binding        analyze.Binding
	auth           AuthorizationView
	commands       []cteCommandExecution
	main           preparedCommandExecution
	MainScratch    ruleCommandScratch
	MainMutation   int
	normalizedRefs []ids.ObjectRef
}

// This dispatch never binds, imports parameters, creates a scope or publishes.
// The existing executors own their command semantics; one Finish owns the stage.
// Engine.mu is held on entry and released on every return.
func (e *Engine) executeCTECommandsLocked(scope *StatementScope, statement parser.Statement, b analyze.Binding, auth AuthorizationView, parameters []sqltype.Value, evaluation exec.Context, creationStage *CatalogStage, creationDescriptor *catalog.Relation) (result Result, failure error) {
	bind := scope.protocolOrZero().bind
	planningBind := bind != nil && !session.ProtocolPlainSelect(statement)
	if len(b.CTEs) == 0 && !planningBind {
		return e.executeBoundCommandLocked(scope, statement, b, auth, parameters, evaluation, creationStage, creationDescriptor, nil)
	}
	var prepared PreparedCTEProgram
	if _, err := e.PrepareCTECommandsLocked(scope, statement, b, auth, parameters, evaluation, creationStage, creationDescriptor, &prepared); err != nil {
		return Result{}, err
	}
	e.Mu.Lock()
	if err := e.publishFoldAttemptLocked(scope, prepared.auth, evaluation.FoldCaptures); err != nil {
		e.Mu.Unlock()
		return Result{}, err
	}
	if planningBind {
		if err := bind.AcceptFolds(evaluation.FoldCaptures); err != nil {
			e.Mu.Unlock()
			return Result{}, err
		}
		e.Mu.Unlock()
		return Result{}, nil
	}
	return e.ExecutePreparedCTECommandsLocked(scope, statement, &prepared, parameters, creationStage, creationDescriptor)
}

// Prepare the complete auxiliary/primary tree without executing any command.
// Enter held; all outcomes release Engine.mu.
func (e *Engine) PrepareCTECommandsLocked(scope *StatementScope, statement parser.Statement, b analyze.Binding, auth AuthorizationView, parameters []sqltype.Value, evaluation exec.Context, creationStage *CatalogStage, creationDescriptor *catalog.Relation, prepared *PreparedCTEProgram) (Result, error) {
	if scope.ruleAction.Auth == nil {
		scope.RoutineAuth = auth
	}
	if len(b.CTEs) == 0 {
		if _, err := e.executeBoundCommandLocked(scope, statement, b, auth, parameters, evaluation, creationStage, creationDescriptor, &prepared.main); err != nil {
			return Result{}, err
		}
		prepared.binding, prepared.auth = b, auth
		prepared.MainScratch, prepared.MainMutation = scope.ruleScratch(), scope.ActiveMutation
		return Result{}, nil
	}
	if err := view.NormalizeRuleCTEsLocked(&e.Catalog.Owner, auth, statement, &b); err != nil {
		e.Mu.Unlock()
		return Result{}, err
	}
	if err := view.CheckRowSecurityBinding(scope.RowSecurity, auth.View, b); err != nil {
		e.Mu.Unlock()
		return Result{}, err
	}
	// Normalized rule auxiliaries select one preparation policy for the entire
	// auxiliary/primary program, even when the primary has no rules of its own.
	if len(view.RuleCTEsOf(b.Rule.CTEs)) != 0 {
		evaluation.PlanStaticOperators = true
		evaluation.SpecializeRuleParameters = true
	}
	normalizedRefs, err := e.admitRuleCTEsLocked(scope, statement, &b)
	if err != nil {
		return Result{}, err
	}
	originalRuleAction := scope.ruleAction
	defer func() { scope.ruleAction = originalRuleAction }()
	main := scope.ActiveMutation
	defer scope.activateMutation(main)
	count, primaryTag := 0, ""
	for _, definition := range b.CTEs {
		mutation, ok := analyze.MutationBinding(definition)
		if !ok {
			continue
		}
		count++
		if scope.Op.EffectiveReadOnly() && !TemporaryDataTarget(auth, mutation, definition.Statement) {
			primaryTag = session.ModifyingTag(statement)
			if primaryTag == "" {
				primaryTag = "SELECT"
			}
		}
	}
	commands := make([]cteCommandExecution, 0, count)
	// Complete every mandatory command's real static preflight/ACL/RLS before
	// effects. No auxiliary parameter frame, rebind, or publication is involved.
	for _, definition := range b.CTEs {
		mutation, ok := analyze.MutationBinding(definition)
		if !ok {
			continue
		}
		syntax, commandBinding, commandAuth := definition.Statement, *mutation, auth
		rewrite := view.RuleCTEsOf(b.Rule.CTEs)[definition]
		if rewrite != nil {
			if _, selectOnly := rewrite.Command.Syntax.(*parser.Select); selectOnly {
				continue
			}
			ruleAuth := ruleAuthorizationOf(rewrite.Command.Auth)
			syntax, commandBinding, commandAuth = rewrite.Command.Syntax, rewrite.Command.Binding, ruleAuth
			scope.ruleAction = RuleActionScope{Auth: &ruleAuth}
		} else {
			scope.ruleAction = originalRuleAction
		}
		index := scope.mutationIndex(definition.Statement)
		if rewrite != nil {
			index = rewrite.Mutation
		}
		if index < 0 {
			e.Mu.Unlock()
			return Result{}, fmt.Errorf("stuntgres: missing admitted CTE target")
		}
		scope.activateMutation(index)
		command := cteCommandExecution{definition: definition, statement: syntax, ruleOwner: rewrite != nil, mutation: index, execution: preparedCommandExecution{primaryTag: primaryTag}}
		if _, err := e.executeBoundCommandLocked(scope, syntax, commandBinding, commandAuth, parameters, evaluation, nil, nil, &command.execution); err != nil {
			return Result{}, err
		}
		command.scratch = scope.ruleScratch()
		commands = append(commands, command)
		e.Mu.Lock()
	}
	scope.activateMutation(main)
	scope.ruleAction = originalRuleAction
	preparedMain := preparedCommandExecution{primaryTag: primaryTag}
	if _, err := e.executeBoundCommandLocked(scope, statement, b, auth, parameters, evaluation, creationStage, creationDescriptor, &preparedMain); err != nil {
		return Result{}, err
	}
	prepared.binding, prepared.auth, prepared.commands = b, auth, commands
	prepared.main, prepared.MainScratch, prepared.MainMutation = preparedMain, scope.ruleScratch(), main
	prepared.normalizedRefs = normalizedRefs
	return Result{}, nil
}

// Enter held; execute only already prepared commands with the original stage.
func (e *Engine) ExecutePreparedCTECommandsLocked(scope *StatementScope, statement parser.Statement, prepared *PreparedCTEProgram, parameters []sqltype.Value, creationStage *CatalogStage, creationDescriptor *catalog.Relation) (result Result, failure error) {
	b, auth, commands := prepared.binding, prepared.auth, prepared.commands
	scope.ActiveMutation = prepared.MainMutation
	scope.installRuleScratch(prepared.MainScratch)
	if len(b.CTEs) == 0 {
		return e.executePreparedCommandLocked(scope, statement, &prepared.main, parameters, creationStage, creationDescriptor)
	}
	originalRuleAction := scope.ruleAction
	main := prepared.MainMutation
	normalizedRefs := prepared.normalizedRefs
	defer func() {
		scope.activateMutation(main)
		scope.ruleAction = originalRuleAction
	}()
	if err := scope.RefreshRelationGrantsLocked(e); err != nil {
		e.Mu.Unlock()
		return Result{}, err
	}
	if err := scope.Op.Context().Err(); err != nil {
		e.Mu.Unlock()
		return Result{}, err
	}
	// Include every executable target, even INSERT without FROM, before effects.
	// Replaced lexical targets retain their name/ACL/lock contexts, not a read
	// occurrence. Actual rewritten targets and sources remain in this frontier.
	// Dead SELECT definitions remain analysis-only and do not open providers.
	leaves := b.QuerySources
	rewrites := view.RuleCTEsOf(b.Rule.CTEs)
targets:
	for i := range scope.mutations {
		mutation := scope.mutations[i]
		if i == scope.ActiveMutation {
			mutation = scope.StatementMutation
		}
		if mutation.LockedRef.Domain() != nil {
			if len(rewrites) != 0 {
				for _, definition := range b.CTEs {
					if definition.Statement != mutation.Statement {
						continue
					}
					if rewrite := rewrites[definition]; rewrite != nil && rewrite.Mutation != i {
						continue targets
					}
				}
			}
			if len(leaves) == len(b.QuerySources) {
				leaves = append([]exec.Leaf(nil), leaves...)
			}
			leaves = append(leaves, exec.Leaf{Ref: mutation.LockedRef, Relation: mutation.LockedDescriptor.(*catalog.Relation), Only: true})
			leaves = append(leaves, mutation.InheritedTargets...)
		}
	}
	if len(scope.mutations) == 0 && b.Ref.Domain() != nil {
		leaves = append(append([]exec.Leaf(nil), leaves...), exec.Leaf{Ref: b.Ref, Relation: b.Relation})
	}
	collectAuth := auth
	collectAuth.Recapture(scope.inheritanceRead(auth.Catalog))
	refs, err := e.QueryTableRefsLocked(collectAuth, auth.State.CurrentUser, leaves, normalizedRefs, scope.Op.Session.SessionToken, plan.RowMark{})
	if err != nil {
		e.Mu.Unlock()
		return Result{}, err
	}
	if _, err := scope.AcquireDataViewsLocked(e, refs); err != nil {
		e.Mu.Unlock()
		return Result{}, err
	}
	var source *exec.Source
	if b.CTESelectBodies {
		source, err = e.CaptureQuerySourcesLocked(scope, b.QuerySources, true)
		if err != nil {
			e.Mu.Unlock()
			return Result{}, err
		}
		// A SELECT CTE owns a separate invocation from each command's input.
		// Select and bridge that actual owner before any producer or effect,
		// without holding Engine.mu while source selection may execute code.
		e.Mu.Unlock()
		err = exec.SelectQuerySources(scope.Op.Context(), source)
		if err == nil {
			source = view.FreezeWriteSources(source, b.QuerySources, scope.authority, HostOf(scope))
		}
		if err != nil {
			source.Close()
			return Result{}, err
		}
		e.Mu.Lock()
		if err = scope.RefreshRelationGrantsLocked(e); err == nil {
			err = scope.Op.Context().Err()
		}
		if err != nil {
			e.Mu.Unlock()
			source.Close()
			return Result{}, err
		}
	} else {
		// Completed modifiers need only a lifetime/authority header, not a
		// second physical capture of every command's own input sources.
		if scope.authority == nil {
			scope.authority = &exec.Authority{Host: HostOf(scope)}
		}
		source = &exec.Source{Materialized: true, FlatCount: 1, Authority: scope.authority, Locks: scope.QueryLockContext}
	}
	previousOwner := scope.cteInvocation
	owner := previousOwner
	findOwner := func(frame uint32) *exec.CTEInvocation {
		for current := owner; current != nil; current = current.Parent {
			if current.Frame == frame {
				return current
			}
		}
		return nil
	}
	addOwners := func(definitions []*plan.CTEDefinition) {
		for _, definition := range definitions {
			if findOwner(definition.Owner) != nil {
				continue
			}
			current := &exec.CTEInvocation{Frame: definition.Owner, Parent: owner, State: exec.State{Source: source, SourceMode: exec.SourceReusable, Parameters: parameters, Input: scope.Input, Output: scope.Output, Locking: source.Locks != nil}}
			current.State.CapturePlannerAdmission()
			current.State.Ctes = current
			owner = current
		}
	}
	addOwners(b.CTEs)
	for i := range commands {
		addOwners(commands[i].execution.binding.CTEs)
	}
	scope.cteInvocation = owner
	transferred := false
	defer func() {
		if transferred {
			scope.cteInvocation = previousOwner
			return
		}
		for current := owner; current != previousOwner; current = current.Parent {
			if err := current.Close(); failure == nil {
				failure = err
			}
			current.State.ReleaseMemberships()
		}
		if owner != previousOwner {
			failure = owner.State.FinishSources(failure)
		}
		source.Close()
		scope.cteInvocation = previousOwner
		if failure != nil {
			if result.Rows != nil {
				result.Rows.Close()
			}
			result = Result{}
		}
	}()
	previousDeferred, previousDeferredCommand := scope.deferAfterProducers, scope.deferredAfterCommand
	defer func() {
		scope.deferAfterProducers, scope.deferredAfterCommand = previousDeferred, previousDeferredCommand
	}()
	consumerEffects := dml.HasStatementTriggers(prepared.main.binding.Relation, dml.StatementEvents(prepared.main.binding, statement), stored.TriggerBefore)
	for i := range commands {
		binding := commands[i].execution.binding
		if dml.HasStatementTriggers(binding.Relation, dml.StatementEvents(binding, commands[i].statement), stored.TriggerBefore) ||
			dml.HasStatementTriggers(binding.Relation, stored.TriggerInsert|stored.TriggerUpdate|stored.TriggerDelete|stored.TriggerTruncate, stored.TriggerAfter) {
			consumerEffects = true
			break
		}
	}
	if consumerEffects {
		// Keep typed eager materialization, but let the existing CTE source
		// demand start modifiers and its EOF qualify their statement events.
		for i := range commands {
			command := &commands[i]
			commandOwner := findOwner(command.definition.Owner)
			if commandOwner == nil {
				e.Mu.Unlock()
				return Result{}, fmt.Errorf("stuntgres: missing admitted CTE owner")
			}
			if commandOwner.Producers == nil {
				commandOwner.Producers = make(map[*plan.CTEDefinition]*exec.CTEProducer)
			}
			producer := &exec.CTEProducer{MutationPending: true, Source: &exec.Source{FlatWidth: len(command.definition.OutputSchema()), Materialized: true, Authority: source.Authority, Host: source.Host, Locks: source.Locks}}
			commandOwner.Producers[command.definition] = producer
			commandOwner.Order = append(commandOwner.Order, producer)
			producer.StartMutation = func() error {
				active, scratch, rule := scope.ActiveMutation, scope.ruleScratch(), scope.ruleAction
				deferred, deferredCommand := scope.deferAfterProducers, scope.deferredAfterCommand
				previousInvocation := scope.cteInvocation
				scope.cteInvocation = owner
				defer func() {
					scope.activateMutation(active)
					scope.installRuleScratch(scratch)
					scope.ruleAction = rule
					scope.deferAfterProducers, scope.deferredAfterCommand = deferred, deferredCommand
					scope.cteInvocation = previousInvocation
				}()
				scope.activateMutation(command.mutation)
				scope.installRuleScratch(command.scratch)
				scope.ruleAction = originalRuleAction
				if command.ruleOwner {
					scope.ruleAction = RuleActionScope{Auth: &command.execution.auth}
				}
				scope.deferAfterProducers = command.execution.binding.Returning != nil
				scope.deferredAfterCommand = scope.effectCommand
				start := 0
				if scope.afterTriggers != nil && scope.afterTriggers.frames[scope.effectCommand] != nil {
					start = len(scope.afterTriggers.frames[scope.effectCommand].producers)
				}
				e.Mu.Lock()
				output, err := e.executePreparedCommandLocked(scope, command.statement, &command.execution, parameters, nil, nil)
				if err != nil {
					return err
				}
				if err := dml.CompleteCTEMutation(commandOwner, command.definition, output.Count, output.Rows); err != nil {
					return err
				}
				if scope.afterTriggers != nil {
					if f := scope.afterTriggers.frames[scope.effectCommand]; f != nil && len(f.producers) > start {
						pending := slices.Clone(f.producers[start:])
						f.producers = f.producers[:start]
						producer.CompleteMutation = func() error {
							for _, event := range pending {
								if err := scope.qualifyAfterProducer(event); err != nil {
									return err
								}
							}
							return nil
						}
					}
				}
				e.Mu.Lock()
				err = scope.RefreshRelationGrantsLocked(e)
				e.Mu.Unlock()
				if err != nil {
					return err
				}
				return scope.Op.Context().Err()
			}
		}
	} else {
		for i := range commands {
			command := &commands[i]
			scope.deferAfterProducers = false
			scope.ActiveMutation = command.mutation
			scope.installRuleScratch(command.scratch)
			scope.ruleAction = originalRuleAction
			if command.ruleOwner {
				scope.ruleAction = RuleActionScope{Auth: &command.execution.auth}
			}
			output, err := e.executePreparedCommandLocked(scope, command.statement, &command.execution, parameters, nil, nil)
			if err != nil {
				return Result{}, err
			}
			commandOwner := findOwner(command.definition.Owner)
			if commandOwner == nil {
				return Result{}, fmt.Errorf("stuntgres: missing admitted CTE owner")
			}
			if err := dml.CompleteCTEMutation(commandOwner, command.definition, output.Count, output.Rows); err != nil {
				return Result{}, err
			}
			e.Mu.Lock()
			if err := scope.RefreshRelationGrantsLocked(e); err != nil {
				e.Mu.Unlock()
				return Result{}, err
			}
			if err := scope.Op.Context().Err(); err != nil {
				e.Mu.Unlock()
				return Result{}, err
			}
		}
	}
	scope.activateMutation(main)
	scope.installRuleScratch(prepared.MainScratch)
	scope.ruleAction = originalRuleAction
	scope.deferAfterProducers = false
	result, failure = e.executePreparedCommandLocked(scope, statement, &prepared.main, parameters, creationStage, creationDescriptor)
	var finishModifiers func() error
	if consumerEffects {
		finishModifiers = func() error {
			// ExecutorFinish exhausts remaining auxiliaries individually,
			// including unreferenced modifiers and consumers stopped before EOF.
			for i := range commands {
				command := &commands[i]
				producer := findOwner(command.definition.Owner).Producers[command.definition]
				if producer.StartMutation != nil {
					start := producer.StartMutation
					producer.StartMutation = nil
					if err := start(); err != nil {
						return err
					}
				}
				if _, err := producer.Ensure(scope.Op.Context(), producer.Source.FlatCount); err != nil {
					return err
				}
			}
			return nil
		}
	}
	if failure == nil && result.Rows != nil && (scope.sqlRoutine.plCapture || scope.sqlRoutine.rows && !parser.HasModifyingCTE(statement)) {
		result.Rows = &exec.CTEOwnedRows{RowResult: result.Rows, Owner: owner, Previous: previousOwner, Source: source, Complete: finishModifiers}
		transferred = true
	}
	if failure == nil && !transferred && finishModifiers != nil {
		failure = finishModifiers()
	}
	return result, failure
}
