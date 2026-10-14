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
}

// This dispatch never binds, imports parameters, creates a scope or publishes.
// The existing executors own their command semantics; one Finish owns the stage.
// Engine.mu is held on entry and released on every return.
func (e *Engine) executeCTECommandsLocked(scope *StatementScope, statement parser.Statement, b binding, auth AuthorizationView, parameters []sqltype.Value, evaluation EvalContext, creationStage *CatalogStage, creationDescriptor *relationDescriptor) (result Result, failure error) {
	if len(b.ctes) == 0 {
		return e.executeBoundCommandLocked(scope, statement, b, auth, parameters, evaluation, creationStage, creationDescriptor, nil)
	}
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
		index := scope.mutationIndex(definition.statement)
		if index < 0 {
			e.mu.Unlock()
			return Result{}, fmt.Errorf("stuntgres: missing admitted CTE target")
		}
		scope.activateMutation(index)
		command := cteCommandExecution{definition: definition, mutation: index, execution: preparedCommandExecution{primaryTag: primaryTag}}
		evaluation.quoteCaptures.sealed = false
		if _, err := e.executeBoundCommandLocked(scope, definition.statement, *definition.mutation, auth, parameters, evaluation, nil, nil, &command.execution); err != nil {
			return Result{}, err
		}
		commands = append(commands, command)
		e.mu.Lock()
	}
	scope.activateMutation(main)
	preparedMain := preparedCommandExecution{primaryTag: primaryTag}
	evaluation.quoteCaptures.sealed = false
	if _, err := e.executeBoundCommandLocked(scope, statement, b, auth, parameters, evaluation, creationStage, creationDescriptor, &preparedMain); err != nil {
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
			leaves = append(leaves, queryLeaf{ref: mutation.lockedRef, relation: mutation.lockedDescriptor.(*relationDescriptor)})
		}
	}
	if len(scope.mutations) == 0 && b.ref.owner != nil {
		leaves = append(append([]queryLeaf(nil), leaves...), queryLeaf{ref: b.ref, relation: b.relation})
	}
	refs, err := e.queryTableRefsLocked(scope.inheritanceRead(auth.catalog), leaves, nil, scope.op.session.token)
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
	owner := &cteInvocation{frame: b.ctes[0].owner, state: queryState{source: source, parameters: parameters, input: scope.input, output: scope.output, locking: source.locks != nil}}
	owner.state.ctes = owner
	scope.cteInvocation = owner
	defer func() {
		if err := owner.close(); failure == nil {
			failure = err
		}
		owner.state.releaseMemberships()
		source.close()
		scope.cteInvocation = nil
		if failure != nil {
			if result.Rows != nil {
				result.Rows.Close()
			}
			result = Result{}
		}
	}()
	for i := range commands {
		command := &commands[i]
		scope.activateMutation(command.mutation)
		output, err := e.executePreparedCommandLocked(scope, command.definition.statement, &command.execution, parameters, nil, nil)
		if err != nil {
			return Result{}, err
		}
		if err := owner.completeMutation(command.definition, output); err != nil {
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
	return e.executePreparedCommandLocked(scope, statement, &preparedMain, parameters, creationStage, creationDescriptor)
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
