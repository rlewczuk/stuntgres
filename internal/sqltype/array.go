package sqltype

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"stuntgres/internal/sqlstate"
)

// Array dimensions describe a rectangular flat payload, not nested Go arrays.
type ArrayDimension struct{ Lower, Length int32 }
type arrayPayload struct {
	dimensions []ArrayDimension
	elements   []Value
}

func (d *Descriptor) IsArray() bool        { return d != nil && d.element != nil }
func (d *Descriptor) Element() *Descriptor { return d.element }
func (d *Descriptor) AssociatedArray() ID  { return d.associatedArray }
func (d *Descriptor) Delimiter() byte {
	if d.IsDomain() {
		return d.base.Type.Delimiter()
	}
	if d.delimiter != 0 {
		return d.delimiter
	}
	return ','
}
func (d *Descriptor) ContainsRegclass() bool {
	if d == nil {
		return false
	}
	physical := d.BaseType()
	if physical.IsArray() {
		return physical.element.ContainsRegclass()
	}
	if physical.IsComposite() {
		for _, member := range physical.composite.members {
			if member.Usage.Type.ContainsRegclass() {
				return true
			}
		}
	}
	return physical.ID() == Regclass
}

func Array(id ID, name string, element *Descriptor) (*Descriptor, error) {
	if id == 0 || id == Unknown || name == "" || !element.Concrete() || IsOperatorAlias(element.BaseType().ID()) || IsRegTS(element.BaseType().ID()) || IsNetwork(element.BaseType().ID()) || IsTransactionIdentifier(element.BaseType().ID()) || !element.IsDomain() && !element.IsEnum() && !element.IsComposite() && !StoredScalarCastType(element.ID()) && element.ID() != Regclass {
		return nil, fmt.Errorf("invalid array descriptor")
	}
	return &Descriptor{id: id, name: name, sql: element.SQLName() + "[]", database: strings.ToUpper(name), element: element}, nil
}

// ArrayValue freezes externally owned slices. Internal codec/coercion builders
// already own their buffers and use the same checked constructor without copying.
func ArrayValue(d *Descriptor, dimensions []ArrayDimension, elements []Value) (Value, error) {
	value, err := ownedArray(d, dimensions, elements)
	if err != nil {
		return Value{}, err
	}
	value.array.dimensions = slices.Clone(dimensions)
	value.array.elements = slices.Clone(elements)
	return value, nil
}

// OwnedArrayValue transfers exclusive ownership of both buffers to the value.
// Callers must not retain mutable aliases. Validation matches ArrayValue.
func OwnedArrayValue(d *Descriptor, dimensions []ArrayDimension, elements []Value) (Value, error) {
	return ownedArray(d, dimensions, elements)
}
func ownedArray(d *Descriptor, dimensions []ArrayDimension, elements []Value) (Value, error) {
	if !d.Concrete() || !d.IsArray() || len(dimensions) > 6 {
		return Value{}, fmt.Errorf("invalid array payload descriptor or rank")
	}
	product := int64(1)
	for _, dim := range dimensions {
		if dim.Length <= 0 || int64(dim.Lower)+int64(dim.Length)-1 >= math.MaxInt32 {
			return Value{}, fmt.Errorf("invalid array dimension")
		}
		if product > int64(math.MaxInt32)/int64(dim.Length) {
			return Value{}, failure("54000", "array size exceeds the maximum allowed")
		}
		product *= int64(dim.Length)
	}
	if len(dimensions) == 0 {
		product = 0
	}
	if product != int64(len(elements)) {
		return Value{}, fmt.Errorf("array shape does not match elements")
	}
	for _, element := range elements {
		if element.typ != d.element {
			return Value{}, fmt.Errorf("foreign array element generation")
		}
	}
	return Value{typ: d, array: &arrayPayload{dimensions: dimensions, elements: elements}}, nil
}
func (v Value) ArrayLen() int {
	if v.null || v.array == nil {
		return 0
	}
	return len(v.array.elements)
}
func (v Value) ArrayElement(i int) Value { return v.array.elements[i] }
func (v Value) ArrayDimensions() []ArrayDimension {
	if v.array == nil {
		return nil
	}
	return slices.Clone(v.array.dimensions)
}

// ArrayRank and ArrayDimension inspect immutable dimensions without cloning.
// ArrayDimension requires an index in [0, ArrayRank()), like ArrayElement.
func (v Value) ArrayRank() int {
	if v.array == nil {
		return 0
	}
	return len(v.array.dimensions)
}
func (v Value) ArrayDimension(i int) ArrayDimension { return v.array.dimensions[i] }

