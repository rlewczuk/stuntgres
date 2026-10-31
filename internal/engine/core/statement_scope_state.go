package core

import (
	"context"
	"sync"
	"time"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

// StatementScope serializes one session's statement, from parsing through Finish.
// Binding uses current resolution metadata; data consumers explicitly acquire a
// policy-selected committed view. Writes stage effects here, never in storage.
type StatementScope struct {
	StatementMutation
	explain                 *explainExecution
	explainAdmission        *explainAdmission
	aggregateGateOnce       sync.Once
	aggregateGate           chan struct{}
	invocationContext       context.Context // Scoped under aggregateGate; inherited by nested commands.
	copyInputRaw            context.Context // Only actual session-owned standard effective children.
	copyInputActive         context.Context // Invocation-private projection, retired at Finish.
	currentSnapshot         *sqltype.Snapshot
	snapshotFrontier        ids.CommitSeq
	snapshotTarget          visibility.Root
	snapshotTargets         map[ids.ObjectAddress]visibility.Root
	snapshotTargetAdmission bool // Actual snapshotPhase command, not CTE bookkeeping.
	mutations               []StatementMutation
	ActiveMutation          int
	cteInvocation           *exec.CTEInvocation
	self                    *StatementScope
	Owner                   *session.TransactionOwner
	Op                      *session.Operation
	ruleAction              RuleActionScope
	RowSecurity             bool                      // Immutable effective setting for this admitted statement.
	PlannerMethods          plan.PlannerMethods       // Immutable effective statement admission.
	rlsActive               []ids.ObjectAddress       // Active policy expansion path, not an occurrence cache.
	rlsPrograms             []*rowSecurityProgram     // Owns runtime plus original selected admission identities.
	rlsAdmissionDepth       int                       // Whole target/source admission, including recursive policies.
	rlsCommand              *preparedCommandExecution // Active original logical command, lexical scratch.
	catalogKeep             *catalogKeepQuery         // Set only while a SELECT captures its own leaves.
	rlsImportWaits          uint64                    // Scope-monotonic actual policy-source waits, never row work.
	rlsDataRefs             []ids.ObjectRef           // Complete prepared physical union before the first snapshot.
	authority               *exec.Authority
	// Borrowed only during this statement's admission/execution. Admission
	// refreshes this coherent snapshot serially before publishing parallel reads;
	// it is neither prepared-plan state nor a cached permission decision.
	RoutineAuth                 AuthorizationView
	Stage                       WriteStage
	alterNames                  map[ids.NameKey]ids.ObjectRef // Phased ALTER claims acquired only at Finish.
	notificationActions         []session.NotificationAction
	notificationEvents          []session.Notification
	View                        *visibility.View
	Finished                    bool
	copyProgress                *copyProgress // Finish-owned COPY lifetime, before transaction publication.
	indexProgress               *indexProgress
	message                     bool
	messageEnded                bool
	messageResult               error
	result                      error
	worked                      bool
	Completed                   bool
	sessionCompleted            bool // Successful session utilities have no private stage to merge.
	controlOutcome              controlOutcome
	ReplacementRead             bool
	LockTicket                  *lock.Ticket
	multiEvents                 []lock.MultiEvent
	QuerySources                []exec.Leaf
	QueryLockAdmission          analyze.LockAdmission
	QueryLockContext            *exec.LockContext
	TxRef                       ids.TxRef
	CommandID                   ids.CommandID
	OwnWrites                   map[ids.ObjectAddress][]storage.RowVersion
	OwnDeletes                  map[ids.ObjectAddress]*visibility.PrivateDeletes
	Expected                    *Description
	admissionWaited             bool
	implicit                    bool
	Input                       sqltype.InputEnvironment
	routinePrograms             []commandRoutineProgram                 // Consumed command graphs; same admission lease as exec.Authority.
	checkPrograms               map[*catalog.Relation]exec.CheckProgram // Lazy ordinary CHECK preparation; shared by borrowed evaluations.
	statementStart              time.Time
	transactionStart            time.Time
	lease                       session.StatementLease // identity for session-layer state; never copied on purpose
	Output                      sqltype.OutputEnvironment
	initialParameters           []sqltype.Usage
	activeSQLRoutineDepth       int             // Root invocation owner; never saved/reset as lexical scratch.
	activeTriggerDepth          int             // Actual user/RI invocations, independent of routine safety depth.
	plAtomic                    bool            // Captured command context, then defer-scoped active invocation barriers.
	plLoops                     *plQueryRows    // Borrowed active adapters; not a named portal registry.
	plExecutingPortal           *protocolPortal // Current CALL/DO portal survives its own internal boundaries.
	routineLive                 *routineLive
	expressionLive              *plExpressionLive
	effectCommand, effectSerial uint64 // admission-private command event ownership; never rewound
	afterTriggers               *afterTriggerState
	deferAfterProducers         bool                       // Lexical eager-CTE materialization boundary.
	deferredAfterCommand        uint64                     // Nested routine queries never borrow that boundary.
	FoldCaptures                *exec.FoldCaptureCandidate // sealed before executor/source publication
	Catalog                     *session.Journal
	foreignConstructions        map[ids.ObjectAddress]*ForeignConstruction // Statement-owned, never lexical routine scratch.
	EventCommand                *EventCommand                              // Borrowed lexical boundary; never publication authority.
	largeObjectRead             catalog.Read                               // Immutable active-query READ view, saved/restored by SQL body activation.
	largeObjectOpening catalog.Read // Descriptor-only active frontier, including stable isolation base.
	CatalogAuthority            *session.CatalogAuthority
	ProjectedRoots              bool // Stage owns complete private roots, not append-only suffixes.
	ForeignActionAuth           *AuthorizationView
	Identity                    exec.IdentitySnapshot
	// Feature state lives in slots. Groups that every statement writes are
	// values. A pointer slot xSlot stays nil until its first write through
	// x(); reads use xOrZero(), which never allocates, so nil means all zero.
	// Only the SQL routine scratch swaps slot pointers (protocol,
	// presentation); nothing else resets a slot to nil. A slot that one
	// family owns is an opaque StatementSlot (DropsSlot).
	Inquiry            inquirySnapshot
	prepared           preparedRebind
	sqlRoutine         sqlRoutineDemand
	Inheritance        inheritanceScope
	Domains            domainScope
	protocolSlot       *protocolScope
	presentationSlot   *presentationCaches
	truncateSlot       *dml.TruncateScope
	DropsSlot          StatementSlot
	foreignActionsSlot *dml.ForeignActionScope
	ForeignKeysSlot    *table.ForeignKeyCreation
	viewCreationSlot   *view.CreationScope
	MaterializedSlot   *view.MaterializedRefreshScope // Lexical admission; routine activation restores exact identity.
	indexesSlot        *index.Scope
	partitionsSlot     *partition.Scope
	foreignTablesSlot  *foreignTableScope
}

// StatementSlot is a statement slot that one family owns: StatementScope
// holds it opaquely and the family recovers its type with one checked
// helper. Core only keeps it across PL/pgSQL exception blocks: the
// checkpoint saves CloneSlot() and a rollback restores the saved slot.
type StatementSlot interface {
	CloneSlot() StatementSlot
}

type WriteStage struct {
	self      *WriteStage
	scope     *StatementScope
	Rows      map[ids.ObjectAddress][]storage.RowVersion
	Deletes   map[ids.ObjectAddress]*visibility.PrivateDeletes
	Replacing bool // Rows replace complete private roots instead of appending.
}
