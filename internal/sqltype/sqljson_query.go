package sqltype

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"stuntgres/internal/sqlstate"
)

// SQLJSONQueryPlan is immutable descriptor authority for a standard query
// expression. Evaluation state and ordered PASSING values belong to the call.
type SQLJSONQueryPlan struct {
	kind, wrapper                 string
	omit                          bool
	result, document, path, jsonb Usage
	passing                       []Usage
	names                         []string
	column                        string
}

type SQLJSONQueryOutcome struct {
	Value Value
	Empty bool
	Error error
}

func BindSQLJSONQuery(kind, wrapper string, omit bool, result, document, path, jsonb Usage, passing []Usage, names []string) (SQLJSONQueryPlan, error) {
	return bindSQLJSONQuery(kind, wrapper, omit, result, document, path, jsonb, passing, names, false)
}
func BindSQLJSONTableColumn(kind, wrapper string, omit bool, result, document, path, jsonb Usage, passing []Usage, names []string) (SQLJSONQueryPlan, error) {
	return bindSQLJSONQuery(kind, wrapper, omit, result, document, path, jsonb, passing, names, true)
}
func bindSQLJSONQuery(kind, wrapper string, omit bool, result, document, path, jsonb Usage, passing []Usage, names []string, table bool) (SQLJSONQueryPlan, error) {
	if kind != "json_exists" && kind != "json_query" && kind != "json_value" || !result.Valid() || !document.Valid() || document.Type.ID() != JSONB || !path.Valid() || path.Type.ID() != JSONPath || !jsonb.Valid() || jsonb.Type.ID() != JSONB || len(passing) != len(names) {
		return SQLJSONQueryPlan{}, fmt.Errorf("invalid SQL/JSON query plan")
	}
	if !table && kind == "json_exists" && result.Type.ID() != Bool {
		return SQLJSONQueryPlan{}, fmt.Errorf("invalid JSON_EXISTS result")
	}
	if wrapper != "" && wrapper != "without" && wrapper != "conditional" && wrapper != "unconditional" || kind != "json_query" && (wrapper != "" || omit) {
		return SQLJSONQueryPlan{}, fmt.Errorf("invalid SQL/JSON query options")
	}
	if document.Type != jsonb.Type || result.Type.ID() == JSONB && result.Type != jsonb.Type {
		return SQLJSONQueryPlan{}, fmt.Errorf("mismatched SQL/JSON query owner")
	}
	for i, u := range passing {
		if !u.Valid() {
			return SQLJSONQueryPlan{}, fmt.Errorf("invalid PASSING usage")
		}
		for _, owner := range []Usage{document, path, result} {
			if u.Type.ID() != 2249 && u.Type.ID() == owner.Type.ID() && u.Type != owner.Type {
				return SQLJSONQueryPlan{}, fmt.Errorf("mismatched PASSING owner")
			}
		}
		for _, previous := range passing[:i] {
			if u.Type.ID() != 2249 && u.Type.ID() == previous.Type.ID() && u.Type != previous.Type {
				return SQLJSONQueryPlan{}, fmt.Errorf("mismatched PASSING owner")
			}
		}
	}
	return SQLJSONQueryPlan{kind: kind, wrapper: wrapper, omit: omit, result: result, document: document, path: path, jsonb: jsonb, passing: append([]Usage(nil), passing...), names: append([]string(nil), names...)}, nil
}

func (p SQLJSONQueryPlan) Evaluate(ctx context.Context, document, path Value, passing []Value, input InputEnvironment, output OutputEnvironment) (SQLJSONQueryOutcome, error) {
	if err := ctx.Err(); err != nil {
		return SQLJSONQueryOutcome{}, err
	}
	if document.typ != p.document.Type || path.typ != p.path.Type || len(passing) != len(p.passing) {
		return SQLJSONQueryOutcome{}, fmt.Errorf("foreign SQL/JSON query inputs")
	}
	if document.null || path.null {
		return SQLJSONQueryOutcome{Value: Null(p.result.Type)}, nil
	}
	if document.jsonb == nil || path.jsonpath == nil {
		return SQLJSONQueryOutcome{}, fmt.Errorf("missing SQL/JSON query payload")
	}
	var local [8]jsonpathBinding
	bindings := local[:min(len(passing), len(local))]
	if len(passing) > len(local) {
		bindings = make([]jsonpathBinding, len(passing))
	}
	bindings, err := p.bindings(ctx, passing, output, bindings)
	if err != nil {
		return SQLJSONQueryOutcome{}, err
	}
	return p.evaluate(ctx, document, path, bindings, input, output)
}

