package sqltype

import "fmt"

// RecordValue preserves an immutable actual tuple shape behind a query-only
// generic RECORD result. It never creates a persistent composite identity.
func RecordValue(target *Descriptor, value Value) (Value, error) {
	if target == nil || !target.IsDeferredRecord() { return Value{}, fmt.Errorf("invalid record result target") }
	if value.IsNull() { return Null(target), nil }
	if value.typ == nil || !value.typ.BaseType().IsComposite() && !value.typ.IsDeferredRecord() || value.composite == nil { return Value{}, failure("42804", "cannot return non-composite value from function returning composite type") }
	shape := value.typ
	if shape.IsDeferredRecord() { shape = value.composite.shape }
	if shape == nil || !shape.BaseType().IsComposite() { return Value{}, fmt.Errorf("missing record result shape") }
	if value.composite.shape != shape {
		value.composite = &compositePayload{fields: value.composite.fields, shape: shape}
	}
	value.typ = target
	return value, nil
}

// ActualRecord exposes a deferred result's immutable tuple shape to an existing
// dynamic-record activation. Ordinary values and typed NULLs are unchanged.
func (v Value) ActualRecord() Value {
	if v.typ != nil && v.typ.IsDeferredRecord() && !v.null && v.composite != nil && v.composite.shape != nil {
		v.typ = v.composite.shape
	}
	return v
}
