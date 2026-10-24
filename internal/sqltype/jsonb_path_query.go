package sqltype

import (
	"context"
	"fmt"
)

// jsonbPathConsumer retains only immutable descriptor authority. Each invocation
// owns its own result state; the NAVIGATE executor owns traversal and errors.
type jsonbPathConsumer struct {
	result Usage
	args   [4]Usage
}

type JSONBPathExistsPlan struct{ jsonbPathConsumer }
type JSONBPathMatchPlan struct{ jsonbPathConsumer }
type JSONBPathQueryFirstPlan struct{ jsonbPathConsumer }
type JSONBPathQueryPlan struct{ jsonbPathConsumer }

func bindJSONBPathConsumer(result Usage, resultID ID, args []Usage) (jsonbPathConsumer, error) {
	p := jsonbPathConsumer{result: result}
	if len(args) != 4 || !result.Valid() || result.Typmod != -1 || result.Type.ID() != resultID {
		return p, fmt.Errorf("invalid JSONPath consumer signature")
	}
	ids := [4]ID{JSONB, JSONPath, JSONB, Bool}
	for i, u := range args {
		if !u.Valid() || u.Typmod != -1 || u.Type.ID() != ids[i] {
			return p, fmt.Errorf("invalid JSONPath consumer argument usage")
		}
		p.args[i] = u
	}
	if args[0].Type != args[2].Type ||
		(resultID == JSONB && result.Type != args[0].Type) ||
		(resultID == Bool && result.Type != args[3].Type) {
		return p, fmt.Errorf("foreign JSONPath consumer descriptor")
	}
	return p, nil
}

func BindJSONBPathExists(result Usage, args ...Usage) (JSONBPathExistsPlan, error) {
	p, err := bindJSONBPathConsumer(result, Bool, args)
	return JSONBPathExistsPlan{p}, err
}
func BindJSONBPathMatch(result Usage, args ...Usage) (JSONBPathMatchPlan, error) {
	p, err := bindJSONBPathConsumer(result, Bool, args)
	return JSONBPathMatchPlan{p}, err
}
func BindJSONBPathQueryFirst(result Usage, args ...Usage) (JSONBPathQueryFirstPlan, error) {
	p, err := bindJSONBPathConsumer(result, JSONB, args)
	return JSONBPathQueryFirstPlan{p}, err
}
func BindJSONBPathQuery(result Usage, args ...Usage) (JSONBPathQueryPlan, error) {
	p, err := bindJSONBPathConsumer(result, JSONB, args)
	return JSONBPathQueryPlan{p}, err
}

// The evaluator only reads vars; this empty payload is shared immutably.
var jsonbPathOperatorVars = jsonbPayload{root: jsonbNode{kind: jsonbObject}}

// EvaluateJSONPath executes the ordinary @?/@@ policy with invocation-owned
// input and warnings. Other BinaryPlan operations use Evaluate.
func (p BinaryPlan) EvaluateJSONPath(ctx context.Context, operands []Value, env JSONPathEnvironment) (Value, error) {
	if len(operands) != 2 || (p.name != "@?" && p.name != "@@") {
		return Value{}, fmt.Errorf("invalid JSONPath operator operands")
	}
	document, path := operands[0], operands[1]
	consumer := jsonbPathConsumer{result: p.result, args: p.args}
	values := [4]Value{
		document, path,
		{typ: p.args[2].Type, text: "{}", jsonb: &jsonbPathOperatorVars},
		Boolean(p.args[3].Type, true),
	}
	if p.name == "@?" {
		return (JSONBPathExistsPlan{consumer}).Evaluate(ctx, values[:], env)
	}
	return (JSONBPathMatchPlan{consumer}).Evaluate(ctx, values[:], env)
}

