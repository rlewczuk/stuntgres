package engine

import (
	"maps"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

// This is a native active-body safety bound, not PostgreSQL's byte-valued
// max_stack_depth setting. Mutual and direct calls borrow the same root owner.
const maxSQLRoutineDepth = 128

func (s *StatementScope) enterSQLRoutine() error {
	if s.activeSQLRoutineDepth >= maxSQLRoutineDepth {
		return sqlError("54001", "stack depth limit exceeded")
	}
	s.activeSQLRoutineDepth++
	return nil
}

func (s *StatementScope) leaveSQLRoutine() {
	s.activeSQLRoutineDepth--
}

// SQL routines use the existing rule-command activation convention: one actual
// StatementScope/WriteStage/Finish and private query/admission/presentation scratch.
// This deliberately excludes effects, constraint queues and completion programs.
// The live replacement reservation is root owner state, not lexical scratch;
// existing waits/providers alone release/reacquire it and Finish sees its real flag.
// It is balanced on every nested success/error/cancellation, never concurrent.
type sqlRoutineScratch struct {
	command              ruleCommandScratch
	mutations            []statementMutation
	activeMutation       int
	cteInvocation        *cteInvocation
	activeViews          []ids.ObjectAddress
	inheritanceRelations []queryLeaf
	inheritanceCatalog   catalog.Read
	authority            *queryAuthority
	routineAuth          AuthorizationView
	expected             *StatementDescription
	prepared             preparedRebind
	admissionWaited      bool
	sqlRoutine           sqlRoutineDemand
	inquiry              inquirySnapshot
	initialParameters    []sqltype.Usage
	protocol             *protocolScope // Swapped as a unit: a nested body starts with no protocol slot.
	input                sqltype.InputEnvironment
	output               sqltype.OutputEnvironment
	view, partitionView  *visibility.View
	ownWrites            map[ids.ObjectAddress][]storage.RowVersion
	ownDeletes           map[ids.ObjectAddress]*visibility.PrivateDeletes
	domainEvaluation     *EvalContext
	domainInputCatalog   catalog.Read
	foreignOwner         *foreignScanOwner
	foreignCursor        *sqlCursor
	presentation         *presentationCaches // Swapped as a unit: a nested body starts with empty caches.
	identity             identitySnapshot
}

func (s *StatementScope) sqlRoutineScratch() sqlRoutineScratch {
	return sqlRoutineScratch{
		command: s.ruleScratch(), mutations: s.mutations, activeMutation: s.activeMutation, cteInvocation: s.cteInvocation,
		activeViews: s.activeViews, inheritanceRelations: s.inheritanceRelations, inheritanceCatalog: s.inheritanceCatalog,
		authority: s.authority, routineAuth: s.routineAuth, expected: s.Expected,
		prepared: s.prepared, admissionWaited: s.admissionWaited,
		initialParameters: s.initialParameters, input: s.input, output: s.output,
		sqlRoutine: s.sqlRoutine, inquiry: s.inquiry,
		protocol: s.protocolSlot,
		view:     s.view, partitionView: s.partitionValidationView, ownWrites: s.ownWrites, ownDeletes: s.ownDeletes,
		domainEvaluation: s.domainEvaluation, domainInputCatalog: s.domainInputCatalog, foreignOwner: s.foreignOwner, foreignCursor: s.foreignCursor,
		presentation: s.presentationSlot, identity: s.identity,
	}
}
func (s *StatementScope) installSQLRoutineScratch(c sqlRoutineScratch) {
	s.installRuleScratch(c.command)
	s.mutations, s.activeMutation, s.cteInvocation = c.mutations, c.activeMutation, c.cteInvocation
	s.activeViews, s.inheritanceRelations, s.inheritanceCatalog = c.activeViews, c.inheritanceRelations, c.inheritanceCatalog
	s.authority, s.routineAuth, s.Expected = c.authority, c.routineAuth, c.expected
	s.prepared, s.admissionWaited = c.prepared, c.admissionWaited
	s.initialParameters, s.sqlRoutine, s.inquiry = c.initialParameters, c.sqlRoutine, c.inquiry
	s.protocolSlot = c.protocol
	s.input, s.output = c.input, c.output
	s.view, s.partitionValidationView = c.view, c.partitionView
	s.ownWrites, s.ownDeletes = c.ownWrites, c.ownDeletes
	s.domainEvaluation, s.domainInputCatalog = c.domainEvaluation, c.domainInputCatalog
	s.foreignOwner, s.foreignCursor = c.foreignOwner, c.foreignCursor
	s.presentationSlot, s.identity = c.presentation, c.identity
}

func (s *StatementScope) activateSQLRoutine(inputs []sqltype.Usage) func(error) error {
	saved := s.sqlRoutineScratch()
	// Preserve the live operation's presentation/time policy, but create fresh
	// provider/capture/query state. The parameter frame itself is proc-owned.
	ownWrites := saved.ownWrites
	if len(s.stage.rows) != 0 {
		ownWrites = maps.Clone(ownWrites)
		if ownWrites == nil {
			ownWrites = make(map[ids.ObjectAddress][]storage.RowVersion, len(s.stage.rows))
		}
		for address, rows := range s.stage.rows {
			ownWrites[address] = mergePrivateRows(ownWrites[address], rows, true)
		}
	}
	ownDeletes := mergeDeletions(saved.ownDeletes, s.stage.deletes)
	s.installSQLRoutineScratch(sqlRoutineScratch{input: saved.input, output: saved.output, initialParameters: inputs, sqlRoutine: sqlRoutineDemand{scalar: true}, ownWrites: ownWrites, ownDeletes: ownDeletes})
	s.input.LockedDomainInput, s.input.DomainChecks = s, s
	return func(primary error) error {
		if s.foreignOwner != nil {
			primary = s.finishForeignOwner(s.foreignOwner, primary)
		}
		if s.view != nil && s.view != saved.view {
			s.view.Release()
		}
		if s.partitionValidationView != nil && s.partitionValidationView != saved.partitionView {
			s.partitionValidationView.Release()
		}
		if s.authority != nil && s.authority != saved.authority {
			s.authority.scope = nil
		}
		s.installSQLRoutineScratch(saved)
		return primary
	}
}

// sqlRoutineDemand is the internal SQL routine result demand of a statement.
type sqlRoutineDemand struct {
	scalar     bool // Internal single-row SELECT demand, never public finalization.
	result     sqltype.Usage
	resultPlan sqltype.CoercionPlan
}
