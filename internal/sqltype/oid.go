package sqltype

import (
	"fmt"
	"strconv"
)

// OIDValue constructs a distinct unsigned four-byte scalar, not a regclass.
func OIDValue(d *Descriptor, n uint32) (Value, error) {
	if d == nil || d.id != Oid {
		return Value{}, fmt.Errorf("invalid oid descriptor")
	}
	return Value{typ: d, oid: n}, nil
}

func (v Value) OID() (uint32, error) {
	if v.null || v.typ == nil || v.typ.id != Oid {
		return 0, fmt.Errorf("not an oid value")
	}
	return v.oid, nil
}

// unsignedInput reproduces PostgreSQL's unsigned strtoul(base 0) scanner,
// retaining the original input for diagnostics and overflow-before-junk order.
func (d *Descriptor) unsignedInput(text string) (uint64, error) {
	s := trim(text)
	negative := false
	if len(s) > 0 && (s[0] == '+' || s[0] == '-') {
		negative = s[0] == '-'
		s = s[1:]
	}
	base := uint64(10)
	if len(s) > 0 && s[0] == '0' {
		base = 8
		if len(s) > 2 && (s[1] == 'x' || s[1] == 'X') {
			if _, ok := digit(s[2], 16); ok {
				base, s = 16, s[2:]
			}
		}
	}
	var n uint64
	i, overflow := 0, false
	for i < len(s) {
		x, ok := digit(s[i], base)
		if !ok {
			break
		}
		if n > (^uint64(0)-x)/base {
			overflow = true
		} else if !overflow {
			n = n*base + x
		}
		i++
	}
	if overflow {
		return 0, failure("22003", fmt.Sprintf("value \"%s\" is out of range for type %s", text, d.name))
	}
	if i == 0 || trim(s[i:]) != "" {
		return 0, failure("22P02", fmt.Sprintf("invalid input syntax for type %s: \"%s\"", d.name, text))
	}
	if negative {
		n = -n
	}
	return n, nil
}

func (d *Descriptor) oidInput(text string) (Value, error) {
	n, err := d.unsignedInput(text)
	if err != nil {
		return Value{}, err
	}
	if d.id == Xid8 {
		return XID8Value(d, n)
	}
	low := uint32(n)
	if n != uint64(low) && n != uint64(int64(int32(low))) {
		return Value{}, failure("22003", fmt.Sprintf("value \"%s\" is out of range for type %s", text, d.name))
	}
	if d.id == Xid {
		return XIDValue(d, low)
	}
	if d.id == Cid {
		return CIDValue(d, low)
	}
	return OIDValue(d, low)
}

func oidCoercion(v Value, target *Descriptor) (Value, error) {
	if target.id == Regclass || target.id == Regtype || target.id == Regrole {
		var oid uint32
		if v.typ.id == Oid {
			oid = v.oid
		} else {
			if v.typ.id == Int8 && (v.integer < 0 || v.integer > 4294967295) {
				return Value{}, failure("22003", "OID out of range")
			}
			oid = uint32(v.integer)
		}
		if target.id == Regtype {
			return RegtypeValue(target, oid)
		}
		if target.id == Regrole {
			return RegroleValue(target, oid)
		}
		// Canonical identity is validated here; avoid parsing it again in String.
		return Value{typ: target, text: strconv.FormatUint(uint64(oid), 10), oid: oid, integer: 1}, nil
	}
	if target.id == Oid {
		if v.typ.id == Regclass || v.typ.id == Regtype || v.typ.id == Regproc || v.typ.id == Regrole {
			if v.integer != 1 {
				return Value{}, fmt.Errorf("unresolved regclass value")
			}
			return OIDValue(target, v.oid)
		}
		if v.typ.id == Int8 && (v.integer < 0 || v.integer > 4294967295) {
			return Value{}, failure("22003", "OID out of range")
		}
		return OIDValue(target, uint32(v.integer))
	}
	n := int64(v.oid)
	if target.id == Int4 {
		n = int64(int32(v.oid))
	}
	return Integer(target, n)
}
