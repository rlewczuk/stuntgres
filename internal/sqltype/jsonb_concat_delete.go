package sqltype

import (
	"context"
	"fmt"
)

func (p BinaryPlan) jsonbConcat(ctx context.Context, left, right Value) (Value, error) {
	if left.jsonb == nil || right.jsonb == nil {
		return Value{}, fmt.Errorf("missing JSONB concatenation payload")
	}
	a, b := left.jsonb.root, right.jsonb.root
	if a.kind > jsonbObject || b.kind > jsonbObject {
		return Value{}, fmt.Errorf("invalid JSONB concatenation root")
	}
	root := jsonbNode{kind: jsonbArray}
	if a.kind == jsonbObject && b.kind == jsonbObject {
		if len(a.fields) == 0 {
			if err := ctx.Err(); err != nil {
				return Value{}, err
			}
			return right, nil
		}
		if len(b.fields) == 0 {
			if err := ctx.Err(); err != nil {
				return Value{}, err
			}
			return left, nil
		}
		root.kind = jsonbObject
		root.fields = make([]jsonbEntry, 0, len(a.fields)+len(b.fields))
		i, j := 0, 0
		for i < len(a.fields) || j < len(b.fields) {
			if err := ctx.Err(); err != nil {
				return Value{}, err
			}
			order := 1
			if j == len(b.fields) {
				order = -1
			} else if i < len(a.fields) {
				var err error
				order, err = compareJSONBKeys(ctx, a.fields[i].key, b.fields[j].key)
				if err != nil {
					return Value{}, err
				}
			}
			if order < 0 {
				root.fields = append(root.fields, a.fields[i])
				i++
			} else {
				root.fields = append(root.fields, b.fields[j])
				j++
				if order == 0 {
					i++
				}
			}
		}
	} else {
		if a.kind == jsonbArray && b.kind == jsonbArray {
			if len(a.items) == 0 {
				if err := ctx.Err(); err != nil {
					return Value{}, err
				}
				return right, nil
			}
			if len(b.items) == 0 {
				if err := ctx.Err(); err != nil {
					return Value{}, err
				}
				return left, nil
			}
		}
		n, m := 1, 1
		if a.kind == jsonbArray {
			n = len(a.items)
		}
		if b.kind == jsonbArray {
			m = len(b.items)
		}
		root.items = make([]jsonbNode, n+m)
		for side, source := range [2]jsonbNode{a, b} {
			offset := 0
			if side == 1 {
				offset = n
			}
			if source.kind != jsonbArray {
				root.items[offset] = source
				continue
			}
			for i, item := range source.items {
				if i%jsonbWorkChunk == 0 {
					if err := ctx.Err(); err != nil {
						return Value{}, err
					}
				}
				root.items[offset+i] = item
			}
		}
	}
	return p.jsonbTopLevelResult(ctx, root)
}

func (p BinaryPlan) jsonbDelete(ctx context.Context, document, key Value) (Value, error) {
	if document.jsonb == nil {
		return Value{}, fmt.Errorf("missing JSONB deletion payload")
	}
	root := document.jsonb.root
	if root.kind > jsonbObject {
		return Value{}, fmt.Errorf("invalid JSONB deletion root")
	}
	if root.kind != jsonbObject && root.kind != jsonbArray {
		return Value{}, failure("22023", "cannot delete from scalar")
	}
	if key.typ.ID() == Int4 {
		if root.kind != jsonbArray {
			return Value{}, failure("22023", "cannot delete from object using integer index")
		}
		index := key.integer
		if index < 0 {
			index += int64(len(root.items))
		}
		if index < 0 || index >= int64(len(root.items)) {
			if err := ctx.Err(); err != nil {
				return Value{}, err
			}
			return document, nil
		}
		items := make([]jsonbNode, len(root.items)-1)
		for i, item := range root.items {
			if i%jsonbWorkChunk == 0 {
				if err := ctx.Err(); err != nil {
					return Value{}, err
				}
			}
			if int64(i) < index {
				items[i] = item
			} else if int64(i) > index {
				items[i-1] = item
			}
		}
		root.items = items
	} else if root.kind == jsonbObject {
		index := -1
		for i, field := range root.fields {
			order, err := compareJSONBKeys(ctx, field.key, key.text)
			if err != nil {
				return Value{}, err
			}
			if order == 0 {
				index = i
				break
			}
			if order > 0 {
				break
			}
		}
		if index < 0 {
			if err := ctx.Err(); err != nil {
				return Value{}, err
			}
			return document, nil
		}
		fields := make([]jsonbEntry, len(root.fields)-1)
		for i, field := range root.fields {
			if i%jsonbWorkChunk == 0 {
				if err := ctx.Err(); err != nil {
					return Value{}, err
				}
			}
			if i < index {
				fields[i] = field
			} else if i > index {
				fields[i-1] = field
			}
		}
		root.fields = fields
	} else {
		// Count first so repeated strings do not leave a large discarded tail
		// retained by the result. No matches preserve the original payload.
		removed := 0
		for _, item := range root.items {
			match, err := jsonbDeleteStringMatch(ctx, item, key.text)
			if err != nil {
				return Value{}, err
			}
			if match {
				removed++
			}
		}
		if removed == 0 {
			if err := ctx.Err(); err != nil {
				return Value{}, err
			}
			return document, nil
		}
		items := make([]jsonbNode, 0, len(root.items)-removed)
		for _, item := range root.items {
			match, err := jsonbDeleteStringMatch(ctx, item, key.text)
			if err != nil {
				return Value{}, err
			}
			if match {
				continue
			}
			items = append(items, item)
		}
		root.items = items
	}
	return p.jsonbTopLevelResult(ctx, root)
}

func jsonbDeleteStringMatch(ctx context.Context, item jsonbNode, key string) (bool, error) {
	if item.kind != jsonbString {
		return false, ctx.Err()
	}
	order, err := compareJSONBKeys(ctx, item.text, key)
	return order == 0, err
}

func (p BinaryPlan) jsonbTopLevelResult(ctx context.Context, root jsonbNode) (Value, error) {
	payload := &jsonbPayload{root: root}
	text, err := jsonbOutput(ctx, &payload.root)
	if err != nil {
		return Value{}, err
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	return Value{typ: p.result.Type, text: text, jsonb: payload}, nil
}
