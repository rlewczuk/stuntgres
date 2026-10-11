package sqltype

import (
	"context"
	"fmt"
	"strings"
)

// IndexClass is an immutable released method-specific class identity. Its input
// and family belong here, not to namespace lookup or expression coercion.
type IndexClass struct {
	Name   string
	Family string
	input  ID
	oid    uint32
	enum   bool
	hash   bool
}

// OID is the released identity. Unowned B-tree diagnostic classes do not
// advertise an implemented ordering provider.
func (c IndexClass) OID() uint32 { return c.oid }

func (c IndexClass) IsHash() bool { return c.hash }

// Inference compares the declared input and family, not class aliases or
// CREATE's broader binary-compatible admission set.
func (c IndexClass) SameInferenceIdentity(other IndexClass) bool {
	return c.Family == other.Family && c.input == other.input && c.enum == other.enum && c.hash == other.hash
}

func LookupIndexClass(name string) (IndexClass, bool) {
	c := IndexClass{Name: name, Family: name}
	switch name {
	case "bool_ops":
		c.input = Bool
		c.oid = 10003
	case "bytea_ops":
		c.input = Bytea
		c.oid = 10006
	case "char_ops":
		c.input = Char
		c.oid = 10007
	case "name_ops":
		c.input = Name
		c.oid = 10028
	case "int2_ops":
		c.input = Int2
		c.Family = "integer_ops"
		c.oid = 1979
	case "int4_ops":
		c.input = Int4
		c.Family = "integer_ops"
		c.oid = 1978
	case "int8_ops":
		c.input = Int8
		c.Family = "integer_ops"
		c.oid = 3124
	case "text_ops", "varchar_ops":
		c.input = Text
		c.Family = "text_ops"
		c.oid = 3126
		if name == "varchar_ops" {
			c.oid = 10044
		}
	case "bpchar_ops":
		c.input = BPChar
		c.oid = 10004
	case "oid_ops":
		c.input = Oid
		c.oid = 1981
	case "float4_ops":
		c.input = Float4
		c.Family = "float_ops"
		c.oid = 10012
	case "float8_ops":
		c.input = Float8
		c.Family = "float_ops"
		c.oid = 3123
	case "date_ops":
		c.input = Date
		c.Family = "datetime_ops"
		c.oid = 3122
	case "time_ops":
		c.input = Time
		c.oid = 10038
	case "timestamp_ops":
		c.input = Timestamp
		c.Family = "datetime_ops"
		c.oid = 3128
	case "timestamptz_ops":
		c.input = Timestamptz
		c.Family = "datetime_ops"
		c.oid = 3127
	case "timetz_ops":
		c.input = Timetz
		c.oid = 10041
	case "interval_ops":
		c.input = Interval
		c.oid = 10022
	case "numeric_ops":
		c.input = Numeric
		c.oid = 3125
	case "money_ops":
		c.input = Money
		c.oid = 10047
	case "uuid_ops":
		c.input = UUID
		c.oid = 10065
	case "inet_ops":
		c.input = Inet
		c.Family = "network_ops"
		c.oid = 10015
	case "jsonb_ops":
		c.input = JSONB
		c.oid = 10088
	case "text_pattern_ops", "varchar_pattern_ops":
		c.input = Text
		c.Family = "text_pattern_ops"
		c.oid = 4217
		if name == "varchar_pattern_ops" {
			c.oid = 4218
		}
	case "bpchar_pattern_ops":
		c.input = BPChar
		c.oid = 4219
	case "enum_ops":
		c.oid = 10069
		c.enum = true
	case "array_ops":
		c.input = NameArray
	case "tid_ops":
		c.input = 27 // Diagnostic identity only; no tid descriptor/provider.
	// Released classes without an owned input/comparator. They cannot authorize
	// an implemented scalar just because their spelling exists in pg_catalog.
	case "record_ops", "record_image_ops", "range_ops", "multirange_ops", "bit_ops", "varbit_ops", "pg_lsn_ops", "tsquery_ops", "tsvector_ops", "xid8_ops":
	default:
		return IndexClass{}, false
	}
	return c, true
}

func DefaultIndexClass(t *Descriptor) (IndexClass, bool) {
	if !t.Concrete() {
		return IndexClass{}, false
	}
	if t.BaseType().IsEnum() {
		return LookupIndexClass("enum_ops")
	}
	return DefaultIndexClassID(t.BaseType().id)
}

