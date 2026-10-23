package sqltype

import (
	"context"
	"fmt"
)

// RegtypeResolver supplies execution-owned type-catalog input authority.
type RegtypeResolver interface {
	ResolveRegtype(context.Context, string) (uint32, error)
}
type RegtypeFormatter interface{ FormatRegtype(uint32) (string, error) }

func RegtypeValue(d *Descriptor, id uint32) (Value, error) {
	if d == nil || d.id != Regtype || !d.Concrete() {
		return Value{}, fmt.Errorf("invalid regtype descriptor")
	}
	return Value{typ: d, oid: id, integer: 1}, nil
}

func (v Value) TypeOID() (uint32, error) {
	if v.null || v.typ == nil || v.typ.BaseType().id != Regtype || v.integer != 1 {
		return 0, fmt.Errorf("not a regtype identity")
	}
	return v.oid, nil
}

func (d *Descriptor) ContainsRegtype() bool {
	if d == nil {
		return false
	}
	physical := d.BaseType()
	if r := physical.RangeType(); r != nil {
		return r.rangeSubtype.ContainsRegtype()
	}
	if physical.IsArray() {
		return physical.element.ContainsRegtype()
	}
	if physical.IsComposite() {
		for _, member := range physical.composite.members {
			if member.Usage.Type.ContainsRegtype() {
				return true
			}
		}
	}
	return physical.id == Regtype
}

// RegtypeCastPair admits only released direct edges and explicit string IO.
func RegtypeCastPair(source, target ID) bool {
	integer := func(id ID) bool { return id == Int2 || id == Int4 || id == Int8 }
	stringType := func(id ID) bool { return id == Text || id == VarChar }
	return source == Regtype && (target == Regtype || target == Oid || target == Int4 || target == Int8 || stringType(target)) ||
		target == Regtype && (source == Oid || integer(source) || stringType(source))
}

// ContainsCatalogIdentifier marks recursive catalog-dependent input/output.
func (d *Descriptor) ContainsCatalogIdentifier() bool {
	return d.ContainsRegclass() || d.ContainsRegtype() || d.ContainsRegproc() || d.ContainsRegrole() || d.ContainsOperatorAlias() || d.ContainsRegObject() || d.ContainsRegTS() || d.ContainsACLItem()
}

// ParameterInput associates provider notices with the actual portal input slot.
// Runtime text IO uses the unchanged environment and has no portal context.
func (input InputEnvironment) ParameterInput(slot int, empty bool) InputEnvironment {
	if input.FTS != nil {
		input.FTS = input.FTS.FTSParameter(slot, empty)
	}
	if resolver, ok := input.Regtype.(interface {
		RegtypeParameter(int, bool) RegtypeResolver
	}); ok {
		input.Regtype = resolver.RegtypeParameter(slot, empty)
	}
	if resolver, ok := input.Operator.(interface {
		OperatorParameter(int, bool) OperatorResolver
	}); ok {
		input.Operator = resolver.OperatorParameter(slot, empty)
	}
	if resolver, ok := input.Regproc.(interface {
		RoutineParameter(int, bool) RoutineResolver
	}); ok {
		input.Regproc = resolver.RoutineParameter(slot, empty)
	}
	if resolver, ok := input.ACLItem.(interface {
		ACLItemParameter(int, bool) ACLItemResolver
	}); ok {
		input.ACLItem = resolver.ACLItemParameter(slot, empty)
	}
	return input
}

// LiteralInput associates codec notices with the enclosing SQL literal, including
// array children and warnings emitted before a later input error.
func (input InputEnvironment) LiteralInput(position int) InputEnvironment {
	if input.FTS != nil {
		input.FTS = input.FTS.FTSLiteral(position)
	}
	if resolver, ok := input.Regproc.(interface {
		RoutineLiteral(int) RoutineResolver
	}); ok {
		input.Regproc = resolver.RoutineLiteral(position)
	}
	if resolver, ok := input.ACLItem.(interface {
		ACLItemLiteral(int) ACLItemResolver
	}); ok {
		input.ACLItem = resolver.ACLItemLiteral(position)
	}
	return input
}
