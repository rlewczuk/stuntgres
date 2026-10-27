package sqltype

import (
	"context"
	"fmt"
	"slices"
)

// publishHstoreUpdate takes already sorted, unique, exclusively owned pair headers.
// Strings remain shared with immutable operands.
func publishHstoreUpdate(ctx context.Context, source *Descriptor, pairs []hstorePair) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	return Value{typ: source, hstore: &hstorePayload{pairs: pairs}}, nil
}

// HstoreConcat merges immutable pairs, taking the entire right pair on conflict.
func (v Value) HstoreConcat(ctx context.Context, source *Descriptor, right Value) (Value, error) {
	if err := ctx.Err(); err != nil { return Value{}, err }
	if err := v.checkHstore(source); err != nil { return Value{}, err }
	if err := right.checkHstore(source); err != nil { return Value{}, err }
	if v.null || right.null { return Null(source), nil }
	a, b := v.hstore.pairs, right.hstore.pairs
	if len(a) == 0 { return right, nil }
	if len(b) == 0 { return v, nil }
	// Count the union so the result owns exactly the required header capacity.
	n, i, j := 0, 0, 0
	for i < len(a) && j < len(b) {
		if err := ctx.Err(); err != nil { return Value{}, err }
		cmp := hstoreKeyCompare(a[i].key, b[j].key)
		if cmp <= 0 { i++ }
		if cmp >= 0 { j++ }
		n++
	}
	n += len(a)-i + len(b)-j
	out := make([]hstorePair, 0, n)
	i, j = 0, 0
	for i < len(a) && j < len(b) {
		if err := ctx.Err(); err != nil { return Value{}, err }
		cmp := hstoreKeyCompare(a[i].key, b[j].key)
		if cmp < 0 { out = append(out, a[i]); i++ } else {
			out = append(out, b[j]); j++
			if cmp == 0 { i++ }
		}
	}
	out = append(out, a[i:]...)
	out = append(out, b[j:]...)
	return publishHstoreUpdate(ctx, source, out)
}

// HstoreDelete removes a key, independently of the pair's NULL flag.
func (v Value) HstoreDelete(ctx context.Context, source *Descriptor, key Value) (Value, error) {
	if err := ctx.Err(); err != nil { return Value{}, err }
	if err := v.checkHstore(source); err != nil { return Value{}, err }
	if !hstoreText(key) { return Value{}, fmt.Errorf("invalid hstore deletion key") }
	if v.null || key.null { return Null(source), nil }
	if _, found := v.findHstore(key.text); !found { return v, nil }
	out := make([]hstorePair, 0, len(v.hstore.pairs)-1)
	for _, pair := range v.hstore.pairs {
		if err := ctx.Err(); err != nil { return Value{}, err }
		if pair.key != key.text { out = append(out, pair) }
	}
	return publishHstoreUpdate(ctx, source, out)
}

// HstoreDeleteHstore removes only exact key/value/NULL matches.
func (v Value) HstoreDeleteHstore(ctx context.Context, source *Descriptor, right Value) (Value, error) {
	if err := ctx.Err(); err != nil { return Value{}, err }
	if err := v.checkHstore(source); err != nil { return Value{}, err }
	if err := right.checkHstore(source); err != nil { return Value{}, err }
	if v.null || right.null { return Null(source), nil }
	if len(v.hstore.pairs) == 0 || len(right.hstore.pairs) == 0 { return v, nil }
	var out []hstorePair
	j := 0
	for i, pair := range v.hstore.pairs {
		if err := ctx.Err(); err != nil { return Value{}, err }
		for j < len(right.hstore.pairs) && hstoreKeyCompare(right.hstore.pairs[j].key, pair.key) < 0 {
			if err := ctx.Err(); err != nil { return Value{}, err }
			j++
		}
		remove := j < len(right.hstore.pairs) && right.hstore.pairs[j].key == pair.key && right.hstore.pairs[j].null == pair.null && (pair.null || right.hstore.pairs[j].value == pair.value)
		if remove && out == nil {
			out = make([]hstorePair, i, len(v.hstore.pairs)-1)
			copy(out, v.hstore.pairs[:i])
		} else if !remove && out != nil { out = append(out, pair) }
	}
	if err := ctx.Err(); err != nil { return Value{}, err }
	if out == nil { return v, nil }
	return publishHstoreUpdate(ctx, source, out)
}

func hstoreUpdateKeys(ctx context.Context, keys Value) ([]string, error) {
	requests := make([]string, 0, keys.ArrayLen())
	for i := range keys.ArrayLen() {
		if err := ctx.Err(); err != nil { return nil, err }
		key := keys.ArrayElement(i)
		if !key.null { requests = append(requests, key.text) }
	}
	var sortErr error
	slices.SortFunc(requests, func(a, b string) int {
		if sortErr == nil { sortErr = ctx.Err() }
		if sortErr != nil { return 0 }
		return hstoreKeyCompare(a, b)
	})
	if sortErr != nil { return nil, sortErr }
	return slices.Compact(requests), ctx.Err()
}

// HstoreDeleteArray ignores NULL requests, duplicates, rank and lower bounds.
func (v Value) HstoreDeleteArray(ctx context.Context, source *Descriptor, keys Value) (Value, error) {
	return v.hstoreSelectKeys(ctx, source, keys, false)
}

// HstoreSlice retains original pairs, including their NULL flags.
func (v Value) HstoreSlice(ctx context.Context, source *Descriptor, keys Value) (Value, error) {
	return v.hstoreSelectKeys(ctx, source, keys, true)
}

func (v Value) hstoreSelectKeys(ctx context.Context, source *Descriptor, keys Value, retain bool) (Value, error) {
	if err := ctx.Err(); err != nil { return Value{}, err }
	if err := v.checkHstore(source); err != nil { return Value{}, err }
	if !hstoreTexts(keys) { return Value{}, fmt.Errorf("invalid hstore update keys") }
	if v.null || keys.null { return Null(source), nil }
	if len(v.hstore.pairs) == 0 { return v, nil }
	requests, err := hstoreUpdateKeys(ctx, keys)
	if err != nil { return Value{}, err }
	var out []hstorePair
	if retain { out = make([]hstorePair, 0, min(len(requests), len(v.hstore.pairs))) }
	j := 0
	for i, pair := range v.hstore.pairs {
		if err := ctx.Err(); err != nil { return Value{}, err }
		for j < len(requests) && hstoreKeyCompare(requests[j], pair.key) < 0 {
			if err := ctx.Err(); err != nil { return Value{}, err }
			j++
		}
		found := j < len(requests) && requests[j] == pair.key
		if retain {
			if found { out = append(out, pair) }
		} else if found && out == nil {
			out = make([]hstorePair, i, len(v.hstore.pairs)-1)
			copy(out, v.hstore.pairs[:i])
		} else if !found && out != nil { out = append(out, pair) }
	}
	if err := ctx.Err(); err != nil { return Value{}, err }
	if !retain && out == nil { return v, nil }
	return publishHstoreUpdate(ctx, source, out)
}
