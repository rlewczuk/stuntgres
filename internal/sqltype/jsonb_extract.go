package sqltype

import (
	"context"
	"fmt"
)

// Extraction borrows immutable nodes; it never reparses the cached output or
// carries invocation-specific path/index state in a bound plan.
func jsonbExtractField(ctx context.Context, node *jsonbNode, key string) (*jsonbNode, error) {
	lo, hi := 0, len(node.fields)
	for lo < hi {
		mid := lo + (hi-lo)/2
		order, err := compareJSONBKeys(ctx, node.fields[mid].key, key)
		if err != nil {
			return nil, err
		}
		if order == 0 {
			return &node.fields[mid].value, nil
		}
		if order < 0 {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return nil, ctx.Err()
}

func jsonbExtractItem(node *jsonbNode, index int64) *jsonbNode {
	if index < 0 {
		index += int64(len(node.items))
	}
	if index < 0 || index >= int64(len(node.items)) {
		return nil
	}
	return &node.items[int(index)]
}

func jsonbExtractResult(ctx context.Context, doc Value, node *jsonbNode, result *Descriptor) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if node == nil {
		return Null(result), nil
	}
	if node.kind > jsonbObject {
		return Value{}, fmt.Errorf("invalid JSONB extraction node")
	}
	if result.ID() == Text {
		if node.kind == jsonbNull {
			return Null(result), nil
		}
		if node.kind == jsonbString {
			return String(result, node.text), nil
		}
	} else if node == &doc.jsonb.root {
		return doc, nil
	}
	text, err := jsonbOutput(ctx, node)
	if err != nil {
		return Value{}, err
	}
	if result.ID() == Text {
		return String(result, text), nil
	}
	return Value{typ: result, text: text, jsonb: &jsonbPayload{root: *node}}, nil
}

func validateJSONBExtractPath(ctx context.Context, path []Value, element *Descriptor) error {
	for i, v := range path {
		if i%jsonbWorkChunk == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		if v.typ != element {
			return fmt.Errorf("foreign JSONB extraction path element")
		}
	}
	return ctx.Err()
}

func extractJSONB(ctx context.Context, doc Value, path []Value, result *Descriptor) (Value, error) {
	if doc.jsonb == nil {
		return Value{}, fmt.Errorf("invalid JSONB payload")
	}
	node := &doc.jsonb.root
	if node.kind > jsonbObject {
		return Value{}, fmt.Errorf("invalid JSONB extraction node")
	}
	for _, part := range path {
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		if part.null {
			return Null(result), nil
		}
		var err error
		switch node.kind {
		case jsonbObject:
			node, err = jsonbExtractField(ctx, node, part.text)
		case jsonbArray:
			var index int64
			index, err = jsonbExtractIndex(ctx, part.text)
			if err == nil {
				node = jsonbExtractItem(node, index)
			}
		case jsonbNull, jsonbString, jsonbNumber, jsonbBoolean:
			return Null(result), nil
		default:
			return Value{}, fmt.Errorf("invalid JSONB extraction node")
		}
		if err != nil {
			return Value{}, err
		}
		if node == nil {
			return Null(result), nil
		}
	}
	return jsonbExtractResult(ctx, doc, node, result)
}

func (p BinaryPlan) jsonbExtraction(ctx context.Context, doc, request Value) (Value, error) {
	if doc.jsonb == nil {
		return Value{}, fmt.Errorf("invalid JSONB payload")
	}
	node := &doc.jsonb.root
	if node.kind > jsonbObject {
		return Value{}, fmt.Errorf("invalid JSONB extraction node")
	}
	if p.name == "#>" || p.name == "#>>" {
		if request.array == nil {
			return Value{}, fmt.Errorf("missing JSONB extraction path payload")
		}
		return extractJSONB(ctx, doc, request.array.elements, p.result.Type)
	}
	var err error
	if request.typ.ID() == Text {
		if node.kind != jsonbObject {
			return Null(p.result.Type), nil
		}
		node, err = jsonbExtractField(ctx, node, request.text)
	} else if node.kind == jsonbArray {
		node = jsonbExtractItem(node, request.integer)
	} else if node.kind == jsonbObject || request.integer != 0 && request.integer != -1 {
		return Null(p.result.Type), nil
	}
	// Integer operators expose PostgreSQL's root scalar wrapper at 0/-1.
	// Nonempty paths deliberately do not: traversal above rejects scalars.
	if err != nil {
		return Value{}, err
	}
	return jsonbExtractResult(ctx, doc, node, p.result.Type)
}

// Match extraction's strtol-style int32 syntax, but poll bounded work even for
// arbitrarily long leading whitespace or zero runs. Invalid means no match.
func jsonbExtractIndex(ctx context.Context, text string) (int64, error) {
	const invalid = int64(-2147483648)
	i := 0
	for i < len(text) && space(text[i]) {
		if i%jsonbWorkChunk == 0 {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
		}
		i++
	}
	negative := false
	if i < len(text) && (text[i] == '-' || text[i] == '+') {
		negative = text[i] == '-'
		i++
	}
	start, n := i, int64(0)
	limit := int64(2147483647)
	if negative {
		limit++
	}
	for i < len(text) {
		if i%jsonbWorkChunk == 0 {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
		}
		c := text[i]
		if c < '0' || c > '9' {
			return invalid, ctx.Err()
		}
		digit := int64(c - '0')
		if n > (limit-digit)/10 {
			return invalid, ctx.Err()
		}
		n = n*10 + digit
		i++
	}
	if i == start {
		return invalid, ctx.Err()
	}
	if negative {
		n = -n
	}
	return n, ctx.Err()
}
