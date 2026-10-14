package engine

import (
	"fmt"
	"strings"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func jsonCreateKind(name string) sqltype.JSONCreateKind {
	switch strings.ToLower(name) {
	case "to_json":
		return sqltype.ToJSON
	case "to_jsonb":
		return sqltype.ToJSONB
	case "array_to_json":
		return sqltype.ArrayToJSON
	case "row_to_json":
		return sqltype.RowToJSON
	case "json_build_array":
		return sqltype.JSONBuildArray
	case "jsonb_build_array":
		return sqltype.JSONBBuildArray
	case "json_build_object":
		return sqltype.JSONBuildObject
	case "jsonb_build_object":
		return sqltype.JSONBBuildObject
	}
	return 0
}

type jsonCreateExpression struct {
	expressionInfo
	kind     sqltype.JSONCreateKind
	plan     sqltype.JSONCreatePlan
	args     []BoundExpr
	variadic bool
	unknown  [2]uint64
}

func (e jsonCreateExpression) unknownArgument(i int) bool {
	return e.unknown[i/64]&(uint64(1)<<uint(i%64)) != 0
}
func (e jsonCreateExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	var scratch [100]sqltype.Value
	if len(e.args) > len(scratch) {
		return sqltype.Value{}, fmt.Errorf("invalid JSON creation expression arity")
	}
	for i, arg := range e.args {
		v, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		scratch[i] = v
	}
	output := c.Output
	output.Regclass, output.Regtype = c.liveScope(), c.liveScope()
	output.Regrole = c.liveScope()
	output.Operator = c.liveScope()
	output.Regproc = c.liveScope()
	output.Regcollation, output.Regnamespace = c.liveScope(), c.liveScope()
	output.RegTS = c.liveScope()
	output.ACLItem = c.liveScope()
	return e.plan.Apply(c.Context, scratch[:len(e.args)], e.variadic && e.kind.Builder(), output)
}
func (e *jsonCreateExpression) replan() error {
	var local [100]sqltype.Usage
	if len(e.args) > len(local) {
		return fmt.Errorf("invalid JSON creation expression arity")
	}
	for i, arg := range e.args {
		local[i] = arg.ResultUsage()
	}
	var err error
	e.plan, err = sqltype.BindJSONCreate(e.kind, e.usage, local[:len(e.args)]...)
	return err
}

func (b *expressionBinder) bindJSONCreate(raw parser.FunctionExpr) (BoundExpr, error) {
	kind := jsonCreateKind(raw.Name.Parts[len(raw.Name.Parts)-1].Text)
	var operands []comparisonOperand
	var small [2]comparisonOperand
	if len(raw.Args) <= len(small) {
		operands = small[:len(raw.Args)]
	} else if len(raw.Args) <= 100 {
		operands = make([]comparisonOperand, len(raw.Args))
	}
	for i, r := range raw.Args {
		o, err := b.stringOperand(r, false)
		if err != nil {
			return nil, err
		}
		if operands != nil {
			operands[i] = o
		}
	}
	if len(raw.ArgNames) != 0 && len(raw.ArgNames) != len(raw.Args) {
		return nil, fmt.Errorf("invalid JSON function argument labels")
	}
	named := false
	for i, label := range raw.ArgNames {
		if label.Text == "" {
			if named {
				return nil, expressionError("42601", "positional argument cannot follow named argument", raw.Args[i].SourcePosition())
			}
			continue
		}
		named = true
		for _, previous := range raw.ArgNames[:i] {
			if previous.Text == label.Text {
				return nil, expressionError("42601", fmt.Sprintf("argument name %q used more than once", label.Text), label.Pos)
			}
		}
	}
	if len(raw.Args) > 100 {
		return nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	visible, err := b.builtinVisible(raw.Name, functionLookup)
	if err != nil {
		return nil, err
	}
	valid := visible && raw.Name.Parts[len(raw.Name.Parts)-1].Text == kind.Name() && !raw.ResolutionOnly && len(raw.ArgNames) == 0
	if kind.Builder() {
		valid = valid && (!raw.Variadic || len(operands) == 1)
	} else {
		valid = valid && len(operands) >= 1 && len(operands) <= 2
		if kind == sqltype.ToJSON || kind == sqltype.ToJSONB {
			valid = valid && len(operands) == 1
		}
		if len(operands) > 0 && operands[0].descriptor() != nil {
			d := operands[0].descriptor().BaseType()
			if kind == sqltype.ArrayToJSON {
				valid = valid && d.IsArray()
			}
			if kind == sqltype.RowToJSON {
				valid = valid && d.IsComposite()
			}
		}
		if len(operands) == 2 && operands[1].descriptor() != nil {
			_, err := sqltype.PlanCoercion(operands[1].node.ResultUsage(), b.typ(sqltype.Bool).Usage(), sqltype.Implicit)
			valid = valid && err == nil
		}
	}
	if !valid {
		names := make([]string, len(operands))
		for i, o := range operands {
			names[i] = "unknown"
			if d := o.descriptor(); d != nil {
				names[i] = b.diagnosticTypeName(d)
			}
			names[i] = functionNamedType(raw, i, names[i])
		}
		return nil, b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
	}
	name := kind.Name()
	switch {
	case raw.Star:
		return nil, expressionError("42809", name+"(*) specified, but "+name+" is not an aggregate function", raw.Pos)
	case raw.Distinct:
		return nil, expressionError("42809", "DISTINCT specified, but "+name+" is not an aggregate function", raw.Pos)
	case len(raw.Order) != 0:
		return nil, expressionError("42809", "ORDER BY specified, but "+name+" is not an aggregate function", raw.Pos)
	case raw.Filter != nil:
		return nil, expressionError("42809", "FILTER specified, but "+name+" is not an aggregate function", raw.Pos)
	case raw.Window != nil:
		return nil, expressionError("42809", "OVER specified, but "+name+" is not a window function nor an aggregate function", raw.Pos)
	}
	args := make([]BoundExpr, len(operands))
	var unknown [2]uint64
	properties := ExprProperties{Volatility: Stable}
	for i, o := range operands {
		arg := o.node
		rawUnknown := false
		if literal, ok := raw.Args[i].(parser.LiteralExpr); ok {
			rawUnknown = literal.Value.Kind == parser.StringValue || literal.Value.Kind == parser.NullValue
		}
		if rawUnknown {
			unknown[i/64] |= uint64(1) << uint(i%64)
		}
		if !kind.Builder() && i == 0 && o.descriptor() == nil {
			if kind != sqltype.RowToJSON {
				return nil, sqlError("42804", "could not determine polymorphic type because input has type unknown")
			}
			// The fixed RECORD formal supplies an occurrence-owned transport identity,
			// not a promotion of catalog RECORD or an anonymous composite text codec.
			var record *sqltype.Descriptor
			if p, ok := o.node.(parameterExpression); ok {
				record = b.parameters[p.number].Type
			}
			if record == nil {
				record, err = sqltype.RecordShape(nil)
				if err != nil {
					return nil, err
				}
			}
			arg, err = b.finalizeOperand(o, record)
		}
		if !kind.Builder() && i == 1 {
			arg, err = b.likeOperand(o, b.typ(sqltype.Bool))
		} else if kind.Builder() && o.descriptor() == nil && rawUnknown {
			arg, err = b.finalizeOperand(o, b.typ(sqltype.Text))
		}
		if err != nil {
			return nil, err
		}
		if arg == nil {
			return nil, fmt.Errorf("missing JSON function operand")
		}
		args[i] = arg
		if parameter, ok := arg.(parameterExpression); ok && parameter.usage.Type == nil {
			b.anyParameters = append(b.anyParameters, parameter)
		}
		p := arg.Properties()
		properties.Volatility = max(properties.Volatility, p.Volatility)
		properties.RowDependent = properties.RowDependent || p.RowDependent
		properties.ParameterDependent = properties.ParameterDependent || p.ParameterDependent
		properties.StageDependent = properties.StageDependent || p.StageDependent
		properties.DomainDependent = properties.DomainDependent || p.DomainDependent
		properties.RuntimeDomainConversion = properties.RuntimeDomainConversion || p.RuntimeDomainConversion
		if arg.ResultUsage().Type != nil && arg.ResultUsage().Type.ContainsCatalogIdentifier() {
			b.regclassConcat = true
		}
	}
	if kind.Builder() && raw.Variadic && (args[0].ResultUsage().Type == nil || !args[0].ResultUsage().Type.BaseType().IsArray()) {
		return nil, expressionError("42804", "VARIADIC argument must be an array", raw.Args[0].SourcePosition())
	}
	id := sqltype.JSON
	if kind == sqltype.ToJSONB || kind == sqltype.JSONBBuildArray || kind == sqltype.JSONBBuildObject {
		id = sqltype.JSONB
	}
	node := jsonCreateExpression{expressionInfo: b.info(b.typ(id).Usage(), raw.Pos, properties), kind: kind, args: args, variadic: raw.Variadic, unknown: unknown}
	if err := node.replan(); err != nil {
		return nil, err
	}
	return node, nil
}
