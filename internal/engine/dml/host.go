package dml

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Host is the engine side of the write executors: one statement's runtime,
// its unique and exclusion windows, its foreign write runtime, and the DML
// pieces below. Every piece has one owner, so Host composes them rather than
// exposing engine internals. The engine's statementHost implements it. No
// method takes a callback; the lock contract is stated per method, and a
// caller never holds Engine.mu.
type Host interface {
	exec.Host        // context, environments, replacement read, foreign enlistment, ...
	index.WriteHost  // unique and exclusion windows and waits
	fdw.WriteRuntime // foreign participants and modification tracking
	Statement
	Stage
	RowLocks
	Presentation
	// BeforeRow captures live trigger provenance under synchronization, then
	// admits/evaluates WHEN and the PL body unlocked in the caller's stage.
	BeforeRow(BeforeRow) (BeforeResult, error)
}

// FinishHost is Host plus the foreign-key runtime that statement Finish runs:
// FK actions, then FK checks. The engine's statementHost implements it.
type FinishHost interface {
	Host
	ForeignKeys
}

// Authority is the statement authority that FK enforcement and actions check
// and evaluate with. The engine's *AuthorizationView implements it, so it
// doubles as an exec.Context's RoutineAuthority.
type Authority interface {
	// Access is the privilege view the authority checks against.
	Access() access.View
	access.Environment
	exec.AdmissionAuthority
	// ActingAs is a copy of the authority running as owner, as RI actions run
	// as the referencing table's owner. A zero owner returns the receiver.
	ActingAs(owner authorization.PrincipalRef) Authority
}

// ForeignKeys is the FK runtime's engine side. The caller holds nothing; the
// lock contract is stated per method.
type ForeignKeys interface {
	// Staged is the statement's staged rows and deletions by relation. Never
	// locks; valid until the next host call that stages.
	Staged() (map[ids.ObjectAddress][]storage.RowVersion, map[ids.ObjectAddress]*visibility.PrivateDeletes)
	// ForeignKeyJobs captures the write authority over the statement's
	// private catalog and the FK jobs and relation lock modes that the staged
	// rows reach (ForeignKeyReach). Locks once, never waits.
	ForeignKeyJobs() ([]ForeignKeyJob, map[ids.ObjectRef]lock.Mode, Authority, error)
	// RecaptureAuthority is a copy of a over the statement's current private
	// catalog. Locks once.
	RecaptureAuthority(a Authority) Authority
	// ForeignKeyAuthority validates the operation and captures its authority
	// over the statement's private catalog. Locks twice, never waits.
	ForeignKeyAuthority() (Authority, error)
	// ForeignKeyRows is the borrowed row image of ref, with idx's index root
	// when idx is set. Locks once.
	ForeignKeyRows(ref ids.ObjectRef, idx ids.ObjectAddress) (table.ForeignKeyRows, error)
	// RetainedForeignKeyRows is current over the transaction snapshot's
	// committed rows of ref under stable isolation, and empty otherwise.
	// Locks once.
	RetainedForeignKeyRows(ref ids.ObjectRef, current table.ForeignKeyRows) (table.ForeignKeyRows, error)
	// RefreshForeignKeyRows moves rows to ref's current committed rows and
	// index root. Locks once.
	RefreshForeignKeyRows(ref ids.ObjectRef, rows *table.ForeignKeyRows) error
	// GrantForeignKey takes ref (or its row) in mode. Waits with the caller
	// holding nothing, releasing the replacement read across the wait; then
	// locks once to revalidate ref and, when set, its storage.
	GrantForeignKey(ref ids.ObjectRef, row ids.RowID, mode lock.Mode, stored *storage.Relation) error
	// RefreshForeignActionJob refreshes job over the owner's private catalog
	// (RefreshForeignActionJob). Locks once.
	RefreshForeignActionJob(job *ForeignKeyJob) error
	// AdmitForeignAction admits ref as job's referencing relation for an
	// internal action, writable or read-only. Locks once.
	AdmitForeignAction(ref ids.ObjectRef, job *ForeignKeyJob, writable bool) error
	// ForeignActionRow is the latest physical version of an admitted
	// referencing row and whether it is live. Locks once.
	ForeignActionRow(ref ids.ObjectRef, candidate storage.RowVersion) (storage.RowVersion, bool, error)
	// DeleteForeignAction stages a CASCADE deletion of a granted referencing
	// row: the owner mutex then Engine.mu; flushes its physical row event after
	// unlocking.
	DeleteForeignAction(ref ids.ObjectRef, old storage.RowVersion) error
	// PlanForeignActionInput fills plan's input environment: the type
	// diagnostic names when the targets carry domain checks, and for SET
	// DEFAULT the bound defaults and retained original child-owner calls.
	// event supplies the actual action diagnostic frame. Locks; never waits.
	PlanForeignActionInput(a Authority, job *ForeignKeyJob, plan *ForeignActionPlan, event ForeignActionTransition, action parser.ForeignKeyAction) error
	// SwapForeignActionAuthority installs next as the running RI action's
	// authority (nil clears it) and returns the previous one, nil when none.
	// Never locks.
	SwapForeignActionAuthority(next Authority) Authority
}

