package sqltype

import (
	"context"
	"fmt"
)

// HstoreFromRecord uses the actual tuple descriptor and each child's output
// codec. A known whole-NULL tuple still contributes all of its live names.
func (d *Descriptor) HstoreFromRecord(ctx context.Context, record Value, env OutputEnvironment) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if !d.IsHstore() {
		return Value{}, fmt.Errorf("invalid hstore record destination")
	}
	record = record.ActualRecord()
	if record.typ == nil || !record.typ.BaseType().IsComposite() {
		if record.typ != nil && record.typ.IsDeferredRecord() {
			return Value{}, failure("42809", "record type has not been registered")
		}
		return Value{}, fmt.Errorf("invalid hstore record source")
	}
	shape := record.typ.BaseType()
	if !record.null && !record.validCompositePayload() {
		return Value{}, fmt.Errorf("invalid hstore record payload")
	}
	pairs := make([]hstorePair, shape.CompositeLen())
	for i := range pairs {
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		pairs[i] = hstorePair{key: shape.CompositeMember(i).Name, null: true}
		if !record.null {
			field := record.CompositeField(i)
			if !field.null {
				text, err := field.typ.OutputWithEnvironment(ctx, field, env)
				if err != nil {
					return Value{}, err
				}
				pairs[i].value, pairs[i].null = text, false
			}
		}
	}
	return d.ownedHstore(ctx, pairs)
}

// HstorePopulateRecord preserves absent fields as immutable typed values. Only
// reached matching strings enter destination input codecs, in descriptor order.
func HstorePopulateRecord(ctx context.Context, base Value, source *Descriptor, replacements Value, env InputEnvironment) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if base.typ == nil || !base.typ.BaseType().IsComposite() && !base.typ.IsDeferredRecord() {
		return Value{}, failure("42804", "first argument must be a rowtype")
	}
	if err := replacements.checkHstore(source); err != nil {
		return Value{}, err
	}
	if replacements.null {
		return base, nil
	}
	original := base.typ
	base = base.ActualRecord()
	if !base.typ.BaseType().IsComposite() {
		return Value{}, failure("42809", "record type has not been registered")
	}
	shape := base.typ.BaseType()
	if !base.null && !base.validCompositePayload() {
		return Value{}, fmt.Errorf("invalid hstore record payload")
	}
	if !base.null && len(replacements.hstore.pairs) == 0 {
		if original.IsDeferredRecord() {
			return RecordValue(original, base)
		}
		return base, nil
	}
	var fields []Value
	if base.null {
		fields = make([]Value, shape.CompositeLen())
	}
	for i := range shape.CompositeLen() {
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		member := shape.CompositeMember(i)
		pair, found := replacements.findHstore(member.Name)
		if !found && !base.null {
			continue
		}
		value := Null(member.Usage.Type)
		var err error
		if found && !pair.null {
			value, err = member.Usage.Type.InputWithEnvironment(ctx, pair.value, member.Usage.Typmod, env)
		} else {
			err = member.Usage.Type.CheckDomainInput(ctx, value, env)
		}
		if err != nil {
			return Value{}, err
		}
		if fields == nil {
			fields = make([]Value, shape.CompositeLen())
			copy(fields, base.composite.fields)
		}
		fields[i] = value
	}
	if fields == nil {
		if err := base.typ.CheckDomainInput(ctx, base, env); err != nil {
			return Value{}, err
		}
		if original.IsDeferredRecord() {
			return RecordValue(original, base)
		}
		return base, nil
	}
	result, err := OwnedCompositeValue(base.typ, fields)
	if err != nil {
		return Value{}, err
	}
	if err := base.typ.CheckDomainInput(ctx, result, env); err != nil {
		return Value{}, err
	}
	if original.IsDeferredRecord() {
		return RecordValue(original, result)
	}
	return result, nil
}
