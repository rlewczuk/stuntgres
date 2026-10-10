package sqltype

import "fmt"

// XIDValue has distinct descriptor authority despite sharing unsigned storage.
func XIDValue(d *Descriptor, n uint32) (Value, error) {
	if d == nil || d.id != Xid {
		return Value{}, fmt.Errorf("invalid xid descriptor")
	}
	return Value{typ: d, oid: n}, nil
}

func (v Value) XID() (uint32, error) {
	if v.null || v.typ == nil || v.typ.id != Xid {
		return 0, fmt.Errorf("not an xid value")
	}
	return v.oid, nil
}
