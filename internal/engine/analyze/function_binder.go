package analyze

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// The expression binder is the analyzer that builtin bind code reaches
// through functions.Binder. These methods map the interface onto the
// binder's existing private services; they add no state of their own.
var _ functions.AggregateBinder = (*Binder)(nil)

// OriginalArgument recovers syntax only through the same private slot that
// supplies an already-analyzed operand. It never binds or matches source keys.
func (b *Binder) OriginalArgument(raw parser.Expr) (parser.Expr, bool) {
	if slot, ok := raw.(parser.ParameterExpr); ok && slot.Number < 0 && -slot.Number <= len(b.Clause.SelectedOperatorSyntax) {
		return b.Clause.SelectedOperatorSyntax[-slot.Number-1], true
	}
	return raw, false
}

func (b *Binder) BuiltinVisible(name parser.ObjectName) (bool, error) {
	return b.builtinVisible(name, FunctionLookup)
}

func (b *Binder) BuiltinRoutineInfo(info expr.Info, provider functions.RoutineProvider, name string, inputs ...sqltype.ID) (expr.Info, error) {
	return b.builtinRoutineInfo(info, provider, name, inputs...)
}

func (b *Binder) BuiltinRoutineReference(provider functions.RoutineProvider, name string, inputs ...sqltype.ID) (ids.ObjectRef, error) {
	return b.builtinRoutineReference(provider, name, inputs...)
}

func (b *Binder) SignatureError(name parser.ObjectName, pos parser.Position, types []string) error {
	return b.functionSignatureErrorForTypes(name, pos, types)
}

func (b *Binder) RecordUnresolvedCallable(err error) {
	b.unresolvedCallable = err
}

func (b *Binder) AnalyzeFilter(filter parser.Expr) error {
	_, err := b.BindAggregateFilter(parser.AggregateExpr{Filter: filter})
	return err
}

func (b *Binder) DefaultConsumer() bool { return b.Consumer.DefaultConsumer }

func (b *Binder) ArrayType(element *sqltype.Descriptor) (*sqltype.Descriptor, bool) {
	return b.definitionArray(element)
}

// The probe passes no label, so selectCommonUsage never fails; operands only
// contribute their usages.
func (b *Binder) CommonUsage(usages []sqltype.Usage) sqltype.Usage {
	operand := func(i int) ComparisonOperand {
		return ComparisonOperand{Node: plan.LiteralExpr{Info: expr.Info{Usage: usages[i]}}}
	}
	common, _ := selectCommonUsage(b.BindingCatalog(), len(usages), operand, b.Type(sqltype.Text).Usage(), "", b.DiagnosticTypeName)
	return common
}

func (b *Binder) ComparisonInfo(left, right expr.Expr) (expr.Info, error) {
	return b.comparisonInfo(left, right)
}

func (b *Binder) Literal(value sqltype.Value, pos parser.Position) expr.Expr {
	return plan.LiteralExpr{Info: b.Info(value.Type().Usage(), pos, expr.Properties{Volatility: expr.Immutable}), Value: value}
}

func (b *Binder) Context() context.Context { return b.Ctx }

func (b *Binder) AdmitSetReturning(pos parser.Position) error {
	if err := b.stringSetAllowed(pos); err != nil {
		return err
	}
	switch b.Clause.AggregateClause {
	case "SELECT", "GROUP BY", "ORDER BY", "DISTINCT ON", "window definitions":
		b.Clause.QueryHasTargetSet = true
	}
	return nil
}

func (b *Binder) RetainUntypedParameter(arg expr.Expr) bool {
	parameter, ok := arg.(plan.ParameterExpr)
	if !ok || parameter.Usage.Type != nil {
		return false
	}
	b.Params.AnyParameters = append(b.Params.AnyParameters, parameter)
	return true
}