// MapArray retains the original immutable payload if every element/descriptor is
// unchanged, otherwise owns one new element buffer. The callback is internal
// typed transformation, never a driver/user callback.
func (v Value) MapArray(d *Descriptor, transform func(Value, *Descriptor) (Value, error)) (Value, error) {
	if !v.typ.Concrete() || !v.typ.BaseType().IsArray() || !d.Concrete() || !d.BaseType().IsArray() {
		return Value{}, fmt.Errorf("invalid array mapping")
	}
	if v.null {
		return Null(d), nil
	}
	if v.array == nil {
		return Value{}, fmt.Errorf("missing array payload")
	}
	var elements []Value
	for i, old := range v.array.elements {
		next, err := transform(old, d.BaseType().element)
		if err != nil {
			return Value{}, err
		}
		if next.typ != d.BaseType().element {
			return Value{}, fmt.Errorf("invalid mapped element generation")
		}
		if next != old && (next.typ != old.typ || !SameDatum(next, old)) && elements == nil {
			elements = slices.Clone(v.array.elements)
		}
		if elements != nil {
			elements[i] = next
		}
	}
	if elements == nil {
		v.typ = d
		return v, nil
	}
	result, err := ownedArray(d.BaseType(), v.array.dimensions, elements)
	if err != nil {
		return Value{}, err
	}
	result.typ = d
	return result, nil
}

func (p CoercionPlan) sizeArray(ctx context.Context, v Value, input InputEnvironment, output OutputEnvironment) (Value, error) {
	if p.target.Typmod < 0 && p.source.Type == p.target.Type {
		return v, nil
	}
	source, target := p.source.Type.element.Usage(), p.target.Type.element.Usage()
	source.Typmod = p.source.Typmod
	target.Typmod = p.target.Typmod
	source.Collation = p.source.Collation
	target.Collation = p.target.Collation
	plan, err := PlanCoercion(source, target, p.context)
	if err != nil {
		return Value{}, err
	}
	return v.MapArray(p.target.Type, func(element Value, _ *Descriptor) (Value, error) { return plan.Apply(ctx, element, input, output) })
}

type arrayReader struct {
	ctx         context.Context
	input       string
	at          int
	descriptor  *Descriptor
	typmod      int32
	environment InputEnvironment
	elements    []Value
}

func (p *arrayReader) malformed(detail string) error {
	err := failure("22P02", fmt.Sprintf("malformed array literal: \"%s\"", p.input)).(*sqlstate.Error)
	err.Detail = detail
	return err
}
func (p *arrayReader) whitespace() {
	for p.at < len(p.input) && space(p.input[p.at]) {
		p.at++
	}
}
func (p *arrayReader) boundInteger(initial bool) (int32, error) {
	start := p.at
	if p.at < len(p.input) && (p.input[p.at] == '-' || p.input[p.at] == '+') {
		p.at++
	}
	digits := p.at
	for p.at < len(p.input) && p.input[p.at] >= '0' && p.input[p.at] <= '9' {
		if err := p.ctx.Err(); err != nil {
			return 0, err
		}
		p.at++
	}
	if digits == p.at {
		if initial {
			return 0, p.malformed(`"[" must introduce explicitly-specified array dimensions.`)
		}
		return 0, p.malformed("Missing array dimension value.")
	}
	n, err := strconv.ParseInt(p.input[start:p.at], 10, 32)
	if err != nil {
		return 0, failure("54000", "array bound is out of integer range")
	}
	return int32(n), nil
}
func (p *arrayReader) token() (string, bool, error) {
	p.whitespace()
	if p.at == len(p.input) {
		return "", false, p.malformed("Unexpected end of input.")
	}
	quoted := p.input[p.at] == '"'
	if quoted {
		p.at++
	}
	start, end := p.at, p.at
	var out strings.Builder
	protected, escaped := quoted, false
	delimiter := p.descriptor.element.Delimiter()
	for p.at < len(p.input) {
		if err := p.ctx.Err(); err != nil {
			return "", false, err
		}
		c := p.input[p.at]
		if c == '\\' {
			if !escaped {
				out.WriteString(p.input[start:p.at])
				escaped = true
			}
			protected = true
			p.at++
			if p.at == len(p.input) {
				return "", false, p.malformed("Unexpected end of input.")
			}
			out.WriteByte(p.input[p.at])
			p.at++
			end = out.Len()
			continue
		}
		if quoted && c == '"' {
			text := p.input[start:p.at]
			if escaped {
				text = out.String()
			}
			p.at++
			p.whitespace()
			if p.at == len(p.input) {
				return "", false, p.malformed("Unexpected end of input.")
			}
			c = p.input[p.at]
			if c == '{' {
				return "", false, p.malformed(`Unexpected "{" character.`)
			}
			if c != delimiter && c != '}' {
				return "", false, p.malformed("Incorrectly quoted array element.")
			}
			return text, false, nil
		}
		if !quoted && (c == delimiter || c == '}') {
			var text string
			if escaped {
				text = out.String()[:end]
			} else {
				text = p.input[start:end]
			}
			if text == "" {
				return "", false, p.malformed(fmt.Sprintf("Unexpected \"%c\" character.", c))
			}
			return text, !protected && strings.EqualFold(text, "NULL"), nil
		}
		if !quoted && c == '"' {
			return "", false, p.malformed("Incorrectly quoted array element.")
		}
		if !quoted && c == '{' {
			return "", false, p.malformed(`Unexpected "{" character.`)
		}
		if escaped {
			out.WriteByte(c)
		}
		p.at++
		if quoted || !space(c) {
			if escaped {
				end = out.Len()
			} else {
				end = p.at
			}
		}
	}
	return "", false, p.malformed("Unexpected end of input.")
}
func (p *arrayReader) dimensionError(specified bool) error {
	if specified {
		return p.malformed("Specified array dimensions do not match array contents.")
	}
	return p.malformed("Multidimensional arrays must have sub-arrays with matching dimensions.")
}

