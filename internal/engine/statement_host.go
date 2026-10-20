package engine

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

// statementHost is the root package's implementation of every consumer host
// interface: execHost now, the analyze and feature hosts later (§7 decision
// 8). It is a defined type over StatementScope, so (*statementHost)(scope) is
// a free conversion and lock contracts stay with the scope. Host methods do
// not widen the public StatementScope facade.
type statementHost StatementScope

// execHost is everything the query runtime reaches above its own layer. Every
// method is called without Engine.mu held; none returns an engine struct.
// Runtime records store the host once (never per row); a detached record holds
// an untyped nil host.
type execHost interface {
	// Environment: read where the runtime reads them today, because a held
	// cursor detaches the host between fetches.
	Context() context.Context
	Environments() (sqltype.InputEnvironment, sqltype.OutputEnvironment)
	StatementStart() time.Time

	// Row locks: lock waits and current row versions at grant time.
	AcquireQueryRow(leaf queryLeaf, row ids.RowID, mark plan.RowMark) (bool, error)
	LatestQueryRow(leaf queryLeaf, candidate storage.RowVersion, hint int, mode lock.Mode) (storage.RowVersion, bool, error)
	QueryLocks() *queryLockContext

	// Foreign owner: release the replacement read lock before external I/O;
	// enlist the statement's foreign transaction participant.
	ReleaseReplacementRead()
	TouchForeignScan(ctx context.Context, source *foreignQuerySource) error
	EnlistForeign(ctx context.Context, wrapper catalog.ForeignDataWrapper, request catalog.ForeignRequest, writing bool) (catalog.ForeignTransaction, error)

	// Sequences: acquires the sequence lock, then reads the generator under
	// Engine.mu.
	CompletePGSequence(ctx context.Context, ref ids.ObjectRef, row []sqltype.Value) error

	// Capabilities: the bodies behind EvalContext's function, expression and
	// plan runtimes. They take Engine.mu, read session state or have side
	// effects (advisory locks, XID assignment, sequence calls, notices). The
	// EvalContext forwarders keep the prologue and nil-host diagnostics.

	// functions.ACLRuntime
	ACLAuthority() *sqltype.ACLAuthority
	ObjectACL(c *EvalContext, address ids.ObjectAddress, result *sqltype.Descriptor) (sqltype.Value, error)
	RoleMembership(c *EvalContext, actor *functions.AccessRole, target functions.AccessRole, privilege string) (bool, error)
	RowSecurityActive(c *EvalContext, relation functions.AccessRelation) (bool, error)
	// functions.AdvisoryRuntime and functions.BlockingRuntime
	Advisory(c *EvalContext, action functions.AdvisoryAction, mode functions.AdvisoryMode, lifetime functions.AdvisoryLifetime, first, second int64, pair bool, result *sqltype.Descriptor) (sqltype.Value, error)
	BlockingInquiry(kind functions.BlockingKind, pid int64, result *sqltype.Descriptor) (sqltype.Value, error)
	// functions.CatalogInquiryRuntime
	CatalogUserName(oid uint32, result *sqltype.Descriptor) (sqltype.Value, error)
	CommentInquiry(c *EvalContext, kind functions.CommentInquiryKind, values [2]sqltype.Value, provenance expr.Info, result *sqltype.Descriptor) (sqltype.Value, error)
	DeparseInquiry(c *EvalContext, signature functions.DeparseSignature, values [3]sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error)
	DescribeObject(c *EvalContext, values [3]sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error)
	IdentifyObject(c *EvalContext, values [3]sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error)
	IdentifyObjectAddress(c *EvalContext, values [3]sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error)
	IndexProperty(kind functions.IndexPropertyKind, oid uint32, ordinalValue sqltype.Value, property string) (value, known bool, err error)
	LookupObjectAddress(c *EvalContext, values [3]sqltype.Value) (ids.ObjectAddress, error)
	VisibilityInquiry(kind functions.VisibilityInquiryKind, value sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error)
	// functions.SessionInquiryRuntime
	CurrentSchemas(implicit bool, result *sqltype.Descriptor) (sqltype.Value, error)
	IdentityValue(c *EvalContext, kind functions.IdentityKind, result *sqltype.Descriptor) (sqltype.Value, error)
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
	// expr.Context: session generators, conversion procedure, regex cache
	FunctionArrayRandomOwner() (expr.RandomOwner, error)
	FunctionRandomOwner() (expr.RandomOwner, error)
	FunctionUUIDReady() error
	FunctionUUIDTime() int64
	FunctionBinaryConversion(source, destination int) (*sqltype.EncodingConversionHandler, error)
	FunctionRegexExecution() *sqltype.PGRegexExecution
	// plan.Runtime
	InvokeSQLRoutine(d *catalog.Procedure, frame *proc.Frame, resultUsage sqltype.Usage) (sqltype.Value, error)
	OpenExtensionPaths(c *EvalContext, n plan.ExtensionPathsExpr, name string) ([]sqltype.Value, error)

	presentationFormatters
}

