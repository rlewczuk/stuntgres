package sqltype

import (
	"context"
	"fmt"

	"stuntgres/internal/workadmission"
)

func (p BinaryPlan) jsonbContainment(ctx context.Context, left, right Value) (Value, error) {
	if left.jsonb == nil || right.jsonb == nil {
		return Value{}, fmt.Errorf("invalid JSONB payload")
	}
	a, b := &left.jsonb.root, &right.jsonb.root
	if p.name == "<@" {
		a, b = b, a
	}
	found, err := jsonbContains(ctx, a, b)
	if err != nil {
		return Value{}, err
	}
	return Boolean(p.result.Type, found), nil
}

type jsonbContainmentFrame struct {
	a, b       *jsonbNode
	item, next int
	state      uint8 // 0: enter, 1: object child, 2: array candidate
}

func jsonbScalarContains(ctx context.Context, a, b *jsonbNode) (bool, error) {
	if a.kind != b.kind {
		return false, nil
	}
	switch a.kind {
	case jsonbNull:
		return true, nil
	case jsonbBoolean:
		return a.boolean == b.boolean, nil
	case jsonbNumber:
		return compareNumericViews(a.number.view(), b.number.view()) == 0, nil
	case jsonbString:
		order, err := compareJSONBKeys(ctx, a.text, b.text)
		return order == 0 && err == nil, err
	}
	return false, nil
}

// jsonbContains walks borrowed immutable nodes one containment frame step at a time.
func jsonbContains(ctx context.Context, a, b *jsonbNode) (bool, error) {
	var local [32]jsonbContainmentFrame
	stack := append(local[:0], jsonbContainmentFrame{a: a, b: b})
	returning, found := false, false
	for len(stack) != 0 {
		if err := workadmission.Admit(ctx, workadmission.WorkJSONBContainment); err != nil {
			return false, err
		}
		f := &stack[len(stack)-1]
		if returning {
			returning = false
			if f.state == 1 && !found {
				stack = stack[:len(stack)-1]
				returning = true
				continue
			}
			if f.state == 2 && found {
				f.item++
				f.next = 0
			}
		}
		if f.state == 0 {
			if f.a.kind > jsonbObject || f.b.kind > jsonbObject {
				return false, fmt.Errorf("invalid JSONB node kind")
			}
			switch {
			case f.a.kind == jsonbObject && f.b.kind == jsonbObject:
				if len(f.a.fields) >= len(f.b.fields) {
					f.state = 1
					continue
				}
			case f.a.kind == jsonbArray && f.b.kind != jsonbObject:
				f.state = 2
				continue
			default:
				var err error
				found, err = jsonbScalarContains(ctx, f.a, f.b)
				if err != nil {
					return false, err
				}
				stack = stack[:len(stack)-1]
				returning = true
				continue
			}
			found = false
			stack = stack[:len(stack)-1]
			returning = true
			continue
		}
		if f.state == 1 {
			if f.item == len(f.b.fields) {
				found = true
				stack = stack[:len(stack)-1]
				returning = true
				continue
			}
			entry := &f.b.fields[f.item]
			value, err := jsonbExtractField(ctx, f.a, entry.key)
			if err != nil {
				return false, err
			}
			if value == nil || value.kind != entry.value.kind {
				found = false
				stack = stack[:len(stack)-1]
				returning = true
				continue
			}
			f.item++
			stack = append(stack, jsonbContainmentFrame{a: value, b: &entry.value})
			continue
		}
		count := 1
		if f.b.kind == jsonbArray {
			count = len(f.b.items)
		}
		if f.item == count {
			found = true
			stack = stack[:len(stack)-1]
			returning = true
			continue
		}
		if f.next == len(f.a.items) {
			found = false
			stack = stack[:len(stack)-1]
			returning = true
			continue
		}
		request := f.b
		if f.b.kind == jsonbArray {
			request = &f.b.items[f.item]
		}
		candidate := &f.a.items[f.next]
		f.next++
		if candidate.kind != request.kind {
			continue
		}
		if request.kind == jsonbArray || request.kind == jsonbObject {
			stack = append(stack, jsonbContainmentFrame{a: candidate, b: request})
			continue
		}
		matched, err := jsonbScalarContains(ctx, candidate, request)
		if err != nil {
			return false, err
		}
		if matched {
			f.item++
			f.next = 0
		}
	}
	return found, ctx.Err()
}