func (p *arrayReader) contents(dimensions []ArrayDimension) ([]ArrayDimension, error) {
	// Like ReadArrayStr, freeze supplied rank immediately and inferred rank at
	// the first scalar (including NULL). Each closing brace checks its own extent.
	var lengths, counts [6]int32
	for i := range lengths {
		lengths[i] = -1
	}
	rank := len(dimensions)
	specified := rank != 0
	frozen := specified
	for i, dim := range dimensions {
		lengths[i] = dim.Length
	}
	depth := 0
	expectDelimiter := false
	for {
		if err := p.ctx.Err(); err != nil {
			return nil, err
		}
		p.whitespace()
		if p.at == len(p.input) {
			return nil, p.malformed("Unexpected end of input.")
		}
		c := p.input[p.at]
		switch {
		case c == '{':
			if expectDelimiter {
				return nil, p.malformed(`Unexpected "{" character.`)
			}
			if depth == len(counts) {
				return nil, failure("54000", "number of array dimensions exceeds the maximum allowed (6)")
			}
			counts[depth] = 0
			depth++
			if depth > rank {
				if frozen {
					return nil, p.dimensionError(specified)
				}
				rank = depth
			}
			p.at++
		case c == '}':
			if counts[depth-1] > 0 && !expectDelimiter {
				return nil, p.malformed(`Unexpected "}" character.`)
			}
			depth--
			if lengths[depth] < 0 {
				lengths[depth] = counts[depth]
			} else if lengths[depth] != counts[depth] {
				return nil, p.dimensionError(specified)
			}
			p.at++
			if depth == 0 {
				if len(p.elements) == 0 {
					return nil, nil
				}
				if !specified {
					dimensions = make([]ArrayDimension, rank)
					for i := range dimensions {
						dimensions[i] = ArrayDimension{1, lengths[i]}
					}
				}
				return dimensions, nil
			}
			if counts[depth-1] == math.MaxInt32 {
				return nil, failure("54000", "array size exceeds the maximum allowed")
			}
			counts[depth-1]++
			expectDelimiter = true
		case c == p.descriptor.element.Delimiter():
			if !expectDelimiter {
				return nil, p.malformed(fmt.Sprintf("Unexpected \"%c\" character.", c))
			}
			p.at++
			expectDelimiter = false
		default:
			text, isNull, err := p.token()
			if err != nil {
				return nil, err
			}
			if expectDelimiter {
				return nil, p.malformed("Unexpected array element.")
			}
			element := Null(p.descriptor.element)
			if !isNull {
				element, err = p.descriptor.element.InputWithEnvironment(p.ctx, text, p.typmod, p.environment)
				if err != nil {
					return nil, err
				}
			}
			if isNull {
				if err := p.descriptor.element.CheckDomainInput(p.ctx, element, p.environment); err != nil {
					return nil, err
				}
			}
			// Scalar input intentionally precedes the scalar-depth check.
			frozen = true
			if depth != rank {
				return nil, p.dimensionError(specified)
			}
			if counts[depth-1] == math.MaxInt32 {
				return nil, failure("54000", "array size exceeds the maximum allowed")
			}
			counts[depth-1]++
			p.elements = append(p.elements, element)
			expectDelimiter = true
		}
	}
}
func (d *Descriptor) arrayInput(ctx context.Context, text string, typmod int32, env InputEnvironment) (Value, error) {
	p := arrayReader{ctx: ctx, input: text, descriptor: d, typmod: typmod, environment: env}
	p.whitespace()
	var dimensions []ArrayDimension
	product := int64(1)
	for p.at < len(text) && text[p.at] == '[' {
		if len(dimensions) == 6 {
			return Value{}, failure("54000", "number of array dimensions exceeds the maximum allowed (6)")
		}
		p.at++
		lower, err := p.boundInteger(true)
		if err != nil {
			return Value{}, err
		}
		upper := lower
		lower = 1
		if p.at < len(text) && text[p.at] == ':' {
			lower = upper
			p.at++
			upper, err = p.boundInteger(false)
			if err != nil {
				return Value{}, err
			}
		}
		if p.at == len(text) || text[p.at] != ']' {
			return Value{}, p.malformed(`Missing "]" after array dimensions.`)
		}
		if upper < lower {
			return Value{}, failure("2202E", "upper bound cannot be less than lower bound")
		}
		if upper == math.MaxInt32 {
			return Value{}, failure("54000", "array upper bound is too large: 2147483647")
		}
		length := int64(upper) - int64(lower) + 1
		if length > math.MaxInt32 {
			return Value{}, failure("54000", "array size exceeds the maximum allowed")
		}
		if product > int64(math.MaxInt32)/length {
			return Value{}, failure("54000", "array size exceeds the maximum allowed")
		}
		product *= length
		dimensions = append(dimensions, ArrayDimension{lower, int32(length)})
		p.at++
		p.whitespace()
	}
	if len(dimensions) > 0 {
		if p.at == len(text) || text[p.at] != '=' {
			return Value{}, p.malformed(`Missing "=" after array dimensions.`)
		}
		p.at++
		p.whitespace()
		if p.at == len(text) || text[p.at] != '{' {
			return Value{}, p.malformed(`Array contents must start with "{".`)
		}
	} else if p.at == len(text) || text[p.at] != '{' {
		return Value{}, p.malformed(`Array value must start with "{" or dimension information.`)
	}
	dimensions, err := p.contents(dimensions)
	if err != nil {
		return Value{}, err
	}
	p.whitespace()
	if p.at != len(text) {
		return Value{}, p.malformed("Junk after closing right brace.")
	}
	return ownedArray(d, dimensions, p.elements)
}
func writeArrayElement(out *strings.Builder, text string, delimiter byte) {
	quoted := text == "" || strings.EqualFold(text, "NULL")
	for i := range len(text) {
		c := text[i]
		quoted = quoted || c == delimiter || c == '{' || c == '}' || c == '"' || c == '\\' || space(c)
	}
	if quoted {
		out.WriteByte('"')
	}
	for i := range len(text) {
		c := text[i]
		if c == '"' || c == '\\' {
			out.WriteByte('\\')
		}
		out.WriteByte(c)
	}
	if quoted {
		out.WriteByte('"')
	}
}
func (d *Descriptor) arrayOutput(ctx context.Context, v Value, env OutputEnvironment) (string, error) {
	if v.array == nil {
		return "", fmt.Errorf("missing array payload")
	}
	if len(v.array.elements) == 0 {
		return "{}", nil
	}
	var out strings.Builder
	custom := false
	for _, dim := range v.array.dimensions {
		custom = custom || dim.Lower != 1
	}
	if custom {
		var integer [32]byte
		for _, dim := range v.array.dimensions {
			out.WriteByte('[')
			out.Write(strconv.AppendInt(integer[:0], int64(dim.Lower), 10))
			out.WriteByte(':')
			out.Write(strconv.AppendInt(integer[:0], int64(dim.Lower)+int64(dim.Length)-1, 10))
			out.WriteByte(']')
		}
		out.WriteByte('=')
	}
	index := 0
	var dimension func(int) error
	dimension = func(rank int) error {
		out.WriteByte('{')
		for i := range int(v.array.dimensions[rank].Length) {
			if err := ctx.Err(); err != nil {
				return err
			}
			if i != 0 {
				out.WriteByte(d.element.Delimiter())
			}
			if rank+1 < len(v.array.dimensions) {
				if err := dimension(rank + 1); err != nil {
					return err
				}
				continue
			}
			element := v.array.elements[index]
			index++
			if element.null {
				out.WriteString("NULL")
				continue
			}
			text, err := element.typ.OutputWithEnvironment(ctx, element, env)
			if err != nil {
				return err
			}
			writeArrayElement(&out, text, d.element.Delimiter())
		}
		out.WriteByte('}')
		return nil
	}
	if err := dimension(0); err != nil {
		return "", err
	}
	return out.String(), nil
}
