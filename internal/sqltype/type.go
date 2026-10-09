// Package sqltype owns concrete type identity, immutable values and the bounded
// input, assignment and comparison providers used by the SQL engine.
package sqltype

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"stuntgres/internal/sqlstate"
)

type ID uint32

const (
	Bool        ID = 16
	Bytea       ID = 17
	Char        ID = 18
	Name        ID = 19
	Int8        ID = 20
	Int2        ID = 21
	Int4        ID = 23
	Text        ID = 25
	Oid         ID = 26
	JSON        ID = 114
	PgNodeTree  ID = 194
	Float4      ID = 700
	Float8      ID = 701
	Unknown     ID = 705
	NameArray   ID = 1003
	BPChar      ID = 1042
	VarChar     ID = 1043
	Date        ID = 1082
	Time        ID = 1083
	Timestamp   ID = 1114
	Timestamptz ID = 1184
	Interval    ID = 1186
	Timetz      ID = 1266
	Numeric     ID = 1700
	Regclass    ID = 2205
	Void        ID = 2278
	UUID        ID = 2950
)

type CollationDerivation uint8

const (
	None CollationDerivation = iota
	Default
)

type CollationRef struct {
	ID         uint32
	Derivation CollationDerivation
}

type Usage struct {
	Type      *Descriptor
	Typmod    int32
	Collation CollationRef
}

// Codec text output is independent of SQL quoting and driver export.
type Codec interface {
	Input(context.Context, string, int32) (Value, error)
	Output(context.Context, Value) (string, error)
	SQLLiteral(context.Context, Value) (string, error)
}

// Descriptor has no mutable or execution-local state. Catalogs own generations.
type Descriptor struct {
	id                  ID
	name, sql, database string
	base                Usage
	element             *Descriptor
	associatedArray     ID
	delimiter           byte
	enum                *enumDefinition
}

func (d *Descriptor) ID() ID          { return d.id }
func (d *Descriptor) Name() string    { return d.name }
func (d *Descriptor) SQLName() string { return d.sql }
func (d *Descriptor) DiagnosticName() string {
	if d.IsUserDefined() {
		return d.sql
	}
	if d.IsArray() {
		return d.element.DiagnosticName() + "[]"
	}
	if d.id == Timestamp {
		return "timestamp without time zone"
	}
	if d.id == Time {
		return "time without time zone"
	}
	if d.id == Timestamptz {
		return "timestamp with time zone"
	}
	if d.id == Timetz {
		return "time with time zone"
	}
	if d.id == BPChar {
		return "character"
	}
	if d.id == VarChar {
		return "character varying"
	}
	return strings.ToLower(d.sql)
}
func (d *Descriptor) DatabaseName() string { return d.database }
func (d *Descriptor) Concrete() bool       { return d != nil && d.id != 0 && d.id != Unknown }
func (d *Descriptor) Usage() Usage {
	if d.IsDomain() {
		return Usage{Type: d, Typmod: -1, Collation: d.base.Collation}
	}
	if d.IsArray() {
		u := d.element.Usage()
		u.Type = d
		u.Typmod = -1
		return u
	}
	u := Usage{Type: d, Typmod: -1}
	if IsCharacter(d.id) {
		u.Collation = CollationRef{ID: 100, Derivation: Default}
	} else if d.id == Name {
		u.Collation = CollationRef{ID: 950, Derivation: Default}
	}
	return u
}

// Builtins returns a new immutable generation, not a shared mutable registry.
func Builtins() []*Descriptor {
	types := []*Descriptor{
		{id: Bool, name: "bool", sql: "BOOLEAN", database: "BOOL", associatedArray: 1000},
		{id: Bytea, name: "bytea", sql: "BYTEA", database: "BYTEA", associatedArray: 1001},
		{id: Char, name: "char", sql: "\"char\"", database: "CHAR"},
		{id: Name, name: "name", sql: "NAME", database: "NAME", associatedArray: NameArray},
		{id: Int8, name: "int8", sql: "BIGINT", database: "INT8", associatedArray: 1016},
		{id: Int2, name: "int2", sql: "SMALLINT", database: "INT2", associatedArray: 1005},
		{id: Int4, name: "int4", sql: "INTEGER", database: "INT4", associatedArray: 1007},
		{id: Text, name: "text", sql: "TEXT", database: "TEXT", associatedArray: 1009},
		{id: Oid, name: "oid", sql: "OID", database: "OID", associatedArray: 1028},
		{id: JSON, name: "json", sql: "JSON", database: "JSON", associatedArray: 199},
		{id: PgNodeTree, name: "pg_node_tree", sql: "PG_NODE_TREE", database: "PG_NODE_TREE"},
		{id: BPChar, name: "bpchar", sql: "BPCHAR", database: "BPCHAR", associatedArray: 1014},
		{id: VarChar, name: "varchar", sql: "VARCHAR", database: "VARCHAR", associatedArray: 1015},
		{id: Float4, name: "float4", sql: "REAL", database: "FLOAT4", associatedArray: 1021},
		{id: Float8, name: "float8", sql: "DOUBLE PRECISION", database: "FLOAT8", associatedArray: 1022},
		{id: Date, name: "date", sql: "DATE", database: "DATE", associatedArray: 1182},
		{id: Time, name: "time", sql: "TIME", database: "TIME", associatedArray: 1183},
		{id: Timestamp, name: "timestamp", sql: "TIMESTAMP", database: "TIMESTAMP", associatedArray: 1115},
		{id: Timestamptz, name: "timestamptz", sql: "TIMESTAMP WITH TIME ZONE", database: "TIMESTAMPTZ", associatedArray: 1185},
		{id: Interval, name: "interval", sql: "INTERVAL", database: "INTERVAL", associatedArray: 1187},
		{id: Timetz, name: "timetz", sql: "TIME WITH TIME ZONE", database: "TIMETZ", associatedArray: 1270},
		{id: Numeric, name: "numeric", sql: "NUMERIC", database: "NUMERIC", associatedArray: 1231},
		{id: UUID, name: "uuid", sql: "UUID", database: "UUID", associatedArray: 2951},
		{id: Regclass, name: "regclass", sql: "REGCLASS", database: "REGCLASS", associatedArray: 2210},
		{id: Void, name: "void", sql: "VOID", database: "VOID"},
		{id: Unknown, name: "unknown"},
	}
	// Reserve the one result buffer before adding associated descriptors.
	result := make([]*Descriptor, 0, len(types)*2)
	result = append(result, types...)
	for _, element := range types {
		if element.associatedArray == 0 {
			continue
		}
		array, err := Array(element.associatedArray, "_"+element.name, element)
		if err != nil {
			panic(err)
		}
		result = append(result, array)
	}
	return result
}

