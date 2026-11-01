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

func (b *Binder) bindSelectedPgTrgm(raw parser.FunctionExpr, selected sqlCallableSelection, symbol string) (expr.Expr, error) {
	procedure := selected.Procedure
	view := selected.auth.ReadCatalog()
	if err := extension.StockPgTrgm.ValidateProcedure(view, procedure); err != nil {
		return nil, err
	}
	if err := functions.ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	if err := functions.CallDecorationError(raw); err != nil {
		return nil, err
	}
	installation, ok := catalog.OriginInstallation(procedure)
	if !ok || procedure.Origin.Declaration >= len(functions.PgTrgmMembers) {
		return nil, fmt.Errorf("stuntgres: missing pg_trgm declaration")
	}
	slot := procedure.Origin.Declaration
	member := functions.PgTrgmMembers[slot]
	count := 2
	if slot == 0 {
		count = 1
	}
	if member.Name != symbol || installation.Members[slot] != procedure.Address() || len(raw.Args) != count || len(raw.ArgNames) != 0 || raw.Variadic {
		return nil, b.functionSignatureError(raw)
	}
	args := make([]expr.Expr, count)
	properties := expr.Properties{Volatility: procedure.Volatility}
	for i := range args {
		arg := selected.args[i]
		if arg == nil {
			operand, err := b.comparisonOperand(raw.Args[i])
			if err != nil {
				return nil, err
			}
			arg, err = b.CoerceConditional(operand, selected.Inputs[i], "function")
			if err != nil {
				return nil, err
			}
		}
		args[i] = arg
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	info := b.Info(b.Type(sqltype.ID(procedure.Result)).Usage(), raw.Pos, properties)
	info.RoutineReference = procedure
	ref := view.Owner().Domain.Ref(view.Epoch(), procedure.Address())
	b.dependency(ref, procedure.Revision())
	node := functions.PgTrgm{
		Info: info, Args: args, Formal: selected.Inputs[0].Type,
		Ref: ref, Member: uint8(slot), Namespace: view.Get(procedure.Namespace()).Name(), Name: procedure.Name(),
	}
	return node, node.Validate()
}
