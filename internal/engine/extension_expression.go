package engine

import (
	"fmt"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type installedFunctionExpression struct {
	expressionInfo
	arg        BoundExpr
	procedure  *procedureDescriptor
	dictionary *tsDictionaryDescriptor
	template   *tsTemplateDescriptor
	ref        ObjectRef
}

func (n installedFunctionExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	value, err := n.arg.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if value.IsNull() {
		return sqltype.Null(n.usage.Type), nil
	}
	input, err := value.Text()
	if err != nil || n.procedure == nil || n.dictionary == nil || n.template == nil || n.procedure.provider != n.dictionary.provider || n.dictionary.provider != n.template.provider {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid installed unaccent expression")
	}
	result, err := n.dictionary.provider.text(c.Context, input)
	if err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.String(n.usage.Type, result), nil
}
func (b *expressionBinder) bindInstalledFunction(raw parser.FunctionExpr) (BoundExpr, error) {
	// Transform operands once before namespace/signature resolution, retaining the
	// actual diagnostic type of unknown and wrong-signature inputs.
	operands := make([]comparisonOperand, len(raw.Args))
	names := make([]string, len(raw.Args))
	for i, arg := range raw.Args {
		o, err := b.stringOperand(arg, true)
		if err != nil {
			return nil, err
		}
		operands[i] = o
		names[i] = "unknown"
		if typ := o.descriptor(); typ != nil {
			names[i] = b.diagnosticTypeName(typ)
		}
		names[i] = functionNamedType(raw, i, names[i])
	}
	if len(raw.Args) > 100 {
		return nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	auth, err := b.bindingAuthorization()
	if err != nil {
		return nil, err
	}
	path, err := b.engine.lookupNamespacesSnapshot(auth, raw.Name, installedFunctionLookup)
	if err != nil {
		return nil, err
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	proc := procedureInNamespaces(auth.catalog, path, name, unaccentTextSignature)
	failure := func() (BoundExpr, error) { return nil, b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names) }
	if proc == nil || len(operands) != 1 || raw.Variadic || len(raw.ArgNames) != 0 || raw.Distinct || raw.All || raw.Star || raw.Window != nil || raw.Filter != nil || len(raw.Order) != 0 {
		return failure()
	}
	text := b.typ(sqltype.Text)
	arg, err := b.finalizeOperand(operands[0], text)
	if err != nil {
		return nil, err
	}
	plan, err := sqltype.PlanCoercion(arg.ResultUsage(), text.Usage(), sqltype.Implicit)
	if err != nil {
		return failure()
	}
	if arg.ResultUsage().Type != text {
		arg = coercionExpression{b.info(text.Usage(), arg.SourcePosition(), coercionProperties(arg, plan)), arg, plan, sqltype.Implicit}
	}
	dict, ok := auth.catalog.state.descriptors[proc.dictionary].(*tsDictionaryDescriptor)
	if !ok {
		return nil, fmt.Errorf("stuntgres: missing installed unaccent dictionary")
	}
	tmpl, ok := auth.catalog.state.descriptors[dict.template].(*tsTemplateDescriptor)
	if !ok {
		return nil, fmt.Errorf("stuntgres: missing installed unaccent template")
	}
	properties := arg.Properties()
	properties.Volatility = max(properties.Volatility, Stable)
	info, err := b.collatedInfo(b.info(text.Usage(), raw.Pos, properties), arg)
	if err != nil {
		return nil, err
	}
	ref := ObjectRef{owner: b.engine.owner, Epoch: auth.catalog.Epoch(), Address: proc.address}
	b.dependencies = append(b.dependencies, expressionDependency{ref: ref, revision: proc.revision})
	return installedFunctionExpression{expressionInfo: info, arg: arg, procedure: proc, dictionary: dict, template: tmpl, ref: ref}, nil
}
