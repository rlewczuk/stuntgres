package sqltype

import "fmt"

// REGPROC is catalog-derived only: there is no named input resolver or storage
// declaration capability. It carries a real procedure identity, not display text.
type RegprocFormatter interface{ FormatRegproc(uint32) (string, error) }

func RegprocValue(d *Descriptor, id uint32) (Value, error) {
	if d == nil || d.id != Regproc || !d.Concrete() {
		return Value{}, fmt.Errorf("invalid regproc descriptor")
	}
	return Value{typ: d, oid: id, integer: 1}, nil
}

func (v Value) ProcedureOID() (uint32, error) {
	if v.null || v.typ == nil || v.typ.BaseType().id != Regproc || v.integer != 1 {
		return 0, fmt.Errorf("not a regproc identity")
	}
	return v.oid, nil
}

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
	return physical.id == Regproc
}
