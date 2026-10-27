package analyze

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *Binder) bindSelectedHstoreConstruct(raw parser.FunctionExpr, selected sqlCallableSelection, symbol string) (expr.Expr, error) {
	procedure := selected.Procedure
	view := selected.auth.ReadCatalog()
	if err := extension.StockHstore.ValidateProcedure(view, procedure); err != nil {
		return nil, err
	}
	if err := functions.ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	installation, ok := view.Extension(procedure.Extension)
	if !ok {
		return nil, fmt.Errorf("stuntgres: missing hstore installation")
	}
	member := -1
	for _, slot := range []int{extension.HstoreTextConstructorSlot, extension.HstoreArraysConstructorSlot, extension.HstoreArrayConstructorSlot, extension.HstoreFetchSlot, extension.HstoreSliceSlot, extension.HstoreExistsSlot, extension.HstoreDefinedSlot, extension.HstoreExistsAllSlot, extension.HstoreExistsAnySlot, extension.HstoreContainsSlot, extension.HstoreContainedSlot, extension.HstoreKeysSlot, extension.HstoreValuesSlot} {
		if installation.Members[slot] == procedure.Address() {
			member = slot
			break
		}
	}
	base, ok := view.Type(installation.Members[extension.HstoreTypeSlot])
	if !ok || member < 0 {
		return nil, fmt.Errorf("stuntgres: invalid hstore selected member")
	}
	result, ok := view.LookupType(sqltype.ID(procedure.Result))
	if !ok {
		return nil, fmt.Errorf("stuntgres: missing hstore result")
	}
	info := b.Info(result.Usage(), raw.Pos, expr.Properties{Volatility: expr.Immutable})
	info.RoutineReference = procedure
	ref := view.Owner().Domain.Ref(view.Epoch(), procedure.Address())
	node := functions.HstoreConstruct{Info: info, Hstore: base.SQLType, Ref: ref, Member: uint8(member), Namespace: view.Get(procedure.Namespace()).Name(), Name: procedure.Name()}
	if len(raw.Args) != node.Arity() || len(raw.ArgNames) != 0 || raw.Variadic || len(selected.Inputs) != node.Arity() {
		return nil, b.functionSignatureError(raw)
	}
	for i := range node.Arity() {
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
		node.Args[i], node.Formals[i] = arg, selected.Inputs[i].Type
		node.Info.Property = expr.MergeProperties(node.Properties(), arg.Properties())
	}
	if node.Symbol() != symbol {
		return nil, fmt.Errorf("stuntgres: invalid hstore selected symbol")
	}
	b.dependency(ref, procedure.Revision())
	return node, node.Validate()
}
