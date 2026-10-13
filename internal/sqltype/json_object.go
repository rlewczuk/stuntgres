package sqltype

import (
	"context"
	"fmt"
	"strings"
)

// jsonObject evaluates the two legacy text-array signatures after BinaryPlan
// validates the descriptor owners and evaluates strictness. Inspect immutable
// array payloads in place and retain order/duplicates rather than building JSONB.
func (p BinaryPlan) jsonObject(ctx context.Context, inputs []Value) (Value, error) {
	for _, v := range inputs {
		if v.array == nil {
			return Value{}, fmt.Errorf("missing JSON object array payload")
		}
	}
	a := inputs[0]
	pairs := a.ArrayLen() / 2
	if p.arity == 1 {
		switch a.ArrayRank() {
		case 0:
		case 1:
			if a.ArrayLen()%2 != 0 {
				return Value{}, failure("2202E", "array must have even number of elements")
			}
		case 2:
			if a.ArrayDimension(1).Length != 2 {
				return Value{}, failure("2202E", "array must have two columns")
			}
		default:
			return Value{}, failure("2202E", "wrong number of array subscripts")
		}
	} else {
		b := inputs[1]
		if a.ArrayRank() != b.ArrayRank() || a.ArrayRank() > 1 {
			return Value{}, failure("2202E", "wrong number of array subscripts")
		}
		if a.ArrayLen() != b.ArrayLen() {
			return Value{}, failure("2202E", "mismatched array dimensions")
		}
		pairs = a.ArrayLen()
	}
	var out strings.Builder
	out.WriteByte('{')
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
	out.WriteByte('}')
	return Value{typ: p.result.Type, text: out.String()}, nil
}
