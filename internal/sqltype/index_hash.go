package sqltype

import (
	"context"
	"fmt"
)

// Identities observed in the pinned PG18.0 pg_opclass oracle. Diagnostic-known
// array/system classes do not authorize new storage types or SQL operators.
func LookupHashIndexClass(name string) (IndexClass, bool) {
	c, _ := LookupIndexClass(name)
	c.Name, c.hash = name, true
	switch name {
	case "bool_ops":
		c.oid = 10048
	case "bytea_ops":
		c.oid = 10049
	case "char_ops":
		c.oid = 10008
	case "name_ops":
		c.oid, c.Family = 10029, "text_ops"
	case "int2_ops":
		c.oid = 10019
	case "int4_ops":
		c.oid = 10020
	case "int8_ops":
		c.oid = 10021
	case "text_ops":
		c.oid = 10037
	case "varchar_ops":
		c.oid = 10045
	case "bpchar_ops":
		c.oid = 10005
	case "oid_ops":
		c.oid = 10031
	case "float4_ops":
		c.oid = 10013
	case "float8_ops":
		c.oid = 10014
	case "numeric_ops":
		c.oid = 10030
	case "date_ops":
		c.oid, c.Family = 10011, "date_ops"
	case "time_ops":
		c.oid = 10039
	case "timestamp_ops":
		c.oid, c.Family = 10046, "timestamp_ops"
	case "timestamptz_ops":
		c.oid, c.Family = 10040, "timestamptz_ops"
	case "timetz_ops":
		c.oid = 10042
	case "interval_ops":
		c.oid = 10023
	case "uuid_ops":
		c.oid = 10066
	case "jsonb_ops":
		c.oid = 10089
	case "enum_ops":
		c.oid = 10070
	case "text_pattern_ops":
		c.oid = 10056
	case "varchar_pattern_ops":
		c.oid = 10057
	case "bpchar_pattern_ops":
		c.oid = 10058
	case "array_ops":
		c.oid = 10001
	case "tid_ops":
		c.oid = 10055
	case "xid_ops":
		c.input, c.oid, c.Family = 28, 10051, name
	case "cid_ops":
		c.input, c.oid, c.Family = 29, 10054, name
	case "xid8_ops":
		c.input, c.oid, c.Family = Xid8, 10052, name
	case "record_ops", "network_ops", "range_ops", "multirange_ops", "pg_lsn_ops":
		c.Family = name // Released, but no owned concrete scalar provider.
	default:
		return IndexClass{}, false
	}
	return c, true
}

func DefaultHashIndexClass(t *Descriptor) (IndexClass, bool) {
	if !t.Concrete() {
		return IndexClass{}, false
	}
	if t.BaseType().IsEnum() {
		return LookupHashIndexClass("enum_ops")
	}
	if t.BaseType().IsArray() {
		return LookupHashIndexClass("array_ops")
	}
	return DefaultHashIndexClassID(t.BaseType().id)
}

func DefaultHashIndexClassID(id ID) (IndexClass, bool) {
	if id == 28 {
		return LookupHashIndexClass("xid_ops")
	}
	if id == 29 {
		return LookupHashIndexClass("cid_ops")
	}
	c, ok := DefaultIndexClassID(id)
	if !ok {
		return IndexClass{}, false
	}
	return LookupHashIndexClass(c.Name)
}

// IndexHash normalizes into the class input representation once per datum. The
// normalized values never escape sqltype or replace the stored typed keys.
// Grouping owns compatible equality/hash, including numeric, NaN and intervals.
type IndexHash struct {
	usage    Usage
	class    IndexClass
	grouping Grouping
}

func BindIndexHash(u Usage, c IndexClass) (IndexHash, error) {
	if !c.hash {
		return IndexHash{}, fmt.Errorf("non-hash class has no hash provider")
	}
	if !c.Accepts(u.Type) {
		return IndexHash{}, failure("42804", fmt.Sprintf("operator class %q does not accept data type %s", c.Name, u.Type.DiagnosticName()))
	}
	if u.Type.BaseType().IsArray() || c.oid == 0 {
		return IndexHash{}, failure("0A000", "index operator class is unsupported")
	}
	normal := u.Type.BaseType()
	if !c.enum && normal.id != c.input {
		// The only admitted representation changes are binary-compatible
		// character keys and integer/regclass into unsigned OID keys.
		normal = &Descriptor{id: c.input}
	}
	keyUsage := u
	keyUsage.Type, keyUsage.Typmod = normal, -1
	g, err := BindGrouping(keyUsage)
	if err != nil {
		return IndexHash{}, err
	}
	return IndexHash{usage: u, class: c, grouping: g}, nil
}

func (p IndexHash) normalize(v Value) (Value, error) {
	if v.typ != p.usage.Type {
		return Value{}, fmt.Errorf("invalid hash index value")
	}
	base := v.typ.BaseType().id
	v.typ = p.grouping.usage.Type
	if !v.null && p.class.input == Oid {
		if base == Int4 {
			v.oid = uint32(v.integer)
		}
		if base == Regclass && v.integer != 1 {
			return Value{}, fmt.Errorf("invalid canonical regclass hash key")
		}
	}
	return v, nil
}

func (p IndexHash) Hash(ctx context.Context, v Value) (uint64, error) {
	v, err := p.normalize(v)
	if err != nil {
		return 0, err
	}
	return p.grouping.Hash(ctx, v)
}

func (p IndexHash) Equal(ctx context.Context, a, b Value) (bool, error) {
	a, err := p.normalize(a)
	if err != nil {
		return false, err
	}
	b, err = p.normalize(b)
	if err != nil {
		return false, err
	}
	return p.grouping.Equal(ctx, a, b)
}

// Row-side coercions and class-coarser equality are not probe authorization.
// A conservative caller may fall back even though building this class is valid.
func (p IndexHash) OrdinaryEquality() bool {
	id := p.usage.Type.BaseType().id
	return p.class.enum || p.class.input == id || p.class.input == Text && id == VarChar
}
