package sqltype

import (
	"context"
	"fmt"
)

// JSONBPathQueryArrayPlan retains descriptor authority, never execution scratch.
type JSONBPathQueryArrayPlan struct {
	result Usage
	args   [4]Usage
}

func BindJSONBPathQueryArray(result Usage, args ...Usage) (JSONBPathQueryArrayPlan, error) {
	p := JSONBPathQueryArrayPlan{result: result}
	if len(args) != 4 || !result.Valid() || result.Typmod != -1 || result.Type.ID() != JSONB {
		return p, fmt.Errorf("invalid JSONPath array signature")
	}
	ids := [4]ID{JSONB, JSONPath, JSONB, Bool}
	for i, u := range args {
		if !u.Valid() || u.Typmod != -1 || u.Type.ID() != ids[i] {
			return p, fmt.Errorf("invalid JSONPath array argument usage")
		}
		if ids[i] == JSONB && u.Type != result.Type {
			return p, fmt.Errorf("foreign JSONPath array descriptor")
		}
		p.args[i] = u
	}
	return p, nil
}

func (p JSONBPathQueryArrayPlan) Evaluate(ctx context.Context, values []Value, env JSONPathEnvironment) (Value, error) {
	if p.result.Type == nil || len(values) != 4 {
		return Value{}, fmt.Errorf("invalid JSONPath array values")
	}
	for i, v := range values {
		if v.typ != p.args[i].Type {
			return Value{}, fmt.Errorf("foreign JSONPath array value")
		}
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	for _, v := range values {
		if v.null {
			return Null(p.result.Type), nil
		}
	}
	if values[0].jsonb == nil || values[1].jsonpath == nil || values[2].jsonb == nil {
		return Value{}, fmt.Errorf("missing JSONPath array payload")
	}
	root := jsonbNode{kind: jsonbArray}
	status, err := executeJSONPath(ctx, values[0], values[1], values[2], env, func(n jsonpathItem) (bool, error) {
		node, err := sqlJSONMaterialize(n)
		if err != nil {
			return false, err
		}
		root.items = append(root.items, *node)
		return true, ctx.Err()
	})
	if err != nil {
		return Value{}, err
	}
	if status.kind == jsonpathSuppressibleStop && !values[3].boolean {
		return Value{}, status.diagnostic
	}
	text, err := jsonbOutput(ctx, &root)
	if err != nil {
		return Value{}, err
	}
	return Value{typ: p.result.Type, text: text, jsonb: &jsonbPayload{root: root}}, nil
}