// Zero Value is invalid, not NULL. Scalars stay inline; only structural tree
// values own a pointer to an immutable payload. Value remains comparable.
type Value struct {
	typ             *Descriptor
	null            bool
	integer         int64
	west            int32
	oid             uint32
	floating        float64
	boolean         bool
	text            string
	exponent, scale int64
	negative        bool
	special         uint8
	uuid            [16]byte
	months, days    int32
	tree            *treePayload
	treeKey         bool // text is an immutable prepared structural equality/output key.
	array           *arrayPayload
}

func (v Value) Type() *Descriptor { return v.typ }
func (v Value) IsNull() bool      { return v.null }

// Retag remints descriptor ownership without changing a concrete scalar datum.
// It preserves logical identity and physical base constraints, not just payload shape.
func (v Value) Retag(d *Descriptor) (Value, error) {
	if v.typ == nil || !v.typ.Concrete() || !d.Concrete() || v.typ.id != d.id || v.typ.IsDomain() != d.IsDomain() || v.typ.BaseType().id != d.BaseType().id {
		return Value{}, fmt.Errorf("incompatible scalar type identity")
	}
	if !compatibleEnum(v.typ.BaseType(), d.BaseType()) {
		return Value{}, fmt.Errorf("incompatible enum declaration")
	}
	if d.IsDomain() {
		from, to := v.typ.Usage().BaseUsage(), d.Usage().BaseUsage()
		if from.Typmod != to.Typmod || from.Collation != to.Collation {
			return Value{}, fmt.Errorf("incompatible domain base constraints")
		}
	}
	if v.typ == d {
		if !v.null && d.BaseType().IsArray() && v.array == nil {
			return Value{}, fmt.Errorf("missing array payload")
		}
		return v, nil
	}
	if v.typ.BaseType().IsArray() {
		return v.MapArray(d, func(element Value, target *Descriptor) (Value, error) { return element.Retag(target) })
	}
	v.typ = d
	return v, nil
}
func Null(d *Descriptor) Value {
	if !d.Concrete() {
		panic("invalid NULL descriptor")
	}
	return Value{typ: d, null: true}
}

// VoidValue is a concrete nonnull function result, distinct from typed NULL.
func VoidValue(d *Descriptor) Value {
	if d == nil || d.id != Void {
		panic("invalid void descriptor")
	}
	return Value{typ: d}
}
func Integer(d *Descriptor, n int64) (Value, error) {
	if d == nil || (d.id != Int2 && d.id != Int4 && d.id != Int8) {
		return Value{}, fmt.Errorf("invalid integer descriptor")
	}
	if d.id == Int2 && (n < -32768 || n > 32767) {
		return Value{}, failure("22003", "smallint out of range")
	}
	if d.id == Int4 && (n < -2147483648 || n > 2147483647) {
		return Value{}, failure("22003", "integer out of range")
	}
	return Value{typ: d, integer: n}, nil
}
func Boolean(d *Descriptor, b bool) Value {
	if d == nil || d.id != Bool {
		panic("invalid boolean descriptor")
	}
	return Value{typ: d, boolean: b}
}
func String(d *Descriptor, s string) Value {
	if d == nil || (!IsCharacter(d.id) && d.id != Name && d.id != Regclass && d.id != Char) {
		panic("invalid string descriptor")
	}
	v := Value{typ: d, text: s}
	if d.id == Regclass && len(s) != 0 && (len(s) == 1 || s[0] != '0') {
		// Keep unresolved regclass input text intact. Canonical identities also
		// carry an inline unsigned payload, validated once rather than per
		// sort comparison; integer marks that payload as valid (OID zero too).
		canonical := true
		for i := range len(s) {
			if s[i] < '0' || s[i] > '9' {
				canonical = false
				break
			}
		}
		if canonical {
			if oid, err := strconv.ParseUint(s, 10, 32); err == nil {
				v.oid, v.integer = uint32(oid), 1
			}
		}
	}
	return v
}

// Names is a checked NAME[] result constructor through the generic payload.
func Names(d *Descriptor, names []string) (Value, error) {
	if d == nil || !d.IsArray() || d.element.ID() != Name {
		return Value{}, fmt.Errorf("invalid NAME[] descriptor")
	}
	elements := make([]Value, len(names))
	for i, name := range names {
		if err := validateCharacterInput(name); err != nil {
			return Value{}, err
		}
		if len(name) > 63 {
			return Value{}, fmt.Errorf("invalid NAME[] element")
		}
		elements[i] = String(d.element, name)
	}
	var dimensions []ArrayDimension
	if len(names) > 0 {
		dimensions = []ArrayDimension{{1, int32(len(names))}}
	}
	return ownedArray(d, dimensions, elements)
}
func (v Value) NamesOutput() (string, error) {
	if v.null || v.typ == nil || !v.typ.IsArray() || v.typ.Element().ID() != Name {
		return "", fmt.Errorf("not a NAME[] value")
	}
	return v.typ.Output(context.Background(), v)
}
func (v Value) Integer() (int64, error) {
	if v.null || v.typ == nil || (v.typ.id != Int2 && v.typ.id != Int4 && v.typ.id != Int8) {
		return 0, fmt.Errorf("not an integer value")
	}
	return v.integer, nil
}
func (v Value) Boolean() (bool, error) {
	if v.null || v.typ == nil || v.typ.id != Bool {
		return false, fmt.Errorf("not a boolean value")
	}
	return v.boolean, nil
}
func (v Value) Text() (string, error) {
	if v.null || v.typ == nil || (!IsCharacter(v.typ.id) && v.typ.id != Name && v.typ.id != Regclass && v.typ.id != Char) {
		return "", fmt.Errorf("not a text value")
	}
	return v.text, nil
}

