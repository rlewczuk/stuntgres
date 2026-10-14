package engine

import (
	"fmt"
	"stuntgres/internal/engine/functions"
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

// Validate provider-specific captured authority without assuming a dictionary.
func (n installedFunctionExpression) validProvider() bool {
	p := n.procedure
	if p == nil || n.arg == nil || n.usage.Type == nil || n.arg.ResultUsage().Type == nil || p.signature != unaccentTextSignature || n.arg.ResultUsage().Type.ID() != sqltype.Text || OID(n.usage.Type.ID()) != p.result {
		return false
	}
	if p.fuzzy != nil {
		return p.fuzzy == stockFuzzystrmatch && p.name == "daitch_mokotoff" && p.result == 1009 && p.volatility == Immutable && p.strict && p.provider == nil && p.queryCodec == nil && p.conversion.Name() == "" && p.dictionary == (ObjectAddress{}) && n.dictionary == nil && n.template == nil && n.usage.Type.IsArray() && n.usage.Type.Element() == n.arg.ResultUsage().Type
	}
	return p.provider == stockUnaccent && p.queryCodec == nil && p.conversion.Name() == "" && p.result == 25 && p.volatility == Stable && p.strict && n.usage.Type == n.arg.ResultUsage().Type && n.dictionary != nil && n.template != nil && p.dictionary == n.dictionary.address && n.dictionary.template == n.template.address && p.provider == n.dictionary.provider && n.dictionary.provider == n.template.provider
}

func (n installedFunctionExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	if !n.validProvider() {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid installed function expression")
	}
	value, err := n.arg.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if value.IsNull() {
		return sqltype.Null(n.usage.Type), nil
	}
	input, err := value.Text()
	if err != nil {
		return sqltype.Value{}, err
	}
	if n.procedure.fuzzy != nil {
		codes, err := functions.DaitchMokotoff(c.Context, input)
		if err != nil {
			return sqltype.Value{}, err
		}
		if codes == nil {
			return sqltype.Null(n.usage.Type), nil
		}
		elements := make([]sqltype.Value, len(codes))
		for i, code := range codes {
			elements[i] = sqltype.String(n.usage.Type.Element(), code)
		}
		return sqltype.OwnedArrayValue(n.usage.Type, []sqltype.ArrayDimension{{Lower: 1, Length: int32(len(codes))}}, elements)
	}
	result, err := n.dictionary.provider.matcher.Text(c.Context, input)
	if err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.String(n.usage.Type, result), nil
}

func (b *expressionBinder) bindInstalledFunction(raw parser.FunctionExpr) (BoundExpr, error) {
	// Transform operands once before namespace/signature resolution, retaining
	// actual diagnostic types for unknown and wrong-signature inputs.
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
	if proc == nil || len(operands) != 1 || raw.Variadic || len(raw.ArgNames) != 0 || raw.Star {
		return failure()
	}
	if proc.fuzzy == nil && (raw.Distinct || raw.All || raw.Window != nil || raw.Filter != nil || len(raw.Order) != 0) {
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
	if proc.fuzzy != nil {
		switch {
		case raw.Distinct:
			return nil, expressionError("42809", "DISTINCT specified, but "+objectNameText(raw.Name)+" is not an aggregate function", raw.Pos)
		case len(raw.Order) != 0:
			return nil, expressionError("42809", "ORDER BY specified, but "+objectNameText(raw.Name)+" is not an aggregate function", raw.Pos)
		case raw.Filter != nil:
			return nil, expressionError("42809", "FILTER specified, but "+objectNameText(raw.Name)+" is not an aggregate function", raw.Pos)
		case raw.Window != nil:
			return nil, expressionError("42809", "OVER specified, but "+objectNameText(raw.Name)+" is not a window function nor an aggregate function", raw.Pos)
		}
	}
	if arg.ResultUsage().Type != text {
		arg = coercionExpression{b.info(text.Usage(), arg.SourcePosition(), coercionProperties(arg, plan)), arg, plan, sqltype.Implicit}
	}
	var dict *tsDictionaryDescriptor
	var tmpl *tsTemplateDescriptor
	if proc.provider != nil {
		var ok bool
		dict, ok = auth.catalog.state.descriptors[proc.dictionary].(*tsDictionaryDescriptor)
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing installed unaccent dictionary")
		}
		tmpl, ok = auth.catalog.state.descriptors[dict.template].(*tsTemplateDescriptor)
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing installed unaccent template")
		}
	}
	properties := arg.Properties()
	properties.Volatility = max(properties.Volatility, proc.volatility)
	info, err := b.collatedInfo(b.info(b.typ(sqltype.ID(proc.result)).Usage(), raw.Pos, properties), arg)
	if err != nil {
		return nil, err
	}
	ref := ObjectRef{owner: b.engine.owner, Epoch: auth.catalog.Epoch(), Address: proc.address}
	b.dependencies = append(b.dependencies, expressionDependency{ref: ref, revision: proc.revision})
	node := installedFunctionExpression{expressionInfo: info, arg: arg, procedure: proc, dictionary: dict, template: tmpl, ref: ref}
	if !node.validProvider() {
		return nil, fmt.Errorf("stuntgres: invalid installed function binding")
	}
	return node, nil
}
