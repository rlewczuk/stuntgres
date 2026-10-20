package engine

import (
	"fmt"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
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

type preparedCTEProgram struct {
	binding        analyze.Binding
	auth           AuthorizationView
	commands       []cteCommandExecution
	main           preparedCommandExecution
	mainScratch    ruleCommandScratch
	mainMutation   int
	normalizedRefs []ids.ObjectRef
}

// This dispatch never binds, imports parameters, creates a scope or publishes.
// The existing executors own their command semantics; one Finish owns the stage.
// Engine.mu is held on entry and released on every return.
func (e *Engine) executeCTECommandsLocked(scope *StatementScope, statement parser.Statement, b analyze.Binding, auth AuthorizationView, parameters []sqltype.Value, evaluation exec.Context, creationStage *CatalogStage, creationDescriptor *catalog.Relation) (result Result, failure error) {
	if len(b.CTEs) == 0 {
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
func (e *Engine) prepareCTECommandsLocked(scope *StatementScope, statement parser.Statement, b analyze.Binding, auth AuthorizationView, parameters []sqltype.Value, evaluation exec.Context, creationStage *CatalogStage, creationDescriptor *catalog.Relation, prepared *preparedCTEProgram) (Result, error) {
	if scope.ruleActionAuth == nil {
		scope.routineAuth = auth
	}
	if len(b.CTEs) == 0 {
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
	if err := checkRowSecurityBinding(scope, auth, b); err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	// Normalized rule auxiliaries select one preparation policy for the entire
	// auxiliary/primary program, even when the primary has no rules of its own.
	if len(ruleCTEsOf(b.Rule.CTEs)) != 0 {
		evaluation.PlanStaticOperators = true
		evaluation.SpecializeRuleParameters = true
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
	for _, definition := range b.CTEs {
		mutation, ok := analyze.MutationBinding(definition)
		if !ok {
			continue
		}
		count++
		if scope.owner.readOnly && !temporaryDataTarget(auth, mutation, definition.Statement) {
			primaryTag = modifyingTag(statement)
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
		rewrite := ruleCTEsOf(b.Rule.CTEs)[definition]
		if rewrite != nil {
			if _, selectOnly := rewrite.command.syntax.(*parser.Select); selectOnly {
				continue
			}
			syntax, commandBinding, commandAuth = rewrite.command.syntax, rewrite.command.binding, rewrite.command.auth
			scope.ruleActionAuth = &rewrite.command.auth
		} else {
			scope.ruleActionAuth = originalRuleAuth
		}
		index := scope.mutationIndex(definition.Statement)
		if rewrite != nil {
			index = rewrite.mutation
		}
		if index < 0 {
			e.mu.Unlock()
			return Result{}, fmt.Errorf("stuntgres: missing admitted CTE target")
		}
		scope.activateMutation(index)
		command := cteCommandExecution{definition: definition, statement: syntax, ruleOwner: rewrite != nil, mutation: index, execution: preparedCommandExecution{primaryTag: primaryTag}}
		evaluation.QuoteCaptures.Sealed = false
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
	evaluation.QuoteCaptures.Sealed = false
	if _, err := e.executeBoundCommandLocked(scope, statement, b, auth, parameters, evaluation, creationStage, creationDescriptor, &preparedMain); err != nil {
		return Result{}, err
	}
	prepared.binding, prepared.auth, prepared.commands = b, auth, commands
	prepared.main, prepared.mainScratch, prepared.mainMutation = preparedMain, scope.ruleScratch(), main
	prepared.normalizedRefs = normalizedRefs
	return Result{}, nil
}

// Enter held; execute only already prepared commands with the original stage.
func (e *Engine) executePreparedCTECommandsLocked(scope *StatementScope, statement parser.Statement, prepared *preparedCTEProgram, parameters []sqltype.Value, creationStage *CatalogStage, creationDescriptor *catalog.Relation) (result Result, failure error) {
	b, auth, commands := prepared.binding, prepared.auth, prepared.commands
	scope.activeMutation = prepared.mainMutation
	scope.installRuleScratch(prepared.mainScratch)
	if len(b.CTEs) == 0 {
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
	leaves := b.QuerySources
	for i := range scope.mutations {
		mutation := scope.mutations[i]
		if i == scope.activeMutation {
			mutation = scope.statementMutation
		}
		if mutation.lockedRef.Domain() != nil {
			if len(leaves) == len(b.QuerySources) {
				leaves = append([]exec.Leaf(nil), leaves...)
			}
			leaves = append(leaves, exec.Leaf{Ref: mutation.lockedRef, Relation: mutation.lockedDescriptor.(*catalog.Relation), Only: true})
			leaves = append(leaves, mutation.inheritedTargets...)
		}
	}
	if len(scope.mutations) == 0 && b.Ref.Domain() != nil {
		leaves = append(append([]exec.Leaf(nil), leaves...), exec.Leaf{Ref: b.Ref, Relation: b.Relation})
	}
	collectAuth := auth
	collectAuth.Recapture(scope.inheritanceRead(auth.Catalog))
	refs, err := e.queryTableRefsLocked(collectAuth, auth.State.CurrentUser, leaves, normalizedRefs, scope.op.Session.SessionToken, plan.RowMark{})
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	if _, err := scope.acquireDataViewsLocked(e, refs); err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	var source *exec.Source
	if b.CTESelectBodies {
		source, err = e.captureQuerySourcesLocked(scope, b.QuerySources, true)
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
	} else {
		// Completed modifiers need only a lifetime/authority header, not a
		// second physical capture of every command's own input sources.
		if scope.authority == nil {
			scope.authority = &exec.Authority{Host: hostOf(scope)}
		}
		source = &exec.Source{Materialized: true, FlatCount: 1, Authority: scope.authority, Locks: scope.queryLocks}
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
			current := &exec.CTEInvocation{Frame: definition.Owner, Parent: owner, State: exec.State{Source: source, Parameters: parameters, Input: scope.input, Output: scope.output, Locking: source.Locks != nil}}
			current.State.Ctes = current
			owner = current
		}
	}
	addOwners(b.CTEs)
	for i := range commands {
		addOwners(commands[i].execution.binding.CTEs)
	}
	scope.cteInvocation = owner
	defer func() {
		for current := owner; current != previousOwner; current = current.Parent {
			if err := current.Close(); failure == nil {
				failure = err
			}
			current.State.ReleaseMemberships()
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
		commandOwner := findOwner(command.definition.Owner)
		if commandOwner == nil {
			return Result{}, fmt.Errorf("stuntgres: missing admitted CTE owner")
		}
		if err := completeCTEMutation(commandOwner, command.definition, output); err != nil {
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

func completeCTEMutation(o *exec.CTEInvocation, definition *plan.CTEDefinition, result Result) error {
	if o.Closed || o.Producers[definition] != nil {
		if result.Rows != nil {
			result.Rows.Close()
		}
		return fmt.Errorf("stuntgres: CTE modifier completed more than once")
	}
	producer := &exec.CTEProducer{Done: true, Closed: true, Affected: result.Count}
	if mutation, _ := analyze.MutationBinding(definition); mutation.Returning != nil {
		rows, ok := result.Rows.(*exec.FinalRows)
		if !ok {
			if result.Rows != nil {
				result.Rows.Close()
			}
			return fmt.Errorf("stuntgres: CTE modifier lacks typed output")
		}
		producer.Source = &exec.Source{Flat: rows.Values, FlatWidth: len(rows.Columns), FlatCount: rows.Count, Materialized: true, Authority: o.State.Source.Authority, Host: o.State.Source.Host, Locks: o.State.Source.Locks}
		rows.Values = nil // Transfer the typed buffer, never Scan/reimport/copy.
		if err := rows.Close(); err != nil {
			return err
		}
	} else if result.Rows != nil {
		result.Rows.Close()
		return fmt.Errorf("stuntgres: no-RETURNING CTE produced a relation")
	}
	if o.Producers == nil {
		o.Producers = make(map[*plan.CTEDefinition]*exec.CTEProducer)
	}
	o.Producers[definition] = producer
	o.Order = append(o.Order, producer)
	return nil
}
