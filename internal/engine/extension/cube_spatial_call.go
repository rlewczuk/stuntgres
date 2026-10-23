package extension

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
)

// ValidateCubeSpatialCall checks a captured call against immutable catalog
// authority. Whole-manifest validation belongs to binding/import admission;
// evaluation checks only this call's captured generation and typed operands.
func ValidateCubeSpatialCall(v catalog.Read, proc *catalog.Procedure, call functions.CubeSpatial) error {
	if v.State() == nil || proc == nil || CubeProviderOf(proc) != StockCube || v.Get(proc.Address()) != proc || call.RoutineReference != proc || !proc.Strict || proc.Volatility != expr.Immutable || proc.RoutineKind != catalog.FunctionRoutine || call.Validate() != nil {
		return fmt.Errorf("stuntgres: invalid cube spatial call")
	}
	op := call.Plan.Operation()
	ext, ok := v.Extension(proc.Extension)
	if !ok || ext.Name() != "cube" || ext.Version != "1.5" || len(ext.Members) != CubeMemberCount || int(op) >= len(cubeSpatialMembers) || ext.Members[cubeSpatialProcedureStart+int(op)] != proc.Address() || proc.Name() != op.Routine() || proc.Result != ids.OID(op.ResultID()) {
		return fmt.Errorf("stuntgres: invalid cube spatial installation")
	}
	base, ok := v.Type(ext.Members[0])
	result, resultOK := v.LookupType(op.ResultID())
	if !ok || !base.SQLType.IsCube() || !resultOK || call.ResultUsage().Type != result || !cubeBinaryInputs(proc.Signature, base.SQLType.ID()) || call.Args[0].ResultUsage().Type != base.SQLType || call.Args[1].ResultUsage().Type != base.SQLType {
		return fmt.Errorf("stuntgres: invalid cube spatial input authority")
	}
	return nil
}
