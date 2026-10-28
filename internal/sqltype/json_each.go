package sqltype

import (
	"context"
	"fmt"

	"stuntgres/internal/workadmission"
)

// JSONEachPlan retains concrete usages only; all scan/iteration state belongs
// to an invocation. The result is one anonymous record, not two separate sets.
type JSONEachPlan struct {
	name          string
	input, result Usage
}

func BindJSONEach(name string, input, result Usage) (JSONEachPlan, error) {
	inputID, valueID := JSON, JSON
	switch name {
	case "json_each":
	case "json_each_text":
		valueID = Text
	case "jsonb_each":
		inputID, valueID = JSONB, JSONB
	case "jsonb_each_text":
		inputID, valueID = JSONB, Text
	default:
		return JSONEachPlan{}, fmt.Errorf("invalid JSON each name")
	}
	if !input.Valid() || input.Typmod != -1 || input.Type.ID() != inputID || !result.Valid() || result.Typmod != -1 || result.Type.ID() != 2249 || result.Type.CompositeLen() != 2 {
		return JSONEachPlan{}, fmt.Errorf("invalid JSON each usage")
	}
	for i, id := range []ID{Text, valueID} {
		member := result.Type.CompositeMember(i)
		memberName := "key"
		if i == 1 {
			memberName = "value"
		}
		if member.Attribute != int32(i+1) || member.Name != memberName || !member.Usage.Valid() || member.Usage.Type.ID() != id || member.Usage.Typmod != -1 || (id == inputID && member.Usage.Type != input.Type) {
			return JSONEachPlan{}, fmt.Errorf("invalid JSON each member usage")
		}
	}
	return JSONEachPlan{name: name, input: input, result: result}, nil
}
func (p JSONEachPlan) Name() string       { return p.name }
func (p JSONEachPlan) ResultUsage() Usage { return p.result }

type JSONEachCursor struct {
	result   *Descriptor
	text     string
	members  []jsonMember
	document Value
	index    int
	asText   bool
}

func (p JSONEachPlan) Open(ctx context.Context, value Value) (JSONEachCursor, error) {
	if p.input.Type == nil || p.result.Type == nil || value.typ != p.input.Type {
		return JSONEachCursor{}, fmt.Errorf("invalid JSON each value")
	}
	if err := ctx.Err(); err != nil {
		return JSONEachCursor{}, err
	}
	cursor := JSONEachCursor{result: p.result.Type, asText: p.name == "json_each_text" || p.name == "jsonb_each_text"}
	if value.null {
		return cursor, nil
	}
	if p.input.Type.ID() == JSONB {
		if value.jsonb == nil {
			return JSONEachCursor{}, fmt.Errorf("invalid JSONB payload")
		}
		if value.jsonb.root.kind != jsonbObject {
			return JSONEachCursor{}, failure("22023", "cannot call "+p.name+" on a non-object")
		}
		cursor.document = value
		return cursor, nil
	}
	scan := jsonScanResult{mode: jsonScanEach, workClass: workadmission.WorkJSONObjectExpand}
	if cursor.asText {
		scan.mode = jsonScanEachText
	}
	if _, err := value.typ.scanJSON(ctx, value.text, &scan); err != nil {
		return JSONEachCursor{}, err
	}
	cursor.text, cursor.members = value.text, scan.members
	return cursor, nil
}

func (c *JSONEachCursor) Next(ctx context.Context) (Value, bool, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, false, err
	}
	if c.result == nil {
		return Value{}, false, fmt.Errorf("closed JSON each cursor")
	}
	if c.document.jsonb != nil {
		fields := c.document.jsonb.root.fields
		if c.index == len(fields) {
			return Null(c.result), false, nil
		}
		if err := workadmission.Admit(ctx, workadmission.WorkJSONBObjectExpand); err != nil {
			return Value{}, false, err
		}
		member := &fields[c.index]
		keyType := c.result.CompositeMember(0).Usage.Type
		value, err := jsonbExtractResult(ctx, c.document, &member.value, c.result.CompositeMember(1).Usage.Type)
		if err != nil {
			return Value{}, false, err
		}
		row, err := OwnedCompositeValue(c.result, []Value{String(keyType, member.key), value})
		if err == nil {
			c.index++
		}
		return row, err == nil, err
	}
	if c.index == len(c.members) {
		return Null(c.result), false, nil
	}
	member := c.members[c.index]
	c.index++
	keyType := c.result.CompositeMember(0).Usage.Type
	valueType := c.result.CompositeMember(1).Usage.Type
	// All slices were validated by Open, including processing-time Unicode
	// checks. Do not reparse each raw value or normalize its number spelling.
	value := Value{typ: valueType, text: c.text[member.start:member.end]}
	if c.asText {
		if member.kind == 'z' {
			value = Null(valueType)
		}
		if member.kind == 's' {
			value = String(valueType, member.decoded)
		}
	}
	row, err := OwnedCompositeValue(c.result, []Value{String(keyType, member.key), value})
	return row, err == nil, err
}
func (c *JSONEachCursor) Close() { *c = JSONEachCursor{} }
