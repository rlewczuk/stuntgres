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

func (b *Binder) bindSelectedHstoreRecord(raw parser.FunctionExpr, selected sqlCallableSelection, symbol string) (expr.Expr, error) {
	procedure := selected.Procedure
	view := selected.auth.ReadCatalog()
	if err := extension.StockHstore.ValidateProcedure(view, procedure); err != nil {
		return nil, err
	}
	if err := functions.ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	installation, ok := catalog.OriginInstallation(procedure)
	if !ok {
		return nil, fmt.Errorf("stuntgres: missing hstore installation")
	}
	member := extension.HstoreRecordConstructorSlot
	if symbol == "hstore_populate_record" {
		member = extension.HstorePopulateRecordSlot
	}
	if installation.Members[member] != procedure.Address() {
		return nil, fmt.Errorf("stuntgres: invalid hstore record selected member")
	}
	base, ok := view.Type(installation.Members[extension.HstoreTypeSlot])
	if !ok {
		return nil, fmt.Errorf("stuntgres: missing hstore type")
	}
	ref := view.Owner().Domain.Ref(view.Epoch(), procedure.Address())
	node := functions.HstoreRecord{Hstore: base.SQLType, Ref: ref, Member: uint8(member), Namespace: view.Get(procedure.Namespace()).Name(), Name: procedure.Name()}
	if len(raw.Args) != node.Arity() || len(raw.ArgNames) != 0 || raw.Variadic || len(selected.Inputs) != node.Arity() {
		return nil, b.functionSignatureError(raw)
	}
	usage := base.SQLType.Usage()
	for i := range node.Arity() {
		arg := selected.args[i]
		if arg == nil {
			target := selected.Targets[i]
			if target.Type == nil {
				return nil, sqlError("42804", "could not determine polymorphic type because input has type unknown")
			}
			operand, err := b.comparisonOperand(raw.Args[i])
			if err != nil {
				return nil, err
			}
			arg, err = b.CoerceConditional(operand, target, "function")
			if err != nil {
				return nil, err
			}
		}
		node.Args[i], node.Formals[i] = arg, arg.ResultUsage().Type
	}
	if node.Arity() == 2 {
		usage = node.Args[0].ResultUsage()
	}
	node.Info = b.Info(usage, raw.Pos, expr.Properties{Volatility: expr.Immutable})
	node.Info.RoutineReference = procedure
	for i := range node.Arity() {
		node.Info.Property = expr.MergeProperties(node.Properties(), node.Args[i].Properties())
	}
	if node.Symbol() != symbol {
		return nil, fmt.Errorf("stuntgres: invalid hstore record selected symbol")
	}
	if node.Arity() == 1 && node.Formals[0].ContainsCatalogIdentifier() {
		b.NoteCatalogText()
	}
	if node.Arity() == 2 {
		if err := b.CaptureTypeInput(usage.Type); err != nil {
			return nil, err
		}
		node.Info.Property.DomainDependent = node.Info.Property.DomainDependent || sqltype.ContainsDomainConstraints(usage.Type)
		node.Info.Property.RuntimeDomainConversion = node.Info.Property.RuntimeDomainConversion || sqltype.ContainsDomainConstraints(usage.Type)
	}
	b.dependency(ref, procedure.Revision())
	return node, node.Validate()
}
