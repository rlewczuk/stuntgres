// Package sqltype owns concrete type identity, immutable values and the bounded
// input, assignment and comparison providers used by the SQL engine.
package sqltype

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"stuntgres/internal/sqlstate"
)

type ID uint32

const (
	Bool      ID = 16
	Name      ID = 19 // Result identity only; not admitted as a stored column type.
	Int8      ID = 20
	Int2      ID = 21
	Int4      ID = 23
	Text      ID = 25
	Float4    ID = 700
	Float8    ID = 701
	Unknown   ID = 705
	Time      ID = 1083
	Timestamp ID = 1114
	Numeric   ID = 1700
	UUID      ID = 2950
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
}

func (d *Descriptor) ID() ID          { return d.id }
func (d *Descriptor) Name() string    { return d.name }
func (d *Descriptor) SQLName() string { return d.sql }
func (d *Descriptor) DiagnosticName() string {
	if d.id == Timestamp {
		return "timestamp without time zone"
	}
	if d.id == Time {
		return "time without time zone"
	}
	return strings.ToLower(d.sql)
}
func (d *Descriptor) DatabaseName() string { return d.database }
func (d *Descriptor) Concrete() bool       { return d != nil && d.id != 0 && d.id != Unknown }
func (d *Descriptor) Usage() Usage {
	u := Usage{Type: d, Typmod: -1}
	if d.id == Text {
		u.Collation = CollationRef{ID: 100, Derivation: Default}
	}
	return u
}

// Builtins returns a new immutable generation, not a shared mutable registry.
func Builtins() []*Descriptor {
	return []*Descriptor{
		{Bool, "bool", "BOOLEAN", "BOOL"},
		{Name, "name", "NAME", "NAME"},
		{Int8, "int8", "BIGINT", "INT8"},
		{Int2, "int2", "SMALLINT", "INT2"},
		{Int4, "int4", "INTEGER", "INT4"},
		{Text, "text", "TEXT", "TEXT"},
		{Float4, "float4", "REAL", "FLOAT4"},
		{Float8, "float8", "DOUBLE PRECISION", "FLOAT8"},
		{Time, "time", "TIME", "TIME"},
		{Timestamp, "timestamp", "TIMESTAMP", "TIMESTAMP"},
		{Numeric, "numeric", "NUMERIC", "NUMERIC"},
		{UUID, "uuid", "UUID", "UUID"},
		{Unknown, "unknown", "", ""},
	}
}

// Zero Value is invalid, not NULL. All payloads are inline or immutable strings.
type Value struct {
	typ             *Descriptor
	null            bool
	integer         int64
	floating        float64
	boolean         bool
	text            string
	exponent, scale int64
	negative        bool
	special         uint8
	uuid            [16]byte
}

