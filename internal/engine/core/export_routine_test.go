package core

import (
	"context"
	"reflect"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// SameSQLRoutineDemand preserves the old scratch identity comparison while
// including immutable row-plan slices. Pointer fields compare by identity,
// never by following their pointees as reflect.DeepEqual would.
func SameSQLRoutineDemand(a, b sqlRoutineDemand) bool {
	var same func(reflect.Value, reflect.Value) bool
	same = func(a, b reflect.Value) bool {
		if a.Type().Comparable() {
			return a.Equal(b)
		}
		switch a.Kind() {
		case reflect.Struct:
			for i := range a.NumField() {
				if !same(a.Field(i), b.Field(i)) {
					return false
				}
			}
			return true
		case reflect.Slice:
			if a.IsNil() != b.IsNil() || a.Len() != b.Len() || a.Cap() != b.Cap() || a.Pointer() != b.Pointer() {
				return false
			}
			for i := range a.Len() {
				if !same(a.Index(i), b.Index(i)) {
					return false
				}
			}
			return true
		default:
			panic("unhandled SQL routine scratch comparison")
		}
	}
	return same(reflect.ValueOf(a), reflect.ValueOf(b))
}

// FinalizeSQLRoutineQuery calls finalizeSQLRoutineQuery.
func FinalizeSQLRoutineQuery(ctx context.Context, root plan.Node, source *exec.Source, parameters []sqltype.Value) (exec.RowResult, int, error) {
	return finalizeSQLRoutineQuery(ctx, root, source, parameters, false)
}

// MaxSQLRoutineDepth is maxSQLRoutineDepth.
const MaxSQLRoutineDepth = maxSQLRoutineDepth

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
func RoutineAnalysisDescriptions(x *sqlRoutineAnalysis) []Description { return x.descriptions }

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

// ScopeInstallSQLRoutineScratch calls x.installSQLRoutineScratch.
func ScopeInstallSQLRoutineScratch(x *StatementScope, c sqlRoutineScratch) {
	x.installSQLRoutineScratch(c)
}

// ScopeInvokeSQLRoutine calls x.invokeSQLRoutine.
func ScopeInvokeSQLRoutine(x *StatementScope, d *catalog.Procedure, frame *proc.Frame, resultUsage sqltype.Usage, supportOwner *catalog.Procedure, inputCollation sqltype.CollationRef, assignmentPlanning bool) (sqltype.Value, error) {
	return x.invokeSQLRoutine(d, frame, resultUsage, supportOwner, inputCollation, assignmentPlanning, nil)
}

// ScopeSQLRoutineScratch calls x.sqlRoutineScratch.
func ScopeSQLRoutineScratch(x *StatementScope) sqlRoutineScratch {
	return x.sqlRoutineScratch()
}

// CommandRoutineProgramRi returns x.ri.
func CommandRoutineProgramRi(x *commandRoutineProgram) bool { return x.ri }

// RoutineDemandFields holds the sqlRoutineDemand fields tests set.
type RoutineDemandFields struct {
	Lexical    *proc.PLFrame
	Result     sqltype.Usage
	ResultPlan proc.ResultPlan
	Scalar     bool
}

// NewRoutineDemand builds a sqlRoutineDemand from f.
func NewRoutineDemand(f RoutineDemandFields) sqlRoutineDemand {
	return sqlRoutineDemand{lexical: f.Lexical, result: f.Result, resultPlan: f.ResultPlan, scalar: f.Scalar}
}

// ScopeAdmitSQLRoutineCommand calls the real bound-command admission owner.
func ScopeAdmitSQLRoutineCommand(s *StatementScope, d *catalog.Procedure, program *stored.RoutineProgram, index int) error {
	return s.admitSQLRoutineCommand(d, program, index)
}
func RewriteTupleStatement(stmt parser.Statement) bool { return rewriteTupleStatement(stmt) }

func RewriteReadOnlyViolation(stmt parser.Statement) string { return rewriteReadOnlyViolation(stmt) }

func RoutineDemandRewrite(x *sqlRoutineDemand) bool { return x.rewrite }

func SetRoutineDemandScalar(x *sqlRoutineDemand, v bool) { x.scalar = v }

func SetRoutineDemandRewrite(x *sqlRoutineDemand, v bool) { x.rewrite = v }
