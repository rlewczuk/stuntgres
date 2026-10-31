package core

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/parser"
)

// EnginePreparedAnalysisValidLocked calls x.preparedAnalysisValidLocked.
func EnginePreparedAnalysisValidLocked(x *Engine, auth AuthorizationView, analysis *Analysis, path sequence.Environment) bool {
	return analysis.Valid(auth.Catalog, x.preparedEnvironmentAtPathLocked(auth, path, false))
}

// ScopeExecuteDeallocateSQL calls x.executeDeallocateSQL.
func ScopeExecuteDeallocateSQL(x *StatementScope, raw *parser.DeallocateSQL, count int) (Result, error) {
	return x.executeDeallocateSQL(raw, count)
}

// ScopeSQLExecuteImporter calls x.sqlExecuteImporter.
func ScopeSQLExecuteImporter(x *StatementScope, raw *parser.ExecuteSQL, utilityInput bool) FrameImporter {
	return x.sqlExecuteImporter(raw, utilityInput)
}

// EnginePreparedAnalysisDefinitionsValidLocked calls Analysis.DefinitionsValid.
func EnginePreparedAnalysisDefinitionsValidLocked(x *Engine, auth AuthorizationView, analysis *Analysis, path sequence.Environment, installedScalarAuthorityRebind bool) bool {
	return analysis.DefinitionsValid(auth.Catalog, x.preparedEnvironmentAtPathLocked(auth, path, false), installedScalarAuthorityRebind)
}

// EnginePreparedAnalysisLocked calls x.preparedAnalysisLocked.
func EnginePreparedAnalysisLocked(x *Engine, s *StatementScope, auth AuthorizationView, b analyze.Binding) (*Analysis, error) {
	return x.preparedAnalysisLocked(s, auth, b)
}

// EnginePreparedInstalledScalarAuthorityRebindLocked calls x.preparedInstalledScalarAuthorityRebindLocked.
func EnginePreparedInstalledScalarAuthorityRebindLocked(x *Engine, auth AuthorizationView, expected *Description, path sequence.Environment) bool {
	return x.preparedInstalledScalarAuthorityRebindLocked(auth, expected, path)
}

// EnginePreparedInstalledScalarLiteralTemporalsLocked calls x.preparedInstalledScalarLiteralTemporalsLocked.
func EnginePreparedInstalledScalarLiteralTemporalsLocked(x *Engine, auth AuthorizationView, expected *Description, path sequence.Environment) []analyze.TemporalConstant {
	return x.preparedInstalledScalarLiteralTemporalsLocked(auth, expected, path)
}

// EngineRetirePreparedInstalledScalarLiteralTemporalsLocked calls x.retirePreparedInstalledScalarLiteralTemporalsLocked.
func EngineRetirePreparedInstalledScalarLiteralTemporalsLocked(x *Engine, auth AuthorizationView, expected *Description, path sequence.Environment, captures []analyze.TemporalConstant) []analyze.TemporalConstant {
	return x.retirePreparedInstalledScalarLiteralTemporalsLocked(auth, expected, path, captures)
}

// ScopeSelectPreparedAnalysis calls x.selectPreparedAnalysis.
func ScopeSelectPreparedAnalysis(x *StatementScope, stmt parser.Statement) error {
	return x.selectPreparedAnalysis(stmt)
}
