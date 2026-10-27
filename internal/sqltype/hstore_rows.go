package sqltype

import (
	"context"
	"fmt"
)

// HstoreRowsKind is a closed projection over immutable hstore pairs.
type HstoreRowsKind uint8

const (
	HstoreRowKeys HstoreRowsKind = iota
	HstoreRowValues
	HstoreRowPairs
)

// HstoreRowsPlan contains only checked descriptors; cursors own invocation state.
type HstoreRowsPlan struct {
	kind HstoreRowsKind
	input, result Usage
}

func BindHstoreRows(kind HstoreRowsKind, input, result Usage) (HstoreRowsPlan, error) {
	invalid := func() (HstoreRowsPlan, error) { return HstoreRowsPlan{}, fmt.Errorf("invalid hstore rows usage") }
	if kind > HstoreRowPairs || !input.Valid() || !input.Type.IsHstore() || input.Typmod != -1 || !result.Valid() || result.Typmod != -1 {
		return invalid()
	}
	if kind != HstoreRowPairs {
		if result.Type.ID() != Text || !result.Type.Concrete() { return invalid() }
	} else {
		if result.Type.ID() != 2249 || !result.Type.IsComposite() || result.Type.CompositeLen() != 2 { return invalid() }
		for i, name := range []string{"key", "value"} {
			m := result.Type.CompositeMember(i)
			if m.Attribute != int32(i+1) || m.Name != name || !m.Usage.Valid() || m.Usage.Type.ID() != Text || m.Usage.Typmod != -1 { return invalid() }
		}
		if result.Type.CompositeMember(0).Usage != result.Type.CompositeMember(1).Usage { return invalid() }
	}
	return HstoreRowsPlan{kind: kind, input: input, result: result}, nil
}

func (p HstoreRowsPlan) Kind() HstoreRowsKind { return p.kind }
func (p HstoreRowsPlan) ResultUsage() Usage { return p.result }

type HstoreRowsCursor struct {
	plan HstoreRowsPlan
	value Value
	index int
}

func (p HstoreRowsPlan) Open(ctx context.Context, value Value) (HstoreRowsCursor, error) {
	if err := ctx.Err(); err != nil { return HstoreRowsCursor{}, err }
	if _, err := BindHstoreRows(p.kind, p.input, p.result); err != nil { return HstoreRowsCursor{}, err }
	if err := value.checkHstore(p.input.Type); err != nil { return HstoreRowsCursor{}, err }
	return HstoreRowsCursor{plan: p, value: value}, nil
}

func (c *HstoreRowsCursor) Next(ctx context.Context) (Value, bool, error) {
	if err := ctx.Err(); err != nil { c.Close(); return Value{}, false, err }
	result := c.plan.result.Type
	if result == nil { return Value{}, false, nil }
	if c.value.null || c.value.hstore == nil || c.index == len(c.value.hstore.pairs) { return Null(result), false, nil }
	pair := &c.value.hstore.pairs[c.index]
	c.index++
	if c.plan.kind == HstoreRowKeys { return String(result, pair.key), true, nil }
	text := result
	if c.plan.kind == HstoreRowPairs { text = result.CompositeMember(0).Usage.Type }
	value := Null(text)
	if !pair.null { value = String(text, pair.value) }
	if c.plan.kind == HstoreRowValues { return value, true, nil }
	row, err := OwnedCompositeValue(result, []Value{String(text, pair.key), value})
	return row, err == nil, err
}

func (c *HstoreRowsCursor) Close() { c.value = Value{}; c.index = 0 }
