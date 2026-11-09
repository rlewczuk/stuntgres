package sqltype

import (
	"context"
	"fmt"

	"stuntgres/internal/workadmission"
)

func jsonbStripNullsValue(ctx context.Context, target Value, arrays bool) (Value, error) {
	if target.jsonb == nil {
		return Value{}, fmt.Errorf("missing JSONB stripping payload")
	}
	root, changed, err := jsonbStripNulls(ctx, &target.jsonb.root, arrays)
	if err != nil {
		return Value{}, err
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if !changed {
		return target, nil
	}
	payload := &jsonbPayload{root: root}
	text, err := jsonbOutput(ctx, &payload.root)
	if err != nil {
		return Value{}, err
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	return Value{typ: target.typ, text: text, jsonb: payload}, nil
}

type jsonbStripFrame struct {
	node    *jsonbNode
	index   int
	changed bool
	items   []jsonbNode
	fields  []jsonbEntry
}

// replacePrefix allocates only on the first omission or changed child. The
// original immutable container remains authoritative for keys and child order.
func (f *jsonbStripFrame) replacePrefix(ctx context.Context, count int) error {
	if f.changed {
		return nil
	}
	f.changed = true
	if f.node.kind == jsonbObject {
		f.fields = make([]jsonbEntry, count, len(f.node.fields))
		return jsonbSetCopy(ctx, f.fields, f.node.fields[:count], workadmission.WorkJSONStripNulls)
	}
	f.items = make([]jsonbNode, count, len(f.node.items))
	return jsonbSetCopy(ctx, f.items, f.node.items[:count], workadmission.WorkJSONStripNulls)
}

func (f *jsonbStripFrame) retain(child jsonbNode) {
	if f.node.kind == jsonbObject {
		entry := f.node.fields[f.index-1]
		entry.value = child
		f.fields = append(f.fields, entry)
	} else {
		f.items = append(f.items, child)
	}
}

// jsonbStripNulls walks the decoded payload in postorder. Unchanged containers
// share their published slices; changed containers own only a lazy survivor
// slice. No recursive Go calls or cached-text parsing are involved.
func jsonbStripNulls(ctx context.Context, root *jsonbNode, arrays bool) (jsonbNode, bool, error) {
	if root == nil {
		return jsonbNode{}, false, fmt.Errorf("invalid JSONB stripping root")
	}
	var local [32]jsonbStripFrame
	stack := local[:1]
	stack[0].node = root
	for {
		if err := workadmission.Admit(ctx, workadmission.WorkJSONStripNulls); err != nil {
			return jsonbNode{}, false, err
		}
		f := &stack[len(stack)-1]
		if f.node.kind > jsonbObject {
			return jsonbNode{}, false, fmt.Errorf("invalid JSONB stripping node")
		}
		count := 0
		switch f.node.kind {
		case jsonbArray:
			count = len(f.node.items)
		case jsonbObject:
			count = len(f.node.fields)
		}
		if f.index < count {
			i := f.index
			f.index++
			var child *jsonbNode
			if f.node.kind == jsonbObject {
				child = &f.node.fields[i].value
			} else {
				child = &f.node.items[i]
			}
			if child.kind == jsonbNull && (f.node.kind == jsonbObject || arrays) {
				if err := f.replacePrefix(ctx, i); err != nil {
					return jsonbNode{}, false, err
				}
				continue
			}
			if child.kind >= jsonbArray {
				// Never retain f across append: a deep input can grow the stack.
				stack = append(stack, jsonbStripFrame{node: child})
				continue
			}
			if f.changed {
				f.retain(*child)
			}
			continue
		}
		out, changed := *f.node, f.changed
		if changed {
			if out.kind == jsonbObject {
				out.fields = f.fields
			} else {
				out.items = f.items
			}
		}
		stack = stack[:len(stack)-1]
		if len(stack) == 0 {
			return out, changed, ctx.Err()
		}
		parent := &stack[len(stack)-1]
		if changed {
			if err := parent.replacePrefix(ctx, parent.index-1); err != nil {
				return jsonbNode{}, false, err
			}
		}
		if parent.changed {
			parent.retain(out)
		}
	}
}
