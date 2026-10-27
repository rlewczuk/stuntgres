package sqltype

import (
	"context"
	"fmt"
)

// HstoreExistsKeys ignores NULL keys and array shape, as the hstore predicates do.
func (v Value) HstoreExistsKeys(ctx context.Context, source, result *Descriptor, keys Value, all bool) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if err := v.checkHstore(source); err != nil {
		return Value{}, err
	}
	if !hstoreTexts(keys) || result == nil || result.ID() != Bool || !result.Concrete() {
		return Value{}, fmt.Errorf("invalid hstore key predicate")
	}
	if v.null || keys.null {
		return Null(result), nil
	}
	for i := range keys.ArrayLen() {
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		key := keys.ArrayElement(i)
		if key.null {
			continue
		}
		_, found := v.findHstore(key.text)
		if found != all {
			return Boolean(result, !all), nil
		}
	}
	return Boolean(result, all), nil
}

// HstoreContains compares both the exact value bytes and the value NULL flag.
func (v Value) HstoreContains(ctx context.Context, source, result *Descriptor, subset Value) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if err := v.checkHstore(source); err != nil {
		return Value{}, err
	}
	if err := subset.checkHstore(source); err != nil {
		return Value{}, err
	}
	if result == nil || result.ID() != Bool || !result.Concrete() {
		return Value{}, fmt.Errorf("invalid hstore containment result")
	}
	if v.null || subset.null {
		return Null(result), nil
	}
	for _, pair := range subset.hstore.pairs {
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		actual, found := v.findHstore(pair.key)
		if !found || actual.null != pair.null || !pair.null && actual.value != pair.value {
			return Boolean(result, false), nil
		}
	}
	return Boolean(result, true), nil
}

// HstoreKeyValues projects corresponding immutable pair positions into TEXT[].
func (v Value) HstoreKeyValues(ctx context.Context, source, result *Descriptor, values bool) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if err := v.checkHstore(source); err != nil {
		return Value{}, err
	}
	if result == nil || !result.IsArray() || result.ID() != 1009 || !result.Concrete() || result.Element().ID() != Text {
		return Value{}, fmt.Errorf("invalid hstore projection result")
	}
	if v.null {
		return Null(result), nil
	}
	elements := make([]Value, len(v.hstore.pairs))
	for i, pair := range v.hstore.pairs {
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		if !values {
			elements[i] = String(result.Element(), pair.key)
		} else if pair.null {
			elements[i] = Null(result.Element())
		} else {
			elements[i] = String(result.Element(), pair.value)
		}
	}
	var dimensions []ArrayDimension
	if len(elements) != 0 {
		dimensions = []ArrayDimension{{Length: int32(len(elements)), Lower: 1}}
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
