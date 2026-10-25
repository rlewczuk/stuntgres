package engine

import (
	"context"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/sqltype"
)

// FinalizeSQLRoutineQuery calls finalizeSQLRoutineQuery.
func FinalizeSQLRoutineQuery(ctx context.Context, root plan.Node, source *exec.Source, parameters []sqltype.Value, scalar bool) (exec.RowResult, int, error) {
	return finalizeSQLRoutineQuery(ctx, root, source, parameters, scalar)
}

// MaxSQLRoutineDepth is maxSQLRoutineDepth.
const MaxSQLRoutineDepth = maxSQLRoutineDepth

// ScopeActivateSQLRoutine calls x.activateSQLRoutine.
func ScopeActivateSQLRoutine(x *StatementScope, inputs []sqltype.Usage, largeObjectRead catalog.Read) func(error) error {
	return x.activateSQLRoutine(inputs, largeObjectRead)
}

// ScopeEnterSQLRoutine calls x.enterSQLRoutine.
func ScopeEnterSQLRoutine(x *StatementScope) error {
	return x.enterSQLRoutine()
}

// ScopeLeaveSQLRoutine calls x.leaveSQLRoutine.
func ScopeLeaveSQLRoutine(x *StatementScope) {
	x.leaveSQLRoutine()
}

// CommandRoutineProgramAdmission returns x.admission.
func CommandRoutineProgramAdmission(x *commandRoutineProgram) *exec.RoutineProgram {
	return x.admission
}

// RoutineAnalysisDescriptions returns x.descriptions.
func RoutineAnalysisDescriptions(x *sqlRoutineAnalysis) []StatementDescription { return x.descriptions }

// RoutineAnalysisPLDiagnostic returns x.plDiagnostic.
func RoutineAnalysisPLDiagnostic(x *sqlRoutineAnalysis) string { return x.plDiagnostic }

// RoutineAnalysisPLProcedure returns x.plProcedure.
func RoutineAnalysisPLProcedure(x *sqlRoutineAnalysis) *catalog.Procedure { return x.plProcedure }

// RoutineDemandLexical returns x.lexical.
func RoutineDemandLexical(x *sqlRoutineDemand) *proc.PLFrame { return x.lexical }

// RoutineDemandPLDiagnostic returns x.plDiagnostic.
func RoutineDemandPLDiagnostic(x *sqlRoutineDemand) string { return x.plDiagnostic }

// RoutineDemandPLPlanning returns x.plPlanning.
func RoutineDemandPLPlanning(x *sqlRoutineDemand) bool { return x.plPlanning }

// RoutineDemandResult returns x.result.
func RoutineDemandResult(x *sqlRoutineDemand) sqltype.Usage { return x.result }

// RoutineDemandScalar returns x.scalar.
func RoutineDemandScalar(x *sqlRoutineDemand) bool { return x.scalar }

// SetRoutineDemandLexical sets x.lexical.
func SetRoutineDemandLexical(x *sqlRoutineDemand, v *proc.PLFrame) { x.lexical = v }

// SetRoutineDemandPLDiagnostic sets x.plDiagnostic.
func SetRoutineDemandPLDiagnostic(x *sqlRoutineDemand, v string) { x.plDiagnostic = v }

// SetRoutineDemandResult sets x.result.
func SetRoutineDemandResult(x *sqlRoutineDemand, v sqltype.Usage) { x.result = v }

// AdmitInquiryBody calls admitInquiryBody.
func AdmitInquiryBody(c *exec.Context, node expr.Info) error {
	return admitInquiryBody(c, node)
}

// ProcedureReturnsSet calls procedureReturnsSet.
func ProcedureReturnsSet(d *catalog.Procedure) bool {
	return procedureReturnsSet(d)
}

// ScopeInstallSQLRoutineScratch calls x.installSQLRoutineScratch.
func ScopeInstallSQLRoutineScratch(x *StatementScope, c sqlRoutineScratch) {
	x.installSQLRoutineScratch(c)
}

// ScopeInvokeSQLRoutine calls x.invokeSQLRoutine.
func ScopeInvokeSQLRoutine(x *StatementScope, d *catalog.Procedure, frame *proc.Frame, resultUsage sqltype.Usage, supportOwner *catalog.Procedure, inputCollation sqltype.CollationRef, assignmentPlanning bool) (sqltype.Value, error) {
	return x.invokeSQLRoutine(d, frame, resultUsage, supportOwner, inputCollation, assignmentPlanning)
}

// ScopeSQLRoutineScratch calls x.sqlRoutineScratch.
func ScopeSQLRoutineScratch(x *StatementScope) sqlRoutineScratch {
	return x.sqlRoutineScratch()
}

// CommandRoutineProgramRi returns x.ri.
func CommandRoutineProgramRi(x *commandRoutineProgram) bool { return x.ri }

// RoutineDemandFields holds the sqlRoutineDemand fields tests set.
type RoutineDemandFields struct {
	Lexical *proc.PLFrame
	Result  sqltype.Usage
}

// NewRoutineDemand builds a sqlRoutineDemand from f.
func NewRoutineDemand(f RoutineDemandFields) sqlRoutineDemand {
	return sqlRoutineDemand{lexical: f.Lexical, result: f.Result}
}