func (b *Binder) NoteCatalogText()     { b.Prepared.RegclassConcat = true }
func (b *Binder) NoteCatalogSnapshot() { b.Consumer.CatalogSnapshot = true }

func (b *Binder) BuiltinOperatorRoutineInfo(info expr.Info, operator string, left, right sqltype.ID) (expr.Info, error) {
	return b.builtinOperatorRoutineInfo(info, operator, left, right)
}

func (b *Binder) CaptureComparisonInput(typ *sqltype.Descriptor) error {
	return b.captureComparisonDiagnosticInput(typ)
}

func (b *Binder) CaptureTypeInput(typ *sqltype.Descriptor) error {
	if err := b.CaptureTypeDiagnosticInput(typ); err != nil {
		return err
	}
	if typ.ContainsCatalogIdentifier() {
		return b.captureElementInput()
	}
	return nil
}

func (b *Binder) ConstantValue(node expr.Expr) (sqltype.Value, bool) {
	literal, ok := node.(plan.LiteralExpr)
	return literal.Value, ok
}

func (b *Binder) BuiltinOperatorVisible(name parser.ObjectName) (bool, error) {
	return b.builtinVisible(name, OperatorLookup)
}

func (b *Binder) CallSignatureError(raw parser.FunctionExpr) error {
	return b.functionSignatureError(raw)
}

func (b *Binder) LiteralNull(arg expr.Expr) bool { return exec.RandomLiteralNull(arg) }

func (b *Binder) NoteVisibilitySnapshot() { b.Consumer.VisibilitySnapshot = true }

func (b *Binder) Frame() uint32 { return b.FrameID() }

func (b *Binder) CapturePolymorphicBody(node expr.Expr) error {
	return b.capturePolymorphicBody(node)
}

func (b *Binder) TargetSet() {
	switch b.Clause.AggregateClause {
	case "SELECT", "GROUP BY", "ORDER BY", "DISTINCT ON", "window definitions":
		b.Clause.QueryHasTargetSet = true
	}
}

func (b *Binder) RegexpSetPlacement(call functions.RegexpCall) error {
	return b.regexpPlacement(call)
}

func (b *Binder) AnalyzeAggregateFilter(raw parser.AggregateExpr) (expr.Expr, error) {
	return b.BindAggregateFilter(raw)
}

func (b *Binder) FinishAggregate(raw parser.AggregateExpr, call functions.AggregateCall, filter expr.Expr, info expr.Info) (expr.Expr, error) {
	// The function-owned carrier was selected in this immutable binding
	// snapshot. Retain that declaration now, before any later catalog COW.
	descriptor, present := catalog.DescriptorAt(b.BindingCatalog(), b.DefinitionReader(), call.Routine.Address)
	procedure, ok := descriptor.(*catalog.Procedure)
	if !present || !ok || procedure.Builtin == nil {
		return nil, fmt.Errorf("stuntgres: missing captured builtin aggregate declaration")
	}
	result, err := b.finishAggregate(raw, plan.AggregateCall{
		Provider:     call.Provider,
		Routine:      call.Routine,
		Procedure:    procedure,
		Arguments:    call.Arguments,
		Direct:       call.Direct,
		StageWidth:   call.StageWidth,
		Operation:    call.Operation,
		IncludeNulls: call.IncludeNulls,
		Order:        withinGroupOrder(call.Order),
	}, filter, info)
	if err != nil || !call.RetainUntypedArgument {
		return result, err
	}
	// ORDER BY matching may have resolved an UNKNOWN target; only an actual
	// remaining ANY occurrence joins the unresolved-parameter guard.
	for _, argument := range b.Grouping.Aggregates[result.(plan.AggregateExpr).Slot].Arguments {
		b.RetainUntypedParameter(argument)
	}
	return result, nil
}

func (b *Binder) DomainTypeName(typ *sqltype.Descriptor) (string, error) {
	return b.domainTypeName(typ)
}

