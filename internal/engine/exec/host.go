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
	// functions.AdvisoryRuntime and functions.BlockingRuntime
	Advisory(c *Context, action functions.AdvisoryAction, mode functions.AdvisoryMode, lifetime functions.AdvisoryLifetime, first, second int64, pair bool, result *sqltype.Descriptor) (sqltype.Value, error)
	BlockingInquiry(kind functions.BlockingKind, pid int64, result *sqltype.Descriptor) (sqltype.Value, error)
	// functions.CatalogInquiryRuntime
	CatalogUserName(oid uint32, result *sqltype.Descriptor) (sqltype.Value, error)
	CommentInquiry(c *Context, kind functions.CommentInquiryKind, values [2]sqltype.Value, provenance expr.Info, result *sqltype.Descriptor) (sqltype.Value, error)
	DeparseInquiry(c *Context, signature functions.DeparseSignature, values [3]sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error)
	DescribeObject(c *Context, values [3]sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error)
	IdentifyObject(c *Context, values [3]sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error)
	IdentifyObjectAddress(c *Context, values [3]sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error)
	IndexProperty(kind functions.IndexPropertyKind, oid uint32, ordinalValue sqltype.Value, property string) (value, known bool, err error)
	LookupObjectAddress(c *Context, values [3]sqltype.Value) (ids.ObjectAddress, error)
	VisibilityInquiry(kind functions.VisibilityInquiryKind, value sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error)
	// functions.SessionInquiryRuntime
	CurrentSchemas(implicit bool, result *sqltype.Descriptor) (sqltype.Value, error)
	IdentityValue(c *Context, kind functions.IdentityKind, result *sqltype.Descriptor) (sqltype.Value, error)
	MyTempNamespace() (uint32, error)
	OtherTempNamespace(oid uint32) (bool, error)
	// functions.TransactionInquiryRuntime
	CommitTimestamp(xid uint32) (time.Time, uint32, bool, error)
	CurrentTransactionID(assign bool) (uint64, error)
	LastCommittedTransaction() (uint32, time.Time, uint32, bool, error)
	MultiTransactionAge(xid uint32) (int32, error)
	TransactionAge(xid uint32) (int32, error)
	TransactionMembers(xid uint32, result *sqltype.Descriptor) ([]sqltype.Value, error)
	TransactionStatus(full uint64) (string, bool, error)
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
	InvokeSQLRoutine(d *catalog.Procedure, frame *proc.Frame, resultUsage sqltype.Usage) (sqltype.Value, error)
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
