package sqltype

import (
	"context"
	"fmt"
	"strings"
)

// IndexClass is an immutable released method-specific class identity. Its input
// and family belong here, not to namespace lookup or expression coercion.
type IndexClass struct {
	Name                    string
	Family                  string
	input                   ID
	oid                     uint32
	method, family, storage uint32
	enum                    bool
	hash                    bool
	gist                    bool
	brin                    bool
	spgist                  bool
	support                 [4]IndexSupport
}

// OID is the released identity. Unowned B-tree diagnostic classes do not
// advertise an implemented ordering provider.
func (c IndexClass) OID() uint32 { return c.oid }

func (c IndexClass) MethodOID() uint32  { return c.method }
func (c IndexClass) FamilyOID() uint32  { return c.family }
func (c IndexClass) StorageOID() uint32 { return c.storage }
func (c IndexClass) InputOID() uint32 {
	if c.enum {
		return 3500
	}
	return uint32(c.input)
}

// IndexClassDefinition contains catalog-selected identity and provider input.
// Construction never resolves a class name or substitutes a builtin class.
type IndexClassDefinition struct {
	OID, Method, Family, Input, Storage uint32
	Name, FamilyName                    string
	Hash, GiST, Brin, SPGiST            bool
}

func NewIndexClass(d IndexClassDefinition) IndexClass {
	return IndexClass{Name: d.Name, Family: d.FamilyName, oid: d.OID,
		method: d.Method, family: d.Family, storage: d.Storage,
		input: ID(d.Input), enum: d.Input == 3500, hash: d.Hash, gist: d.GiST, brin: d.Brin, spgist: d.SPGiST}
}

func (c IndexClass) IsHash() bool   { return c.hash }
func (c IndexClass) IsGiST() bool   { return c.gist }
func (c IndexClass) IsBrin() bool   { return c.brin }
func (c IndexClass) IsSPGiST() bool { return c.spgist }

// SameIdentity compares the declared class, independently of the catalog
// members captured for execution or current presentation names.
func (c IndexClass) SameIdentity(other IndexClass) bool {
	c.support, other.support = [4]IndexSupport{}, [4]IndexSupport{}
	c.Name, other.Name, c.Family, other.Family = "", "", "", ""
	return c == other
}

// OIDKeyValue supplies the physical oid_ops datum for diagnostic output only.
// Like comparison and HASH normalization, it reinterprets binary-compatible
// input bits; it is not a SQL cast and never evaluates domain constraints.
func (c IndexClass) OIDKeyValue(v Value, oid *Descriptor) (Value, error) {
	if c.input != Oid || oid == nil || oid.id != Oid || !c.Accepts(v.typ) {
		return Value{}, fmt.Errorf("invalid OID index output value")
	}
	if !v.null {
		switch v.typ.BaseType().id {
		case Int4:
			v.oid = uint32(v.integer)
		case Regclass:
			if v.integer != 1 {
				return Value{}, fmt.Errorf("invalid canonical regclass index key")
			}
		}
	}
	v.typ = oid
	return v, nil
}

// Inference compares the declared input and family, not class aliases or
// CREATE's broader binary-compatible admission set.
func (c IndexClass) SameInferenceIdentity(other IndexClass) bool {
	return c.family == other.family && c.method == other.method && c.input == other.input && c.enum == other.enum && c.hash == other.hash && c.gist == other.gist && c.brin == other.brin && c.spgist == other.spgist
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
		c.Family = "text_ops"
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
	case "xid8_ops":
		c.input = Xid8
		c.oid = 10053 // Observed on the pinned PostgreSQL 18.0 oracle.
	case "pg_lsn_ops":
		c.input, c.oid = PgLSN, 10067 // Allocated identity observed on the pinned PostgreSQL 18.6 oracle.
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
		c.input, c.oid = Tid, 10050
	case "macaddr_ops":
		c.input, c.oid = Macaddr, 10024 // Observed on the pinned PostgreSQL 18.6 oracle.
	// Released classes without an owned input/comparator. They cannot authorize
	// an implemented scalar just because their spelling exists in pg_catalog.
	case "record_ops", "record_image_ops", "range_ops", "multirange_ops", "bit_ops", "varbit_ops", "tsquery_ops", "tsvector_ops":
	default:
		return IndexClass{}, false
	}
	return c.withSeedIdentity(403), true
}

