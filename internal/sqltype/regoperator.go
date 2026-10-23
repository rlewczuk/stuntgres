package sqltype

import (
	"context"
	"fmt"
)

// OperatorResolver supplies statement-owned operator and operand-type authority.
type OperatorResolver interface {
	ResolveRegoper(context.Context, string) (uint32, error)
	ResolveRegoperator(context.Context, string) (uint32, error)
}
type OperatorFormatter interface {
	FormatRegoper(uint32) (string, error)
	FormatRegoperator(uint32) (string, error)
}

func IsOperatorAlias(id ID) bool { return id == Regoper || id == Regoperator }

// OperatorCastPair admits released direct scalar edges and explicit string IO.
func OperatorCastPair(source, target ID) bool {
	return IsOperatorAlias(source) && (IsOperatorAlias(target) || target == Oid || target == Int4 || target == Int8 || IsCharacter(target)) ||
		IsOperatorAlias(target) && (source == Oid || source == Int2 || source == Int4 || source == Int8 || source == Text || source == VarChar)
}
func OperatorValue(d *Descriptor, id uint32) (Value, error) {
	if d == nil || !IsOperatorAlias(d.id) || !d.Concrete() {
		return Value{}, fmt.Errorf("invalid operator alias descriptor")
	}
	return Value{typ: d, oid: id, integer: 1}, nil
}
func (v Value) OperatorOID() (uint32, error) {
	if v.null || v.typ == nil || !IsOperatorAlias(v.typ.BaseType().id) || v.integer != 1 {
		return 0, fmt.Errorf("not an operator identity")
	}
	return v.oid, nil
}
func (d *Descriptor) ContainsOperatorAlias() bool {
	if d == nil {
		return false
	}
	p := d.BaseType()
	if r := p.RangeType(); r != nil {
		return r.rangeSubtype.ContainsOperatorAlias()
	}
	if p.IsArray() {
		return p.element.ContainsOperatorAlias()
	}
	if p.IsComposite() {
		for _, m := range p.composite.members {
			if m.Usage.Type.ContainsOperatorAlias() {
				return true
			}
		}
	}
	return IsOperatorAlias(p.id)
}
