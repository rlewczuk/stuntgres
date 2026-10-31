package exec

import (
	"context"
	"time"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/fts"
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
	// functions.CatalogInquiryRuntime
	FormatType(uint32, int32, bool, *sqltype.Descriptor) (sqltype.Value, error)
	BaseType(uint32, *sqltype.Descriptor) (sqltype.Value, error)
	CatalogUserName(oid uint32, result *sqltype.Descriptor) (sqltype.Value, error)
	TablespaceLocation(oid uint32, result *sqltype.Descriptor) (sqltype.Value, error)
	TablespaceDatabase(oid uint32, result *sqltype.Descriptor) (sqltype.Value, bool, error)
	SizeInquiry(functions.SizeKind, sqltype.Value, string, *sqltype.Descriptor) (sqltype.Value, error)
	DatabaseFilesInquiry(functions.DatabaseFilesKind, sqltype.Value, sqltype.Value, *sqltype.Descriptor) (sqltype.Value, error)
	// functions.GenericFileListRuntime
	GenericFileList(functions.GenericFileListKind, string) error
	LogicalDecoding(sqltype.Value, sqltype.Value) error
	ReplicationSlot(functions.ReplicationSlotKind, uint8, [5]sqltype.Value) error
	SyncReplicationSlots() error
	ReplicationOrigin(functions.ReplicationOriginKind, [2]sqltype.Value, *sqltype.Descriptor) (sqltype.Value, error)
	AttributeStatistics(clear, variadic bool, values []sqltype.Value) (bool, error)
	RelationStatistics(clear, variadic bool, values []sqltype.Value) (bool, error)
	GenericFileRead(functions.GenericFileReadKind, string, int64, bool) error
	GenericFileTmpdir(uint32) error
	PartitionTree(uint32, *sqltype.Descriptor) (expr.SourceCursor, error)
	PartitionAncestors(uint32, *sqltype.Descriptor) (expr.SourceCursor, error)
	PartitionRoot(uint32, *sqltype.Descriptor) (sqltype.Value, error)
	RelationIsPublishable(uint32, *sqltype.Descriptor) (sqltype.Value, error)
	PublicationTables(sqltype.Value, *sqltype.Descriptor) (expr.SourceCursor, error)
	CommentInquiry(c *Context, kind functions.CommentInquiryKind, values [2]sqltype.Value, provenance expr.Info, result *sqltype.Descriptor) (sqltype.Value, error)
	DeparseInquiry(c *Context, signature functions.DeparseSignature, values [3]sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error)
	DescribeObject(c *Context, values [3]sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error)
	IdentifyObject(c *Context, values [3]sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error)
	IdentifyObjectAddress(c *Context, values [3]sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error)
	IndexProperty(kind functions.IndexPropertyKind, oid uint32, ordinalValue sqltype.Value, property string) (value, known bool, err error)
	LookupObjectAddress(c *Context, values [3]sqltype.Value) (ids.ObjectAddress, error)
	// functions.EventTriggerDroppedRuntime
	EventTriggerDroppedObjects() ([][]sqltype.Value, error)
	EventTriggerDDLCommands() ([][]sqltype.Value, error)
	EventTriggerRewriteValue(bool, *sqltype.Descriptor) (sqltype.Value, error)
	VisibilityInquiry(kind functions.VisibilityInquiryKind, value sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error)
	// functions.SessionInquiryRuntime
	CurrentSchemas(implicit bool, result *sqltype.Descriptor) (sqltype.Value, error)
	IdentityValue(c *Context, kind functions.IdentityKind, result *sqltype.Descriptor) (sqltype.Value, error)
	MyTempNamespace() (uint32, error)
	OtherTempNamespace(oid uint32) (bool, error)
	ListeningChannels(result *sqltype.Descriptor) ([]sqltype.Value, error)
	NotificationQueueUsage() (float64, error)
	// functions.SessionActivityRuntime
	SessionActivityValue(functions.SessionActivityKind, *sqltype.Descriptor) (sqltype.Value, error)
	// functions.ServerSignalRuntime
	ServerSignalValue(functions.ServerSignalKind, int64, *sqltype.Descriptor) (sqltype.Value, error)
	// functions.SessionSignalRuntime
	SessionSignalValue(*Context, functions.SessionSignalKind, int64, int64, *sqltype.Descriptor) (sqltype.Value, error)
	// functions.TriggerDepthRuntime
	TriggerDepthValue(*sqltype.Descriptor) (sqltype.Value, error)
	// functions.TransactionInquiryRuntime
	CommitTimestamp(xid uint32) (time.Time, uint32, bool, error)
	CurrentTransactionID(assign bool) (uint64, error)
	CurrentSnapshot() (*sqltype.Snapshot, error)
	LastCommittedTransaction() (uint32, time.Time, uint32, bool, error)
	MultiTransactionAge(xid uint32) (int32, error)
	TransactionAge(xid uint32) (int32, error)
	TransactionMembers(xid uint32, result *sqltype.Descriptor) ([]sqltype.Value, error)
	TransactionStatus(full uint64) (string, bool, error)
	Pgrowlocks(name string, result *sqltype.Descriptor) ([]sqltype.Value, error)
	ValidatePgrowlocks(n plan.InstalledPgrowlocksExpr) error
	ValidateTablefuncNormal(n plan.InstalledTablefuncNormalExpr) error
	ValidateConnectby(n plan.InstalledConnectbyExpr) error
	Connectby(request functions.ConnectbyRequest) ([]sqltype.Value, error)
	TablefuncCrosstab(text string, result *sqltype.Descriptor) ([]sqltype.Value, error)
	ValidateTablefuncCrosstab(n plan.InstalledTablefuncCrosstabExpr) error
	VerifyHeapam(args [6]sqltype.Value, result *sqltype.Descriptor) ([]sqltype.Value, error)
	ValidateVerifyHeapam(n plan.InstalledVerifyHeapamExpr) error
	UnaccentText(procedure ids.ObjectRef, input string, result *sqltype.Descriptor) (sqltype.Value, error)
	Amcheck(n plan.InstalledAmcheckExpr, oid uint32, options [3]bool) error
	Pgstatindex(n plan.InstalledPgstatindexExpr, input sqltype.Value) ([8]int64, [2]float64, error)
	Pageinspect(n plan.InstalledPageinspectExpr, index uint32) ([]sqltype.BrinStoredColumn, error)
	PageinspectNotice(notice sqlstate.Error)
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
	InvokeSQLRoutine(d *catalog.Procedure, frame *proc.Frame, resultUsage sqltype.Usage, inputCollation sqltype.CollationRef, assignmentPlanning, constantPlanning bool) (sqltype.Value, error)
	InvokeSQLRoutineSet(d *catalog.Procedure, frame *proc.Frame, resultUsage sqltype.Usage, inputCollation sqltype.CollationRef, assignmentPlanning, constantPlanning bool) ([]sqltype.Value, error)
	RewriteQuerySQL(query sqltype.Value, text string, result *sqltype.Descriptor) (sqltype.Value, error)
	MapXMLRows(functions.XMLMappingOperation, [5]sqltype.Value, *sqltype.Descriptor, sqltype.OutputEnvironment) (sqltype.Value, error)
	LexizeDictionary(dictionary sqltype.Value, text string, result *sqltype.Descriptor) (sqltype.Value, error)
	ConvertTextSearch(ctx context.Context, kind fts.ConversionKind, explicit bool, configuration sqltype.Value, text string, result *sqltype.Descriptor) (sqltype.Value, error)
	ConvertJSONTextSearch(ctx context.Context, explicit bool, configuration, document, filter sqltype.Value, filtered bool, result *sqltype.Descriptor) (sqltype.Value, error)
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