func failure(code, message string) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
}
func space(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v'
}
func trim(s string) string {
	for len(s) > 0 && space(s[0]) {
		s = s[1:]
	}
	for len(s) > 0 && space(s[len(s)-1]) {
		s = s[:len(s)-1]
	}
	return s
}

func (d *Descriptor) Input(ctx context.Context, text string, typmod int32) (Value, error) {
	return d.InputWithEnvironment(ctx, text, typmod, InputEnvironment{})
}

func (d *Descriptor) InputWithEnvironment(ctx context.Context, text string, typmod int32, env InputEnvironment) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if d.IsDomain() {
		if typmod != -1 {
			return Value{}, fmt.Errorf("invalid domain input modifier")
		}
		base := d.Usage().BaseUsage()
		value, err := base.Type.InputWithEnvironment(ctx, text, base.Typmod, env)
		if err != nil {
			return Value{}, err
		}
		return d.Wrap(value)
	}
	if !d.Concrete() || !d.validTypmod(typmod) {
		return Value{}, fmt.Errorf("invalid input usage")
	}
	if d.IsArray() {
		if err := validateCharacterInput(text); err != nil {
			return Value{}, err
		}
		return d.arrayInput(ctx, text, typmod, env)
	}
	if d.id == PgNodeTree {
		return Value{}, failure("0A000", "cannot accept a value of type pg_node_tree")
	}
	if IsCharacter(d.id) || d.id == Name || d.id == JSON {
		if err := validateCharacterInput(text); err != nil {
			return Value{}, err
		}
	} else if !utf8.ValidString(text) || strings.IndexByte(text, 0) >= 0 {
		return Value{}, failure("22P02", "invalid textual input")
	}
	if d.IsEnum() {
		return d.enumInput(text)
	}
	switch d.id {
	case JSON:
		return d.jsonInput(ctx, text)
	case Oid:
		return d.oidInput(text)
	case Char:
		if len(text) == 4 && text[0] == '\\' && text[1] >= '0' && text[1] <= '7' && text[2] >= '0' && text[2] <= '7' && text[3] >= '0' && text[3] <= '7' {
			value := byte((text[1]-'0')<<6 | (text[2]-'0')<<3 | (text[3] - '0'))
			if value == 0 {
				return String(d, ""), nil
			}
			return String(d, string([]byte{value})), nil
		}
		if len(text) > 1 {
			text = text[:1]
		}
		return String(d, text), nil
	case Name:
		return String(d, clipName(text)), nil
	case Bytea:
		return d.byteaInput(text)
	case UUID:
		return d.uuidInput(text)
	case Date:
		return d.civilInput(text, typmod, env)
	case Time, Timetz:
		return d.timeInput(text, typmod, env)
	case Interval:
		return d.intervalInput(ctx, text, typmod, env)
	case Numeric:
		return d.numericInput(text, typmod)
	case Timestamp, Timestamptz:
		return d.civilInput(text, typmod, env)
	case Text, BPChar, VarChar:
		return sizeCharacter(String(d, text), typmod, false)
	case Regclass:
		if env.Regclass != nil {
			n, err := env.Regclass.ResolveRegclass(ctx, text)
			if err != nil {
				return Value{}, err
			}
			return String(d, strconv.FormatUint(uint64(n), 10)), nil
		}
		n, err := strconv.ParseUint(text, 10, 32)
		if err != nil {
			return Value{}, fmt.Errorf("regclass input lacks catalog authority")
		}
		return String(d, strconv.FormatUint(n, 10)), nil
	case Bool:
		s := strings.ToLower(trim(text))
		if s == "1" {
			return Boolean(d, true), nil
		}
		if s == "0" {
			return Boolean(d, false), nil
		}
		if s != "" && s != "o" {
			for _, p := range []string{"true", "yes", "on"} {
				if strings.HasPrefix(p, s) {
					return Boolean(d, true), nil
				}
			}
			for _, p := range []string{"false", "no", "off"} {
				if strings.HasPrefix(p, s) {
					return Boolean(d, false), nil
				}
			}
		}
		return Value{}, failure("22P02", fmt.Sprintf("invalid input syntax for type boolean: \"%s\"", text))
	case Int2, Int4, Int8:
		return d.integerInput(text)
	case Float4, Float8:
		return d.floatInput(text)
	}
	return Value{}, fmt.Errorf("missing input provider")
}

func digit(c byte, base uint64) (uint64, bool) {
	var n uint64
	switch {
	case c >= '0' && c <= '9':
		n = uint64(c - '0')
	case c >= 'a' && c <= 'f':
		n = uint64(c - 'a' + 10)
	case c >= 'A' && c <= 'F':
		n = uint64(c - 'A' + 10)
	default:
		return 0, false
	}
	return n, n < base
}

