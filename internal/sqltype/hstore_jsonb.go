package sqltype

import (
	"context"
	"fmt"
)

// HstoreJSONB borrows frozen, canonically ordered hstore pairs to construct a
// genuine JSONB object. Only loose numeric values pass through numeric input;
// neither the source nor an intermediate JSON document is reparsed.
func (v Value) HstoreJSONB(ctx context.Context, source, result *Descriptor, loose bool) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if !source.IsHstore() || v.Type() != source || result == nil || result.ID() != JSONB || !result.Concrete() || result.BaseType() != result {
		return Value{}, fmt.Errorf("invalid hstore JSONB conversion")
	}
	if v.null {
		return Null(result), nil
	}
	if !v.validHstore() {
		return Value{}, fmt.Errorf("invalid hstore payload")
	}
	fields := make([]jsonbEntry, len(v.hstore.pairs))
	for i, pair := range v.hstore.pairs {
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		node := jsonbNode{kind: jsonbNull}
		if !pair.null {
			node = jsonbNode{kind: jsonbString, text: pair.value}
			if loose {
				switch pair.value {
				case "t", "f":
					node = jsonbNode{kind: jsonbBoolean, boolean: pair.value == "t"}
				default:
					end, valid, err := scanJSONNumber(ctx, pair.value, 0)
					if err != nil {
						return Value{}, err
					}
					if valid && end == len(pair.value) {
						number, err := result.numericInput(pair.value, -1)
						if err != nil {
							return Value{}, err
						}
						node = jsonbNode{kind: jsonbNumber, number: jsonbDecimal{digits: number.text, exponent: number.exponent, scale: number.scale, negative: number.negative}}
					}
				}
			}
		}
		fields[i] = jsonbEntry{key: pair.key, value: node, ordinal: i}
	}
	payload := &jsonbPayload{root: jsonbNode{kind: jsonbObject, fields: fields}}
	text, err := jsonbOutput(ctx, &payload.root)
	if err != nil {
		return Value{}, err
	}
	return Value{typ: result, text: text, jsonb: payload}, nil
}