// withSeedIdentity stamps bootstrap/test values only. Catalog construction uses
// NewIndexClass and never resolves a released class spelling.
func (c IndexClass) withSeedIdentity(method uint32) IndexClass {
	c.method, c.family, c.storage = method, 0, 0
	for _, fact := range supportedIndexClasses {
		if fact.method == method && fact.name == c.Name {
			c.family, c.storage = fact.family, fact.keyType
			break
		}
	}
	return c
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
	case Text, VarChar, PgNodeTree:
		name = "text_ops"
	case BPChar:
		name = "bpchar_ops"
	case Oid, Regclass, Regtype, Regproc, Regprocedure, Regrole, Regoper, Regoperator, Regcollation, Regnamespace, Regconfig, Regdictionary:
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
	case Xid8:
		name = "xid8_ops"
	case PgLSN:
		name = "pg_lsn_ops"
	case NameArray:
		name = "array_ops"
	case 27:
		name = "tid_ops"
	case Macaddr:
		name = "macaddr_ops"
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
	if (c.gist || c.spgist) && c.input == 3831 {
		return t.BaseType().IsRange()
	}
	if c.gist && c.input == 4537 {
		return t.BaseType().IsMultirange()
	}
	return c.AcceptsID(t.BaseType().id)
}

// AcceptsID checks released input identity only; it grants no runtime provider.
func (c IndexClass) AcceptsID(id ID) bool {
	if (c.gist || c.spgist) && c.input == 3831 {
		return isRange(id)
	}
	if c.gist && c.input == 4537 {
		return isMultirange(id)
	}
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
		return id == Int4 || id == Regclass || id == Regtype || id == Regrole || IsOperatorAlias(id) || IsRoutineAlias(id) || IsRegObjectAlias(id) || IsRegTS(id)
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

// IndexSupport is an immutable catalog-selected member, never a type resolver.
// Routine is checked against the native provider's released formal signature.
type IndexSupportProvider uint8

const (
	BuiltinIndexSupport IndexSupportProvider = iota
	CubeIndexSupport
)

type IndexSupport struct {
	Provider                             IndexSupportProvider
	Family, Member, Routine, Left, Right uint32
	Revision                             uint64
	FamilyRevision, RoutineRevision      uint64
}

func (c IndexClass) WithSupport(slot int, support IndexSupport) IndexClass {
	if slot >= 1 && slot <= len(c.support) {
		c.support[slot-1] = support
	}
	return c
}

func (c IndexClass) Support(slot int) IndexSupport {
	if slot < 1 || slot > len(c.support) {
		return IndexSupport{}
	}
	return c.support[slot-1]
}

type IndexComparison struct {
	comparison Comparison
	input      ID
	routine    uint32
	support    IndexSupport
}

func BindIndexOrdering(u Usage, c IndexClass) (IndexComparison, error) {
	if c.hash || c.gist || c.brin || c.spgist {
		return IndexComparison{}, fmt.Errorf("non-btree class has no ordering provider")
	}
	if !c.Accepts(u.Type) {
		return IndexComparison{}, failure("42804", fmt.Sprintf("operator class %q does not accept data type %s", c.Name, u.Type.DiagnosticName()))
	}
	if c.input == NameArray {
		return IndexComparison{}, failure("0A000", "index operator class is unsupported")
	}
	s := c.Support(1)
	if s.Provider == CubeIndexSupport {
		if !u.Type.BaseType().IsCube() || c.input != u.Type.BaseType().ID() || c.method != 403 || s.Family != c.family || s.Left != uint32(c.input) || s.Right != s.Left || s.Member == 0 || s.Routine == 0 || s.Revision == 0 || s.FamilyRevision == 0 || s.RoutineRevision == 0 || !u.validCollation() {
			return IndexComparison{}, fmt.Errorf("invalid installed cube index support")
		}
		p, err := BindComparison(u.Type, u.Type, u.Collation, false)
		if err != nil {
			return IndexComparison{}, err
		}
		p.orderingBound, p.defaultBtreeOpclass = true, c.OID()
		return IndexComparison{comparison: p, input: c.input, routine: s.Routine, support: s}, nil
	}
	if s.Provider != BuiltinIndexSupport {
		return IndexComparison{}, fmt.Errorf("unknown index support provider")
	}
	n, ok := NativeIndexRoutineByOID(s.Routine)
	if !ok || n.Kind != IndexCompareRoutine || n.Left != s.Left || n.Right != s.Right || s.Member == 0 {
		return IndexComparison{}, fmt.Errorf("index comparison has no registered native support")
	}
	if !u.validCollation() {
		return IndexComparison{}, fmt.Errorf("invalid ordering collation")
	}
	// The selected operation owns admission. Comparison supplies only typed
	// datum primitives and retained collation, not another class lookup.
	p := Comparison{left: u.Type, right: u.Type, inputCollation: u.Collation, orderingBound: true, defaultBtreeOpclass: c.OID()}
	if n.Pattern {
		p.inputCollation = CollationRef{ID: 950, Derivation: Default}
	}
	return IndexComparison{comparison: p, input: ID(n.Left), routine: n.OID, support: s}, nil
}

func (p IndexComparison) Support() IndexSupport { return p.support }

func (p IndexComparison) DatumComparison() Comparison { return p.comparison }

// SameOperation ignores ACL/name-only descriptor revisions. Captures still
// retain those revisions, but metadata publication is not an ordering change.
func (p IndexComparison) SameOperation(other IndexComparison) bool {
	p.support.Revision, p.support.FamilyRevision, p.support.RoutineRevision = 0, 0, 0
	other.support.Revision, other.support.FamilyRevision, other.support.RoutineRevision = 0, 0, 0
	p.comparison.defaultBtreeOpclass, other.comparison.defaultBtreeOpclass = 0, 0
	return p == other
}

func bindIndexPair(left, right Usage, support IndexSupport) (IndexComparison, error) {
	n, ok := NativeIndexRoutineByOID(support.Routine)
	if !ok || n.Kind != IndexCompareRoutine || n.Left != support.Left || n.Right != support.Right || support.Member == 0 {
		return IndexComparison{}, fmt.Errorf("foreign-key pair has no registered native comparison")
	}
	p := Comparison{left: left.Type, right: right.Type, inputCollation: left.Collation, orderingBound: true}
	return IndexComparison{comparison: p, input: ID(n.Left), routine: n.OID, support: support}, nil
}

func (p *IndexComparison) Compare(ctx context.Context, a, b Value, input InputEnvironment) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if a.typ != p.comparison.left || b.typ != p.comparison.right || a.null || b.null || !a.typ.Concrete() {
		return 0, fmt.Errorf("invalid index comparison values")
	}
	if p.support.Provider == CubeIndexSupport {
		return p.comparison.Compare(ctx, a, b, input)
	}
	if p.routine == 351 {
		return nativeInt4Compare(a.integer, b.integer), nil
	}
	if p.routine == 836 {
		// MACADDR stores the unsigned six octets in network order, in 48 bits.
		av, bv := uint64(a.integer), uint64(b.integer)
		if av < bv {
			return -1, nil
		}
		if av > bv {
			return 1, nil
		}
		return 0, nil
	}
	if p.routine == 181 {
		n := a.integer - b.integer
		if n < -2147483648 || n > 2147483647 {
			return 0, failure("22003", "integer out of range")
		}
		return int(n), nil
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
		return p.comparison.inputCollation.CompareText(ctx, strings.TrimRight(a.text, " "), strings.TrimRight(b.text, " "))
	}
	return p.comparison.Compare(ctx, a, b, input)
}