func (d *Descriptor) integerInput(text string) (Value, error) {
	name, limit := "integer", uint64(2147483648)
	if d.id == Int2 {
		name, limit = "smallint", 32768
	}
	if d.id == Int8 {
		name, limit = "bigint", uint64(1)<<63
	}
	invalid := func() (Value, error) {
		return Value{}, failure("22P02", fmt.Sprintf("invalid input syntax for type %s: \"%s\"", name, text))
	}
	overflow := func() (Value, error) {
		return Value{}, failure("22003", fmt.Sprintf("value \"%s\" is out of range for type %s", text, name))
	}
	s := text
	for len(s) > 0 && space(s[0]) {
		s = s[1:]
	}
	negative := false
	if len(s) > 0 && (s[0] == '+' || s[0] == '-') {
		negative, s = s[0] == '-', s[1:]
	}
	base, prefixed := uint64(10), false
	if len(s) >= 2 && s[0] == '0' {
		switch s[1] {
		case 'x', 'X':
			base, prefixed = 16, true
		case 'o', 'O':
			base, prefixed = 8, true
		case 'b', 'B':
			base, prefixed = 2, true
		}
		if prefixed {
			s = s[2:]
		}
	}
	var n uint64
	digits, previous := 0, false
	for i := range len(s) {
		c := s[i]
		if c == '_' {
			if (!previous && !(prefixed && i == 0)) || i+1 == len(s) {
				return invalid()
			}
			if _, ok := digit(s[i+1], base); !ok {
				return invalid()
			}
			previous = false
			continue
		}
		v, ok := digit(c, base)
		if !ok {
			if digits == 0 {
				return invalid()
			}
			rest := s[i:]
			for len(rest) > 0 && space(rest[0]) {
				rest = rest[1:]
			}
			if rest != "" {
				return invalid()
			}
			break
		}
		// Match release parsing order: check the accumulated prefix before
		// multiplying, then reject final range only after trailing syntax.
		if n > limit/base {
			return overflow()
		}
		n = n*base + v
		digits++
		previous = true
	}
	if digits == 0 {
		return invalid()
	}
	if n > limit || !negative && n == limit {
		return overflow()
	}
	value := int64(n)
	if negative {
		value = -value
	}
	return Integer(d, value)
}

func (d *Descriptor) Output(ctx context.Context, v Value) (string, error) {
	return d.OutputWithEnvironment(ctx, v, OutputEnvironment{})
}

func (d *Descriptor) OutputWithEnvironment(ctx context.Context, v Value, env OutputEnvironment) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if d == nil || v.typ != d || v.null {
		return "", fmt.Errorf("invalid output value")
	}
	if d.IsDomain() {
		v.typ = d.BaseType()
		return v.typ.OutputWithEnvironment(ctx, v, env)
	}
	if d.IsArray() {
		return d.arrayOutput(ctx, v, env)
	}
	if d.IsEnum() {
		return d.enumOutput(v)
	}
	switch d.id {
	case PgNodeTree:
		if v.treeKey {
			return v.text, nil
		}
		if v.tree == nil {
			return "", fmt.Errorf("missing structural tree payload")
		}
		return v.tree.value.StructuralOutput(ctx)
	case Void:
		return "", nil
	case Oid:
		return strconv.FormatUint(uint64(v.oid), 10), nil
	case Bytea:
		return byteaOutput(v.text, env.ByteaOutput)
	case UUID:
		return uuidOutput(v.uuid), nil
	case Date:
		return dateStyledOutput(v.integer, env.DateStyle), nil
	case Time:
		return timeOutput(v.integer), nil
	case Timetz:
		return timeOutput(v.integer) + zoneOffsetOutput(-v.west), nil
	case Interval:
		return intervalOutput(v, env.IntervalStyle)
	case Numeric:
		return numericOutput(v), nil
	case Timestamp:
		return timestampStyledOutput(v.integer, env.DateStyle, "", 0, false), nil
	case Timestamptz:
		return timestamptzOutput(v.integer, env), nil
	case Int2, Int4, Int8:
		return strconv.FormatInt(v.integer, 10), nil
	case Float4, Float8:
		return floatOutput(v.floating, d.id), nil
	case Char:
		if len(v.text) > 0 && v.text[0] >= 128 {
			return fmt.Sprintf("\\%03o", v.text[0]), nil
		}
		return v.text, nil
	case Regclass:
		if env.Regclass != nil {
			n, err := strconv.ParseUint(v.text, 10, 32)
			if err != nil {
				return "", err
			}
			return env.Regclass.FormatRegclass(uint32(n))
		}
		return v.text, nil
	case Text, BPChar, VarChar, Name, JSON:
		return v.text, nil
	case Bool:
		if v.boolean {
			return "t", nil
		}
		return "f", nil
	}
	return "", fmt.Errorf("missing output provider")
}
func (d *Descriptor) SQLLiteral(ctx context.Context, v Value) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if v.typ != d || !d.Concrete() {
		return "", fmt.Errorf("invalid literal value")
	}
	if v.null {
		return "NULL", nil
	}
	if d.IsDomain() {
		v.typ = d.BaseType()
		return v.typ.SQLLiteral(ctx, v)
	}
	if d.IsArray() || d.IsEnum() {
		var text string
		var err error
		if d.IsArray() {
			text, err = d.arrayOutput(ctx, v, OutputEnvironment{})
		} else {
			text, err = d.enumOutput(v)
		}
		if err != nil {
			return "", err
		}
		return "'" + strings.ReplaceAll(text, "'", "''") + "'", nil
	}
	switch d.id {
	case Bytea:
		return byteaHex(v.text, true), nil
	case UUID:
		return "'" + uuidOutput(v.uuid) + "'", nil
	case Date:
		return "'" + dateOutput(v.integer) + "'", nil
	case Time:
		return "'" + timeOutput(v.integer) + "'", nil
	case Timetz:
		return timetzSQLLiteral(v), nil
	case Timestamptz:
		if v.integer == timestampEnd {
			return "TIMESTAMPTZ(0) '294276-12-31 23:59:59.999999+00'", nil
		}
		return "'" + timestamptzOutput(v.integer, OutputEnvironment{}) + "'", nil
	case Interval:
		return "INTERVAL '" + intervalCanonical(v) + "'", nil
	case Timestamp:
		if v.integer == timestampEnd {
			return "TIMESTAMP(0) '294276-12-31 23:59:59.999999'", nil
		}
		return "'" + timestampOutput(v.integer) + "'", nil
	case Char:
		text, err := d.Output(ctx, v)
		if err != nil {
			return "", err
		}
		return "'" + strings.ReplaceAll(text, "'", "''") + "'", nil
	case Text, BPChar, VarChar, Name, Regclass, JSON:
		return "'" + strings.ReplaceAll(v.text, "'", "''") + "'", nil
	case Oid, Float4, Float8, Numeric:
		text, err := d.Output(ctx, v)
		if err != nil {
			return "", err
		}
		return "'" + text + "'", nil
	case Bool:
		if v.boolean {
			return "TRUE", nil
		}
		return "FALSE", nil
	}
	return d.Output(ctx, v)
}

