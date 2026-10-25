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

// unsignedPrefix reproduces strtoul(base 0), returning the original byte offset
// after the number. No digits is end zero; overflow precedes trailing junk.
func unsignedPrefix(text string) (n uint64, end int, overflow bool) {
	start := 0
	negative := false
	if len(text)>0 && (text[0]=='+' || text[0]=='-') {
		negative = text[0]=='-'
		start++
	}
	base := uint64(10)
	if start<len(text) && text[start]=='0' {
		base=8
		if start+2<len(text) && (text[start+1]=='x' || text[start+1]=='X') {
			if _,ok:=digit(text[start+2],16);ok { base=16;start+=2 }
		}
	}
	end=start
	for end<len(text) {
		x,ok:=digit(text[end],base)
		if !ok { break }
		if n>(^uint64(0)-x)/base { overflow=true } else if !overflow { n=n*base+x }
		end++
	}
	if end==start { return 0,0,false }
	if negative { n=-n }
	return n,end,overflow
}

// Whole-input scalar policy remains separate from vector prefix scanning.
func (d *Descriptor) unsignedInput(text string) (uint64,error) {
	s:=trim(text)
	n,end,overflow:=unsignedPrefix(s)
	if overflow { return 0,vectorInputError("22003",d.name,text) }
	if end==0 || trim(s[end:])!="" { return 0,vectorInputError("22P02",d.name,text) }
	return n,nil
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
	if target.id == Regclass || target.id == Regtype || target.id == Regrole || IsOperatorAlias(target.id) || IsRoutineAlias(target.id) || IsRegObjectAlias(target.id) || IsRegTS(target.id) {
		var oid uint32
		if v.typ.id == Oid || v.typ.id == Regclass || v.typ.id == Regtype || v.typ.id == Regrole || IsOperatorAlias(v.typ.id) || IsRoutineAlias(v.typ.id) || IsRegObjectAlias(v.typ.id) || IsRegTS(v.typ.id) {
			oid = v.oid
		} else {
			if v.typ.id == Int8 && (v.integer < 0 || v.integer > 4294967295) {
				return Value{}, failure("22003", "OID out of range")
			}
			oid = uint32(v.integer)
		}
		if IsRoutineAlias(target.id) {
			return RegprocValue(target, oid)
		}
		if target.id == Regtype {
			return RegtypeValue(target, oid)
		}
		if target.id == Regrole {
			return RegroleValue(target, oid)
		}
		if IsOperatorAlias(target.id) {
			return OperatorValue(target, oid)
		}
		if IsRegObjectAlias(target.id) {
			return RegObjectValue(target, oid)
		}
		if IsRegTS(target.id) {
			return RegTSValue(target, oid)
		}
		// Canonical identity is validated here; avoid parsing it again in String.
		return Value{typ: target, text: strconv.FormatUint(uint64(oid), 10), oid: oid, integer: 1}, nil
	}
	if target.id == Oid {
		if v.typ.id == Regclass || v.typ.id == Regtype || v.typ.id == Regrole || IsOperatorAlias(v.typ.id) || IsRoutineAlias(v.typ.id) || IsRegObjectAlias(v.typ.id) || IsRegTS(v.typ.id) {
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
