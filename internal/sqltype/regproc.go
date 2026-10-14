package sqltype

import (
	"context"
	"fmt"
)

// Routine aliases carry real unsigned procedure identities, not display text.
// Both input authority and presentation belong to the consuming statement.
type RoutineResolver interface {
	ResolveRegproc(context.Context, string) (uint32, error)
	ResolveRegprocedure(context.Context, string) (uint32, error)
}
type RegprocFormatter interface {
	FormatRegproc(uint32) (string, error)
	FormatRegprocedure(uint32) (string, error)
}

func IsRoutineAlias(id ID) bool { return id == Regproc || id == Regprocedure }

func RegprocValue(d *Descriptor, id uint32) (Value, error) {
	if d == nil || !IsRoutineAlias(d.id) || !d.Concrete() {
		return Value{}, fmt.Errorf("invalid routine alias descriptor")
	}
	return Value{typ: d, oid: id, integer: 1}, nil
}

func (v Value) ProcedureOID() (uint32, error) {
	if v.null || v.typ == nil || !IsRoutineAlias(v.typ.BaseType().id) || v.integer != 1 {
		return 0, fmt.Errorf("not a routine identity")
	}
	return v.oid, nil
}

// ContainsRegproc includes both routine aliases through physical wrappers.
func (d *Descriptor) ContainsRegproc() bool {
	if d == nil {
		return false
	}
	physical := d.BaseType()
	if physical.IsArray() {
		return physical.element.ContainsRegproc()
	}
	if physical.IsComposite() {
		for _, member := range physical.composite.members {
			if member.Usage.Type.ContainsRegproc() {
				return true
			}
		}
	}
	return IsRoutineAlias(physical.id)
}