func (p jsonbPathConsumer) validate(ctx context.Context, values []Value) (bool, error) {
	if p.result.Type == nil || len(values) != 4 {
		return false, fmt.Errorf("invalid JSONPath consumer values")
	}
	for i, v := range values {
		if v.typ != p.args[i].Type {
			return false, fmt.Errorf("foreign JSONPath consumer value")
		}
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	for _, v := range values {
		if v.null {
			return true, nil
		}
	}
	if values[0].jsonb == nil || values[1].jsonpath == nil || values[1].jsonpath.root == nil || values[2].jsonb == nil {
		return false, fmt.Errorf("missing JSONPath consumer payload")
	}
	return false, nil
}

func (p jsonbPathConsumer) output(ctx context.Context, n *jsonbNode) (Value, error) {
	if n == nil {
		return Null(p.result.Type), nil
	}
	text, err := jsonbOutput(ctx, n)
	if err != nil {
		return Value{}, err
	}
	return Value{typ: p.result.Type, text: text, jsonb: &jsonbPayload{root: *n}}, nil
}

func (p JSONBPathExistsPlan) Evaluate(ctx context.Context, values []Value, env JSONPathEnvironment) (Value, error) {
	null, err := p.validate(ctx, values)
	if err != nil {
		return Value{}, err
	}
	if null {
		return Null(p.result.Type), nil
	}
	found := false
	status, err := executeJSONPath(ctx, values[0], values[1], values[2], env, func(jsonpathItem) (bool, error) {
		found = true
		// Strict existence must demand the tail, including ordinary errors.
		return values[1].jsonpath.strict, ctx.Err()
	})
	if err != nil {
		return Value{}, err
	}
	if status.kind == jsonpathSuppressibleStop {
		if !values[3].boolean {
			return Value{}, status.diagnostic
		}
		return Null(p.result.Type), nil
	}
	return Boolean(p.result.Type, found), nil
}

func (p JSONBPathMatchPlan) Evaluate(ctx context.Context, values []Value, env JSONPathEnvironment) (Value, error) {
	null, err := p.validate(ctx, values)
	if err != nil {
		return Value{}, err
	}
	if null {
		return Null(p.result.Type), nil
	}
	var first jsonpathItem
	count := 0
	status, err := executeJSONPath(ctx, values[0], values[1], values[2], env, func(n jsonpathItem) (bool, error) {
		if count == 0 {
			first = n
		}
		if count < 2 {
			count++
		}
		return true, ctx.Err()
	})
	if err != nil {
		return Value{}, err
	}
	if status.kind == jsonpathSuppressibleStop && !values[3].boolean {
		return Value{}, status.diagnostic
	}
	// A silent structural stop still leaves a meaningful singleton bool prefix.
	if count == 1 {
		if first.itemKind() == jsonbBoolean {
			return Boolean(p.result.Type, first.boolean), nil
		}
		if first.itemKind() == jsonbNull {
			return Null(p.result.Type), nil
		}
	}
	if values[3].boolean {
		return Null(p.result.Type), nil
	}
	return Value{}, failure("22038", "single boolean result is expected")
}

func (p JSONBPathQueryFirstPlan) Evaluate(ctx context.Context, values []Value, env JSONPathEnvironment) (Value, error) {
	null, err := p.validate(ctx, values)
	if err != nil {
		return Value{}, err
	}
	if null {
		return Null(p.result.Type), nil
	}
	var first jsonpathItem
	found := false
	status, err := executeJSONPath(ctx, values[0], values[1], values[2], env, func(n jsonpathItem) (bool, error) {
		if !found {
			first, found = n, true
		}
		// First selects one item, but never truncates demanded traversal.
		return true, ctx.Err()
	})
	if err != nil {
		return Value{}, err
	}
	if status.kind == jsonpathSuppressibleStop && !values[3].boolean {
		return Value{}, status.diagnostic
	}
	if !found {
		return Null(p.result.Type), nil
	}
	node, err := sqlJSONMaterialize(first)
	if err != nil {
		return Value{}, err
	}
	return p.output(ctx, node)
}

func (p JSONBPathQueryPlan) Evaluate(ctx context.Context, values []Value, env JSONPathEnvironment) ([]Value, error) {
	null, err := p.validate(ctx, values)
	if err != nil || null {
		return nil, err
	}
	var result []Value
	status, err := executeJSONPath(ctx, values[0], values[1], values[2], env, func(n jsonpathItem) (bool, error) {
		node, err := sqlJSONMaterialize(n)
		if err != nil {
			return false, err
		}
		v, err := p.output(ctx, node)
		if err != nil {
			return false, err
		}
		result = append(result, v)
		return true, nil
	})
	// Publish only a complete invocation or an explicitly silent prefix.
	if err != nil {
		return nil, err
	}
	if status.kind == jsonpathSuppressibleStop && !values[3].boolean {
		return nil, status.diagnostic
	}
	return result, nil
}
