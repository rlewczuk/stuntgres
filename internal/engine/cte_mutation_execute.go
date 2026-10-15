package engine

import (
	"fmt"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type cteCommandExecution struct {
	definition *cteDefinition
	execution  preparedCommandExecution
	mutation   int
	statement  parser.Statement
	ruleOwner  bool
	scratch    ruleCommandScratch
}

type preparedCTEProgram struct {
	binding        binding
	auth           AuthorizationView
	commands       []cteCommandExecution
	main           preparedCommandExecution
	mainScratch    ruleCommandScratch
	mainMutation   int
	normalizedRefs []ObjectRef
}

// This dispatch never binds, imports parameters, creates a scope or publishes.
// The existing executors own their command semantics; one Finish owns the stage.
// Engine.mu is held on entry and released on every return.
func (e *Engine) executeCTECommandsLocked(scope *StatementScope, statement parser.Statement, b binding, auth AuthorizationView, parameters []sqltype.Value, evaluation EvalContext, creationStage *CatalogStage, creationDescriptor *relationDescriptor) (result Result, failure error) {
	if len(b.ctes) == 0 {
		return e.executeBoundCommandLocked(scope, statement, b, auth, parameters, evaluation, creationStage, creationDescriptor, nil)
	}
	var prepared preparedCTEProgram
	if _, err := e.prepareCTECommandsLocked(scope, statement, b, auth, parameters, evaluation, creationStage, creationDescriptor, &prepared); err != nil {
		return Result{}, err
	}
	e.mu.Lock()
	return e.executePreparedCTECommandsLocked(scope, statement, &prepared, parameters, creationStage, creationDescriptor)
}

// Prepare the complete auxiliary/primary tree without executing any command.
// Enter held; all outcomes release Engine.mu.
func (e *Engine) prepareCTECommandsLocked(scope *StatementScope, statement parser.Statement, b binding, auth AuthorizationView, parameters []sqltype.Value, evaluation EvalContext, creationStage *CatalogStage, creationDescriptor *relationDescriptor, prepared *preparedCTEProgram) (Result, error) {
	if len(b.ctes) == 0 {
		if _, err := e.executeBoundCommandLocked(scope, statement, b, auth, parameters, evaluation, creationStage, creationDescriptor, &prepared.main); err != nil {
			return Result{}, err
		}
		prepared.binding, prepared.auth = b, auth
		prepared.mainScratch, prepared.mainMutation = scope.ruleScratch(), scope.activeMutation
		return Result{}, nil
	}
	if err := e.normalizeRuleCTEsLocked(scope, auth, statement, &b); err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	// Normalized rule auxiliaries select one preparation policy for the entire
	// auxiliary/primary program, even when the primary has no rules of its own.
	if len(b.ruleCTEs) != 0 {
		evaluation.planStaticOperators = true
		evaluation.specializeRuleParameters = true
	}
	normalizedRefs, err := e.admitRuleCTEsLocked(scope, statement, &b)
	if err != nil {
		return Result{}, err
	}
	originalRuleAuth := scope.ruleActionAuth
	defer func() { scope.ruleActionAuth = originalRuleAuth }()
	main := scope.activeMutation
	defer scope.activateMutation(main)
	count, primaryTag := 0, ""
	for _, definition := range b.ctes {
		if definition.mutation == nil {
			continue
		}
		count++
		if scope.owner.readOnly && !temporaryDataTarget(auth, definition.mutation, definition.statement) {
			primaryTag = modifyingTag(statement)
			if primaryTag == "" {
				primaryTag = "SELECT"
			}
		}
	}
	commands := make([]cteCommandExecution, 0, count)
	// Complete every mandatory command's real static preflight/ACL/RLS before
	// effects. No auxiliary parameter frame, rebind, or publication is involved.
	for _, definition := range b.ctes {
		if definition.mutation == nil {
			continue
		}
		syntax, commandBinding, commandAuth := definition.statement, *definition.mutation, auth
		rewrite := b.ruleCTEs[definition]
		if rewrite != nil {
			if _, selectOnly := rewrite.command.syntax.(*parser.Select); selectOnly {
				continue
			}
			syntax, commandBinding, commandAuth = rewrite.command.syntax, rewrite.command.binding, rewrite.command.auth
			scope.ruleActionAuth = &rewrite.command.auth
		} else {
			scope.ruleActionAuth = originalRuleAuth
		}
		index := scope.mutationIndex(definition.statement)
		if rewrite != nil {
			index = rewrite.mutation
		}
		if index < 0 {
			e.mu.Unlock()
			return Result{}, fmt.Errorf("stuntgres: missing admitted CTE target")
		}
		scope.activateMutation(index)
		command := cteCommandExecution{definition: definition, statement: syntax, ruleOwner: rewrite != nil, mutation: index, execution: preparedCommandExecution{primaryTag: primaryTag}}
		evaluation.quoteCaptures.sealed = false
		if _, err := e.executeBoundCommandLocked(scope, syntax, commandBinding, commandAuth, parameters, evaluation, nil, nil, &command.execution); err != nil {
			return Result{}, err
		}
		command.scratch = scope.ruleScratch()
		commands = append(commands, command)
		e.mu.Lock()
	}
	scope.activateMutation(main)
	scope.ruleActionAuth = originalRuleAuth
	preparedMain := preparedCommandExecution{primaryTag: primaryTag}
	evaluation.quoteCaptures.sealed = false
	if _, err := e.executeBoundCommandLocked(scope, statement, b, auth, parameters, evaluation, creationStage, creationDescriptor, &preparedMain); err != nil {
		return Result{}, err
	}
	prepared.binding, prepared.auth, prepared.commands = b, auth, commands
	prepared.main, prepared.mainScratch, prepared.mainMutation = preparedMain, scope.ruleScratch(), main
	prepared.normalizedRefs = normalizedRefs
	return Result{}, nil
}

// Enter held; execute only already prepared commands with the original stage.
func (e *Engine) executePreparedCTECommandsLocked(scope *StatementScope, statement parser.Statement, prepared *preparedCTEProgram, parameters []sqltype.Value, creationStage *CatalogStage, creationDescriptor *relationDescriptor) (result Result, failure error) {
	b, auth, commands := prepared.binding, prepared.auth, prepared.commands
	scope.activeMutation = prepared.mainMutation
	scope.installRuleScratch(prepared.mainScratch)
	if len(b.ctes) == 0 {
		return e.executePreparedCommandLocked(scope, statement, &prepared.main, parameters, creationStage, creationDescriptor)
	}
	originalRuleAuth := scope.ruleActionAuth
	main := prepared.mainMutation
	normalizedRefs := prepared.normalizedRefs
	defer func() {
		scope.activateMutation(main)
		scope.ruleActionAuth = originalRuleAuth
	}()
	if err := scope.refreshRelationGrantsLocked(e); err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	if err := scope.op.Context().Err(); err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	// Include every target, even INSERT without FROM, before the first effect.
	// Dead SELECT definitions remain analysis-only and do not open providers.
	leaves := b.querySources
	for i := range scope.mutations {
		mutation := scope.mutations[i]
		if i == scope.activeMutation {
			mutation = scope.statementMutation
		}
		if mutation.lockedRef.owner != nil {
			if len(leaves) == len(b.querySources) {
				leaves = append([]queryLeaf(nil), leaves...)
			}
			leaves = append(leaves, queryLeaf{ref: mutation.lockedRef, relation: mutation.lockedDescriptor.(*relationDescriptor), only: true})
			leaves = append(leaves, mutation.inheritedTargets...)
		}
	}
	if len(scope.mutations) == 0 && b.ref.owner != nil {
		leaves = append(append([]queryLeaf(nil), leaves...), queryLeaf{ref: b.ref, relation: b.relation})
	}
	collectAuth := auth
	collectAuth.recaptureCatalog(scope.inheritanceRead(auth.catalog))
	refs, err := e.queryTableRefsLocked(collectAuth, auth.state.CurrentUser, leaves, normalizedRefs, scope.op.session.token, queryRowMark{})
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	if _, err := scope.acquireDataViewsLocked(e, refs); err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	var source *querySource
	if b.cteSelectBodies {
		source, err = e.captureQuerySourcesLocked(scope, b.querySources, true)
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
	} else {
		// Completed modifiers need only a lifetime/authority header, not a
		// second physical capture of every command's own input sources.
		if scope.authority == nil {
			scope.authority = &queryAuthority{scope: scope}
		}
		source = &querySource{materialized: true, flatCount: 1, authority: scope.authority, locks: scope.queryLocks}
	}
	previousOwner := scope.cteInvocation
	owner := previousOwner
	findOwner := func(frame uint32) *cteInvocation {
		for current := owner; current != nil; current = current.parent {
			if current.frame == frame {
				return current
			}
		}
		return nil
	}
	addOwners := func(definitions []*cteDefinition) {
		for _, definition := range definitions {
			if findOwner(definition.owner) != nil {
				continue
			}
			current := &cteInvocation{frame: definition.owner, parent: owner, state: queryState{source: source, parameters: parameters, input: scope.input, output: scope.output, locking: source.locks != nil}}
			current.state.ctes = current
			owner = current
		}
	}
	addOwners(b.ctes)
	for i := range commands {
		addOwners(commands[i].execution.binding.ctes)
	}
	scope.cteInvocation = owner
	defer func() {
		for current := owner; current != previousOwner; current = current.parent {
			if err := current.close(); failure == nil {
				failure = err
			}
			current.state.releaseMemberships()
		}
		source.close()
		scope.cteInvocation = previousOwner
		if failure != nil {
			if result.Rows != nil {
				result.Rows.Close()
			}
			result = Result{}
		}
	}()
	for i := range commands {
		command := &commands[i]
		scope.activeMutation = command.mutation
		scope.installRuleScratch(command.scratch)
		scope.ruleActionAuth = originalRuleAuth
		if command.ruleOwner {
			scope.ruleActionAuth = &command.execution.auth
		}
		output, err := e.executePreparedCommandLocked(scope, command.statement, &command.execution, parameters, nil, nil)
		if err != nil {
			return Result{}, err
		}
		commandOwner := findOwner(command.definition.owner)
		if commandOwner == nil {
			return Result{}, fmt.Errorf("stuntgres: missing admitted CTE owner")
		}
		if err := commandOwner.completeMutation(command.definition, output); err != nil {
			return Result{}, err
		}
		e.mu.Lock()
		if err := scope.refreshRelationGrantsLocked(e); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		if err := scope.op.Context().Err(); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
	}
	scope.activateMutation(main)
	scope.installRuleScratch(prepared.mainScratch)
	scope.ruleActionAuth = originalRuleAuth
	return e.executePreparedCommandLocked(scope, statement, &prepared.main, parameters, creationStage, creationDescriptor)
}

func (o *cteInvocation) completeMutation(definition *cteDefinition, result Result) error {
	if o.closed || o.producers[definition] != nil {
		if result.Rows != nil {
			result.Rows.Close()
		}
		return fmt.Errorf("stuntgres: CTE modifier completed more than once")
	}
	producer := &cteProducer{done: true, closed: true, affected: result.Count}
	if definition.mutation.returning != nil {
		rows, ok := result.Rows.(*finalRows)
		if !ok {
			if result.Rows != nil {
				result.Rows.Close()
			}
			return fmt.Errorf("stuntgres: CTE modifier lacks typed output")
		}
		producer.source = &querySource{flat: rows.values, flatWidth: len(rows.schema), flatCount: rows.count, materialized: true, authority: o.state.source.authority, scope: o.state.source.scope, locks: o.state.source.locks}
		rows.values = nil // Transfer the typed buffer, never Scan/reimport/copy.
		if err := rows.Close(); err != nil {
			return err
		}
	} else if result.Rows != nil {
		result.Rows.Close()
		return fmt.Errorf("stuntgres: no-RETURNING CTE produced a relation")
	}
	if o.producers == nil {
		o.producers = make(map[*cteDefinition]*cteProducer)
	}
	o.producers[definition] = producer
	o.order = append(o.order, producer)
	return nil
}

func (s *queryState) inheritCTEs() {
	if s.ctes == nil && s.source != nil {
		if scope := s.source.liveScope(); scope != nil {
			s.ctes = scope.cteInvocation
		}
	}
}
