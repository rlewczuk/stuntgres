package engine

import "stuntgres/internal/engine/exec"

// Temporary aliases for the executor types that moved into exec
// (ENGINE-SPLIT step 11c). Step 11d removes them.
type (
	admissionAuthority      = exec.AdmissionAuthority
	admissionProgram        = exec.AdmissionProgram
	aggregateDistinctState  = exec.AggregateDistinctState
	capturedHashIndex       = exec.CapturedHashIndex
	cteInvocation           = exec.CTEInvocation
	cteProducer             = exec.CTEProducer
	cursorExpressionData    = exec.CursorExpressionData
	detachedPresentation    = exec.DetachedPresentation
	domainCheckProgram      = exec.DomainCheckProgram
	evalAdmission           = exec.EvalAdmission
	EvalContext             = exec.Context
	evalFrame               = exec.EvalFrame
	evalStatement           = exec.EvalStatement
	execHost                = exec.Host
	execPreparation         = exec.Preparation
	finalRows               = exec.FinalRows
	foreignExecution        = exec.ForeignExecution
	foreignQuerySource      = exec.ForeignQuerySource
	foreignScanOwner        = exec.ForeignScanOwner
	ForeignValueExporter    = exec.ForeignValueExporter
	ForeignValueImporter    = exec.ForeignValueImporter
	functionSetInvocation   = exec.FunctionSetInvocation
	identitySnapshot        = exec.IdentitySnapshot
	indexedStage            = exec.Stage
	inheritanceQuerySegment = exec.InheritanceQuerySegment
	operatorGroups          = exec.OperatorGroups
	operatorOutputKey       = exec.OperatorOutputKey
	orderedAggregateInput   = exec.OrderedAggregateInput
	pgSequencesSource       = exec.PGSequencesSource
	presentationFormatters  = exec.PresentationFormatters
	queryAuthority          = exec.Authority
	queryGroup              = exec.QueryGroup
	queryLeaf               = exec.Leaf
	queryLockContext        = exec.LockContext
	queryLockLeaf           = exec.QueryLockLeaf
	queryLockLineage        = exec.QueryLockLineage
	queryPair               = exec.QueryPair
	queryParallelRun        = exec.QueryParallelRun
	queryPhysicalRow        = exec.QueryPhysicalRow
	querySortRow            = exec.QuerySortRow
	querySource             = exec.Source
	queryState              = exec.State
	quoteCaptureCandidate   = exec.QuoteCaptureCandidate
	quoteConstant           = exec.QuoteConstant
	quoteOrigin             = exec.QuoteOrigin
	recursiveUnionStage     = exec.RecursiveUnionStage
	regTSOutputKey          = exec.RegTSOutputKey
	routineBodyContext      = exec.RoutineBodyContext
	RowCursor               = exec.RowCursor
	RowResult               = exec.RowResult
	setArm                  = exec.SetArm
	subqueryMembershipKey   = exec.SubqueryMembershipKey
	windowNtileState        = exec.WindowNtileState
	windowOrderState        = exec.WindowOrderState
	windowSums              = exec.WindowSums
	windowSumState          = exec.WindowSumState
)
