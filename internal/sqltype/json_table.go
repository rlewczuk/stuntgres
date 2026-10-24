package sqltype

import (
	"context"
	"fmt"
)

// JSONTableRows freezes typed PASSING authority. Each Select discards its entire
// private prefix on a suppressible row-path error, unlike a streaming SRF.
type JSONTableRows struct{ plan SQLJSONQueryPlan }

func BindJSONTableRows(document, path, jsonb Usage, passing []Usage, names []string) (JSONTableRows, error) {
	p, err := BindSQLJSONQuery("json_query", "", false, jsonb, document, path, jsonb, passing, names)
	return JSONTableRows{plan: p}, err
}

type JSONTableInvocation struct {
	plan     SQLJSONQueryPlan
	bindings []jsonpathBinding
}

func (p JSONTableRows) Start(ctx context.Context, passing []Value, output OutputEnvironment) (JSONTableInvocation, error) {
	if len(passing) != len(p.plan.passing) {
		return JSONTableInvocation{}, fmt.Errorf("invalid JSON_TABLE bindings")
	}
	bindings, err := p.plan.bindings(ctx, passing, output, make([]jsonpathBinding, len(passing)))
	return JSONTableInvocation{plan: p.plan, bindings: bindings}, err
}
func (p JSONTableInvocation) Select(ctx context.Context, document, path Value, throw bool, env JSONPathEnvironment) ([]Value, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if document.typ != p.plan.document.Type || path.typ != p.plan.path.Type {
		return nil, fmt.Errorf("foreign JSON_TABLE row inputs")
	}
	if document.null {
		return nil, nil
	}
	if document.jsonb == nil || path.jsonpath == nil {
		return nil, fmt.Errorf("missing JSON_TABLE row payload")
	}
	executor := jsonpathExecutor{ctx: ctx, root: &document.jsonb.root, strict: path.jsonpath.strict, bindings: p.bindings, input: env.Input, notice: env.Notice, timezone: true}
	var rows []Value
	status, err := executor.execute(path, func(item jsonpathItem) (bool, error) {
		node, err := sqlJSONMaterialize(item)
		if err != nil {
			return false, err
		}
		text, err := jsonbOutput(ctx, node)
		if err != nil {
			return false, err
		}
		rows = append(rows, Value{typ: p.plan.jsonb.Type, text: text, jsonb: &jsonbPayload{root: *node}})
		return true, ctx.Err()
	})
	if err != nil {
		return nil, err
	}
	if status.kind == jsonpathSuppressibleStop {
		if throw {
			return nil, status.diagnostic
		}
		return nil, nil
	}
	return rows, nil
}
