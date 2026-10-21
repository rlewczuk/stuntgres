package engine

import (
	"maps"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// ruleActionScope is the rule-owner authority of the rule action command now
// executing; nil auth means no rule action is active. Command switches save
// and restore it as one unit.
type ruleActionScope struct {
	auth *AuthorizationView
}

// Only command-local target/source scratch changes. Publication, the operation,
// parameter frame, original signature, snapshot frontier and budget remain owned
// by the original StatementScope and its one WriteStage/Finish.
type ruleCommandScratch struct {
	mutation   statementMutation
	ref        ids.ObjectRef
	descriptor catalog.Descriptor
	storage    *storage.Relation
	viewWrite  *viewWrite
	sources    []exec.Leaf
	unique     *index.UniqueWriteBinding
	conflict   *insertConflictState
	returning  *insertReturningOutput
	partition  *partitionInsertState
	locks      *exec.LockContext
	admission  analyze.LockAdmission
	ruleAction ruleActionScope
	quotes     *exec.QuoteCaptureCandidate
}

func (s *StatementScope) ruleScratch() ruleCommandScratch {
	return ruleCommandScratch{mutation: s.statementMutation, ref: s.lockedRef, descriptor: s.lockedDescriptor, storage: s.lockedStorage, viewWrite: s.viewWrite, sources: s.querySources, unique: s.uniqueBinding, conflict: s.insertConflict, returning: s.insertReturning, partition: s.partitionInsert, locks: s.queryLocks, admission: s.queryLockAdmission, ruleAction: s.ruleAction, quotes: s.quoteCaptures}
}
func (s *StatementScope) installRuleScratch(c ruleCommandScratch) {
	s.statementMutation = c.mutation
	s.lockedRef, s.lockedDescriptor, s.lockedStorage = c.ref, c.descriptor, c.storage
	s.viewWrite, s.querySources = c.viewWrite, c.sources
	s.uniqueBinding, s.insertConflict, s.insertReturning, s.partitionInsert = c.unique, c.conflict, c.returning, c.partition
	s.queryLocks, s.queryLockAdmission = c.locks, c.admission
	s.ruleAction = c.ruleAction
	s.quoteCaptures = c.quotes
}

func bindingRuleRelation(b analyze.Binding) *catalog.Relation {
	return physicalBinding(b).Relation
}

func (e *Engine) executeRuleProgramLocked(scope *StatementScope, stmt parser.Statement, b analyze.Binding, auth AuthorizationView, parameters []sqltype.Value, candidate StatementDescription, quoteChanged bool) (Result, error) {
	program := ruleProgram{count: -1, returning: -1}
	count, err := ruleRewriter{e.catalog.owner()}.expandLocked(auth, stmt, b, nil, &program)
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	program.count = count
	if tag := modifyingTag(stmt); scope.owner.ReadOnly && tag != "" && !temporaryDataTarget(auth, &b, stmt) {
		e.mu.Unlock()
		return Result{}, sqlError("25006", "cannot execute "+tag+" in a read-only transaction")
	}
	for i := range program.commands {
		command := &program.commands[i]
		if err := e.normalizeRuleCTEsLocked(scope, command.auth, command.syntax, &command.binding); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
	}
	if err := checkRowSecurityRuleProgram(scope, auth, b, program); err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	// Empty suppression has no executor. Nonempty actions retain caller ACL
	// obligations, after every original/action row-security rewrite guard.
	if len(program.commands) != 0 {
		if err := analyze.CheckBinding(auth, b, stmt); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
	}
	for i := range program.commands {
		command := &program.commands[i]
		if err := analyze.CheckBinding(ruleAuthorizationOf(command.auth), command.binding, command.syntax); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
	}
	refs, err := e.admitRuleCommandsLocked(scope, stmt, &program)
	if err != nil {
		return Result{}, err
	}
	if len(refs) != 0 {
		if _, err := scope.acquireDataViewsLocked(e, refs); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
	}
	e.mu.Unlock()
	scope.routineAuth = auth
	original := scope.ruleScratch()
	originalMutation := scope.activeMutation
	originalWrites, originalDeletes := scope.ownWrites, scope.ownDeletes
	originalView := scope.view
	defer func() {
		scope.installRuleScratch(original)
		scope.activeMutation = originalMutation
		scope.ownWrites, scope.ownDeletes = originalWrites, originalDeletes
		if originalView != nil && scope.view != originalView {
			originalView.Release()
		}
	}()
	result := Result{}
	failed := true
	defer func() {
		if failed {
			if result.Rows != nil {
				result.Rows.Close()
			}
			for _, rows := range result.RowSets {
				rows.Close()
			}
		}
	}()
	prepared := make([]preparedCTEProgram, len(program.commands))
	for i, command := range program.commands {
		if err := scope.op.Context().Err(); err != nil {
			return Result{}, err
		}
		physical := physicalBinding(command.binding)
		commandAuth := ruleAuthorizationOf(command.auth)
		scope.installRuleScratch(ruleCommandScratch{mutation: statementMutation{statement: command.syntax}, ref: physical.Ref, descriptor: physical.Relation, storage: physical.Storage, viewWrite: viewWriteOf(command.binding.ViewWrite), sources: command.binding.QuerySources, ruleAction: ruleActionScope{auth: &commandAuth}})
		captures := &exec.QuoteCaptureCandidate{}
		if command.binding.Parameters.Count == 0 {
			*captures = exec.QuoteCaptureCandidate{Entries: candidate.quotes, Private: quoteChanged}
			pruneQuoteCaptures(captures, commandAuth.Catalog)
		}
		evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: scope.op.Context(), Parameters: parameters, Host: hostOf(scope), Catalog: commandAuth.Catalog, Input: scope.input, Output: scope.output, QuoteCaptures: captures, PlanStaticOperators: true, SpecializeRuleParameters: true}}
		e.mu.Lock()
		if _, err := e.prepareCTECommandsLocked(scope, command.syntax, command.binding, commandAuth, parameters, evaluation, nil, nil, &prepared[i]); err != nil {
			return Result{}, err
		}
	}
	for i, command := range program.commands {
		if err := scope.op.Context().Err(); err != nil {
			return Result{}, err
		}
		scope.activeMutation = prepared[i].mainMutation
		scope.installRuleScratch(prepared[i].mainScratch)
		// Immutable prefix roots are visible to this program only. No owner merge
		// or newer committed frontier is introduced between actions.
		if originalView != nil {
			data := *originalView.Data()
			data.Own = maps.Clone(originalWrites)
			if data.Own == nil && len(scope.stage.rows) != 0 {
				data.Own = make(map[ids.ObjectAddress][]storage.RowVersion, len(scope.stage.rows))
			}
			for address, rows := range scope.stage.rows {
				data.Own[address] = mergePrivateRows(originalWrites[address], rows, false)
			}
			data.Deleted = mergeDeletions(originalDeletes, scope.stage.deletes)
			if scope.view != originalView {
				scope.view.Release()
			}
			scope.view = visibility.NewView(&data)
			scope.ownWrites, scope.ownDeletes = data.Own, data.Deleted
		}
		e.mu.Lock()
		output, err := e.executePreparedCTECommandsLocked(scope, command.syntax, &prepared[i], parameters, nil, nil)
		if err != nil {
			if output.Rows != nil {
				output.Rows.Close()
			}
			for _, rows := range output.RowSets {
				rows.Close()
			}
			return Result{}, err
		}
		if i == program.count {
			result.Count = output.Count
		}
		if output.Rows != nil {
			if command.binding.Returning != nil && (i == program.returning || i == program.count) {
				result.Rows = output.Rows
			} else if _, selecting := command.syntax.(*parser.Select); selecting && b.Returning == nil && scope.Expected == nil {
				result.RowSets = append(result.RowSets, output.Rows)
			} else {
				output.Rows.Close()
			}
		}
		for _, rows := range output.RowSets {
			if b.Returning == nil && scope.Expected == nil {
				result.RowSets = append(result.RowSets, rows)
			} else {
				rows.Close()
			}
		}
	}
	failed = false
	return result, nil
}
