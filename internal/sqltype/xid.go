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

// IsTransactionIdentifier classifies scalar identity, not array executability.
func IsTransactionIdentifier(id ID) bool { return id == Xid || id == Cid || id == Xid8 }

func CIDValue(d *Descriptor, n uint32) (Value, error) {
	if d == nil || d.id != Cid {
		return Value{}, fmt.Errorf("invalid cid descriptor")
	}
	return Value{typ: d, oid: n}, nil
}

func (v Value) CID() (uint32, error) {
	if v.null || v.typ == nil || v.typ.id != Cid {
		return 0, fmt.Errorf("not a cid value")
	}
	return v.oid, nil
}

func XID8Value(d *Descriptor, n uint64) (Value, error) {
	if d == nil || d.id != Xid8 {
		return Value{}, fmt.Errorf("invalid xid8 descriptor")
	}
	return Value{typ: d, integer: int64(n)}, nil
}

func (v Value) XID8() (uint64, error) {
	if v.null || v.typ == nil || v.typ.id != Xid8 {
		return 0, fmt.Errorf("not an xid8 value")
	}
	return uint64(v.integer), nil
}
