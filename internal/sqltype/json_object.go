package sqltype

import (
	"context"
	"fmt"
	"strings"
)

// textObjectPairCount validates the common text-array shapes before key checks.
func textObjectPairCount(inputs []Value) (int, error) {
	for _, v := range inputs {
		if v.array == nil {
			return 0, fmt.Errorf("missing JSON object array payload")
		}
	}
	a := inputs[0]
	pairs := a.ArrayLen() / 2
	if len(inputs) == 1 {
		switch a.ArrayRank() {
		case 0:
		case 1:
			if a.ArrayLen()%2 != 0 {
				return 0, failure("2202E", "array must have even number of elements")
			}
		case 2:
			if a.ArrayDimension(1).Length != 2 {
				return 0, failure("2202E", "array must have two columns")
			}
		default:
			return 0, failure("2202E", "wrong number of array subscripts")
		}
	} else {
		b := inputs[1]
		if a.ArrayRank() != b.ArrayRank() || a.ArrayRank() > 1 {
			return 0, failure("2202E", "wrong number of array subscripts")
		}
		if a.ArrayLen() != b.ArrayLen() {
			return 0, failure("2202E", "mismatched array dimensions")
		}
		pairs = a.ArrayLen()
	}
	return pairs, nil
}

// textObject borrows immutable array elements. JSON retains order and duplicates;
// JSONB constructs string/null nodes and normalizes them without reparsing text.
func (p BinaryPlan) textObject(ctx context.Context, inputs []Value) (Value, error) {
	pairs, err := textObjectPairCount(inputs)
	if err != nil {
		return Value{}, err
	}
	binary := p.name == "jsonb_object"
	var fields []jsonbEntry
	if binary {
		fields = make([]jsonbEntry, pairs)
	}
	a := inputs[0]
	var out strings.Builder
	if !binary {
		out.WriteByte('{')
	}
	for i := range pairs {
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		var key, value Value
		if p.arity == 1 {
			key, value = a.ArrayElement(2*i), a.ArrayElement(2*i+1)
		} else {
			key, value = a.ArrayElement(i), inputs[1].ArrayElement(i)
		}
		if key.null {
			return Value{}, failure("22004", "null value not allowed for object key")
		}
		if binary {
			node := jsonbNode{kind: jsonbNull}
			if !value.null {
				node = jsonbNode{kind: jsonbString, text: value.text}
			}
			fields[i] = jsonbEntry{key: key.text, value: node, ordinal: i}
			continue
		}
		if i != 0 {
			out.WriteString(", ")
		}
		if err := writeJSONBString(ctx, &out, key.text); err != nil {
			return Value{}, err
		}
		out.WriteString(" : ")
		if value.null {
			out.WriteString("null")
		} else if err := writeJSONBString(ctx, &out, value.text); err != nil {
			return Value{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if binary {
		if err := sortJSONBFields(ctx, fields); err != nil {
			return Value{}, err
		}
		fields, err = compactJSONBFields(ctx, fields, false)
		if err != nil {
			return Value{}, err
		}
		payload := &jsonbPayload{root: jsonbNode{kind: jsonbObject, fields: fields}}
		text, err := jsonbOutput(ctx, &payload.root)
		if err != nil {
			return Value{}, err
		}
		return Value{typ: p.result.Type, text: text, jsonb: payload}, nil
	}
	out.WriteByte('}')
	return Value{typ: p.result.Type, text: out.String()}, nil
}