var _ Codec = (*Descriptor)(nil)

type CoercionContext uint8

const (
	Implicit CoercionContext = iota
	Assignment
	Explicit
)

type coercionMethod uint8

const (
	identity coercionMethod = iota
	integerConversion
	viaIO
	booleanText
	floatConversion
	numericConversion
	integerBoolean
	booleanInteger
	floatIntegerConversion
	stringConversion
	nameConversion
	dateTimestampConversion
	oidConversion
	datetimeConversion
	charTextConversion
)

type CoercionPlan struct {
	source, target               Usage
	method                       coercionMethod
	context                      CoercionContext
	logicalSource, logicalTarget *Descriptor
}

type castSpec struct {
	source, target ID
	minimum        CoercionContext
	method         coercionMethod
}

// Fixed provider definitions are read-only; identity/ownership comes from the
// source and target catalog descriptors retained by each bound plan.
var baselineCasts = [...]castSpec{
	{Int2, Oid, Implicit, oidConversion},
	{Int4, Oid, Implicit, oidConversion},
	{Int8, Oid, Implicit, oidConversion},
	{Regclass, Oid, Implicit, oidConversion},
	{Oid, Int4, Assignment, oidConversion},
	{Oid, Int8, Assignment, oidConversion},
	{Date, Timestamp, Implicit, dateTimestampConversion},
	{Date, Timestamptz, Implicit, datetimeConversion},
	{Time, Timetz, Implicit, datetimeConversion},
	{Timestamp, Date, Assignment, datetimeConversion},
	{Timestamp, Time, Assignment, datetimeConversion},
	{Timestamp, Timestamptz, Implicit, datetimeConversion},
	{Timestamptz, Date, Assignment, datetimeConversion},
	{Timestamptz, Time, Assignment, datetimeConversion},
	{Timestamptz, Timestamp, Assignment, datetimeConversion},
	{Timestamptz, Timetz, Assignment, datetimeConversion},
	{Timetz, Time, Assignment, datetimeConversion},
	{Name, Text, Implicit, stringConversion},
	{Name, BPChar, Assignment, stringConversion},
	{Name, VarChar, Assignment, stringConversion},
	{Text, Name, Implicit, nameConversion},
	{BPChar, Name, Implicit, nameConversion},
	{VarChar, Name, Implicit, nameConversion},
	{Int2, Int4, Implicit, integerConversion},
	{Int2, Int8, Implicit, integerConversion},
	{Int4, Int2, Assignment, integerConversion},
	{Int8, Int2, Assignment, integerConversion},
	{Int2, Float4, Implicit, floatConversion},
	{Int2, Float8, Implicit, floatConversion},
	{Int4, Int8, Implicit, integerConversion},
	{Int8, Int4, Assignment, integerConversion},
	{Int4, Float4, Implicit, floatConversion},
	{Int4, Float8, Implicit, floatConversion},
	{Int8, Float4, Implicit, floatConversion},
	{Int8, Float8, Implicit, floatConversion},
	{Float4, Float8, Implicit, floatConversion},
	{Float8, Float4, Assignment, floatConversion},
	{Float4, Int2, Assignment, floatIntegerConversion},
	{Float4, Int4, Assignment, floatIntegerConversion},
	{Float4, Int8, Assignment, floatIntegerConversion},
	{Float8, Int2, Assignment, floatIntegerConversion},
	{Float8, Int4, Assignment, floatIntegerConversion},
	{Float8, Int8, Assignment, floatIntegerConversion},
	{Float4, Text, Assignment, viaIO},
	{Float8, Text, Assignment, viaIO},
	{Int4, Numeric, Implicit, numericConversion},
	{Int8, Numeric, Implicit, numericConversion},
	{Int2, Numeric, Implicit, numericConversion},
	{Numeric, Int2, Assignment, numericConversion},
	{Numeric, Int4, Assignment, numericConversion},
	{Numeric, Int8, Assignment, numericConversion},
	{Numeric, Float4, Implicit, numericConversion},
	{Numeric, Float8, Implicit, numericConversion},
	{Float4, Numeric, Assignment, numericConversion},
	{Float8, Numeric, Assignment, numericConversion},
}

func BaselineCastType(id ID) bool {
	return id == Int4 || id == Int8 || id == Text || id == Bool
}

// StoredScalarCastType is the concrete annotation surface of stored expressions.
// Admission of a type does not add conversion edges to PlanCoercion.
func StoredScalarCastType(id ID) bool {
	switch id {
	case Bool, Int2, Int4, Int8, Oid, Text, Name, BPChar, VarChar, Bytea, UUID,
		Numeric, Float4, Float8, Date, Time, Timetz, Timestamp, Timestamptz, Interval, JSON:
		return true
	}
	return false
}

func StoredNumericCastPair(source, target ID) bool {
	numeric := func(id ID) bool {
		return id == Int2 || id == Int4 || id == Int8 || id == Numeric || id == Float4 || id == Float8
	}
	if !numeric(source) || !numeric(target) {
		return false
	}
	for _, spec := range baselineCasts {
		if spec.source == source && spec.target == target {
			return true
		}
	}
	return false
}

func TemporalCastType(id ID) bool {
	return id == Date || id == Time || id == Timestamp || id == Timestamptz || id == Timetz
}

func TemporalCastPair(source, target ID) bool {
	return TemporalCastType(source) && (TemporalCastType(target) || target == Text) ||
		source == Text && TemporalCastType(target)
}

