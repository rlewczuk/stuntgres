package core

import (
	"context"
	"sync"
	"time"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/copyfmt"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// CheckStatementSignature calls checkStatementSignature.
func CheckStatementSignature(expected StatementDescription, actual StatementDescription) error {
	return checkStatementSignature(expected, actual)
}

// DisposeSQLCursors calls disposeSQLCursors.
func DisposeSQLCursors(ctx context.Context, cursors []*sqlCursor) error {
	return disposeSQLCursors(ctx, cursors)
}

// PreparedAnalysis names preparedAnalysis.
type PreparedAnalysis = preparedAnalysis

// ProtocolPortal names protocolPortal.
type ProtocolPortal = protocolPortal

// RetireProtocolPortalsAfter calls retireProtocolPortalsAfter.
func RetireProtocolPortalsAfter(t *session.TransactionOwner, frontier uint64) {
	retireProtocolPortalsAfter(t, frontier)
}

// SQLCursor names sqlCursor.
type SQLCursor = sqlCursor

// StatementHost names statementHost.
type StatementHost = statementHost

// ValidateSQLRoutineResult calls validateSQLRoutineResult.
func ValidateSQLRoutineResult(columns []catalog.Column, result sqltype.Usage) (sqltype.CoercionPlan, error) {
	return validateSQLRoutineResult(columns, result)
}

// EngineExecuteStatement calls x.executeStatement.
func EngineExecuteStatement(x *Engine, scope *StatementScope, stmt parser.Statement, count int, importFrame FrameImporter) (Result, error) {
	return x.executeStatement(scope, stmt, count, importFrame)
}

// EnginePreparedAnalysisValidLocked calls x.preparedAnalysisValidLocked.
func EnginePreparedAnalysisValidLocked(x *Engine, auth AuthorizationView, analysis *preparedAnalysis, path sequence.Environment) bool {
	return x.preparedAnalysisValidLocked(auth, analysis, path)
}

// DescriptionAnalysis returns x.analysis.
func DescriptionAnalysis(x *StatementDescription) *preparedAnalysis { return x.analysis }

// DescriptionPlanGeneration returns x.planGeneration.
func DescriptionPlanGeneration(x *StatementDescription) uint64 { return x.planGeneration }

// DescriptionPlanOwner returns x.planOwner.
func DescriptionPlanOwner(x *StatementDescription) ids.SessionToken { return x.planOwner }

// DescriptionRegtypes returns x.regtypes.
func DescriptionRegtypes(x *StatementDescription) *regobj.Analysis { return x.regtypes }

// SetDescriptionCatalogOwner sets x.catalogOwner.
func SetDescriptionCatalogOwner(x *StatementDescription, v *catalog.Owner) { x.catalogOwner = v }

// SetDescriptionRefreshTemporals sets x.refreshTemporals.
func SetDescriptionRefreshTemporals(x *StatementDescription, v bool) { x.refreshTemporals = v }

// ScopeAcquireRelationTarget calls x.acquireRelationTarget.
func ScopeAcquireRelationTarget(x *StatementScope, stmt parser.Statement, authorityStmt parser.Statement, execute bool, source bool) error {
	return x.acquireRelationTarget(stmt, authorityStmt, execute, source)
}

// ScopeBeginProtocolSimplePortal calls x.beginProtocolSimplePortal.
func ScopeBeginProtocolSimplePortal(x *StatementScope) error {
	return x.beginProtocolSimplePortal()
}

// ScopeCaptureProtocolOutput calls x.captureProtocolOutput.
func ScopeCaptureProtocolOutput(x *StatementScope, rows *exec.FinalRows, values []sqltype.Value, catalog catalog.Read) error {
	return x.captureProtocolOutput(rows, values, catalog)
}

// ScopeCloseCursor calls x.closeCursor.
func ScopeCloseCursor(x *StatementScope, close *parser.CloseCursor) (Result, error) {
	return x.closeCursor(close)
}

// ScopeExecuteDeallocateSQL calls x.executeDeallocateSQL.
func ScopeExecuteDeallocateSQL(x *StatementScope, raw *parser.DeallocateSQL, count int) (Result, error) {
	return x.executeDeallocateSQL(raw, count)
}

// ScopeExecuteDiscard calls x.executeDiscard.
func ScopeExecuteDiscard(x *StatementScope, raw *parser.Discard, count int) (Result, error) {
	return x.executeDiscard(raw, count)
}

// ScopeExecuteFetchCursor calls x.executeFetchCursor.
func ScopeExecuteFetchCursor(x *StatementScope, fetch *parser.FetchCursor, count int) (Result, error) {
	return x.executeFetchCursor(fetch, count)
}

// ScopeFetchCursor calls x.fetchCursor.
func ScopeFetchCursor(x *StatementScope, fetch *parser.FetchCursor) (Result, error) {
	return x.fetchCursor(fetch)
}

// ScopeFetchCursorPortal calls x.fetchCursorPortal.
func ScopeFetchCursorPortal(x *StatementScope, c *sqlCursor, fetch *parser.FetchCursor) (Result, error) {
	return x.fetchCursorPortal(c, fetch)
}

// ScopeInstallCursor calls x.installCursor.
func ScopeInstallCursor(x *StatementScope, declaration *parser.DeclareCursor, requestedName *string, root plan.Node, source *exec.Source, parameters []sqltype.Value, scroll bool) (*sqlCursor, string, error) {
	return x.installCursor(declaration, requestedName, root, source, parameters, scroll)
}

// ScopeProtocol calls x.protocol.
func ScopeProtocol(x *StatementScope) *protocolScope {
	return x.protocol()
}

// ScopeProtocolOrZero calls x.protocolOrZero.
func ScopeProtocolOrZero(x *StatementScope) protocolScope {
	return x.protocolOrZero()
}

// ScopeProtocolSimpleIO calls x.protocolSimpleIO.
func ScopeProtocolSimpleIO(x *StatementScope, call func() error) error {
	return x.protocolSimpleIO(call)
}

// ScopeResolveCursorPortal calls x.resolveCursorPortal.
func ScopeResolveCursorPortal(x *StatementScope, name string) (*sqlCursor, error) {
	return x.resolveCursorPortal(name)
}

// ScopeSQLExecuteImporter calls x.sqlExecuteImporter.
func ScopeSQLExecuteImporter(x *StatementScope, raw *parser.ExecuteSQL, utilityInput bool) FrameImporter {
	return x.sqlExecuteImporter(raw, utilityInput)
}

// ScopeStageDiscardTemporary calls x.stageDiscardTemporary.
func ScopeStageDiscardTemporary(x *StatementScope, closeCursors bool) error {
	return x.stageDiscardTemporary(closeCursors)
}

// ScopeStartProtocolCopy calls x.startProtocolCopy.
func ScopeStartProtocolCopy(x *StatementScope, hooks CopyProtocolHooks, from bool, options copyfmt.Options, columns int) error {
	return x.startProtocolCopy(hooks, from, options, columns)
}

// CursorsClear calls x.clear.
func CursorsClear(x *cursorRegistry) []*sqlCursor {
	return x.clear()
}

// CursorsEndTransaction calls x.endTransaction.
func CursorsEndTransaction(x *cursorRegistry, ref ids.TxRef) []*sqlCursor {
	return x.endTransaction(ref)
}

// CursorsOrdinal returns x.ordinal.
func CursorsOrdinal(x *cursorRegistry) uint64 { return x.ordinal }

// CursorsToken returns x.token.
func CursorsToken(x *cursorRegistry) ids.SessionToken { return x.token }

// SetCursorsOrdinal sets x.ordinal.
func SetCursorsOrdinal(x *cursorRegistry, v uint64) { x.ordinal = v }

// PreparedAnalysisDependencies returns x.dependencies.
func PreparedAnalysisDependencies(x *preparedAnalysis) []catalog.ExpressionDependency {
	return x.dependencies
}

// PreparedAnalysisNeedsReanalysis returns x.needsReanalysis.
func PreparedAnalysisNeedsReanalysis(x *preparedAnalysis) bool { return x.needsReanalysis }

// PreparedAnalysisSources returns x.sources.
func PreparedAnalysisSources(x *preparedAnalysis) map[*parser.TableReference]analyze.PreparedSource {
	return x.sources
}

// PreparedAnalysisTarget returns x.target.
func PreparedAnalysisTarget(x *preparedAnalysis) ids.ObjectRef { return x.target }

// PreparedAnalysisTargets returns x.targets.
func PreparedAnalysisTargets(x *preparedAnalysis) map[parser.Statement]ids.ObjectRef {
	return x.targets
}

// SetPreparedAnalysisDomain sets x.domain.
func SetPreparedAnalysisDomain(x *preparedAnalysis, v *ids.Domain) { x.domain = v }

// SetPreparedAnalysisPrivate sets x.private.
func SetPreparedAnalysisPrivate(x *preparedAnalysis, v *session.Journal) { x.private = v }

// PortalFields holds the protocolPortal fields tests set.
type PortalFields struct {
	Tx       ids.TxRef
	Born     uint64
	Entry    sqlPreparedStatement
	Context  session.ProtocolContext
	Columns  []catalog.Column
	Producer *sqlCursor
	Rows     exec.RowResult
	Started  bool
}

// NewPortal builds a protocolPortal from f.
func NewPortal(f PortalFields) *protocolPortal {
	return &protocolPortal{tx: f.Tx, born: f.Born, entry: f.Entry, context: f.Context, columns: f.Columns, producer: f.Producer, rows: f.Rows, started: f.Started}
}

// PortalBornRef returns &x.born.
func PortalBornRef(x *protocolPortal) *uint64 { return &x.born }

// PortalClose calls x.close.
func PortalClose(x *protocolPortal, ctx context.Context) error {
	return x.close(ctx)
}

// PortalColumns returns x.columns.
func PortalColumns(x *protocolPortal) []catalog.Column { return x.columns }

// PortalContext returns x.context.
func PortalContext(x *protocolPortal) session.ProtocolContext { return x.context }

// PortalEntryRef returns &x.entry.
func PortalEntryRef(x *protocolPortal) *sqlPreparedStatement { return &x.entry }

// PortalNext calls x.next.
func PortalNext(x *protocolPortal, ctx context.Context, values []sqltype.Value) (bool, error) {
	return x.next(ctx, values)
}

// PortalProducer returns x.producer.
func PortalProducer(x *protocolPortal) *sqlCursor { return x.producer }

// PortalRetired returns x.retired.
func PortalRetired(x *protocolPortal) bool { return x.retired }

// PortalRow returns x.row.
func PortalRow(x *protocolPortal) []sqltype.Value { return x.row }

// PortalRows returns x.rows.
func PortalRows(x *protocolPortal) exec.RowResult { return x.rows }

// PortalScratch returns x.scratch.
func PortalScratch(x *protocolPortal) []byte { return x.scratch }

// PortalTx returns x.tx.
func PortalTx(x *protocolPortal) ids.TxRef { return x.tx }

// PortalTxRef returns &x.tx.
func PortalTxRef(x *protocolPortal) *ids.TxRef { return &x.tx }

// SetPortalRows sets x.rows.
func SetPortalRows(x *protocolPortal, v exec.RowResult) { x.rows = v }

// SetPortalTx sets x.tx.
func SetPortalTx(x *protocolPortal, v ids.TxRef) { x.tx = v }

// ProtocolScopeBind returns x.bind.
func ProtocolScopeBind(x protocolScope) *protocolPortal { return x.bind }

// ProtocolScopeCommandRef returns &x.command.
func ProtocolScopeCommandRef(x *protocolScope) *bool { return &x.command }

// ProtocolScopeSimpleRef returns &x.simple.
func ProtocolScopeSimpleRef(x *protocolScope) *bool { return &x.simple }

// SetProtocolScopeCommand sets x.command.
func SetProtocolScopeCommand(x *protocolScope, v bool) { x.command = v }

// ReplacementAdmissionMuRef returns &x.mu.
func ReplacementAdmissionMuRef(x *replacementAdmission) *sync.Mutex { return &x.mu }

// ReplacementAdmissionReaders returns x.readers.
func ReplacementAdmissionReaders(x *replacementAdmission) int { return x.readers }

// ReplacementAdmissionWriters returns x.writers.
func ReplacementAdmissionWriters(x *replacementAdmission) int { return x.writers }

// ReplacementAdmissionWriting returns x.writing.
func ReplacementAdmissionWriting(x *replacementAdmission) bool { return x.writing }

// CursorAuthority returns x.authority.
func CursorAuthority(x *sqlCursor) *exec.Authority { return x.authority }

// CursorCatalog returns x.catalog.
func CursorCatalog(x *sqlCursor) catalog.Read { return x.catalog }

// CursorCount returns x.count.
func CursorCount(x *sqlCursor) int { return x.count }

// CursorCreated returns x.created.
func CursorCreated(x *sqlCursor) sqltype.Value { return x.created }

// CursorDispose calls x.dispose.
func CursorDispose(x *sqlCursor, ctx context.Context) error {
	return x.dispose(ctx)
}

// CursorEpoch returns x.epoch.
func CursorEpoch(x *sqlCursor) ids.CatalogEpoch { return x.epoch }

// CursorHold returns x.hold.
func CursorHold(x *sqlCursor) bool { return x.hold }

// CursorLeaves returns x.leaves.
func CursorLeaves(x *sqlCursor) []exec.Leaf { return x.leaves }

// CursorLocks returns x.locks.
func CursorLocks(x *sqlCursor) *exec.LockContext { return x.locks }

// CursorOrdinal returns x.ordinal.
func CursorOrdinal(x *sqlCursor) uint64 { return x.ordinal }

// CursorPinned returns x.pinned.
func CursorPinned(x *sqlCursor) bool { return x.pinned }

// CursorPromoted returns x.promoted.
func CursorPromoted(x *sqlCursor) bool { return x.promoted }

// CursorRecordShape returns x.recordShape.
func CursorRecordShape(x *sqlCursor) *sqltype.Descriptor { return x.recordShape }

// CursorRoot returns x.root.
func CursorRoot(x *sqlCursor) *plan.ProjectNode { return x.root }

// CursorRoutineAuth returns x.routineAuth.
func CursorRoutineAuth(x *sqlCursor) AuthorizationView { return x.routineAuth }

// CursorRoutineAuthRef returns &x.routineAuth.
func CursorRoutineAuthRef(x *sqlCursor) *AuthorizationView { return &x.routineAuth }

// CursorRoutineDependencies returns x.routineDependencies.
func CursorRoutineDependencies(x *sqlCursor) []catalog.ExpressionDependency {
	return x.routineDependencies
}

// CursorSchema returns x.schema.
func CursorSchema(x *sqlCursor) []catalog.Column { return x.schema }

// CursorScroll returns x.scroll.
func CursorScroll(x *sqlCursor) bool { return x.scroll }

// CursorState returns x.state.
func CursorState(x *sqlCursor) exec.State { return x.state }

// CursorStatement returns x.statement.
func CursorStatement(x *sqlCursor) string { return x.statement }

// CursorStoreOffset returns x.storeOffset.
func CursorStoreOffset(x *sqlCursor) int64 { return x.storeOffset }

// CursorTx returns x.tx.
func CursorTx(x *sqlCursor) ids.TxRef { return x.tx }

// CursorValues returns x.values.
func CursorValues(x *sqlCursor) []sqltype.Value { return x.values }

// SetCursorPinned sets x.pinned.
func SetCursorPinned(x *sqlCursor, v bool) { x.pinned = v }

// SetCursorRoutineDependencies sets x.routineDependencies.
func SetCursorRoutineDependencies(x *sqlCursor, v []catalog.ExpressionDependency) {
	x.routineDependencies = v
}

// SetCursorStage sets x.stage.
func SetCursorStage(x *sqlCursor, v *exec.Stage) { x.stage = v }

// SetCursorTx sets x.tx.
func SetCursorTx(x *sqlCursor, v ids.TxRef) { x.tx = v }

// PreparedRegistryEntries returns x.entries.
func PreparedRegistryEntries(x *sqlPreparedRegistry) map[string]*sqlPreparedStatement {
	return x.entries
}

// PreparedRegistryPortals returns x.portals.
func PreparedRegistryPortals(x *sqlPreparedRegistry) map[string]*protocolPortal { return x.portals }

// PreparedRegistryToken returns x.token.
func PreparedRegistryToken(x *sqlPreparedRegistry) ids.SessionToken { return x.token }

// SetPreparedRegistryPortals sets x.portals.
func SetPreparedRegistryPortals(x *sqlPreparedRegistry, v map[string]*protocolPortal) { x.portals = v }

// PreparedStatementFields holds the sqlPreparedStatement fields tests set.
type PreparedStatementFields struct {
	Body        parser.Statement
	Description StatementDescription
}

// NewPreparedStatement builds a sqlPreparedStatement from f.
func NewPreparedStatement(f PreparedStatementFields) *sqlPreparedStatement {
	return &sqlPreparedStatement{body: f.Body, description: f.Description}
}

// PreparedStatementBody returns x.body.
func PreparedStatementBody(x *sqlPreparedStatement) parser.Statement { return x.body }

// EngineHoldPrecommit calls x.holdPrecommit.
func EngineHoldPrecommit(x *Engine, op *session.Operation, owner *session.TransactionOwner, start time.Time, settlement *StatementScope) error {
	return x.holdPrecommit(op, owner, start, settlement)
}

// EnginePreparedAnalysisDefinitionsValidLocked calls x.preparedAnalysisDefinitionsValidLocked.
func EnginePreparedAnalysisDefinitionsValidLocked(x *Engine, auth AuthorizationView, analysis *preparedAnalysis, path sequence.Environment, installedScalarAuthorityRebind bool) bool {
	return x.preparedAnalysisDefinitionsValidLocked(auth, analysis, path, installedScalarAuthorityRebind)
}

// EnginePreparedAnalysisLocked calls x.preparedAnalysisLocked.
func EnginePreparedAnalysisLocked(x *Engine, s *StatementScope, auth AuthorizationView, b analyze.Binding) (*preparedAnalysis, error) {
	return x.preparedAnalysisLocked(s, auth, b)
}

// EnginePreparedInstalledScalarAuthorityRebindLocked calls x.preparedInstalledScalarAuthorityRebindLocked.
func EnginePreparedInstalledScalarAuthorityRebindLocked(x *Engine, auth AuthorizationView, expected *StatementDescription, path sequence.Environment) bool {
	return x.preparedInstalledScalarAuthorityRebindLocked(auth, expected, path)
}

// EnginePreparedInstalledScalarLiteralTemporalsLocked calls x.preparedInstalledScalarLiteralTemporalsLocked.
func EnginePreparedInstalledScalarLiteralTemporalsLocked(x *Engine, auth AuthorizationView, expected *StatementDescription, path sequence.Environment) []analyze.TemporalConstant {
	return x.preparedInstalledScalarLiteralTemporalsLocked(auth, expected, path)
}

// EngineRetirePreparedInstalledScalarLiteralTemporalsLocked calls x.retirePreparedInstalledScalarLiteralTemporalsLocked.
func EngineRetirePreparedInstalledScalarLiteralTemporalsLocked(x *Engine, auth AuthorizationView, expected *StatementDescription, path sequence.Environment, captures []analyze.TemporalConstant) []analyze.TemporalConstant {
	return x.retirePreparedInstalledScalarLiteralTemporalsLocked(auth, expected, path, captures)
}

// EngineRegisterExtension calls x.registerExtension.
func EngineRegisterExtension(x *Engine, name string, implementation extension.Installer) error {
	return x.registerExtension(name, implementation)
}

// DescriptionCatalogEpochRef returns &x.catalogEpoch.
func DescriptionCatalogEpochRef(x *StatementDescription) *ids.CatalogEpoch { return &x.catalogEpoch }

// DescriptionPlanGenerationRef returns &x.planGeneration.
func DescriptionPlanGenerationRef(x *StatementDescription) *uint64 { return &x.planGeneration }

// DescriptionQuoteGeneration returns x.quoteGeneration.
func DescriptionQuoteGeneration(x *StatementDescription) uint64 { return x.quoteGeneration }

// DescriptionRetireAnalysisTypeCasts calls x.retireAnalysisTypeCasts.
func DescriptionRetireAnalysisTypeCasts(x *StatementDescription) {
	x.retireAnalysisTypeCasts()
}

// DescriptionRetireAnalysisTypeCastsForRebind calls x.retireAnalysisTypeCastsForRebind.
func DescriptionRetireAnalysisTypeCastsForRebind(x *StatementDescription, installedScalarAuthorityRebind bool) map[analyze.PreparedTypeKey]analyze.TypeOccurrence {
	return x.retireAnalysisTypeCastsForRebind(installedScalarAuthorityRebind)
}

// DescriptionTypes returns x.types.
func DescriptionTypes(x *StatementDescription) map[analyze.PreparedTypeKey]analyze.TypeOccurrence {
	return x.types
}

// DescriptionFields holds the StatementDescription fields tests set.
type DescriptionFields struct {
	Parameters     analyze.ParameterInfo
	Results        []catalog.Column
	CatalogOwner   *catalog.Owner
	CatalogEpoch   ids.CatalogEpoch
	Temporals      []analyze.TemporalConstant
	Types          map[analyze.PreparedTypeKey]analyze.TypeOccurrence
	Regtypes       *regobj.Analysis
	Analysis       *preparedAnalysis
	PlanGeneration uint64
}

// NewDescription builds a StatementDescription from f.
func NewDescription(f DescriptionFields) *StatementDescription {
	return &StatementDescription{Parameters: f.Parameters, Results: f.Results, catalogOwner: f.CatalogOwner, catalogEpoch: f.CatalogEpoch, Temporals: f.Temporals, types: f.Types, regtypes: f.Regtypes, analysis: f.Analysis, planGeneration: f.PlanGeneration}
}

// SetDescriptionAnalysis sets x.analysis.
func SetDescriptionAnalysis(x *StatementDescription, v *preparedAnalysis) { x.analysis = v }

// SetDescriptionPlanGeneration sets x.planGeneration.
func SetDescriptionPlanGeneration(x *StatementDescription, v uint64) { x.planGeneration = v }

// SetDescriptionTypes sets x.types.
func SetDescriptionTypes(x *StatementDescription, v map[analyze.PreparedTypeKey]analyze.TypeOccurrence) {
	x.types = v
}

// ScopeForeignWrites calls x.foreignWrites.
func ScopeForeignWrites(x *StatementScope) fdw.WriteRuntime {
	return x.foreignWrites()
}

// ScopeInstallSQLCursor calls x.installSQLCursor.
func ScopeInstallSQLCursor(x *StatementScope, declaration *parser.DeclareCursor, root plan.Node, source *exec.Source, parameters []sqltype.Value) (Result, error) {
	return x.installSQLCursor(declaration, root, source, parameters)
}

// ScopePresentationOrZero calls x.presentationOrZero.
func ScopePresentationOrZero(x *StatementScope) presentationCaches {
	return x.presentationOrZero()
}

// ScopeSelectPreparedAnalysis calls x.selectPreparedAnalysis.
func ScopeSelectPreparedAnalysis(x *StatementScope, stmt parser.Statement) error {
	return x.selectPreparedAnalysis(stmt)
}

// CursorsRollback calls x.rollback.
func CursorsRollback(x *cursorRegistry, mark uint64) []*sqlCursor {
	return x.rollback(mark)
}

// PreparedAnalysisFields holds the preparedAnalysis fields tests set.
type PreparedAnalysisFields struct {
	Domain       *ids.Domain
	Epoch        ids.CatalogEpoch
	Session      ids.SessionToken
	Private      *session.Journal
	Path         sequence.Environment
	Namespace    uint64
	Dependencies []catalog.ExpressionDependency
	Types        map[ids.ObjectAddress]*catalog.Type
}

// NewPreparedAnalysis builds a preparedAnalysis from f.
func NewPreparedAnalysis(f PreparedAnalysisFields) *preparedAnalysis {
	return &preparedAnalysis{domain: f.Domain, epoch: f.Epoch, session: f.Session, private: f.Private, path: f.Path, namespace: f.Namespace, dependencies: f.Dependencies, types: f.Types}
}

// PreparedAnalysisEpochRef returns &x.epoch.
func PreparedAnalysisEpochRef(x *preparedAnalysis) *ids.CatalogEpoch { return &x.epoch }

// PreparedAnalysisNamespaceRef returns &x.namespace.
func PreparedAnalysisNamespaceRef(x *preparedAnalysis) *uint64 { return &x.namespace }

// PreparedAnalysisPath returns x.path.
func PreparedAnalysisPath(x *preparedAnalysis) sequence.Environment { return x.path }

// PreparedAnalysisPathRef returns &x.path.
func PreparedAnalysisPathRef(x *preparedAnalysis) *sequence.Environment { return &x.path }

// SetPreparedAnalysisCasts sets x.casts.
func SetPreparedAnalysisCasts(x *preparedAnalysis, v map[catalog.CastPair]*catalog.Cast) { x.casts = v }

// SetPreparedAnalysisDependencies sets x.dependencies.
func SetPreparedAnalysisDependencies(x *preparedAnalysis, v []catalog.ExpressionDependency) {
	x.dependencies = v
}

// SetPreparedAnalysisNeedsReanalysis sets x.needsReanalysis.
func SetPreparedAnalysisNeedsReanalysis(x *preparedAnalysis, v bool) { x.needsReanalysis = v }

// SetPreparedAnalysisSession sets x.session.
func SetPreparedAnalysisSession(x *preparedAnalysis, v ids.SessionToken) { x.session = v }

// SetPreparedAnalysisTypes sets x.types.
func SetPreparedAnalysisTypes(x *preparedAnalysis, v map[ids.ObjectAddress]*catalog.Type) {
	x.types = v
}

// PreparedCommandExecutionFields holds the preparedCommandExecution fields tests set.
type PreparedCommandExecutionFields struct {
	PolicyWaits uint64
}

// NewPreparedCommandExecution builds a preparedCommandExecution from f.
func NewPreparedCommandExecution(f PreparedCommandExecutionFields) *preparedCommandExecution {
	return &preparedCommandExecution{policyWaits: f.PolicyWaits}
}

// PreparedCommandExecutionPolicyWaits returns x.policyWaits.
func PreparedCommandExecutionPolicyWaits(x *preparedCommandExecution) uint64 { return x.policyWaits }

// PresentationCachesConversionDefaults returns x.conversionDefaults.
func PresentationCachesConversionDefaults(x presentationCaches) map[[2]int]conversionDefault {
	return x.conversionDefaults
}

// SetProtocolScopeBind sets x.bind.
func SetProtocolScopeBind(x *protocolScope, v *protocolPortal) { x.bind = v }

// CursorForeignOwner returns x.foreignOwner.
func CursorForeignOwner(x *sqlCursor) *exec.ForeignScanOwner { return x.foreignOwner }

// CursorRLSPrograms returns x.rlsPrograms.
func CursorRLSPrograms(x *sqlCursor) []*rowSecurityProgram { return x.rlsPrograms }

// CursorReleaseProducer calls x.releaseProducer.
func CursorReleaseProducer(x *sqlCursor, ctx context.Context) error {
	return x.releaseProducer(ctx)
}

// CursorFields holds the sqlCursor fields tests set.
type CursorFields struct {
	Tx           ids.TxRef
	Ordinal      uint64
	Epoch        ids.CatalogEpoch
	Hold         bool
	Promoted     bool
	Pinned       bool
	Values       []sqltype.Value
	Count        int
	Root         *plan.ProjectNode
	Schema       []catalog.Column
	Source       *exec.Source
	State        exec.State
	Stage        *exec.Stage
	Authority    *exec.Authority
	ForeignOwner *exec.ForeignScanOwner
	Scroll       bool
}

// NewCursor builds a sqlCursor from f.
func NewCursor(f CursorFields) *sqlCursor {
	return &sqlCursor{tx: f.Tx, ordinal: f.Ordinal, epoch: f.Epoch, hold: f.Hold, promoted: f.Promoted, pinned: f.Pinned, values: f.Values, count: f.Count, root: f.Root, schema: f.Schema, Source: f.Source, state: f.State, stage: f.Stage, authority: f.Authority, foreignOwner: f.ForeignOwner, scroll: f.Scroll}
}

// StatementHostFields holds the statementHost fields tests set.
type StatementHostFields struct {
	Op *session.Operation
}

// NewStatementHost builds a statementHost from f.
func NewStatementHost(f StatementHostFields) *statementHost {
	return &statementHost{Op: f.Op}
}

// AuthorizationOf calls authorizationOf.
func AuthorizationOf(a analyze.Authority) *AuthorizationView { return authorizationOf(a) }

// CursorStage returns x.stage.
func CursorStage(x *sqlCursor) *exec.Stage { return x.stage }

// BindingHostView calls x.view.
func BindingHostView(x *BindingHost, a analyze.Authority) AuthorizationView {
	return x.view(a)
}