// Statement is the active command's state. Its methods never lock and never
// wait; they read the statement at call time, because writable CTEs, rules
// and SQL routine bodies swap the active command while an executor runs.
type Statement interface {
	// Command is the active command's DML state: a pointer to the active
	// slot, so it follows every command switch.
	Command() *Command
	// Target is the admitted target and its storage.
	Target() (ids.ObjectRef, *storage.Relation)
	// PrivateRows is the statement's own and staged rows and deletions of a,
	// valid until the next host call that stages or evaluates.
	PrivateRows(a ids.ObjectAddress) PrivateRows
	// Snapshot is the owner's isolation (stable for REPEATABLE READ and
	// SERIALIZABLE) and snapshot frontier.
	Snapshot() (stable bool, frontier ids.CommitSeq)
	// UniqueJournal is the statement ticket's unique-key journal.
	UniqueJournal() lock.UniqueJournal
	// QueryAuthority is the statement's query authority, for frozen view
	// write sources.
	QueryAuthority() *exec.Authority
	// ForeignActions is the statement's FK action scope. With allocate it is
	// allocated on first use; without, it is nil until then.
	ForeignActions(allocate bool) *ForeignActionScope
	// IndexScope is the statement's index scope (existing-row outputs and
	// the exclusion projection modes), allocated like ForeignActions.
	IndexScope(allocate bool) *index.Scope
	// Ticket is the statement's transaction lock ticket.
	Ticket() *lock.Ticket
	// CatalogRead is a read of state owned by the engine's catalog store.
	CatalogRead(state *catalog.State) catalog.Read
	// PositionedRow is the row WHERE CURRENT OF names in b, read from the
	// session's cursor registry.
	PositionedRow(b analyze.Binding) (MutationRowIdentity, error)
	// CheckWriteBinding is the ON CONFLICT binding privilege check over v.
	CheckWriteBinding(v access.View, b analyze.Binding) error
	// DisclosureEnv is the operation's access environment over v, for key
	// disclosure decisions on error paths.
	DisclosureEnv(v access.View) access.Environment
	// CommandError maps a statement timeout or cancellation of the
	// operation to its SQL error.
	CommandError(err error) error
	// CopyCommandError is CommandError for COPY, which also maps wire
	// protocol cancellation.
	CopyCommandError(err error) error
	// Operation is the statement's operation: its COPY input state, pending
	// COPY input error and context source.
	Operation() *session.Operation
}

// PrivateRows is the statement's view of one relation's private rows: the
// transaction's own writes and deletions and this statement's staged ones.
type PrivateRows struct {
	Own           []storage.RowVersion
	OwnDeletes    *visibility.PrivateDeletes
	Staged        []storage.RowVersion
	StagedDeletes *visibility.PrivateDeletes
}