// OIDCastPair admits only OID-related explicit syntax, including unsupported
// integer directions so the binder can report the normal cannot-cast diagnostic.
func OIDCastPair(source, target ID) bool {
	integer := func(id ID) bool { return id == Int2 || id == Int4 || id == Int8 }
	return source == Oid && (target == Oid || target == Text || integer(target)) ||
		target == Oid && (source == Text || integer(source))
}

// PlanCoercion admits only bounded directional edges, never transitive casts.
func PlanCoercion(source, target Usage, context CoercionContext) (CoercionPlan, error) {
	if !source.Valid() || !target.Valid() {
		return CoercionPlan{}, fmt.Errorf("invalid coercion usage")
	}
	if source.Type.IsDomain() || target.Type.IsDomain() {
		from, to := source.BaseUsage(), target.BaseUsage()
		p, err := PlanCoercion(from, to, context)
		if err != nil && context == Explicit && (source.Type.IsDomain() && to.Type.id == Text || target.Type.IsDomain() && from.Type.id == Text) {
			p = CoercionPlan{source: from, target: to, context: context, method: viaIO}
			err = nil
		}
		p.logicalSource, p.logicalTarget = source.Type, target.Type
		return p, err
	}
	p := CoercionPlan{source: source, target: target, context: context}
	if source.Type.IsEnum() || target.Type.IsEnum() {
		if source.Type == target.Type {
			return p, nil
		}
		if context == Explicit && (source.Type.id == Text || target.Type.id == Text) {
			p.method = viaIO
			return p, nil
		}
		if context == Assignment && source.Type.IsEnum() && IsCharacter(target.Type.id) {
			p.method = viaIO
			return p, nil
		}
		return p, failure("42804", "incompatible source and destination types")
	}
	if source.Type.IsArray() || target.Type.IsArray() {
		if source.Type == target.Type {
			return p, nil
		}
		if context == Explicit && (source.Type.id == Text || target.Type.id == Text) {
			p.method = viaIO
			return p, nil
		}
		return p, failure("42804", "incompatible source and destination types")
	}
	if source.Type.id == JSON || target.Type.id == JSON {
		if source.Type == target.Type {
			return p, nil
		}
		if context == Explicit && (source.Type.id == Text || target.Type.id == Text) {
			p.method = viaIO
			return p, nil
		}
		return p, failure("42804", "incompatible source and destination types")
	}
	if source.Type.id == PgNodeTree || target.Type.id == PgNodeTree {
		if source.Type.id == target.Type.id {
			return p, nil
		}
		if context == Explicit && (source.Type.id == Text || target.Type.id == Text) {
			p.method = viaIO
			return p, nil
		}
		return p, failure("42804", "incompatible source and destination types")
	}
	if context == Explicit {
		if !(source.Type == target.Type && StoredScalarCastType(source.Type.id)) && !StoredNumericCastPair(source.Type.id, target.Type.id) && !(BaselineCastType(source.Type.id) && BaselineCastType(target.Type.id)) && !(IsCharacter(source.Type.id) && IsCharacter(target.Type.id)) && !OIDCastPair(source.Type.id, target.Type.id) && !TemporalCastPair(source.Type.id, target.Type.id) {
			return p, failure("0A000", "explicit casts are supported only for int4, int8, text and bool")
		}
		switch {
		case source.Type.id == Text && !IsCharacter(target.Type.id):
			p.method = viaIO
			return p, nil
		case source.Type.id == Int4 && target.Type.id == Bool:
			p.method = integerBoolean
			return p, nil
		case source.Type.id == Bool && target.Type.id == Int4:
			p.method = booleanInteger
			return p, nil
		}
	}
	if source.Type == target.Type {
		return p, nil
	}
	if target.Type.id == Name && IsCharacter(source.Type.id) {
		p.method = nameConversion
		return p, nil
	}
	if source.Type.id == Name && (target.Type.id == Text || context == Assignment && IsCharacter(target.Type.id)) {
		p.method = stringConversion
		return p, nil
	}
	if context == Assignment && target.Type.id == Name {
		p.method = viaIO
		return p, nil
	}
	if IsCharacter(source.Type.id) && IsCharacter(target.Type.id) {
		p.method = stringConversion
		return p, nil
	}
	if context == Assignment && IsCharacter(target.Type.id) || context == Explicit && target.Type.id == Text && (BaselineCastType(source.Type.id) || source.Type.id == Oid || TemporalCastType(source.Type.id)) {
		p.method = viaIO
		if source.Type.id == Bool {
			p.method = booleanText
		}
		return p, nil
	}
	for _, spec := range baselineCasts {
		if spec.source == source.Type.id && spec.target == target.Type.id && context >= spec.minimum {
			p.method = spec.method
			return p, nil
		}
	}
	return p, failure("42804", "incompatible source and destination types")
}
func (p CoercionPlan) applyBase(ctx context.Context, v Value, input InputEnvironment, output OutputEnvironment) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if v.typ != p.source.Type || !p.target.Type.Concrete() {
		return Value{}, fmt.Errorf("foreign coercion value")
	}
	if v.null {
		return Null(p.target.Type), nil
	}
	switch p.method {
	case charTextConversion:
		text, err := p.source.Type.ConcatOutput(ctx, v, output)
		if err != nil {
			return Value{}, err
		}
		return String(p.target.Type, text), nil
	case oidConversion:
		return oidCoercion(v, p.target.Type)
	case datetimeConversion:
		return temporalConversion(v, p.target, input)
	case dateTimestampConversion:
		if v.integer == DateNegativeInfinity || v.integer == DatePositiveInfinity {
			return TimestampValue(p.target.Type, v.integer)
		}
		if v.integer < timestampMinimum/microsPerDay || v.integer >= timestampEnd/microsPerDay {
			return Value{}, failure("22008", "date out of range for timestamp")
		}
		return TimestampValue(p.target.Type, v.integer*microsPerDay)
	case identity:
		if p.target.Type.IsArray() {
			return p.sizeArray(ctx, v, input, output)
		}
		if p.target.Type.id == Interval {
			return adjustInterval(v, p.target.Typmod)
		}
		if IsCharacter(p.target.Type.id) {
			return sizeCharacter(v, p.target.Typmod, p.context == Explicit)
		}
		if p.target.Type.id == Numeric {
			return sizeNumeric(v, p.target.Typmod)
		}
		if p.target.Type.id == Timestamp || p.target.Type.id == Time || p.target.Type.id == Timestamptz || p.target.Type.id == Timetz {
			v.integer = roundTimestamp(v.integer, p.target.Typmod)
		}
		return v, nil
	case nameConversion:
		text := clipName(v.text)
		if v.typ.id == BPChar {
			text = strings.TrimRight(text, " ")
		}
		return String(p.target.Type, text), nil
	case stringConversion:
		if v.typ.id == BPChar && p.target.Type.id != BPChar {
			v.text = strings.TrimRight(v.text, " ")
		}
		v.typ = p.target.Type
		return sizeCharacter(v, p.target.Typmod, p.context == Explicit)
	case numericConversion:
		return numericCoercion(ctx, v, p.target)
	case integerConversion:
		return Integer(p.target.Type, v.integer)
	case integerBoolean:
		return Boolean(p.target.Type, v.integer != 0), nil
	case booleanInteger:
		var n int64
		if v.boolean {
			n = 1
		}
		return Integer(p.target.Type, n)
	case floatIntegerConversion:
		n := math.RoundToEven(v.floating)
		limit, name := float64(2147483648), "integer"
		switch p.target.Type.id {
		case Int2:
			limit, name = 32768, "smallint"
		case Int8:
			limit, name = 9223372036854775808, "bigint"
		}
		// Check the rounded float before converting: +2^63 cannot be represented
		// by int64, while the exactly represented -2^63 is a valid minimum.
		if math.IsNaN(n) || n < -limit || n >= limit {
			return Value{}, failure("22003", name+" out of range")
		}
		return Integer(p.target.Type, int64(n))
	case floatConversion:
		n := v.floating
		if v.typ.id == Int2 || v.typ.id == Int4 || v.typ.id == Int8 {
			n = float64(v.integer)
			if p.target.Type.id == Float4 {
				n = float64(float32(v.integer))
			}
		}
		return Float(p.target.Type, n)
	case viaIO:
		text, err := p.source.Type.OutputWithEnvironment(ctx, v, output)
		if err != nil {
			return Value{}, err
		}
		return p.target.Type.InputWithEnvironment(ctx, text, p.target.Typmod, input)
	case booleanText:
		// Boolean string conversion uses true/false rather than boolout's t/f.
		return sizeCharacter(String(p.target.Type, strconv.FormatBool(v.boolean)), p.target.Typmod, false)
	}
	return Value{}, fmt.Errorf("invalid coercion plan")
}

