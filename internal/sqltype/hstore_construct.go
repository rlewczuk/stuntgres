package sqltype

import (
	"context"
	"fmt"
)

func hstoreText(v Value) bool { return v.typ != nil && v.typ.ID() == Text && !v.typ.IsDomain() }
func hstoreTexts(v Value) bool {
	return v.typ != nil && v.typ.ID() == 1009 && v.typ.Concrete() && v.typ.IsArray() && v.typ.Element().ID() == Text && (v.null || v.array != nil)
}

// HstoreFromText shares immutable text strings, never an encoded hstore input.
func (d *Descriptor) HstoreFromText(ctx context.Context, key, value Value) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if !d.IsHstore() || !hstoreText(key) || !hstoreText(value) || key.typ != value.typ {
		return Value{}, fmt.Errorf("invalid hstore text constructor")
	}
	if key.null {
		return Null(d), nil
	}
	return d.ownedHstore(ctx, []hstorePair{{key: key.text, value: value.text, null: value.null}})
}

func (d *Descriptor) HstoreFromArrays(ctx context.Context, keys, values Value) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if !d.IsHstore() || !hstoreTexts(keys) || !hstoreTexts(values) || keys.typ != values.typ {
		return Value{}, fmt.Errorf("invalid hstore arrays constructor")
	}
	if keys.null {
		return Null(d), nil
	}
	if keys.ArrayRank() > 1 || !values.null && values.ArrayRank() > 1 {
		return Value{}, failure("2202E", "wrong number of array subscripts")
	}
	if !values.null && (keys.ArrayRank() != values.ArrayRank() || keys.ArrayRank() == 1 && keys.ArrayDimension(0) != values.ArrayDimension(0)) {
		return Value{}, failure("2202E", "arrays must have same bounds")
	}
	pairs := make([]hstorePair, keys.ArrayLen())
	for i := range pairs {
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		key := keys.ArrayElement(i)
		if key.null {
			return Value{}, failure("22004", "null value not allowed for hstore key")
		}
		pairs[i].key = key.text
		pairs[i].null = values.null
		if !values.null {
			value := values.ArrayElement(i)
			pairs[i].value, pairs[i].null = value.text, value.null
		}
	}
	return d.ownedHstore(ctx, pairs)
}

func (d *Descriptor) HstoreFromArray(ctx context.Context, input Value) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if !d.IsHstore() || !hstoreTexts(input) {
		return Value{}, fmt.Errorf("invalid hstore array constructor")
	}
	if input.null {
		return Null(d), nil
	}
	switch input.ArrayRank() {
	case 0:
	case 1:
		if input.ArrayLen()%2 != 0 {
			return Value{}, failure("2202E", "array must have even number of elements")
		}
	case 2:
		if input.ArrayDimension(1).Length != 2 {
			return Value{}, failure("2202E", "array must have two columns")
		}
	default:
		return Value{}, failure("2202E", "wrong number of array subscripts")
	}
	pairs := make([]hstorePair, input.ArrayLen()/2)
	for i := range pairs {
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		key, value := input.ArrayElement(2*i), input.ArrayElement(2*i+1)
		if key.null {
			return Value{}, failure("22004", "null value not allowed for hstore key")
		}
		pairs[i] = hstorePair{key: key.text, value: value.text, null: value.null}
	}
	return d.ownedHstore(ctx, pairs)
}

// findHstore distinguishes a missing key from a present NULL-valued pair.
func (v Value) findHstore(key string) (hstorePair, bool) {
	lo, hi := 0, len(v.hstore.pairs)
	for lo < hi {
		mid := lo + (hi-lo)/2
		if hstoreKeyCompare(v.hstore.pairs[mid].key, key) < 0 {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo < len(v.hstore.pairs) && v.hstore.pairs[lo].key == key {
		return v.hstore.pairs[lo], true
	}
	return hstorePair{}, false
}
func (v Value) checkHstore(source *Descriptor) error {
	if !source.IsHstore() || v.typ != source || !v.null && !v.validHstore() {
		return fmt.Errorf("invalid hstore lookup source")
	}
	return nil
}
func (v Value) HstoreFetch(ctx context.Context, source *Descriptor, key Value) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if err := v.checkHstore(source); err != nil {
		return Value{}, err
	}
	if !hstoreText(key) {
		return Value{}, fmt.Errorf("invalid hstore lookup key")
	}
	if v.null || key.null {
		return Null(key.typ), nil
	}
	pair, found := v.findHstore(key.text)
	if !found || pair.null {
		return Null(key.typ), nil
	}
	return String(key.typ, pair.value), nil
}
func (v Value) HstoreExists(ctx context.Context, source, result *Descriptor, key Value, defined bool) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if err := v.checkHstore(source); err != nil {
		return Value{}, err
	}
	if !hstoreText(key) || result == nil || result.ID() != Bool || !result.Concrete() {
		return Value{}, fmt.Errorf("invalid hstore predicate")
	}
	if v.null || key.null {
		return Null(result), nil
	}
	pair, found := v.findHstore(key.text)
	return Boolean(result, found && (!defined || !pair.null)), nil
}
func (v Value) HstoreFetchArray(ctx context.Context, source, result *Descriptor, keys Value) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if err := v.checkHstore(source); err != nil {
		return Value{}, err
	}
	if !hstoreTexts(keys) || keys.typ != result {
		return Value{}, fmt.Errorf("invalid hstore array lookup")
	}
	if v.null || keys.null {
		return Null(result), nil
	}
	elements := make([]Value, keys.ArrayLen())
	for i := range elements {
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		key := keys.ArrayElement(i)
		elements[i] = Null(result.Element())
		if !key.null {
			if pair, found := v.findHstore(key.text); found && !pair.null {
				elements[i] = String(result.Element(), pair.value)
			}
		}
	}
	dimensions := make([]ArrayDimension, keys.ArrayRank())
	for i := range dimensions {
		dimensions[i] = keys.ArrayDimension(i)
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	value, err := OwnedArrayValue(result, dimensions, elements)
	if err != nil {
		return Value{}, err
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	return value, nil
}