// extremaArgumentTypeName spells an aggregate operand as DomainTypeName does.
func (b *Binder) extremaArgumentTypeName(operand ComparisonOperand) (string, error) {
	return b.domainTypeName(operand.descriptor())
}

func (b *Binder) domainTypeName(typ *sqltype.Descriptor) (string, error) {
	if typ == nil {
		return "unknown", nil
	}
	if !typ.IsDomain() {
		return typ.DiagnosticName(), nil
	}
	auth, err := b.BindingAuthorization()
	if err != nil {
		return "", err
	}
	path, err := b.Host.LookupPath(auth, TypeLookup)
	if err != nil {
		return "", err
	}
	d := auth.ReadCatalog().MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.ID())})
	return catalog.TypeDiagnosticNameAt(auth.ReadCatalog(), nil, d, path), nil
}

func (b *Binder) JSONReturningType(raw parser.TypeName) (sqltype.Usage, error) {
	return b.Host.ResolveAnnotationTypeInput(raw, false, false, true, true, false)
}

// SequenceArgument shares regclass input, with its prepared sequence-literal
// capture, with explicit regclass casts. A column DEFAULT admits only a
// non-NULL captured literal provider.
func (b *Binder) SequenceArgument(raw parser.Expr) (expr.Expr, ids.ObjectRef, error) {
	node, ref, err := b.bindRegclass(raw)
	if err == nil && b.Consumer.DefaultConsumer {
		literal, ok := node.(plan.LiteralExpr)
		if !ok || literal.Value.IsNull() || ref == (ids.ObjectRef{}) {
			return nil, ref, expr.ErrorAt("0A000", "unsupported sequence DEFAULT argument", parser.ExpressionStart(raw))
		}
	}
	return node, ref, err
}

func (b *Binder) SequenceTemporary(ref ids.ObjectRef, pos parser.Position) (ids.SessionToken, error) {
	var temporary ids.SessionToken
	for _, constant := range b.Prepared.SequenceConstants {
		if constant.Ref == ref {
			temporary = constant.Temporary
		}
	}
	if b.Consumer.DefaultConsumer {
		if err := sequence.ValidateDefaultTemporaryProvider(temporary, b.Consumer.DefaultTemporary, pos); err != nil {
			return temporary, err
		}
		// Stored defaults do not enter call-site prepared literal reanalysis.
		b.Prepared.SequenceConstants = nil
	}
	return temporary, nil
}

// withinGroupOrder converts a family's WITHIN GROUP keys: an argument key
// reads stage slot 0; other keys own the stage slots after value and
// separator, in key order.
func withinGroupOrder(keys []functions.AggregateSortKey) []plan.SortKey {
	if keys == nil {
		return nil
	}
	order := make([]plan.SortKey, len(keys))
	for i, key := range keys {
		slot, cache := -1, 2+i
		if key.Argument {
			slot, cache = 0, 0
		}
		order[i] = plan.SortKey{Expression: key.Expression, Comparison: key.Comparison, Position: key.Key.Pos.Character, Desc: key.Key.Desc, NullsFirst: key.Key.Nulls == parser.NullsFirst || key.Key.Nulls == parser.NullsDefault && key.Key.Desc, Slot: slot, Cache: cache}
	}
	return order
}

func (b *Binder) CollationName(id uint32) string { return b.collationName(id) }

func (b *Binder) CommonArgumentUsage(operands []functions.Argument, label string) (sqltype.Usage, error) {
	return selectCommonUsage(b.BindingCatalog(), len(operands), func(i int) ComparisonOperand {
		return ComparisonOperand{Node: operands[i].Expression, raw: operands[i].Raw}
	}, b.Type(sqltype.Text).Usage(), label, (*sqltype.Descriptor).DiagnosticName)
}

func (b *Binder) CollationRelabel(child expr.Expr, info expr.Info) expr.Expr {
	return plan.CollationExpr{Info: info, Child: child}
}