type Comparison struct{ left, right *Descriptor }

// BindOrdering validates the actual builtin provider and bounded C collation
// before any rows are read, including empty and all-NULL inputs.
func BindOrdering(u Usage) (Comparison, error) {
	if !u.Type.Concrete() {
		return Comparison{}, fmt.Errorf("invalid ordering usage")
	}
	if !u.validCollation() {
		return Comparison{}, fmt.Errorf("invalid ordering collation")
	}
	if u.Type.BaseType().IsArray() || u.Type.BaseType().id == JSON {
		return Comparison{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "could not identify an ordering operator for type " + u.Type.BaseType().DiagnosticName(), Hint: "Use an explicit ordering operator or modify the query."}
	}
	if u.Type.BaseType().IsEnum() {
		// Ordering uses the base enum provider, not polymorphic scalar operator
		// eligibility. Retain the domain identity for Compare's datum checks.
		return Comparison{u.Type, u.Type}, nil
	}
	switch u.Type.BaseType().id {
	case Bool, Bytea, Char, Name, Int2, Int4, Int8, Text, Oid, BPChar, VarChar, Float4, Float8, Date, Time, Timestamp, Timestamptz, Timetz, Interval, Numeric, UUID, Regclass:
	case Void:
		message := "could not identify an ordering operator for type void"
		return Comparison{}, &sqlstate.Error{
			Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883",
			Message: message,
			Hint:    "Use an explicit ordering operator or modify the query.",
		}
	default:
		return Comparison{}, fmt.Errorf("invalid ordering type")
	}
	return BindComparison(u.Type, u.Type)
}

func (p Comparison) SessionDependent() bool {
	return p.left != nil && p.right != nil && p.left.BaseType().id != p.right.BaseType().id && (p.left.BaseType().id == Timestamptz || p.right.BaseType().id == Timestamptz)
}