func (p SQLJSONQueryPlan) bindings(ctx context.Context, passing []Value, output OutputEnvironment, bindings []jsonpathBinding) ([]jsonpathBinding, error) {
	temporalCount := 0
	for _, value := range passing {
		if value.typ != nil && !value.null && sqlJSONTemporal(value.typ.BaseType().ID()) {
			temporalCount++
		}
	}
	var temporals []Value
	if temporalCount > 0 {
		temporals = make([]Value, 0, temporalCount)
	}
	for i, v := range passing {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if v.typ != p.passing[i].Type {
			return nil, fmt.Errorf("foreign SQL/JSON PASSING value")
		}
		base, err := v.BaseValue()
		if err != nil {
			return nil, err
		}
		item := jsonpathItem{}
		if !base.null && sqlJSONTemporal(base.typ.ID()) {
			temporals = append(temporals, base)
			item.temporal = &temporals[len(temporals)-1]
		} else if !base.null && base.typ.ID() == JSONB {
			if base.jsonb == nil {
				return nil, fmt.Errorf("missing PASSING JSONB payload")
			}
			item.jsonbNode = &base.jsonb.root
		} else {
			writer := jsonCreateWriter{ctx: ctx, env: output, result: p.jsonb.Type, binary: true}
			if err := writer.value(base, false); err != nil {
				return nil, err
			}
			item.jsonbNode = &writer.tree.root
		}
		bindings[i] = jsonpathBinding{name: p.names[i], item: item}
	}
	return bindings, nil
}

func (p SQLJSONQueryPlan) evaluate(ctx context.Context, document, path Value, bindings []jsonpathBinding, input InputEnvironment, output OutputEnvironment) (SQLJSONQueryOutcome, error) {
	executor := jsonpathExecutor{ctx: ctx, root: &document.jsonb.root, strict: path.jsonpath.strict, bindings: bindings, input: input}
	count := 0
	var first jsonpathItem
	var items []jsonbNode
	status, err := executor.execute(path, func(item jsonpathItem) (bool, error) {
		if count == 0 {
			first = item
		}
		if p.kind == "json_query" && (p.wrapper == "unconditional" || p.wrapper == "conditional" && count > 0) {
			if p.wrapper == "conditional" && count == 1 {
				node, err := sqlJSONMaterialize(first)
				if err != nil {
					return false, err
				}
				items = append(items, *node)
			}
			node, err := sqlJSONMaterialize(item)
			if err != nil {
				return false, err
			}
			items = append(items, *node)
		}
		if count < 2 {
			count++
		}
		return p.kind != "json_exists" || path.jsonpath.strict, ctx.Err()
	})
	if err != nil {
		return SQLJSONQueryOutcome{}, err
	}
	if status.kind == jsonpathSuppressibleStop {
		return SQLJSONQueryOutcome{Error: status.diagnostic}, nil
	}
	if p.kind == "json_exists" {
		value, err := p.exists(ctx, count != 0, input)
		if err != nil {
			if sqlJSONConversionError(err) {
				return SQLJSONQueryOutcome{Error: err}, nil
			}
			return SQLJSONQueryOutcome{}, err
		}
		return SQLJSONQueryOutcome{Value: value}, nil
	}
	if count == 0 {
		return SQLJSONQueryOutcome{Empty: true}, nil
	}
	if p.kind == "json_value" {
		if count > 1 {
			return SQLJSONQueryOutcome{Error: failure("22034", p.scalarMessage())}, nil
		}
		if first.itemKind() == jsonbArray || first.itemKind() == jsonbObject {
			return SQLJSONQueryOutcome{Error: failure("2203F", p.scalarMessage())}, nil
		}
	} else if count > 1 && (p.wrapper == "" || p.wrapper == "without") {
		message := "JSON path expression in JSON_QUERY must return single item when no wrapper is requested"
		if p.column != "" {
			message = "JSON path expression for column \"" + p.column + "\" must return single item when no wrapper is requested"
		}
		diagnostic := failure("22034", message).(*sqlstate.Error)
		diagnostic.Hint = "Use the WITH WRAPPER clause to wrap SQL/JSON items into an array."
		return SQLJSONQueryOutcome{Error: diagnostic}, nil
	}
	if p.kind == "json_query" && (p.wrapper == "unconditional" || p.wrapper == "conditional" && count > 1) {
		first = jsonpathItem{jsonbNode: &jsonbNode{kind: jsonbArray, items: items}}
	}
	value, err := p.convert(ctx, first, input, output)
	if err != nil {
		if sqlJSONConversionError(err) {
			return SQLJSONQueryOutcome{Error: err}, nil
		}
		return SQLJSONQueryOutcome{}, err
	}
	return SQLJSONQueryOutcome{Value: value}, nil
}

func sqlJSONMaterialize(item jsonpathItem) (*jsonbNode, error) {
	if item.temporal != nil {
		return &jsonbNode{kind: jsonbString, text: sqlJSONTemporalJSON(*item.temporal)}, nil
	}
	if item.jsonbNode == nil {
		return nil, fmt.Errorf("missing SQL/JSON item")
	}
	return item.jsonbNode, nil
}

