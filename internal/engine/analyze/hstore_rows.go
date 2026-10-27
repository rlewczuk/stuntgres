package analyze

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *Binder) bindSelectedHstoreRows(raw parser.FunctionExpr, selected sqlCallableSelection, symbol string) (expr.Expr, error) {
	procedure := selected.Procedure
	view := selected.auth.ReadCatalog()
	if err := extension.StockHstore.ValidateProcedure(view, procedure); err != nil { return nil, err }
	if err := functions.ValidateCallSyntax(b, raw); err != nil { return nil, err }
	if len(raw.Args) != 1 || raw.Variadic { return nil, b.functionSignatureError(raw) }
	if err := b.AdmitSetReturning(raw.Pos); err != nil { return nil, err }
	arg := selected.args[0]
	if arg == nil {
		operand, err := b.comparisonOperand(raw.Args[0])
		if err != nil { return nil, err }
		arg, err = b.CoerceConditional(operand, selected.Inputs[0], "function")
		if err != nil { return nil, err }
	}
	member, kind := extension.HstoreSkeysSlot, sqltype.HstoreRowKeys
	switch symbol {
	case "hstore_skeys":
	case "hstore_svals": member, kind = extension.HstoreSvalsSlot, sqltype.HstoreRowValues
	case "hstore_each": member, kind = extension.HstoreEachSlot, sqltype.HstoreRowPairs
	default: return nil, fmt.Errorf("stuntgres: invalid hstore rows symbol")
	}
	installation, ok := view.Extension(procedure.Extension)
	if !ok || installation.Members[member] != procedure.Address() { return nil, fmt.Errorf("stuntgres: invalid hstore rows member") }
	usage := b.Type(sqltype.Text).Usage()
	if kind == sqltype.HstoreRowPairs {
		shape, err := sqltype.RecordShape([]sqltype.CompositeMember{{Attribute: 1, Name: "key", Usage: usage}, {Attribute: 2, Name: "value", Usage: usage}})
		if err != nil { return nil, err }
		usage = shape.Usage()
	}
	plan, err := sqltype.BindHstoreRows(kind, arg.ResultUsage(), usage)
	if err != nil { return nil, err }
	properties := expr.MergeProperties(expr.Properties{Volatility: expr.Immutable, StageDependent: true}, arg.Properties())
	info := b.Info(usage, raw.Pos, properties)
	info.RoutineReference = procedure
	ref := view.Owner().Domain.Ref(view.Epoch(), procedure.Address())
	b.dependency(ref, procedure.Revision())
	node := functions.HstoreRows{Info: info, Arg: arg, Formal: selected.Inputs[0].Type, Plan: plan, Ref: ref, Member: uint8(member), Namespace: view.Get(procedure.Namespace()).Name(), Name: procedure.Name()}
	return node, node.Validate()
}
