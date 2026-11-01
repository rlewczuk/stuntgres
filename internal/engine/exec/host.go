package exec

import (
	"context"
	"time"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Host is everything the query runtime reaches above its own layer. Every
// method is called without Engine.mu held; none returns an engine struct.
// Runtime records store the host once (never per row); a detached record holds
// an untyped nil host.
type Host interface {
	// Environment: read where the runtime reads them today, because a held
	// cursor detaches the host between fetches.
	Context() context.Context
	Environments() (sqltype.InputEnvironment, sqltype.OutputEnvironment)
	// CaptureParallelEvaluation captures immutable facts before support can
	// replace lexical scratch. Each factory call owns fresh mutable caches.
	CaptureParallelEvaluation() func() *EvaluationCapture
	StatementStart() time.Time
	// CTEInvocation is the innermost data-modifying CTE owner of the statement.
	CTEInvocation() *CTEInvocation
	// OpenNamedRelation resolves current invocation data, never retained rows.
	OpenNamedRelation(name string, schema plan.QuerySchema) (*Source, error)

	// Row locks: lock waits and current row versions at grant time.
	AcquireQueryRow(leaf Leaf, row ids.RowID, mark plan.RowMark) (bool, error)
	LatestQueryRow(leaf Leaf, candidate storage.RowVersion, hint int, mode lock.Mode) (storage.RowVersion, bool, error)
	QueryLocks() *LockContext

	// Foreign owner: release the replacement read lock before external I/O;
	// enlist the statement's foreign transaction participant.
	ReleaseReplacementRead()
	TouchForeignScan(ctx context.Context, source *ForeignQuerySource) error
	EnlistForeign(ctx context.Context, wrapper catalog.ForeignDataWrapper, request catalog.ForeignRequest, writing bool) (catalog.ForeignTransaction, error)
	ForeignCharacteristics() (*catalog.ForeignTransactionCharacteristics, error)
	AsyncAppendEnabled() bool
	AdmittedPlannerMethods() plan.PlannerMethods

	// Admission: the statement's routine admission authority and its
	// original dependency manifest. Reads statement-owned fields only; fails
	// once the statement has finished.
	RoutineAdmission() (RoutineAccess, []catalog.ExpressionDependency, error)

	// Sequences: acquires the sequence lock, then reads the generator under
	// Engine.mu.
	CompletePGSequence(ctx context.Context, ref ids.ObjectRef, row []sqltype.Value) error

	// Capabilities: the bodies behind Context's function, expression and
	// plan runtimes. They take Engine.mu, read session state or have side
	// effects (advisory locks, XID assignment, sequence calls, notices). The
	// Context forwarders keep the prologue and nil-host diagnostics.

	// functions.ACLRuntime
	ACLAuthority() *sqltype.ACLAuthority
	ObjectACL(c *Context, address ids.ObjectAddress, result *sqltype.Descriptor) (sqltype.Value, error)
	RoleMembership(c *Context, actor *functions.AccessRole, target functions.AccessRole, privilege string) (bool, error)
	RowSecurityActive(c *Context, relation functions.AccessRelation) (bool, error)
	ObjectPrivilege(c *Context, kind functions.AccessKind, actor *functions.AccessRole, object functions.AccessRole, privilege string) (bool, bool, error)
	RelationPrivilege(c *Context, kind functions.AccessKind, actor *functions.AccessRole, relation functions.AccessRelation, column functions.AccessColumn, privilege string) (bool, bool, error)
	// functions.SettingRuntime
	InvokeSetting(string, []sqltype.Value, *sqltype.Descriptor) (sqltype.Value, error)
	// functions.AdvisoryRuntime and functions.BlockingRuntime
	Advisory(c *Context, action functions.AdvisoryAction, mode functions.AdvisoryMode, lifetime functions.AdvisoryLifetime, first, second int64, pair bool, result *sqltype.Descriptor) (sqltype.Value, error)
	BlockingInquiry(kind functions.BlockingKind, pid int64, result *sqltype.Descriptor) (sqltype.Value, error)
	// functions.InputValidityRuntime
	CheckTypeInput(c *Context, value, typeName string) (*sqlstate.Error, error)
	// Replication and statistics functions
	LogicalDecoding(sqltype.Value, sqltype.Value) error
	ReplicationSlot(functions.ReplicationSlotKind, uint8, [5]sqltype.Value) error
	SyncReplicationSlots() error
	ReplicationOrigin(functions.ReplicationOriginKind, [2]sqltype.Value, *sqltype.Descriptor) (sqltype.Value, error)
	AttributeStatistics(clear, variadic bool, values []sqltype.Value) (bool, error)
	RelationStatistics(clear, variadic bool, values []sqltype.Value) (bool, error)
	// functions.EventTriggerDroppedRuntime
	EventTriggerDroppedObjects() ([][]sqltype.Value, error)
	EventTriggerDDLCommands() ([][]sqltype.Value, error)
	EventTriggerRewriteValue(bool, *sqltype.Descriptor) (sqltype.Value, error)
	// functions.SessionActivityRuntime
	SessionActivityValue(functions.SessionActivityKind, *sqltype.Descriptor) (sqltype.Value, error)
	// functions.ServerSignalRuntime
	ServerSignalValue(functions.ServerSignalKind, int64, *sqltype.Descriptor) (sqltype.Value, error)
	// functions.SessionSignalRuntime
	SessionSignalValue(*Context, functions.SessionSignalKind, int64, int64, *sqltype.Descriptor) (sqltype.Value, error)
	StatsSnapshotValue(bool, *sqltype.Descriptor) (sqltype.Value, error)
	// functions.TriggerDepthRuntime
	TriggerDepthValue(*sqltype.Descriptor) (sqltype.Value, error)
	// Capability groups (host_capabilities.go). Each accessor converts the
	// host to the group's own type: a pointer conversion, never an
	// allocation.
	Contrib() ContribRuntime
	TextSearch() TextSearchRuntime
	CatalogInquiry() CatalogInquiryRuntime
	SessionInquiry() SessionInquiryRuntime
	TransactionInquiry() TransactionInquiryRuntime
	// functions.SequenceRuntime
	Sequence(request functions.SequenceRequest) (int64, error)
	// functions.LargeObjectRuntime
	LargeObject(functions.LargeObjectRequest) (functions.LargeObjectResult, error)
	// expr.Context: session generators, conversion procedure, regex cache
	FunctionArrayRandomOwner() (expr.RandomOwner, error)
	FunctionRandomOwner() (expr.RandomOwner, error)
	FunctionUUIDReady() error
	FunctionUUIDTime() int64
	FunctionUUIDV1(multicast bool) ([16]byte, error)
	FunctionBinaryConversion(source, destination int) (*sqltype.EncodingConversionHandler, error)
	FunctionRegexExecution() *sqltype.PGRegexExecution
	// plan.Runtime
	InvokeSQLRoutine(d *catalog.Procedure, frame *proc.Frame, resultUsage sqltype.Usage, inputCollation sqltype.CollationRef, assignmentPlanning, constantPlanning bool, accepted *AcceptedDefaultInvocation) (sqltype.Value, error)
	InvokeSQLRoutineSet(d *catalog.Procedure, frame *proc.Frame, resultUsage sqltype.Usage, inputCollation sqltype.CollationRef, assignmentPlanning, constantPlanning bool) ([]sqltype.Value, error)
	PrepareAcceptedDefault(*Context, *catalog.Relation, FoldOrigin, DefaultPreparationPolicy) (expr.Expr, *AcceptedDefault, bool, error)
	MapXMLRows(functions.XMLMappingOperation, [5]sqltype.Value, *sqltype.Descriptor, sqltype.OutputEnvironment) (sqltype.Value, error)
	OpenExtensionPaths(c *Context, n plan.ExtensionPathsExpr, name string) ([]sqltype.Value, error)

	PresentationFormatters
}

// PresentationFormatters are the nine sqltype output formatters. Each call
// reads the statement's comment/deparse snapshot; without a host, output uses
// DetachedPresentation.
type PresentationFormatters interface {
	sqltype.RegclassFormatter
	sqltype.RegtypeFormatter
	sqltype.RegprocFormatter
	sqltype.RegroleFormatter
	sqltype.OperatorFormatter
	sqltype.RegcollationFormatter
	sqltype.RegnamespaceFormatter
	sqltype.RegTSFormatter
	sqltype.ACLItemFormatter
}

// EvaluationCapture closes outside-gate expression input and presentation.
// One evaluator and its synchronous derived frames own each instance.
type EvaluationCapture struct {
	Input        sqltype.InputEnvironment
	Output       sqltype.OutputEnvironment
	Presentation PresentationFormatters
	// FlushDiagnostics is called once by the coordinator after workers join,
	// including failure/cancellation. Derived states never flush their owner.
	FlushDiagnostics func()
}