func sqlJSONConversionError(err error) bool {
	var diagnostic *sqlstate.Error
	return errors.As(err, &diagnostic) && (strings.HasPrefix(diagnostic.Code, "22") || strings.HasPrefix(diagnostic.Code, "23"))
}

func (p SQLJSONQueryPlan) convert(ctx context.Context, item jsonpathItem, input InputEnvironment, output OutputEnvironment) (Value, error) {
	target := p.result.Type
	base := target.BaseType()
	if p.kind == "json_value" && item.itemKind() == jsonbNull {
		return p.null(ctx, input)
	}
	if item.temporal != nil && p.kind == "json_value" && base.ID() != JSON && base.ID() != JSONB {
		text, err := item.temporal.typ.OutputWithEnvironment(ctx, *item.temporal, output)
		if err != nil {
			return Value{}, err
		}
		return target.InputWithEnvironment(ctx, text, p.result.Typmod, input)
	}
	node, err := sqlJSONMaterialize(item)
	if err != nil {
		return Value{}, err
	}
	if p.kind == "json_value" && node.kind == jsonbBoolean && base.ID() != JSON && base.ID() != JSONB {
		text := "f"
		if node.boolean {
			text = "t"
		}
		return target.InputWithEnvironment(ctx, text, p.result.Typmod, input)
	}
	if p.kind == "json_query" && !p.omit && node.kind == jsonbString {
		text, err := jsonbOutput(ctx, node)
		if err != nil {
			return Value{}, err
		}
		if base.ID() != JSON && base.ID() != JSONB {
			return target.InputWithEnvironment(ctx, text, p.result.Typmod, input)
		}
	}
	if p.kind == "json_query" && p.omit && node.kind == jsonbString && (base.ID() == JSON || base.ID() == JSONB) {
		return target.InputWithEnvironment(ctx, node.text, p.result.Typmod, input)
	}
	if node.kind == jsonbNull {
		return target.InputWithEnvironment(ctx, "null", p.result.Typmod, input)
	}
	return populateJSONBField(ctx, p.result, Null(target), node, input, "")
}

func (p SQLJSONQueryPlan) null(ctx context.Context, input InputEnvironment) (Value, error) {
	if p.result.Type.IsDomain() {
		if err := p.result.Type.CheckDomainInput(ctx, Null(p.result.Type.BaseType()), input); err != nil {
			return Value{}, err
		}
	}
	return Null(p.result.Type), nil
}

// Behavior converts generated behavior values outside the recoverable result
// boundary. The expression carrier alone evaluates a selected DEFAULT child.
func (p SQLJSONQueryPlan) Behavior(ctx context.Context, kind, branch string, input InputEnvironment, output OutputEnvironment) (Value, error) {
	var value Value
	var err error
	switch kind {
	case "true", "false":
		value, err = p.exists(ctx, kind == "true", input)
	case "empty array", "empty object":
		node := jsonbNode{kind: jsonbArray}
		if kind == "empty object" {
			node.kind = jsonbObject
		}
		value, err = p.convert(ctx, jsonpathItem{jsonbNode: &node}, input, output)
	default:
		value, err = p.null(ctx, input)
	}
	if err != nil && sqlJSONConversionError(err) {
		return Value{}, SQLJSONBehaviorError(branch, kind, err)
	}
	return value, err
}

func SQLJSONBehaviorError(branch, kind string, cause error) error {
	if !sqlJSONConversionError(cause) {
		return cause
	}
	diagnostic := failure("42804", "could not coerce ON "+branch+" expression ("+strings.ToUpper(kind)+") to the RETURNING type").(*sqlstate.Error)
	var inner *sqlstate.Error
	if errors.As(cause, &inner) {
		diagnostic.Detail = inner.Message
	}
	return diagnostic
}

// TableColumn adds table-only result conversion and diagnostic context without
// changing standalone SQL/JSON function behavior.
func (p SQLJSONQueryPlan) TableColumn(name string, result Usage) SQLJSONQueryPlan {
	p.column, p.result = name, result
	return p
}
func (p SQLJSONQueryPlan) scalarMessage() string {
	if p.column != "" {
		return "JSON path expression for column \"" + p.column + "\" must return single scalar item"
	}
	return "JSON path expression in JSON_VALUE must return single scalar item"
}
func (p SQLJSONQueryPlan) exists(ctx context.Context, truth bool, input InputEnvironment) (Value, error) {
	if p.column == "" {
		return Boolean(p.result.Type, truth), nil
	}
	text := "false"
	if truth {
		text = "true"
	}
	if p.result.Type.BaseType().ID() == Int4 {
		text = "0"
		if truth {
			text = "1"
		}
	}
	return p.result.Type.InputWithEnvironment(ctx, text, p.result.Typmod, input)
}
