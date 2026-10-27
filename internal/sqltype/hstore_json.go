package sqltype

import (
	"context"
	"fmt"
	"strings"
)

// HstoreJSON emits textual JSON directly from owned immutable hstore pairs.
// Source and result generations are supplied by the selected installed call.
func (v Value) HstoreJSON(ctx context.Context, source, result *Descriptor, loose bool) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if !source.IsHstore() || v.Type() != source || result == nil || result.ID() != JSON || !result.Concrete() || result.BaseType() != result {
		return Value{}, fmt.Errorf("invalid hstore JSON conversion")
	}
	if v.null {
		return Null(result), nil
	}
	if !v.validHstore() {
		return Value{}, fmt.Errorf("invalid hstore payload")
	}
	var out strings.Builder
	out.WriteByte('{')
	for i, pair := range v.hstore.pairs {
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		if i != 0 {
			out.WriteString(", ")
		}
		if err := writeJSONBString(ctx, &out, pair.key); err != nil {
			return Value{}, err
		}
		out.WriteString(": ")
		if pair.null {
			out.WriteString("null")
			continue
		}
		if loose {
			switch pair.value {
			case "t":
				out.WriteString("true")
				continue
			case "f":
				out.WriteString("false")
				continue
			}
			end, valid, err := scanJSONNumber(ctx, pair.value, 0)
			if err != nil {
				return Value{}, err
			}
			if valid && end == len(pair.value) {
				out.WriteString(pair.value)
				continue
			}
		}
		if err := writeJSONBString(ctx, &out, pair.value); err != nil {
			return Value{}, err
		}
	}
	out.WriteByte('}')
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	return Value{typ: result, text: out.String()}, nil
}
