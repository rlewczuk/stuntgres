package analyze

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
)

func (b *Binder) bindSelectedHstoreJSON(raw parser.FunctionExpr, selected sqlCallableSelection, symbol string) (expr.Expr, error) {
	procedure := selected.Procedure
	view := selected.auth.ReadCatalog()
	if err := extension.StockHstore.ValidateProcedure(view, procedure); err != nil {
		return nil, err
	}
	if err := functions.ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	if len(raw.Args) != 1 || len(raw.ArgNames) != 0 || raw.Variadic {
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
	installation, ok := view.Extension(procedure.Extension)
	if !ok {
		return nil, fmt.Errorf("stuntgres: missing hstore JSON installation")
	}
	var member int
	switch symbol {
	case "hstore_to_json":
		member = extension.HstoreJSONProcedureSlot
	case "hstore_to_json_loose":
		member = extension.HstoreJSONLooseProcedureSlot
	case "hstore_to_jsonb":
		member = extension.HstoreJSONBProcedureSlot
	case "hstore_to_jsonb_loose":
		member = extension.HstoreJSONBLooseProcedureSlot
	default:
		return nil, fmt.Errorf("stuntgres: invalid hstore JSON symbol")
	}
	_, result, loose, _ := functions.HstoreJSONDeclaration(uint8(member))
	if installation.Members[member] != procedure.Address() {
		return nil, fmt.Errorf("stuntgres: invalid hstore JSON member")
	}
	properties := expr.MergeProperties(expr.Properties{Volatility: expr.Immutable}, arg.Properties())
	info := b.Info(b.Type(result).Usage(), raw.Pos, properties)
	info.RoutineReference = procedure
	ref := view.Owner().Domain.Ref(view.Epoch(), procedure.Address())
	b.dependency(ref, procedure.Revision())
	node := functions.HstoreJSON{Info: info, Arg: arg, Formal: selected.Inputs[0].Type, Loose: loose, Ref: ref, Member: uint8(member), Namespace: view.Get(procedure.Namespace()).Name(), Name: procedure.Name()}
	return node, node.Validate()
}
