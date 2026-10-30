package sqltype

import (
	"context"
	"fmt"

	"stuntgres/internal/util"
	"stuntgres/internal/workadmission"
)

// JSONBElementsPlan binds the two scalar-result JSONB array expansion signatures.
// A plan is immutable; each invocation owns a cursor borrowing immutable nodes.
type JSONBElementsPlan struct {
	name          string
	input, result Usage
}

func BindJSONBElements(name string, input, result Usage) (JSONBElementsPlan, error) {
	id := JSONB
	switch name {
	case "jsonb_array_elements":
	case "jsonb_array_elements_text":
		id = Text
	default:
		return JSONBElementsPlan{}, fmt.Errorf("invalid JSONB elements name")
	}
	if !input.Valid() || input.Typmod != -1 || input.Type.ID() != JSONB || !result.Valid() || result.Typmod != -1 || result.Type.ID() != id {
		return JSONBElementsPlan{}, fmt.Errorf("invalid JSONB elements usage")
	}
	return JSONBElementsPlan{name: name, input: input, result: result}, nil
}
func (p JSONBElementsPlan) Name() string       { return p.name }
func (p JSONBElementsPlan) ResultUsage() Usage { return p.result }

type JSONBElementCursor struct {
	result  *Descriptor
	payload *jsonbPayload
	index   int
	text    bool
}

func (p JSONBElementsPlan) Open(ctx context.Context, value Value) (JSONBElementCursor, error) {
	if p.input.Type == nil || p.result.Type == nil || value.typ != p.input.Type {
		return JSONBElementCursor{}, fmt.Errorf("invalid JSONB elements value")
	}
	if err := ctx.Err(); err != nil {
		return JSONBElementCursor{}, err
	}
	c := JSONBElementCursor{result: p.result.Type, text: p.name == "jsonb_array_elements_text"}
	if value.null {
		return c, nil
	}
	if value.jsonb == nil {
		return JSONBElementCursor{}, fmt.Errorf("invalid JSONB payload")
	}
	if value.jsonb.root.kind != jsonbArray {
		message := "cannot extract elements from a scalar"
		if value.jsonb.root.kind == jsonbObject {
			message = "cannot extract elements from an object"
		}
		return JSONBElementCursor{}, failure("22023", message)
	}
	c.payload = value.jsonb
	if err := ctx.Err(); err != nil {
		return JSONBElementCursor{}, err
	}
	return c, nil
}

func (c *JSONBElementCursor) Next(ctx context.Context) (Value, bool, error) {
	if err := util.ExecutionCheckpoint(ctx, "jsonb.elements.next"); err != nil {
		return Value{}, false, err
	}
	if c.result == nil {
		return Value{}, false, fmt.Errorf("closed JSONB element cursor")
	}
	if c.payload == nil || c.index >= len(c.payload.root.items) {
		return Null(c.result), false, nil
	}
	if err := workadmission.Admit(ctx, workadmission.WorkJSONBArrayExpand); err != nil {
		return Value{}, false, err
	}
	n := &c.payload.root.items[c.index]
	if c.text {
		switch n.kind {
		case jsonbNull:
			c.index++
			return Null(c.result), true, nil
		case jsonbString:
			c.index++
			return String(c.result, n.text), true, nil
		}
	}
	text, err := jsonbOutput(ctx, n)
	if err != nil {
		return Value{}, false, err
	}
	c.index++
	if c.text {
		return String(c.result, text), true, nil
	}
	return Value{typ: c.result, text: text, jsonb: &jsonbPayload{root: *n}}, true, nil
}
func (c *JSONBElementCursor) Close() { *c = JSONBElementCursor{} }
