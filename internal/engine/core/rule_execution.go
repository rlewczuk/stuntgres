package core

import (
	"maps"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// RuleActionScope is the rule-owner authority of the rule action command now
// executing; nil auth means no rule action is active. Command switches save
// and restore it as one unit.
type RuleActionScope struct {
	Auth *AuthorizationView
}

// Only command-local target/source scratch changes. Publication, the operation,
// parameter frame, original signature, snapshot frontier and budget remain owned
// by the original StatementScope and its one WriteStage/Finish.
type ruleCommandScratch struct {
	Mutation   StatementMutation
	Ref        ids.ObjectRef
	Descriptor catalog.Descriptor
	Storage    *storage.Relation
	ViewWrite  *view.Write
	Sources    []exec.Leaf
	unique     *index.UniqueWriteBinding
	conflict   *dml.InsertConflictState
	returning  *dml.InsertReturningOutput
	partition  *partition.InsertState
	locks      *exec.LockContext
	admission  analyze.LockAdmission
	RuleAction RuleActionScope
	quotes     *exec.QuoteCaptureCandidate
	rlsCommand *preparedCommandExecution
}

func (s *StatementScope) ruleScratch() ruleCommandScratch {
	return ruleCommandScratch{Mutation: s.StatementMutation, Ref: s.LockedRef, Descriptor: s.LockedDescriptor, Storage: s.LockedStorage, ViewWrite: s.ViewWrite, Sources: s.QuerySources, unique: s.UniqueBinding, conflict: s.InsertConflict, returning: s.InsertReturning, partition: s.PartitionInsert, locks: s.QueryLockContext, admission: s.QueryLockAdmission, RuleAction: s.ruleAction, quotes: s.quoteCaptures, rlsCommand: s.rlsCommand}
}
func (s *StatementScope) installRuleScratch(c ruleCommandScratch) {
	s.StatementMutation = c.Mutation
	s.LockedRef, s.LockedDescriptor, s.LockedStorage = c.Ref, c.Descriptor, c.Storage
	s.ViewWrite, s.QuerySources = c.ViewWrite, c.Sources
	s.UniqueBinding, s.InsertConflict, s.InsertReturning, s.PartitionInsert = c.unique, c.conflict, c.returning, c.partition
	s.QueryLockContext, s.QueryLockAdmission = c.locks, c.admission
	s.ruleAction = c.RuleAction
	s.quoteCaptures = c.quotes
	s.rlsCommand = c.rlsCommand
}

func (e *Engine) executeRuleProgramLocked(scope *StatementScope, stmt parser.Statement, b analyze.Binding, auth AuthorizationView, parameters []sqltype.Value, candidate StatementDescription, quoteChanged bool) (Result, error) {
	program := view.RuleProgram{Count: -1, Returning: -1}
	count, err := view.RuleRewriter{Owner: &e.Catalog.Owner}.ExpandLocked(auth, stmt, b, nil, &program)
	if err != nil {
		e.Mu.Unlock()
		return Result{}, err
	}
	program.Count = count
	if tag := session.ModifyingTag(stmt); scope.Owner.ReadOnly && tag != "" && !TemporaryDataTarget(auth, &b, stmt) {
		e.Mu.Unlock()
		return Result{}, SQLError("25006", "cannot execute "+tag+" in a read-only transaction")
	}
	for i := range program.Commands {
		command := &program.Commands[i]
		if err := view.NormalizeRuleCTEsLocked(&e.Catalog.Owner, command.Auth, command.Syntax, &command.Binding); err != nil {
			e.Mu.Unlock()
			return Result{}, err
		}
	}
	if err := view.CheckRowSecurityRuleProgram(scope.RowSecurity, auth.View, b, program); err != nil {
		e.Mu.Unlock()
		return Result{}, err
	}
	// Empty suppression has no executor. Nonempty actions retain caller ACL
	// obligations, after every original/action row-security rewrite guard.
	if len(program.Commands) != 0 {
		if err := analyze.CheckBinding(auth, b, stmt); err != nil {
			e.Mu.Unlock()
			return Result{}, err
		}
	}
	for i := range program.Commands {
		command := &program.Commands[i]
		if err := analyze.CheckBinding(ruleAuthorizationOf(command.Auth), command.Binding, command.Syntax); err != nil {
			e.Mu.Unlock()
			return Result{}, err
		}
	}
	refs, err := e.AdmitRuleCommandsLocked(scope, stmt, &program)
	if err != nil {
		return Result{}, err
	}
	if len(refs) != 0 {
		if _, err := scope.AcquireDataViewsLocked(e, refs); err != nil {
			e.Mu.Unlock()
			return Result{}, err
		}
	}
	e.Mu.Unlock()
	scope.RoutineAuth = auth
	original := scope.ruleScratch()
	captureOwner := program.Returning
	if captureOwner < 0 {
		captureOwner = program.Count
	}
	originalMutation := scope.ActiveMutation
	originalWrites, originalDeletes := scope.OwnWrites, scope.OwnDeletes
	originalView := scope.View
	defer func() {
		scope.installRuleScratch(original)
		scope.ActiveMutation = originalMutation
		scope.OwnWrites, scope.OwnDeletes = originalWrites, originalDeletes
		if originalView != nil && scope.View != originalView {
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
	prepared := make([]PreparedCTEProgram, len(program.Commands))
	for i, command := range program.Commands {
		if err := scope.Op.Context().Err(); err != nil {
			return Result{}, err
		}
		physical := view.PhysicalBinding(command.Binding)
		commandAuth := ruleAuthorizationOf(command.Auth)
		scope.installRuleScratch(ruleCommandScratch{Mutation: StatementMutation{Statement: command.Syntax}, Ref: physical.Ref, Descriptor: physical.Relation, Storage: physical.Storage, ViewWrite: view.ViewWriteOf(command.Binding.ViewWrite), Sources: command.Binding.QuerySources, RuleAction: RuleActionScope{Auth: &commandAuth}})
		// Preparation saves this command's scratch for execution. Only the
		// designated result provider inherits the original capture policy.
		if i == captureOwner {
			scope.Command.Capture = original.Mutation.Command.Capture
		}
		captures := &exec.QuoteCaptureCandidate{}
		if command.Binding.Parameters.Count == 0 {
			*captures = exec.QuoteCaptureCandidate{Entries: candidate.Quotes, Private: quoteChanged}
			stored.PruneQuoteCaptures(captures, commandAuth.Catalog)
		}
		evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: scope.Op.Context(), Parameters: parameters, Host: HostOf(scope), Catalog: commandAuth.Catalog, Input: scope.Input, Output: scope.Output, QuoteCaptures: captures, PlanStaticOperators: true, SpecializeRuleParameters: true}}
		e.Mu.Lock()
		if _, err := e.PrepareCTECommandsLocked(scope, command.Syntax, command.Binding, commandAuth, parameters, evaluation, nil, nil, &prepared[i]); err != nil {
			return Result{}, err
		}
	}
	for i, command := range program.Commands {
		if err := scope.Op.Context().Err(); err != nil {
			return Result{}, err
		}
		scope.ActiveMutation = prepared[i].MainMutation
		scope.installRuleScratch(prepared[i].MainScratch)
		// Immutable prefix roots are visible to this program only. No owner merge
		// or newer committed frontier is introduced between actions.
		if originalView != nil {
			data := *originalView.Data()
			data.Own = maps.Clone(originalWrites)
			if data.Own == nil && len(scope.Stage.Rows) != 0 {
				data.Own = make(map[ids.ObjectAddress][]storage.RowVersion, len(scope.Stage.Rows))
			}
			for address, rows := range scope.Stage.Rows {
				data.Own[address] = visibility.MergePrivateRows(originalWrites[address], rows, false)
			}
			data.Deleted = visibility.MergeDeletions(originalDeletes, scope.Stage.Deletes)
			if scope.View != originalView {
				scope.View.Release()
			}
			scope.View = visibility.NewView(&data)
			scope.OwnWrites, scope.OwnDeletes = data.Own, data.Deleted
		}
		e.Mu.Lock()
		output, err := e.ExecutePreparedCTECommandsLocked(scope, command.Syntax, &prepared[i], parameters, nil, nil)
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