// Stage is the write stage's windows. Each takes the owner mutex and then
// Engine.mu itself, never waits, and expects the caller to hold nothing.
// The work around the windows (key classification, row grants, index
// candidate validation and revalidation after a stale binding) is the
// caller's.
type Stage interface {
	// Active fails once the statement has finished or its operation is no
	// longer current. It takes no engine lock.
	Active() error
	// ReadmitRevalidation recaptures the statement's write authority on
	// current's catalog under Engine.mu, then readmits the prepared programs
	// and prior's index programs unlocked, before rows staged under prior
	// are revalidated. Never waits.
	ReadmitRevalidation(prior, current *index.UniqueWriteBinding) error
	// PrepareReplacement checks the stage authority, the active statement,
	// inherited replacements and an internal FK action's admission and row
	// grants, in that order, before a replacement.
	PrepareReplacement(ref ids.ObjectRef, old []storage.RowVersion) error
	// StageInsert stages a validated insert batch. stale reports that the
	// unique binding's catalog moved; nothing is staged and the caller
	// revalidates and retries.
	StageInsert(ref ids.ObjectRef, batch index.WriteBatch) (stale bool, err error)
	// StageReplacement stages the successors of old in lane. modes holds the
	// per-row action intents when batch has no ActionMode. stale is as for
	// StageInsert. The physical row events are flushed after unlocking on
	// success and dropped on failure.
	StageReplacement(ref ids.ObjectRef, old []storage.RowVersion, batch index.WriteBatch, modes []lock.MultiMode, lane ReplaceLane) (stale bool, err error)
	// AppendAcceptedInsert appends one validated RETURNING insert, checking
	// the INSERT binding in the same window. stale is as for StageInsert. c
	// is passed by value so per-row candidates stay off the heap.
	AppendAcceptedInsert(ref ids.ObjectRef, c index.WriteCandidate) (accepted, stale bool, err error)
	// Delete stages a granted deletion; it reports false when the row is no
	// longer live, and flushes its physical row event after unlocking.
	Delete(c visibility.DeleteCandidate) (bool, error)
}

// ReplaceLane is how StageReplacement stages successors.
type ReplaceLane uint8

const (
	// ReplaceMerge merges the successors into the staged rows.
	ReplaceMerge ReplaceLane = iota
	// ReplaceAppend appends them: ordinary UPDATE has already proved each
	// target distinct from the staged rows.
	ReplaceAppend
	// ReplaceConflict appends ON CONFLICT DO UPDATE successors after the
	// affected-row cardinality check.
	ReplaceConflict
)

// RowLocks is row acquisition and the latest-row reads.
type RowLocks interface {
	// AcquireRow takes c's row FOR UPDATE after checking the candidate's
	// authority. Waits with the caller holding nothing, releasing the
	// replacement read across the wait; then locks once to revalidate.
	AcquireRow(c visibility.DeleteCandidate) error
	// AcquireUpdateRow takes row in mode. Waits like AcquireRow; then locks
	// once to revalidate the target.
	AcquireUpdateRow(ref ids.ObjectRef, row ids.RowID, mode lock.Mode) error
	// AcquirePartitionLock takes a partition's relation lock in mode. Waits;
	// then locks once to record the partition.
	AcquirePartitionLock(ref ids.ObjectRef, mode lock.Mode) error
	// LatestRow reads the current version of a row for r.Kind, with that
	// kind's gate, isolation policy and errors. Locks once, never waits.
	LatestRow(r RowRequest) (LatestRow, error)
	// PhysicalRowEvent records a physical row event for row: Engine.mu
	// briefly for the transaction ID, then the lock manager. Never waits.
	PhysicalRowEvent(ref ids.ObjectRef, row storage.RowVersion, mode lock.MultiMode, successor ids.VersionID) error
}

// RowKind selects a LatestRow read.
type RowKind uint8

