package sqltype

import (
	"context"
	"fmt"
	"strconv"
)

// Regobject providers capture the two distinct catalog lookup authorities.
// Neither namespace lookup nor collation identity lookup executes comparisons.
type RegcollationResolver interface {
	ResolveRegcollation(context.Context, string) (uint32, error)
}
type RegnamespaceResolver interface {
	ResolveRegnamespace(context.Context, string) (uint32, error)
}
type RegcollationFormatter interface{ FormatRegcollation(uint32) (string, error) }
type RegnamespaceFormatter interface{ FormatRegnamespace(uint32) (string, error) }

func IsRegObjectAlias(id ID) bool { return id == Regcollation || id == Regnamespace }

func RegObjectValue(d *Descriptor, id uint32) (Value, error) {
	if d == nil || !IsRegObjectAlias(d.id) || !d.Concrete() {
		return Value{}, fmt.Errorf("invalid collation/namespace alias descriptor")
	}
	return Value{typ: d, oid: id, integer: 1}, nil
}

func (v Value) ObjectOID() (uint32, error) {
	if v.null || v.typ == nil || !IsRegObjectAlias(v.typ.BaseType().id) || v.integer != 1 {
		return 0, fmt.Errorf("not a collation/namespace identity")
	}
	return v.oid, nil
}

func (d *Descriptor) ContainsRegObject() bool    { return d.containsRegObject(0) }
func (d *Descriptor) ContainsRegcollation() bool { return d.containsRegObject(Regcollation) }

func (d *Descriptor) containsRegObject(kind ID) bool {
	if d == nil {
		return false
	}
	physical := d.BaseType()
	if r := physical.RangeType(); r != nil {
		return r.rangeSubtype.containsRegObject(kind)
	}
	if physical.IsArray() {
		return physical.element.containsRegObject(kind)
	}
	if physical.IsComposite() {
		for _, member := range physical.composite.members {
			if member.Usage.Type.containsRegObject(kind) {
				return true
			}
		}
	}
	return IsRegObjectAlias(physical.id) && (kind == 0 || physical.id == kind)
}

func (d *Descriptor) regObjectInput(ctx context.Context, text string, env InputEnvironment) (Value, error) {
	if text == "-" {
		return RegObjectValue(d, 0)
	}
	digits := len(text) != 0
	for i := range len(text) {
		if text[i] < '0' || text[i] > '9' {
			digits = false
			break
		}
	}
	if digits {
		oid := Descriptor{id: Oid, name: "oid"}
		value, err := oid.oidInput(text)
		if err != nil {
			return Value{}, err
		}
		return RegObjectValue(d, value.oid)
	}
	var id uint32
	var err error
	switch d.id {
	case Regcollation:
		if env.Regcollation == nil {
			return Value{}, fmt.Errorf("regcollation input lacks catalog authority")
		}
		id, err = env.Regcollation.ResolveRegcollation(ctx, text)
	case Regnamespace:
		if env.Regnamespace == nil {
			return Value{}, fmt.Errorf("regnamespace input lacks catalog authority")
		}
		id, err = env.Regnamespace.ResolveRegnamespace(ctx, text)
	}
	if err != nil {
		return Value{}, err
	}
	return RegObjectValue(d, id)
}

func (d *Descriptor) regObjectOutput(v Value, env OutputEnvironment) (string, error) {
	id, err := v.ObjectOID()
	if err != nil {
		return "", err
	}
	if d.id == Regcollation && env.Regcollation != nil {
		return env.Regcollation.FormatRegcollation(id)
	}
	if d.id == Regnamespace && env.Regnamespace != nil {
		return env.Regnamespace.FormatRegnamespace(id)
	}
	if id == 0 {
		return "-", nil
	}
	return strconv.FormatUint(uint64(id), 10), nil
}
