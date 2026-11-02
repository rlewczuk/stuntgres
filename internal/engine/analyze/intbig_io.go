package analyze

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *Binder) bindSelectedIntbigIO(raw parser.FunctionExpr, selected sqlCallableSelection) (expr.Expr, error) {
	procedure := selected.Procedure
	view := selected.auth.ReadCatalog()
	if err := extension.StockIntarray.ValidateProcedure(view, procedure); err != nil {
		return nil, err
	}
	symbol, err := extension.ProcedureSymbol(view, procedure)
	if err != nil {
		return nil, err
	}
	if procedure.Origin == nil || procedure.Origin.Declaration != 4 && procedure.Origin.Declaration != 5 {
		return nil, fmt.Errorf("stuntgres: invalid intbig I/O role")
	}
	slot := procedure.Origin.Declaration
	if slot == 4 && symbol != "_intbig_in" || slot == 5 && symbol != "_intbig_out" {
		return nil, fmt.Errorf("stuntgres: invalid intbig I/O symbol")
	}
	if err := functions.ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	if err := functions.CallDecorationError(raw); err != nil {
		return nil, err
	}
	if len(raw.Args) != 1 || len(raw.ArgNames) != 0 || raw.Variadic || len(selected.Inputs) != 1 {
		return nil, b.functionSignatureError(raw)
	}
	arg := selected.args[0]
	if arg == nil {
		operand, err := b.comparisonOperand(raw.Args[0])
		if err != nil {
			return nil, err
		}
		arg, err = b.CoerceConditional(operand, selected.Inputs[0], "function")
		if err != nil {
			return nil, err
		}
	}
	base, ok := view.Type(procedure.Origin.Installation.Members[3])
	if !ok || !base.SQLType.IsIntbigGkey() || !catalog.SameInstallation(base, procedure.Origin.Installation) {
		return nil, fmt.Errorf("stuntgres: missing intbig I/O type role")
	}
	result, ok := b.authorityType(selected.auth, sqltype.ID(procedure.Result))
	if !ok {
		return nil, fmt.Errorf("stuntgres: missing intbig I/O result")
	}
	properties := expr.MergeProperties(expr.Properties{Volatility: expr.Immutable}, arg.Properties())
	info := b.Info(result.Usage(), raw.Pos, properties)
	info.RoutineReference = procedure
	ref := view.Owner().Domain.Ref(view.Epoch(), procedure.Address())
	b.dependency(ref, procedure.Revision())
	node := functions.IntbigIO{Info: info, Arg: arg, Formal: selected.Inputs[0].Type, Datum: base.SQLType, Ref: ref, Member: uint8(slot), Namespace: view.Get(procedure.Namespace()).Name(), Name: procedure.Name()}
	return node, node.Validate()
}