// DefaultIndexClassID supports released system-attribute diagnostic identities
// without constructing/admitting a new storage type or comparison provider.
func DefaultIndexClassID(id ID) (IndexClass, bool) {
	var name string
	switch id {
	case Bool:
		name = "bool_ops"
	case Bytea:
		name = "bytea_ops"
	case Char:
		name = "char_ops"
	case Name:
		name = "name_ops"
	case Int2:
		name = "int2_ops"
	case Int4:
		name = "int4_ops"
	case Int8:
		name = "int8_ops"
	case Text, VarChar:
		name = "text_ops"
	case BPChar:
		name = "bpchar_ops"
	case Oid, Regclass, Regtype:
		name = "oid_ops"
	case Float4:
		name = "float4_ops"
	case Float8:
		name = "float8_ops"
	case Date:
		name = "date_ops"
	case Time:
		name = "time_ops"
	case Timestamp:
		name = "timestamp_ops"
	case Timestamptz:
		name = "timestamptz_ops"
	case Timetz:
		name = "timetz_ops"
	case Interval:
		name = "interval_ops"
	case Numeric:
		name = "numeric_ops"
	case Money:
		name = "money_ops"
	case UUID:
		name = "uuid_ops"
	case Inet, Cidr:
		name = "inet_ops"
	case JSONB:
		name = "jsonb_ops"
	case NameArray:
		name = "array_ops"
	case 27:
		name = "tid_ops"
	default:
		return IndexClass{}, false
	}
	return LookupIndexClass(name)
}

// Accepts models executable keys with released directional binary compatibility,
// never catalog identity alone or general implicit/assignment casts. Diagnostic
// system identities use AcceptsID instead. Domains flatten; values stay typed.
func (c IndexClass) Accepts(t *Descriptor) bool {
	if !t.Concrete() {
		return false
	}
	if c.hash && c.input == NameArray {
		return t.BaseType().IsArray()
	}
	if c.enum {
		return t.BaseType().IsEnum()
	}
	return c.AcceptsID(t.BaseType().id)
}

// AcceptsID checks released input identity only; it grants no runtime provider.
func (c IndexClass) AcceptsID(id ID) bool {
	if c.input == 0 {
		return false
	}
	if id == c.input {
		return true
	}
	switch c.input {
	case Inet:
		return id == Cidr
	case Text:
		return id == VarChar
	case BPChar:
		return id == Text || id == VarChar
	case Oid:
		return id == Int4 || id == Regclass || id == Regtype
	}
	return false
}

// SystemAttributeType supplies only released diagnostic identity. These are
// not concrete descriptors, codecs, comparisons or new SQL type admission.
func SystemAttributeType(name string) (ID, string) {
	switch name {
	case "ctid":
		return 27, "tid"
	case "xmin", "xmax":
		return 28, "xid"
	case "cmin", "cmax":
		return 29, "cid"
	case "tableoid":
		return Oid, "oid"
	}
	return 0, ""
}

type IndexComparison struct {
	comparison Comparison
	input      ID
}

func BindIndexOrdering(u Usage, c IndexClass) (IndexComparison, error) {
	if c.hash {
		return IndexComparison{}, fmt.Errorf("hash class has no ordering provider")
	}
	if !c.Accepts(u.Type) {
		return IndexComparison{}, failure("42804", fmt.Sprintf("operator class %q does not accept data type %s", c.Name, u.Type.DiagnosticName()))
	}
	if c.input == NameArray {
		return IndexComparison{}, failure("0A000", "index operator class is unsupported")
	}
	p, err := BindOrdering(u)
	return IndexComparison{comparison: p, input: c.input}, err
}

func (p IndexComparison) Compare(ctx context.Context, a, b Value, input InputEnvironment) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if a.typ != p.comparison.left || b.typ != p.comparison.right || a.null || b.null || !a.typ.Concrete() {
		return 0, fmt.Errorf("invalid index comparison values")
	}
	if p.input == Oid && a.typ.BaseType().id == Int4 {
		av, bv := uint32(a.integer), uint32(b.integer)
		if av < bv {
			return -1, nil
		}
		if av > bv {
			return 1, nil
		}
		return 0, nil
	}
	if p.input == BPChar {
		return strings.Compare(strings.TrimRight(a.text, " "), strings.TrimRight(b.text, " ")), nil
	}
	return p.comparison.Compare(ctx, a, b, input)
}