var _ execHost = (*statementHost)(nil)

// presentationFormatters are the nine sqltype output formatters. Each call
// reads the statement's comment/deparse snapshot; without a host, output uses
// detachedPresentation.
type presentationFormatters interface {
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

// hostOf returns an untyped nil for a nil scope, never a typed nil.
func hostOf(scope *StatementScope) execHost {
	if scope == nil {
		return nil
	}
	return (*statementHost)(scope)
}

// scopeOf is the checked conversion back for engine code that reads the scope
// behind a runtime record's host.
func scopeOf(host execHost) *StatementScope {
	h, _ := host.(*statementHost)
	return (*StatementScope)(h)
}

func (h *statementHost) scope() *StatementScope { return (*StatementScope)(h) }

func (h *statementHost) Context() context.Context { return h.op.Context() }
func (h *statementHost) Environments() (sqltype.InputEnvironment, sqltype.OutputEnvironment) {
	return h.input, h.output
}

func (h *statementHost) StatementStart() time.Time { return h.statementStart }

// QueryLocks is the statement's row-lock context, read at recheck time.
func (h *statementHost) QueryLocks() *queryLockContext { return h.queryLocks }

func (h *statementHost) ReleaseReplacementRead() { h.scope().foreignUnlocked() }
func (h *statementHost) EnlistForeign(ctx context.Context, wrapper catalog.ForeignDataWrapper, request catalog.ForeignRequest, writing bool) (catalog.ForeignTransaction, error) {
	return h.scope().enlistForeign(ctx, wrapper, request, writing)
}

func (h *statementHost) FormatRegclass(id uint32) (string, error) {
	return h.scope().FormatRegclass(id)
}

func (h *statementHost) FormatRegtype(id uint32) (string, error) {
	return h.scope().FormatRegtype(id)
}

func (h *statementHost) FormatRegproc(id uint32) (string, error) {
	return h.scope().FormatRegproc(id)
}

func (h *statementHost) FormatRegprocedure(id uint32) (string, error) {
	return h.scope().FormatRegprocedure(id)
}

func (h *statementHost) FormatRegrole(id uint32) (string, error) {
	return h.scope().FormatRegrole(id)
}

func (h *statementHost) FormatRegoper(id uint32) (string, error) {
	return h.scope().FormatRegoper(id)
}

func (h *statementHost) FormatRegoperator(id uint32) (string, error) {
	return h.scope().FormatRegoperator(id)
}

func (h *statementHost) FormatRegcollation(id uint32) (string, error) {
	return h.scope().FormatRegcollation(id)
}

func (h *statementHost) FormatRegnamespace(id uint32) (string, error) {
	return h.scope().FormatRegnamespace(id)
}

func (h *statementHost) FormatRegconfig(id uint32) (string, error) {
	return h.scope().FormatRegconfig(id)
}

func (h *statementHost) FormatRegdictionary(id uint32) (string, error) {
	return h.scope().FormatRegdictionary(id)
}

func (h *statementHost) FormatACLRole(authority *sqltype.ACLAuthority, id uint32) (string, error) {
	return h.scope().FormatACLRole(authority, id)
}
