package sqltype

import (
	"context"
	"fmt"
)

type jsonScanMode uint8

const (
	jsonScanKeys jsonScanMode = iota
	jsonScanEach
	jsonScanEachText
)

type jsonMember struct {
	key, decoded string
	start, end   int
	kind         byte
}

type jsonScanResult struct {
	root    byte
	mode    jsonScanMode
	keys    []string
	members []jsonMember
	extract *jsonExtraction
}

func (s *jsonScanResult) checkRoot() error {
	if s.extract != nil {
		return nil
	}
	if s.root == '{' {
		return nil
	}
	if s.mode != jsonScanKeys {
		if s.root == '[' {
			return failure("22023", "cannot deconstruct an array as an object")
		}
		return failure("22023", "cannot deconstruct a scalar")
	}
	if s.root == '[' {
		return failure("22023", "cannot call json_object_keys on an array")
	}
	return failure("22023", "cannot call json_object_keys on a scalar")
}

func jsonTokenKind(kind byte) string {
	switch kind {
	case '{':
		return "object"
	case '[':
		return "array"
	case 's':
		return "string"
	case 'n':
		return "number"
	case 't', 'f':
		return "boolean"
	case 'z':
		return "null"
	default:
		return ""
	}
}
func jsonbRootKind(kind jsonbKind) string {
	switch kind {
	case jsonbObject:
		return "object"
	case jsonbArray:
		return "array"
	case jsonbString:
		return "string"
	case jsonbNumber:
		return "number"
	case jsonbBoolean:
		return "boolean"
	case jsonbNull:
		return "null"
	default:
		return ""
	}
}

func (p BinaryPlan) jsonScalar(ctx context.Context, value Value) (Value, error) {
	var kind string
	switch p.name {
	case "json_typeof":
		l := jsonLexer{ctx: ctx, text: value.text, line: 1}
		if err := l.next(); err != nil {
			return Value{}, err
		}
		kind = jsonTokenKind(l.kind)
	case "jsonb_typeof":
		if value.jsonb == nil {
			return Value{}, fmt.Errorf("invalid JSONB payload")
		}
		kind = jsonbRootKind(value.jsonb.root.kind)
	default:
		return Value{}, fmt.Errorf("invalid JSON scalar operation")
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if kind == "" {
		return Value{}, fmt.Errorf("invalid JSON root")
	}
	return String(p.result.Type, kind), nil
}

// JSONKeysPlan binds exactly the two selected strict SETOF TEXT signatures.
// Cursor state is invocation-private; plans retain only concrete type usages.
type JSONKeysPlan struct {
	name          string
	input, result Usage
}

func BindJSONKeys(name string, input, result Usage) (JSONKeysPlan, error) {
	p := JSONKeysPlan{name: name, input: input, result: result}
	id := JSON
	if name == "jsonb_object_keys" {
		id = JSONB
	} else if name != "json_object_keys" {
		return JSONKeysPlan{}, fmt.Errorf("invalid JSON keys name")
	}
	if !input.Valid() || input.Typmod != -1 || input.Type.ID() != id || !result.Valid() || result.Typmod != -1 || result.Type.ID() != Text {
		return JSONKeysPlan{}, fmt.Errorf("invalid JSON keys usage")
	}
	return p, nil
}
func (p JSONKeysPlan) Name() string       { return p.name }
func (p JSONKeysPlan) ResultUsage() Usage { return p.result }

// JSONB borrows its immutable canonical fields. JSON owns decoded key headers,
// validated eagerly before any key can escape, without numeric conversion.
type JSONKeyCursor struct {
	result  *Descriptor
	keys    []string
	payload *jsonbPayload
	index   int
}

func (p JSONKeysPlan) Open(ctx context.Context, value Value) (JSONKeyCursor, error) {
	if p.input.Type == nil || p.result.Type == nil || value.typ != p.input.Type {
		return JSONKeyCursor{}, fmt.Errorf("invalid JSON keys value")
	}
	if err := ctx.Err(); err != nil {
		return JSONKeyCursor{}, err
	}
	cursor := JSONKeyCursor{result: p.result.Type}
	if value.null {
		return cursor, nil
	}
	if p.name == "json_object_keys" {
		var scan jsonScanResult
		if _, err := value.typ.scanJSON(ctx, value.text, &scan); err != nil {
			return JSONKeyCursor{}, err
		}
		cursor.keys = scan.keys
	} else {
		if value.jsonb == nil {
			return JSONKeyCursor{}, fmt.Errorf("invalid JSONB payload")
		}
		if value.jsonb.root.kind != jsonbObject {
			message := "cannot call jsonb_object_keys on a scalar"
			if value.jsonb.root.kind == jsonbArray {
				message = "cannot call jsonb_object_keys on an array"
			}
			return JSONKeyCursor{}, failure("22023", message)
		}
		cursor.payload = value.jsonb
	}
	if err := ctx.Err(); err != nil {
		return JSONKeyCursor{}, err
	}
	return cursor, nil
}
func (c *JSONKeyCursor) Next(ctx context.Context) (Value, bool, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, false, err
	}
	if c.result == nil {
		return Value{}, false, fmt.Errorf("closed JSON key cursor")
	}
	var key string
	if c.payload != nil {
		if c.index >= len(c.payload.root.fields) {
			return Null(c.result), false, nil
		}
		key = c.payload.root.fields[c.index].key
	} else {
		if c.index >= len(c.keys) {
			return Null(c.result), false, nil
		}
		key = c.keys[c.index]
	}
	c.index++
	return String(c.result, key), true, nil
}
func (c *JSONKeyCursor) Close() { *c = JSONKeyCursor{} }