func (v Value) Type() *Descriptor { return v.typ }
func (v Value) IsNull() bool      { return v.null }
func Null(d *Descriptor) Value {
	if !d.Concrete() {
		panic("invalid NULL descriptor")
	}
	return Value{typ: d, null: true}
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
	if d == nil || (d.id != Text && d.id != Name) {
		panic("invalid string descriptor")
	}
	return Value{typ: d, text: s}
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
	if v.null || v.typ == nil || (v.typ.id != Text && v.typ.id != Name) {
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
	if !d.Concrete() || !d.validTypmod(typmod) {
		return Value{}, fmt.Errorf("invalid input usage")
	}
	if !utf8.ValidString(text) || strings.IndexByte(text, 0) >= 0 {
		return Value{}, failure("22P02", "invalid textual input")
	}
	switch d.id {
	case UUID:
		return d.uuidInput(text)
	case Time:
		return d.timeInput(text, typmod, env)
	case Numeric:
		return d.numericInput(text, typmod)
	case Timestamp:
		return d.timestampInput(text, typmod, env)
	case Text:
		return String(d, text), nil
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
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if v.typ != d || v.null {
		return "", fmt.Errorf("invalid output value")
	}
	switch d.id {
	case UUID:
		return uuidOutput(v.uuid), nil
	case Time:
		return timeOutput(v.integer), nil
	case Numeric:
		return numericOutput(v), nil
	case Timestamp:
		return timestampOutput(v.integer), nil
	case Int2, Int4, Int8:
		return strconv.FormatInt(v.integer, 10), nil
	case Float4, Float8:
		return floatOutput(v.floating, d.id), nil
	case Text, Name:
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
	switch d.id {
	case UUID:
		return "'" + uuidOutput(v.uuid) + "'", nil
	case Time:
		return "'" + timeOutput(v.integer) + "'", nil
	case Timestamp:
		if v.integer == timestampEnd {
			return "TIMESTAMP(0) '294276-12-31 23:59:59.999999'", nil
		}
		return "'" + timestampOutput(v.integer) + "'", nil
	case Text, Name:
		return "'" + strings.ReplaceAll(v.text, "'", "''") + "'", nil
	case Float4, Float8, Numeric:
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
)

type coercionMethod uint8

const (
	identity coercionMethod = iota
	integerConversion
	viaIO
	booleanText
	floatConversion
	numericConversion
)

type CoercionPlan struct {
	source, target Usage
	method         coercionMethod
}

type castSpec struct {
	source, target ID
	minimum        CoercionContext
	method         coercionMethod
}

// Fixed provider definitions are read-only; identity/ownership comes from the
// source and target catalog descriptors retained by each bound plan.
var baselineCasts = [...]castSpec{
	{Int2, Int4, Implicit, integerConversion},
	{Int2, Int8, Implicit, integerConversion},
	{Int4, Int2, Assignment, integerConversion},
	{Int8, Int2, Assignment, integerConversion},
	{Int2, Text, Assignment, viaIO},
	{Int2, Float4, Implicit, floatConversion},
	{Int2, Float8, Implicit, floatConversion},
	{Int4, Int8, Implicit, integerConversion},
	{Int8, Int4, Assignment, integerConversion},
	{Int4, Text, Assignment, viaIO},
	{Int8, Text, Assignment, viaIO},
	{Bool, Text, Assignment, booleanText},
	{Int4, Float4, Implicit, floatConversion},
	{Int4, Float8, Implicit, floatConversion},
	{Int8, Float4, Implicit, floatConversion},
	{Int8, Float8, Implicit, floatConversion},
	{Float4, Float8, Implicit, floatConversion},
	{Float8, Float4, Assignment, floatConversion},
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
	{Numeric, Text, Assignment, viaIO},
}

// PlanCoercion admits only bounded directional edges, never transitive casts.
func PlanCoercion(source, target Usage, context CoercionContext) (CoercionPlan, error) {
	p := CoercionPlan{source: source, target: target}
	if !source.Valid() || !target.Valid() {
		return p, fmt.Errorf("invalid coercion usage")
	}
	if source.Type == target.Type {
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
func (p CoercionPlan) Apply(ctx context.Context, v Value) (Value, error) {
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
	case identity:
		if p.target.Type.id == Numeric {
			return sizeNumeric(v, p.target.Typmod)
		}
		if p.target.Type.id == Timestamp || p.target.Type.id == Time {
			v.integer = roundTimestamp(v.integer, p.target.Typmod)
		}
		return v, nil
	case numericConversion:
		return numericCoercion(ctx, v, p.target)
	case integerConversion:
		return Integer(p.target.Type, v.integer)
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
		text, err := p.source.Type.Output(ctx, v)
		if err != nil {
			return Value{}, err
		}
		return p.target.Type.Input(ctx, text, -1)
	case booleanText:
		// PostgreSQL's registered bool-to-text function differs from boolout.
		return String(p.target.Type, strconv.FormatBool(v.boolean)), nil
	}
	return Value{}, fmt.Errorf("invalid coercion plan")
}

type Comparison struct{ left, right *Descriptor }

func BindComparison(left, right *Descriptor) (Comparison, error) {
	p := Comparison{left, right}
	if !left.Concrete() || !right.Concrete() {
		return p, fmt.Errorf("invalid comparison usage")
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
func (p Comparison) Compare(ctx context.Context, a, b Value) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if a.typ != p.left || b.typ != p.right || a.null || b.null || !p.left.Concrete() {
		return 0, fmt.Errorf("invalid comparison values")
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
	switch p.left.id {
	case UUID:
		for i, x := range a.uuid {
			if x < b.uuid[i] {
				return -1, nil
			}
			if x > b.uuid[i] {
				return 1, nil
			}
		}
	case Int2, Int4, Int8, Timestamp, Time:
		if a.integer < b.integer {
			return -1, nil
		}
		if a.integer > b.integer {
			return 1, nil
		}
	case Text, Name:
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

func (p ComparisonPredicate) Evaluate(ctx context.Context, a, b Value) (Value, error) {
	if p.operator > CompareGreaterEqual || a.typ != p.operands.left || b.typ != p.operands.right || !p.operands.left.Concrete() || !p.operands.right.Concrete() || !p.result.Concrete() || p.result.id != Bool {
		return Value{}, fmt.Errorf("invalid comparison predicate values")
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if a.null || b.null {
		return Null(p.result), nil
	}
	sign, err := p.operands.Compare(ctx, a, b)
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
