package sqltype

import (
	"context"
	"fmt"
)

// JSONElementsPlan owns concrete type usages, never mutable invocation state.
type JSONElementsPlan struct {
	name          string
	input, result Usage
}

func BindJSONElements(name string, input, result Usage) (JSONElementsPlan, error) {
	if name != "json_array_elements" && name != "json_array_elements_text" {
		return JSONElementsPlan{}, fmt.Errorf("invalid JSON elements name")
	}
	id := JSON
	if name == "json_array_elements_text" {
		id = Text
	}
	if !input.Valid() || input.Typmod != -1 || input.Type.ID() != JSON || !result.Valid() || result.Typmod != -1 || result.Type.ID() != id || id == JSON && result.Type != input.Type {
		return JSONElementsPlan{}, fmt.Errorf("invalid JSON elements usage")
	}
	return JSONElementsPlan{name: name, input: input, result: result}, nil
}

func (p JSONElementsPlan) Name() string       { return p.name }
func (p JSONElementsPlan) ResultUsage() Usage { return p.result }

type JSONElementsCursor struct {
	result  *Descriptor
	text    string
	members []jsonMember
	index   int
	asText  bool
}

func (p JSONElementsPlan) Open(ctx context.Context, value Value) (JSONElementsCursor, error) {
	if p.input.Type == nil || p.result.Type == nil || value.typ != p.input.Type {
		return JSONElementsCursor{}, fmt.Errorf("invalid JSON elements value")
	}
	if err := ctx.Err(); err != nil {
		return JSONElementsCursor{}, err
	}
	cursor := JSONElementsCursor{result: p.result.Type, asText: p.name == "json_array_elements_text"}
	if value.null {
		return cursor, nil
	}
	scan := jsonScanResult{mode: jsonScanElements}
	if cursor.asText {
		scan.mode = jsonScanElementsText
	}
	if _, err := value.typ.scanJSON(ctx, value.text, &scan); err != nil {
		return JSONElementsCursor{}, err
	}
	cursor.text, cursor.members = value.text, scan.members
	return cursor, nil
}

func (c *JSONElementsCursor) Next(ctx context.Context) (Value, bool, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, false, err
	}
	if c.result == nil {
		return Value{}, false, fmt.Errorf("closed JSON elements cursor")
	}
	if c.index == len(c.members) {
		return Null(c.result), false, nil
	}
	member := c.members[c.index]
	c.index++
	// Open validated these spans. Preserve raw nested spelling and huge numbers
	// without reparsing or allocating a composite for this scalar result.
	value := Value{typ: c.result, text: c.text[member.start:member.end]}
	if c.asText {
		if member.kind == 'z' {
			value = Null(c.result)
		}
		if member.kind == 's' {
			value = String(c.result, member.decoded)
		}
	}
	return value, true, nil
}

func (c *JSONElementsCursor) Close() { *c = JSONElementsCursor{} }
