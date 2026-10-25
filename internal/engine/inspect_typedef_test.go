package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// EngineDomainStageIndexRootLocked calls x.domainStageIndexRootLocked.
func EngineDomainStageIndexRootLocked(x *Engine, stage *CatalogStage, address ids.ObjectAddress) *storage.IndexRoot {
	return x.domainStageIndexRootLocked(stage, address)
}

// CaptureEnumOperationInput calls captureEnumOperationInput.
func CaptureEnumOperationInput(auth AuthorizationView, input *sqltype.InputEnvironment) error {
	return captureEnumOperationInput(auth, input)
}

// EngineEndTemporaryCollations calls x.endTemporaryCollations.
func EngineEndTemporaryCollations(x *Engine, source *session.Session) error {
	return x.endTemporaryCollations(source)
}

// EngineRemoveTemporaryNamespaceCandidate calls x.removeTemporaryNamespaceCandidate.
func EngineRemoveTemporaryNamespaceCandidate(x *Engine, session *session.Session, base catalog.Read, namespaces []*catalog.Namespace, children []ids.ObjectAddress, token ids.SessionToken) (bool, error) {
	return x.removeTemporaryNamespaceCandidate(session, base, namespaces, children, token)
}

// EngineResolveAnnotationType calls x.resolveAnnotationType.
func EngineResolveAnnotationType(x *Engine, raw parser.TypeName, op *session.Operation, unknownNull bool) (sqltype.Usage, error) {
	return x.resolveAnnotationType(raw, op, unknownNull)
}

// EngineResolveAnnotationTypeMode calls x.resolveAnnotationTypeMode.
func EngineResolveAnnotationTypeMode(x *Engine, raw parser.TypeName, op *session.Operation, unknownNull bool, domainBase bool) (sqltype.Usage, error) {
	return x.resolveAnnotationTypeMode(raw, op, unknownNull, domainBase)
}

// EngineResolveTableColumnType calls x.resolveTableColumnType.
func EngineResolveTableColumnType(x *Engine, raw parser.TypeName, op *session.Operation) (sqltype.Usage, error) {
	return x.resolveTableColumnType(raw, op)
}

// EngineResolveType calls x.resolveType.
func EngineResolveType(x *Engine, raw parser.TypeName, op *session.Operation) (sqltype.Usage, error) {
	return x.resolveType(raw, op)
}

// ScopeConversionProcedure calls x.conversionProcedure.
func ScopeConversionProcedure(x *StatementScope, source int, destination int) (*catalog.Procedure, error) {
	return x.conversionProcedure(source, destination)
}

// ScopeReadmitPreparedPrograms calls x.readmitPreparedPrograms.
func ScopeReadmitPreparedPrograms(x *StatementScope, view AuthorizationView) error {
	return x.readmitPreparedPrograms(view)
}

// ConversionDefaultConversion returns x.conversion.
func ConversionDefaultConversion(x conversionDefault) *catalog.Conversion { return x.conversion }
