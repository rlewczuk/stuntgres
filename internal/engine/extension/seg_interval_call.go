package extension

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
)

// ValidateSegIntervalCall checks a captured call against immutable catalog
// authority. Whole-manifest validation belongs to binding/import admission;
// evaluation checks only this call's captured generation and typed operands.
func ValidateSegIntervalCall(v catalog.Read, proc *catalog.Procedure, call functions.SegInterval) error {
	if v.State() == nil || proc == nil || SegProviderOf(proc) != StockSeg || v.Get(proc.Address()) != proc || call.RoutineReference != proc || !proc.Strict || proc.Volatility != expr.Immutable || proc.RoutineKind != catalog.FunctionRoutine || call.Validate() != nil {
		return fmt.Errorf("stuntgres: invalid seg interval call")
	}
	op := call.Plan.Operation()
	if !proc.Origin.Valid(proc.Address()) {
		return fmt.Errorf("stuntgres: invalid seg interval origin")
	}
	ext := proc.Origin.Installation
	if ext.Name() != "seg" || ext.Version != "1.4" || ext.Implementation != StockSeg || len(ext.Members) != SegMemberCount || int(op) >= len(segIntervalMembers) || ext.Members[segIntervalProcedureStart+int(op)] != proc.Address() || v.NameAddress(catalog.DescriptorNameKey(proc)) != proc.Address() || proc.Result != ids.OID(op.ResultID()) {
		return fmt.Errorf("stuntgres: invalid seg interval installation")
	}
	if _, ok := v.Namespace(proc.Namespace()); !ok {
		return fmt.Errorf("stuntgres: invalid seg interval namespace")
	}
	base, ok := v.Type(ext.Members[0])
	result, resultOK := v.LookupType(op.ResultID())
	if !ok || !base.SQLType.IsSeg() || !resultOK || call.ResultUsage().Type != result || !segBinaryInputs(proc.Signature, base.SQLType.ID()) || call.Args[0].ResultUsage().Type != base.SQLType || call.Args[1].ResultUsage().Type != base.SQLType {
		return fmt.Errorf("stuntgres: invalid seg interval input authority")
	}
	return nil
}