const (
	// RowUpdate checks both catalog epochs and the repeatable-read conflict;
	// Own reads the transaction's own writes.
	RowUpdate RowKind = iota
	// RowDelete checks that the relation storage is still Storage and the
	// repeatable-read conflict; Own reads the transaction's own writes.
	RowDelete
	// RowMerge checks both epochs, prefers the transaction's own version,
	// follows committed updates and fails only on a concurrent delete.
	RowMerge
	// RowConflictUpdate checks the ref epoch, Parent's row shape, the write
	// authority and the ON CONFLICT binding; Own reads this command's and the
	// transaction's private rows. It also reports Parent and Schema.
	RowConflictUpdate
)

// RowRequest names one row for LatestRow. Hint is the expected position of
// the row in its source.
type RowRequest struct {
	Kind    RowKind
	Ref     ids.ObjectRef
	Row     ids.RowID
	Own     bool
	Hint    int
	Storage *storage.Relation // RowDelete: the candidate's storage
	Parent  *catalog.Relation // RowConflictUpdate: the probed parent
}

// LatestRow is the current version of a requested row and whether it is
// live. Parent and Schema are set for RowConflictUpdate.
type LatestRow struct {
	Version storage.RowVersion
	Live    bool
	Parent  *catalog.Relation
	Schema  string
}

// UpdateRowRequest is the RowUpdate request of an UPDATE candidate.
func UpdateRowRequest(ref ids.ObjectRef, c UpdateCandidate) RowRequest {
	return RowRequest{Kind: RowUpdate, Ref: ref, Row: c.Row, Own: c.Born == 0, Hint: c.Slot}
}

// MergeRowRequest is the RowMerge request of a MERGE target candidate.
func MergeRowRequest(ref ids.ObjectRef, c UpdateCandidate) RowRequest {
	return RowRequest{Kind: RowMerge, Ref: ref, Row: c.Row, Hint: c.Slot}
}

// DeleteRowRequest is the RowDelete request of a deletion candidate.
func DeleteRowRequest(c visibility.DeleteCandidate) RowRequest {
	return RowRequest{Kind: RowDelete, Ref: c.Ref, Row: c.Row, Own: c.Own, Hint: c.Ordinal, Storage: c.Storage}
}

// Presentation is diagnostic and output presentation. Each method locks
// once and never waits.
type Presentation interface {
	// RowDetail is the failing-row detail of row for b, limited to the
	// columns the actor may SELECT.
	RowDetail(b *analyze.Binding, row []sqltype.Value, p DetailPurpose) (string, error)
	// SchemaName is the name of namespace ns in the statement's catalog.
	SchemaName(ns ids.ObjectAddress) (string, bool)
	// CaptureRegclassOutput captures the reg* and ACL presentation of rows.
	CaptureRegclassOutput(rows *exec.FinalRows) error
}

// DetailPurpose selects a RowDetail rendering.
type DetailPurpose uint8

const (
	// DetailConstraint is a constraint violation's detail; a row routed into
	// a partition is shown as the named target row, and an empty detail is
	// kept.
	DetailConstraint DetailPurpose = iota
	// DetailViewCheck is a view CHECK OPTION detail; it is empty when the
	// actor may see no column.
	DetailViewCheck
)

// Outcome is an executor's result: the row count and, with RETURNING, the
// output rows.
type Outcome struct {
	Count int64
	Rows  *exec.FinalRows
}

// Rowwise commands have no final query stage to finish scalar-only captures.
// A required producer close can veto their result, never a primary failure.
func (out *Outcome) finishSources(state *exec.State, err *error) {
	*err = state.FinishSources(*err)
	if *err != nil {
		if out.Rows != nil {
			out.Rows.Close()
		}
		*out = Outcome{}
	}
}

// A local FROM/USING stage can consume its producer's first required Close
// error. Settle that error before the outer source owner finishes; a later
// idempotent Close cannot recover it. An existing primary failure always wins.
func (out *Outcome) finishStage(stage *exec.Stage, err *error) {
	closeErr := stage.Close()
	if *err == nil {
		*err = closeErr
	}
	if *err != nil {
		if out.Rows != nil {
			out.Rows.Close()
		}
		*out = Outcome{}
	}
}