func BindComparison(left, right *Descriptor) (Comparison, error) {
	p := Comparison{left, right}
	if !left.Concrete() || !right.Concrete() {
		return p, fmt.Errorf("invalid comparison usage")
	}
	// Enum operators are polymorphic over actual enum types. Domains are not
	// enum inputs, even when both operands name the same domain over an enum.
	if left.BaseType().IsEnum() || right.BaseType().IsEnum() {
		if !left.IsEnum() || !right.IsEnum() || left != right {
			return p, failure("42883", "incompatible comparison operands")
		}
		return p, nil
	}
	if left.IsDomain() || right.IsDomain() {
		_, err := BindComparison(left.BaseType(), right.BaseType())
		return p, err
	}
	if left.IsArray() || right.IsArray() || left.id == JSON || right.id == JSON {
		return p, failure("42883", "incompatible comparison operands")
	}
	if dateTimestampType(left.id) && dateTimestampType(right.id) {
		return p, nil
	}
	if IsCharacter(left.id) && IsCharacter(right.id) {
		return p, nil
	}
	if left.id == Name && right.id == Text || left.id == Text && right.id == Name {
		return p, nil
	}
	if exactNumber(left.id) && exactNumber(right.id) {
		return p, nil
	}
	if (isFloat(left.id) && numericFloatOperand(right.id)) || (isFloat(right.id) && numericFloatOperand(left.id)) {
		return p, nil
	}
	if left == right || (left.id == Int2 || left.id == Int4 || left.id == Int8) && (right.id == Int2 || right.id == Int4 || right.id == Int8) {
		return p, nil
	}
	return p, failure("42883", "incompatible comparison operands")
}
func (p Comparison) Compare(ctx context.Context, a, b Value, input InputEnvironment) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if a.typ != p.left || b.typ != p.right || a.null || b.null || !p.left.Concrete() {
		return 0, fmt.Errorf("invalid comparison values")
	}
	a.typ, b.typ = p.left.BaseType(), p.right.BaseType()
	p.left, p.right = a.typ, b.typ
	if p.left.IsEnum() {
		if _, err := p.left.enumOutput(a); err != nil {
			return 0, err
		}
		if _, err := p.right.enumOutput(b); err != nil {
			return 0, err
		}
		if a.integer < b.integer {
			return -1, nil
		}
		if a.integer > b.integer {
			return 1, nil
		}
		return 0, nil
	}
	if p.left.id != p.right.id && dateTimestampType(p.left.id) && dateTimestampType(p.right.id) {
		return compareDatetime(a, b, input), nil
	}
	if IsCharacter(p.left.id) && IsCharacter(p.right.id) {
		left, right := a.text, b.text
		if p.left.id == BPChar {
			left = strings.TrimRight(left, " ")
		}
		if p.right.id == BPChar {
			right = strings.TrimRight(right, " ")
		}
		if p.left.id == BPChar && p.right.id == VarChar || p.right.id == BPChar && p.left.id == VarChar {
			left, right = strings.TrimRight(left, " "), strings.TrimRight(right, " ")
		}
		return strings.Compare(left, right), nil
	}
	if p.left.id == Name && p.right.id == Text || p.left.id == Text && p.right.id == Name {
		return strings.Compare(a.text, b.text), nil
	}
	if isFloat(p.left.id) || isFloat(p.right.id) {
		left, err := checkedFloatOperand(ctx, a)
		if err != nil {
			return 0, err
		}
		right, err := checkedFloatOperand(ctx, b)
		if err != nil {
			return 0, err
		}
		return compareFloats(left, right), nil
	}
	if p.left.id == Numeric || p.right.id == Numeric {
		return compareNumeric(a, b), nil
	}
	if p.left.id == Timetz {
		ak, bk := a.integer+int64(a.west)*1000000, b.integer+int64(b.west)*1000000
		if ak < bk {
			return -1, nil
		}
		if ak > bk {
			return 1, nil
		}
		if a.west < b.west {
			return -1, nil
		}
		if a.west > b.west {
			return 1, nil
		}
		return 0, nil
	}
	switch p.left.id {
	case Regclass:
		if a.integer != 1 || b.integer != 1 {
			return 0, fmt.Errorf("invalid regclass comparison value")
		}
		if a.oid < b.oid {
			return -1, nil
		}
		if a.oid > b.oid {
			return 1, nil
		}
	case Interval:
		return compareIntervals(a, b), nil
	case Oid:
		if a.oid < b.oid {
			return -1, nil
		}
		if a.oid > b.oid {
			return 1, nil
		}
	case UUID:
		for i, x := range a.uuid {
			if x < b.uuid[i] {
				return -1, nil
			}
			if x > b.uuid[i] {
				return 1, nil
			}
		}
	case Int2, Int4, Int8, Timestamp, Timestamptz, Time, Date:
		if a.integer < b.integer {
			return -1, nil
		}
		if a.integer > b.integer {
			return 1, nil
		}
	case Bytea, Name, Char:
		return strings.Compare(a.text, b.text), nil
	case Bool:
		if !a.boolean && b.boolean {
			return -1, nil
		}
		if a.boolean && !b.boolean {
			return 1, nil
		}
	}
	return 0, nil
}

// ComparisonPredicate is a strict SQL predicate over the nonnull ordering
// provider. NULL policy belongs here, not in Comparison or its sorting callers.
type ComparisonOperator uint8

const (
	CompareEqual ComparisonOperator = iota
	CompareNotEqual
	CompareLess
	CompareLessEqual
	CompareGreater
	CompareGreaterEqual
)

type ComparisonPredicate struct {
	operands Comparison
	result   *Descriptor
	operator ComparisonOperator
}

func (p ComparisonPredicate) SessionDependent() bool { return p.operands.SessionDependent() }

func BindComparisonPredicate(operator ComparisonOperator, left, right, result *Descriptor) (ComparisonPredicate, error) {
	if operator > CompareGreaterEqual || result == nil || result.id != Bool || !result.Concrete() {
		return ComparisonPredicate{}, fmt.Errorf("invalid comparison predicate")
	}
	operands, err := BindComparison(left, right)
	if err != nil {
		return ComparisonPredicate{}, err
	}
	return ComparisonPredicate{operands: operands, result: result, operator: operator}, nil
}

func (p ComparisonPredicate) Evaluate(ctx context.Context, a, b Value, input InputEnvironment) (Value, error) {
	if p.operator > CompareGreaterEqual || a.typ != p.operands.left || b.typ != p.operands.right || !p.operands.left.Concrete() || !p.operands.right.Concrete() || !p.result.Concrete() || p.result.id != Bool {
		return Value{}, fmt.Errorf("invalid comparison predicate values")
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if a.null || b.null {
		return Null(p.result), nil
	}
	sign, err := p.operands.Compare(ctx, a, b, input)
	if err != nil {
		return Value{}, err
	}
	var truth bool
	switch p.operator {
	case CompareEqual:
		truth = sign == 0
	case CompareNotEqual:
		truth = sign != 0
	case CompareLess:
		truth = sign < 0
	case CompareLessEqual:
		truth = sign <= 0
	case CompareGreater:
		truth = sign > 0
	case CompareGreaterEqual:
		truth = sign >= 0
	}
	return Boolean(p.result, truth), nil
}
