package sqltype

import (
	"context"
	"fmt"

	"stuntgres/internal/workadmission"
)

// jsonbExists searches only the immutable root, admitting each candidate.
func jsonbExists(ctx context.Context, root *jsonbNode, key string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	switch root.kind {
	case jsonbObject:
		lo, hi := 0, len(root.fields)
		for lo < hi {
			if err := workadmission.Admit(ctx, workadmission.WorkJSONBExistence); err != nil {
				return false, err
			}
			mid := lo + (hi-lo)/2
			order, err := compareJSONBKeys(ctx, root.fields[mid].key, key)
			if err != nil {
				return false, err
			}
			if order == 0 {
				return true, nil
			}
			if order < 0 {
				lo = mid + 1
			} else {
				hi = mid
			}
		}
	case jsonbArray:
		for i := range root.items {
			if err := workadmission.Admit(ctx, workadmission.WorkJSONBExistence); err != nil {
				return false, err
			}
			item := &root.items[i]
			if item.kind != jsonbString {
				continue
			}
			order, err := compareJSONBKeys(ctx, item.text, key)
			if err != nil || order == 0 {
				return err == nil, err
			}
		}
	case jsonbString:
		if err := workadmission.Admit(ctx, workadmission.WorkJSONBExistence); err != nil {
			return false, err
		}
		order, err := compareJSONBKeys(ctx, root.text, key)
		return order == 0 && err == nil, err
	case jsonbNull, jsonbNumber, jsonbBoolean:
	default:
		return false, fmt.Errorf("invalid JSONB root kind")
	}
	return false, ctx.Err()
}

func (p BinaryPlan) jsonbExistence(ctx context.Context, document, request Value) (Value, error) {
	if document.jsonb == nil {
		return Value{}, fmt.Errorf("invalid JSONB payload")
	}
	if document.jsonb.root.kind > jsonbObject {
		return Value{}, fmt.Errorf("invalid JSONB root kind")
	}
	if p.name == "?" {
		found, err := jsonbExists(ctx, &document.jsonb.root, request.text)
		if err != nil {
			return Value{}, err
		}
		return Boolean(p.result.Type, found), nil
	}
	if request.array == nil {
		return Value{}, fmt.Errorf("missing array payload")
	}
	all := p.name == "?&"
	for i := range request.ArrayLen() {
		if i%jsonbWorkChunk == 0 {
			if err := ctx.Err(); err != nil {
				return Value{}, err
			}
		}
		key := request.ArrayElement(i)
		if key.null {
			continue
		}
		found, err := jsonbExists(ctx, &document.jsonb.root, key.text)
		if err != nil {
			return Value{}, err
		}
		if found != all {
			return Boolean(p.result.Type, found), nil
		}
	}
	return Boolean(p.result.Type, all), nil
}
