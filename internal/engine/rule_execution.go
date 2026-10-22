package engine

import (
	"maps"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/view"
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
	viewWrite  *view.Write
	sources    []exec.Leaf
	unique     *index.UniqueWriteBinding
	conflict   *dml.InsertConflictState
	returning  *dml.InsertReturningOutput
	partition  *partition.InsertState
	locks      *exec.LockContext
	admission  analyze.LockAdmission
	ruleAction ruleActionScope
	quotes     *exec.QuoteCaptureCandidate
}

func (s *StatementScope) ruleScratch() ruleCommandScratch {
	return ruleCommandScratch{mutation: s.statementMutation, ref: s.lockedRef, descriptor: s.lockedDescriptor, storage: s.lockedStorage, viewWrite: s.ViewWrite, sources: s.querySources, unique: s.UniqueBinding, conflict: s.InsertConflict, returning: s.InsertReturning, partition: s.PartitionInsert, locks: s.queryLocks, admission: s.queryLockAdmission, ruleAction: s.ruleAction, quotes: s.quoteCaptures}
}
func (s *StatementScope) installRuleScratch(c ruleCommandScratch) {
	s.statementMutation = c.mutation
	s.lockedRef, s.lockedDescriptor, s.lockedStorage = c.ref, c.descriptor, c.storage
	s.ViewWrite, s.querySources = c.viewWrite, c.sources
	s.UniqueBinding, s.InsertConflict, s.InsertReturning, s.PartitionInsert = c.unique, c.conflict, c.returning, c.partition
	s.queryLocks, s.queryLockAdmission = c.locks, c.admission
	s.ruleAction = c.ruleAction
	s.quoteCaptures = c.quotes
}

func (e *Engine) executeRuleProgramLocked(scope *StatementScope, stmt parser.Statement, b analyze.Binding, auth AuthorizationView, parameters []sqltype.Value, candidate StatementDescription, quoteChanged bool) (Result, error) {
	program := view.RuleProgram{Count: -1, Returning: -1}
	count, err := view.RuleRewriter{Owner: e.catalog.owner()}.ExpandLocked(auth, stmt, b, nil, &program)
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	program.Count = count
	if tag := modifyingTag(stmt); scope.owner.ReadOnly && tag != "" && !temporaryDataTarget(auth, &b, stmt) {
		e.mu.Unlock()
		return Result{}, sqlError("25006", "cannot execute "+tag+" in a read-only transaction")
	}
	for i := range program.Commands {
		command := &program.Commands[i]
		if err := view.NormalizeRuleCTEsLocked(e.catalog.owner(), command.Auth, command.Syntax, &command.Binding); err != nil {
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
	if len(program.Commands) != 0 {
		if err := analyze.CheckBinding(auth, b, stmt); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
	}
	for i := range program.Commands {
		command := &program.Commands[i]
		if err := analyze.CheckBinding(ruleAuthorizationOf(command.Auth), command.Binding, command.Syntax); err != nil {
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
	prepared := make([]preparedCTEProgram, len(program.Commands))
	for i, command := range program.Commands {
		if err := scope.op.Context().Err(); err != nil {
			return Result{}, err
		}
		physical := view.PhysicalBinding(command.Binding)
		commandAuth := ruleAuthorizationOf(command.Auth)
		scope.installRuleScratch(ruleCommandScratch{mutation: statementMutation{statement: command.Syntax}, ref: physical.Ref, descriptor: physical.Relation, storage: physical.Storage, viewWrite: view.ViewWriteOf(command.Binding.ViewWrite), sources: command.Binding.QuerySources, ruleAction: ruleActionScope{auth: &commandAuth}})
		captures := &exec.QuoteCaptureCandidate{}
		if command.Binding.Parameters.Count == 0 {
			*captures = exec.QuoteCaptureCandidate{Entries: candidate.quotes, Private: quoteChanged}
			pruneQuoteCaptures(captures, commandAuth.Catalog)
		}
		evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: scope.op.Context(), Parameters: parameters, Host: hostOf(scope), Catalog: commandAuth.Catalog, Input: scope.input, Output: scope.output, QuoteCaptures: captures, PlanStaticOperators: true, SpecializeRuleParameters: true}}
		e.mu.Lock()
		if _, err := e.prepareCTECommandsLocked(scope, command.Syntax, command.Binding, commandAuth, parameters, evaluation, nil, nil, &prepared[i]); err != nil {
			return Result{}, err
		}
	}
	for i, command := range program.Commands {
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
		output, err := e.executePreparedCTECommandsLocked(scope, command.Syntax, &prepared[i], parameters, nil, nil)
		if err != nil {
			if output.Rows != nil {
				output.Rows.Close()
			}
			for _, rows := range output.RowSets {
				rows.Close()
			}
			return Result{}, err
		}
		if i == program.Count {
			result.Count = output.Count
		}
		if output.Rows != nil {
			if command.Binding.Returning != nil && (i == program.Returning || i == program.Count) {
				result.Rows = output.Rows
			} else if _, selecting := command.Syntax.(*parser.Select); selecting && b.Returning == nil && scope.Expected == nil {
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
