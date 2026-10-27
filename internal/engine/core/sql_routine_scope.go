package core

import (
	"maps"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// This is a native active-body safety bound, not PostgreSQL's byte-valued
// max_stack_depth setting. Mutual and direct calls borrow the same root owner.
const maxSQLRoutineDepth = 128

func (s *StatementScope) enterSQLRoutine() error {
	if s.activeSQLRoutineDepth >= maxSQLRoutineDepth {
		return SQLError("54001", "stack depth limit exceeded")
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
	command             ruleCommandScratch
	mutations           []StatementMutation
	activeMutation      int
	cteInvocation       *exec.CTEInvocation
	activeViews         []ids.ObjectAddress
	inheritance         inheritanceScope
	materialized        *view.MaterializedRefreshScope
	largeObjectRead     catalog.Read
	authority           *exec.Authority
	routineAuth         AuthorizationView
	routineDependencies []catalog.ExpressionDependency
	expected            *Description
	prepared            preparedRebind
	admissionWaited     bool
	sqlRoutine          sqlRoutineDemand
	inquiry             inquirySnapshot
	initialParameters   []sqltype.Usage
	protocol            *protocolScope // Swapped as a unit: a nested body starts with no protocol slot.
	input               sqltype.InputEnvironment
	output              sqltype.OutputEnvironment
	view, partitionView *visibility.View
	rlsDataRefs         []ids.ObjectRef // Same lexical snapshot owner as view; not a global source cache.
	ownWrites           map[ids.ObjectAddress][]storage.RowVersion
	ownDeletes          map[ids.ObjectAddress]*visibility.PrivateDeletes
	domainEvaluation    *exec.Context
	domainInputCatalog  catalog.Read
	foreignOwner        *exec.ForeignScanOwner
	foreignCursor       *sqlCursor
	presentation        *presentationCaches // Swapped as a unit: a nested body starts with empty caches.
	identity            exec.IdentitySnapshot
	completed           bool // Nested utility outcome, not root row publication.
	sessionCompleted    bool
}

func (s *StatementScope) sqlRoutineScratch() sqlRoutineScratch {
	return sqlRoutineScratch{
		command: s.ruleScratch(), mutations: s.mutations, activeMutation: s.ActiveMutation, cteInvocation: s.cteInvocation,
		activeViews: s.ViewCreationOrZero().Active, inheritance: s.Inheritance,
		materialized: s.MaterializedSlot,
		authority:    s.authority, routineAuth: s.RoutineAuth, expected: s.Expected,
		routineDependencies: s.RoutineDependencies,
		prepared:            s.prepared, admissionWaited: s.admissionWaited,
		initialParameters: s.initialParameters, input: s.Input, output: s.Output,
		sqlRoutine: s.sqlRoutine, inquiry: s.Inquiry,
		protocol: s.protocolSlot,
		view:     s.View, partitionView: s.PartitionsOrZero().ValidationView, ownWrites: s.OwnWrites, ownDeletes: s.OwnDeletes,
		domainEvaluation: s.Domains.evaluation, domainInputCatalog: s.Domains.InputCatalog, foreignOwner: s.foreignTablesOrZero().owner, foreignCursor: s.foreignTablesOrZero().cursor,
		presentation: s.presentationSlot, identity: s.Identity,
		largeObjectRead: s.largeObjectRead,
		rlsDataRefs:     s.rlsDataRefs,
		completed:       s.Completed, sessionCompleted: s.sessionCompleted,
	}
}
func (s *StatementScope) installSQLRoutineScratch(c sqlRoutineScratch) {
	s.installRuleScratch(c.command)
	s.mutations, s.ActiveMutation, s.cteInvocation = c.mutations, c.activeMutation, c.cteInvocation
	s.setActiveViews(c.activeViews)
	s.Inheritance = c.inheritance
	s.MaterializedSlot = c.materialized
	s.authority, s.RoutineAuth, s.Expected = c.authority, c.routineAuth, c.expected
	s.RoutineDependencies = c.routineDependencies
	s.prepared, s.admissionWaited = c.prepared, c.admissionWaited
	s.initialParameters, s.sqlRoutine, s.Inquiry = c.initialParameters, c.sqlRoutine, c.inquiry
	s.protocolSlot = c.protocol
	s.Input, s.Output = c.input, c.output
	s.View = c.view
	s.setPartitionValidationView(c.partitionView)
	s.OwnWrites, s.OwnDeletes = c.ownWrites, c.ownDeletes
	s.Domains.evaluation, s.Domains.InputCatalog = c.domainEvaluation, c.domainInputCatalog
	s.setForeignLexical(c.foreignOwner, c.foreignCursor)
	s.presentationSlot, s.Identity = c.presentation, c.identity
	s.largeObjectRead = c.largeObjectRead
	s.rlsDataRefs = c.rlsDataRefs
	s.Completed, s.sessionCompleted = c.completed, c.sessionCompleted
}

// ActivateSQLRoutine borrows the statement journal with fresh lexical command
// scratch. The caller must invoke the returned restoration exactly once.
func (s *StatementScope) ActivateSQLRoutine(inputs []sqltype.Usage, largeObjectRead catalog.Read) func(error) error {
	saved := s.sqlRoutineScratch()
	// Preserve the live operation's presentation/time policy, but create fresh
	// provider/capture/query state. The parameter frame itself is proc-owned.
	ownWrites := saved.ownWrites
	policy := saved.sqlRoutine.policy
	if policy.readOnly {
		ownWrites = policy.writes
	}
	if !policy.readOnly && len(s.Stage.Rows) != 0 {
		ownWrites = maps.Clone(ownWrites)
		if ownWrites == nil {
			ownWrites = make(map[ids.ObjectAddress][]storage.RowVersion, len(s.Stage.Rows))
		}
		for address, rows := range s.Stage.Rows {
			ownWrites[address] = visibility.MergePrivateRows(ownWrites[address], rows, true)
		}
	}
	ownDeletes := saved.ownDeletes
	if policy.readOnly {
		ownDeletes = policy.deletes
	} else {
		ownDeletes = visibility.MergeDeletions(ownDeletes, s.Stage.Deletes)
	}
	s.installSQLRoutineScratch(sqlRoutineScratch{input: saved.input, output: saved.output, initialParameters: inputs, sqlRoutine: sqlRoutineDemand{scalar: true, policy: policy}, ownWrites: ownWrites, ownDeletes: ownDeletes, largeObjectRead: largeObjectRead})
	s.Input.LockedDomainInput, s.Input.DomainChecks = s, s
	return func(primary error) error {
		if s.foreignTablesOrZero().owner != nil {
			primary = s.finishForeignOwner(s.foreignTablesOrZero().owner, primary)
		}
		if s.View != nil && s.View != saved.view {
			s.View.Release()
		}
		if s.PartitionsOrZero().ValidationView != nil && s.PartitionsOrZero().ValidationView != saved.partitionView {
			s.PartitionsOrZero().ValidationView.Release()
		}
		if s.authority != nil && s.authority != saved.authority {
			s.authority.Host = nil
		}
		s.installSQLRoutineScratch(saved)
		if policy.active {
			// A reached SET may escape this lexical command. Restoring query
			// scratch must not restore its obsolete codecs, lookup inputs or
			// identity over the operation's current effective environment.
			cleanup := s.refreshRestrictedPresentation()
			s.Input.LockedDomainInput, s.Input.DomainChecks = saved.input.LockedDomainInput, saved.input.DomainChecks
			if primary == nil {
				primary = cleanup
			}
		}
		return primary
	}
}

// sqlRoutineDemand is the internal SQL routine result demand of a statement.
type sqlRoutineDemand struct {
	lexical            *proc.PLFrame
	plDiagnostic       string                // Invocation-private cached PL signature; balanced with lexical scratch.
	plPlanning         bool                  // PL diagnostic provenance; cleared at the real executor handoff.
	closedParameters   bool                  // Reached dynamic USING frame, including an empty frame.
	assignment         *analyze.PLAssignment // Reached semantic-datum projection, never retained.
	assignmentPlanning bool                  // Borrowed PL origin for a called SQL body, not its destination.
	scalar             bool                  // Internal single-row SELECT demand, never public finalization.
	plArgumentRow      bool                  // NonSTRICT bound-argument projection: one row, always SPI.
	rewrite            bool                  // Nested all-row ts_rewrite demand, with separate open/fetch diagnostics.
	readOnlySPI        bool                  // Connectby classifies the canonical executable binding before execution.
	policy             routineCommandPolicy  // Lexical invocation policy, shared by its reached commands only.
	rows               bool                  // Private all-row PL demand; no rewrite policy or scalar cardinality.
	plCapture          bool                  // Bounded primary SELECT capture, including after writable CTE completion.
	sourceBase         *visibility.Data      // Borrowed coherent roots for runtime-named nested sources; saved outer View owns them.
	result             sqltype.Usage
	resultPlan         proc.ResultPlan
	definition         *proc.Definition
	name               string // Live declaration qualifier, separate from immutable body syntax.
	terminal           bool
	binding            *analyze.Binding
	plCallBinding      *analyze.Binding     // One reached winner; never a frozen SQL-body recipe.
	inputCollation     sqltype.CollationRef // Only quoted aggregate-support formal annotation.
	cursor             *parser.DeclareCursor
	cursorName         *string // nil means allocate only at actual installation; explicit empty stays distinct.
	openedCursor       *sqlCursor
	openedCursorName   string
}
