package analyze

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *Binder) bindSelectedCitextRegexp(raw parser.FunctionExpr, selected sqlCallableSelection, symbol string) (expr.Expr, error) {
	procedure := selected.Procedure
	view := selected.auth.ReadCatalog()
	if err := extension.StockCitext.ValidateProcedure(view, procedure); err != nil {
		return nil, err
	}
	if err := functions.ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	if err := functions.CallDecorationError(raw); err != nil {
		return nil, err
	}
	var names [7]string
	copy(names[:], procedure.Installed.ArgumentNames[:])
	written, labels, valid := functions.RegexpSlots(raw, names, len(raw.Args))
	if !valid || raw.Variadic {
		return nil, b.functionSignatureError(raw)
	}
	args := make([]expr.Expr, len(raw.Args))
	properties := expr.Properties{Volatility: expr.Immutable}
	for i, slot := range written[:len(args)] {
		argument := selected.args[i]
		if argument == nil {
			operand, err := b.comparisonOperand(raw.Args[i])
			if err != nil {
				return nil, err
			}
			argument, err = b.CoerceConditional(operand, selected.Inputs[i], "function")
			if err != nil {
				return nil, err
			}
		}
		args[slot] = argument
		properties = expr.MergeProperties(properties, argument.Properties())
	}
	member := procedure.Origin.Declaration
	if member < extension.CitextRegexpProcedureStart || member >= extension.CitextRegexpProcedureEnd {
		return nil, fmt.Errorf("stuntgres: invalid citext regexp member")
	}
	properties.StageDependent = properties.StageDependent || functions.RegexpStageDependent(symbol)
	result := b.Type(sqltype.ID(procedure.Result))
	input, err := b.CollatedInfo(b.Info(args[0].ResultUsage(), raw.Pos, properties), args...)
	if err != nil {
		return nil, err
	}
	info := b.Info(result.Usage(), raw.Pos, properties)
	if info.Usage.Collation.ID != 0 {
		info.Usage.Collation = input.Usage.Collation
		info.CollationConflict = input.CollationConflict
	}
	info.RoutineReference = procedure
	ref := view.Owner().Domain.Ref(view.Epoch(), procedure.Address())
	b.dependency(ref, procedure.Revision())
	capture := functions.CitextRegexpCapture{
		Ref: ref, Namespace: view.Get(procedure.Namespace()).Name(), Name: procedure.Name(),
		Member: uint8(member), Text: b.Type(sqltype.Text).Usage(),
		CanInline: !procedure.Installed.ReturnsSet && procedure.Installed.ArgumentNames[len(args)-1] != "flags",
	}
	call, err := functions.NewCitextRegexpCall(info, symbol, args, written, labels, capture)
	if err != nil {
		return nil, err
	}
	if call.Plan.ReturnsSet() {
		if err := b.regexpPlacement(call); err != nil {
			return nil, err
		}
		return functions.RegexpSet{RegexpCall: call}, nil
	}
	return functions.Regexp{RegexpCall: call}, nil
}
