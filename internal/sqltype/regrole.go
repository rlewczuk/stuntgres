package sqltype

import (
	"context"
	"fmt"
)

// Regrole providers own immutable role-catalog input and output authority.
type RegroleResolver interface {
	ResolveRegrole(context.Context, string) (uint32, error)
}
type RegroleFormatter interface{ FormatRegrole(uint32) (string, error) }

func RegroleValue(d *Descriptor, id uint32) (Value, error) {
	if d == nil || d.id != Regrole || !d.Concrete() {
		return Value{}, fmt.Errorf("invalid regrole descriptor")
	}
	return Value{typ: d, oid: id, integer: 1}, nil
}

func (v Value) RoleOID() (uint32, error) {
	if v.null || v.typ == nil || v.typ.BaseType().id != Regrole || v.integer != 1 {
		return 0, fmt.Errorf("not a regrole identity")
	}
	return v.oid, nil
}

func (d *Descriptor) ContainsRegrole() bool {
	if d == nil {
		return false
	}
	physical := d.BaseType()
	if r := physical.RangeType(); r != nil {
		return r.rangeSubtype.ContainsRegrole()
	}
	if physical.IsArray() {
		return physical.element.ContainsRegrole()
	}
	if physical.IsComposite() {
		for _, member := range physical.composite.members {
			if member.Usage.Type.ContainsRegrole() {
				return true
			}
		}
	}
	return physical.id == Regrole
}

func RegroleCastPair(source, target ID) bool {
	integer := func(id ID) bool { return id == Int2 || id == Int4 || id == Int8 }
	stringType := func(id ID) bool { return id == Text || id == VarChar }
	return source == Regrole && (target == Regrole || target == Oid || target == Int4 || target == Int8 || stringType(target)) || target == Regrole && (source == Oid || integer(source) || stringType(source))
}

func (d *Descriptor) regroleInput(ctx context.Context, text string, env InputEnvironment) (Value, error) {
	if text == "-" {
		return RegroleValue(d, 0)
	}
	digits := len(text) != 0
	for i := range len(text) {
		if text[i] < '0' || text[i] > '9' {
			digits = false
			break
		}
	}
	if digits {
		// Reuse oidin, including its octal scanner and diagnostic type name.
		oid := Descriptor{id: Oid, name: "oid"}
		v, err := oid.oidInput(text)
		if err != nil {
			return Value{}, err
		}
		return RegroleValue(d, v.oid)
	}
	if env.Regrole == nil {
		return Value{}, fmt.Errorf("regrole input lacks catalog authority")
	}
	id, err := env.Regrole.ResolveRegrole(ctx, text)
	if err != nil {
		return Value{}, err
	}
	return RegroleValue(d, id)
}
