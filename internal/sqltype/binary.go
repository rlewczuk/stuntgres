package sqltype

import (
	"context"
	"fmt"
	"math"
	"math/bits"
	"strings"
)

// BinaryPlan implements the closed selected typed scalar signatures.
type BinaryPlan struct {
	name   string
	args   [4]Usage
	arity  int
	result Usage
}

func BindBinary(name string, result Usage, args ...Usage) (BinaryPlan, error) {
	p := BinaryPlan{name: name, result: result, arity: len(args)}
	ids, arity, id := [4]ID{Bytea, Bytea}, 2, Bytea
	switch name {
	case "->", "->>", "#>", "#>>":
		right := Text
		if name == "#>" || name == "#>>" {
			right = 1009
		} else if len(args) == 2 && args[1].Type != nil && args[1].Type.ID() == Int4 {
			right = Int4
		}
		document := JSON
		if len(args) == 2 && args[0].Type != nil && args[0].Type.ID() == JSONB {
			document = JSONB
		}
		ids, id = [4]ID{document, right}, document
		if name == "->>" || name == "#>>" {
			id = Text
		}
	case "?":
		ids, id = [4]ID{JSONB, Text}, Bool
	case "?|", "?&":
		ids, id = [4]ID{JSONB, 1009}, Bool
	case "json_typeof", "jsonb_typeof":
		input := JSON
		if name == "jsonb_typeof" {
			input = JSONB
		}
		ids, arity, id = [4]ID{input}, 1, Text
	case "jsonb_pretty":
		ids, arity, id = [4]ID{JSONB}, 1, Text
	case "json_object", "jsonb_object":
		ids, arity, id = [4]ID{1009, 1009}, len(args), JSON
		if name == "jsonb_object" {
			id = JSONB
		}
		if arity < 1 || arity > 2 {
			return p, fmt.Errorf("invalid JSON object arity")
		}
	case "json_array_length":
		ids, arity, id = [4]ID{JSON}, 1, Int4
	case "jsonb_array_length":
		ids, arity, id = [4]ID{JSONB}, 1, Int4
	case "length", "octet_length", "bit_length":
		arity, id = 1, Int4
	case "crc32", "crc32c":
		arity, id = 1, Int8
	case "bit_count":
		arity, id = 1, Int8
	case "get_bit", "set_bit":
		ids, id = [4]ID{Bytea, Int8, Int4}, Int4
		if name == "set_bit" {
			arity, id = 3, Bytea
		}
	case "get_byte", "set_byte":
		ids, id = [4]ID{Bytea, Int4, Int4}, Int4
		if name == "set_byte" {
			arity, id = 3, Bytea
		}
	case "md5":
		arity, id = 1, Text
	case "sha224", "sha256", "sha384", "sha512", "reverse":
		arity = 1
	case "||":
		if len(args) > 0 && args[0].Type != nil && args[0].Type.ID() == VarBit {
			ids, id = [4]ID{VarBit, VarBit}, VarBit
		}
	case "btrim", "ltrim", "rtrim":
	case "position":
		id = Int4
	case "substring", "substr":
		ids, arity = [4]ID{Bytea, Int4, Int4}, 2
		if len(args) == 3 {
			arity = 3
		}
	case "overlay":
		ids, arity = [4]ID{Bytea, Bytea, Int4, Int4}, 3
		if len(args) == 4 {
			arity = 4
		}
	case "convert":
		ids, arity = [4]ID{Bytea, Name, Name}, 3
	case "convert_from":
		ids, id = [4]ID{Bytea, Name}, Text
	case "convert_to":
		ids = [4]ID{Text, Name}
	case "encode":
		ids, id = [4]ID{Bytea, Text}, Text
	case "decode":
		ids = [4]ID{Text, Text}
	default:
		return p, fmt.Errorf("invalid binary operation")
	}
	if len(args) > 0 && args[0].Type != nil && args[0].Type.ID() == Bit {
		switch name {
		case "bit_count", "bit_length", "length", "octet_length", "position", "substring", "overlay":
			for i := range arity {
				if ids[i] == Bytea {
					ids[i] = Bit
				}
			}
			if id == Bytea {
				id = Bit
			}
		default:
			return p, fmt.Errorf("invalid bit operation")
		}
	}
	if len(args) != arity {
		return p, fmt.Errorf("invalid binary arity")
	}
	for i, u := range args {
		if !u.Valid() || u.Typmod != -1 || u.Type.ID() != ids[i] {
			return p, fmt.Errorf("invalid binary argument usage")
		}
		for j := range i {
			if ids[j] == ids[i] && u.Type != args[j].Type {
				return p, fmt.Errorf("mismatched binary descriptor owner")
			}
		}
		p.args[i] = u
	}
	if !result.Valid() || result.Typmod != -1 || result.Type.ID() != id {
		return p, fmt.Errorf("invalid binary result usage")
	}
	for i, u := range args {
		if ids[i] == id && result.Type != u.Type {
			return p, fmt.Errorf("mismatched binary result owner")
		}
	}
	if ids[0] == JSONB && (name == "#>" || name == "#>>") {
		element := args[1].Type.element
		if element == nil || element.ID() != Text || id == Text && result.Type != element {
			return p, fmt.Errorf("foreign JSONB extraction path")
		}
	}
	return p, nil
}
func (p BinaryPlan) Name() string       { return p.name }
func (p BinaryPlan) ResultUsage() Usage { return p.result }

// Immutable reports intrinsic volatility, independently of argument properties.
func (p BinaryPlan) Immutable() bool {
	return p.name != "convert" && p.name != "convert_from" && p.name != "convert_to"
}

// EncodingInput exposes the already typed conversion frontier without embedding
// catalog callbacks in comparable plans or evaluating operands twice.
func (p BinaryPlan) EncodingInput(ctx context.Context, values ...Value) (raw, source, destination string, null bool, err error) {
	if p.Immutable() || p.result.Type == nil || len(values) != p.arity {
		err = fmt.Errorf("invalid binary conversion values")
		return
	}
	for i, value := range values {
		if value.typ != p.args[i].Type {
			err = fmt.Errorf("invalid binary conversion values")
			return
		}
	}
	if err = ctx.Err(); err != nil {
		return
	}
	for _, value := range values {
		if value.null {
			null = true
			return
		}
	}
	raw = values[0].text
	switch p.name {
	case "convert":
		source, destination = values[1].text, values[2].text
	case "convert_from":
		source, destination = values[1].text, "UTF8"
	case "convert_to":
		source, destination = "UTF8", values[1].text
	}
	return
}

func (p BinaryPlan) EncodingResult(raw string) (Value, error) {
	if p.Immutable() || p.result.Type == nil {
		return Value{}, fmt.Errorf("invalid binary conversion result")
	}
	if p.result.Type.ID() == Text {
		return String(p.result.Type, raw), nil
	}
	return Binary(p.result.Type, raw)
}

func integerLength(d *Descriptor, count int64, bits bool) (Value, error) {
	if bits {
		if count > math.MaxInt32/8 {
			return Value{}, failure("22003", "integer out of range")
		}
		count *= 8
	}
	return Integer(d, count)
}

func (p BinaryPlan) Evaluate(ctx context.Context, values ...Value) (Value, error) {
	if p.result.Type == nil || len(values) != p.arity {
		return Value{}, fmt.Errorf("invalid binary values")
	}
	for i, v := range values {
		if v.typ != p.args[i].Type {
			return Value{}, fmt.Errorf("invalid binary values")
		}
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if p.args[0].Type.ID() == JSONB && (p.name == "#>" || p.name == "#>>") && values[1].array != nil {
		if err := validateJSONBExtractPath(ctx, values[1].array.elements, p.args[1].Type.element); err != nil {
			return Value{}, err
		}
	}
	for _, v := range values {
		if v.null {
			return Null(p.result.Type), nil
		}
	}
	if p.args[0].Type.ID() == Bit {
		return p.evaluateBits(ctx, values)
	}
	if p.args[0].Type.ID() == VarBit {
		return p.concatenateBits(ctx, values[0], values[1])
	}
	a := values[0]
	switch p.name {
	case "->", "->>", "#>", "#>>":
		if a.typ.ID() == JSONB {
			return p.jsonbExtraction(ctx, a, values[1])
		}
		return p.jsonExtraction(ctx, values[0], values[1])
	case "?", "?|", "?&":
		return p.jsonbExistence(ctx, values[0], values[1])
	case "json_typeof", "jsonb_typeof":
		return p.jsonScalar(ctx, a)
	case "jsonb_pretty":
		if a.jsonb == nil {
			return Value{}, fmt.Errorf("invalid JSONB payload")
		}
		text, err := jsonbPrettyOutput(ctx, &a.jsonb.root)
		if err != nil {
			return Value{}, err
		}
		return String(p.result.Type, text), nil
	case "json_object", "jsonb_object":
		return p.textObject(ctx, values)
	case "json_array_length":
		count, err := jsonArrayLength(ctx, a.text)
		if err != nil {
			return Value{}, err
		}
		return Integer(p.result.Type, count)
	case "jsonb_array_length":
		if a.jsonb == nil {
			return Value{}, fmt.Errorf("invalid JSONB payload")
		}
		switch a.jsonb.root.kind {
		case jsonbObject:
			return Value{}, failure("22023", "cannot get array length of a non-array")
		case jsonbArray:
			if err := ctx.Err(); err != nil {
				return Value{}, err
			}
			return Integer(p.result.Type, int64(len(a.jsonb.root.items)))
		case jsonbNull, jsonbString, jsonbNumber, jsonbBoolean:
			return Value{}, failure("22023", "cannot get array length of a scalar")
		default:
			return Value{}, fmt.Errorf("invalid JSONB root kind")
		}
	case "bit_count":
		var count int64
		for i := range len(a.text) {
			if i&4095 == 0 {
				if err := ctx.Err(); err != nil {
					return Value{}, err
				}
			}
			count += int64(bits.OnesCount8(a.text[i]))
		}
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		return Integer(p.result.Type, count)
	case "get_bit", "get_byte", "set_bit", "set_byte":
		offset := values[1].integer
		index := offset
		bit := p.name == "get_bit" || p.name == "set_bit"
		if bit {
			index = offset / 8
		}
		if offset < 0 || index >= int64(len(a.text)) {
			upper := int64(len(a.text)) - 1
			if bit {
				// Saturate only the diagnostic for theoretically unrepresentable
				// bit lengths; the division-based validity check cannot overflow.
				upper = math.MaxInt64
				if int64(len(a.text)) <= math.MaxInt64/8 {
					upper = int64(len(a.text))*8 - 1
				}
			}
			return Value{}, failure("2202E", fmt.Sprintf("index %d out of valid range, 0..%d", offset, upper))
		}
		old := a.text[int(index)]
		var mask byte
		if bit {
			mask = 1 << uint(offset%8)
		}
		switch p.name {
		case "get_byte":
			return Integer(p.result.Type, int64(old))
		case "get_bit":
			if old&mask != 0 {
				return Integer(p.result.Type, 1)
			}
			return Integer(p.result.Type, 0)
		}
		next := byte(values[2].integer)
		if bit {
			if values[2].integer != 0 && values[2].integer != 1 {
				return Value{}, failure("22023", "new bit must be 0 or 1")
			}
			next = old &^ mask
			if values[2].integer == 1 {
				next |= mask
			}
		}
		if next == old {
			return a, nil
		}
		var out strings.Builder
		out.Grow(len(a.text))
		if err := stringWrite(ctx, &out, a.text[:int(index)]); err != nil {
			return Value{}, err
		}
		out.WriteByte(next)
		if err := stringWrite(ctx, &out, a.text[int(index)+1:]); err != nil {
			return Value{}, err
		}
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		return Binary(p.result.Type, out.String())
	case "convert", "convert_from", "convert_to", "encode", "decode":
		var raw string
		var err error
		switch p.name {
		case "convert":
			raw, err = ConvertBinaryEncoding(ctx, a.text, values[1].text, values[2].text)
		case "convert_from":
			raw, err = ConvertBinaryEncoding(ctx, a.text, values[1].text, "UTF8")
		case "convert_to":
			raw, err = ConvertBinaryEncoding(ctx, a.text, "UTF8", values[1].text)
		case "encode", "decode":
			raw, err = binaryFormat(ctx, a.text, values[1].text, p.name == "encode")
		}
		if err != nil {
			return Value{}, err
		}
		if p.result.Type.ID() == Text {
			return String(p.result.Type, raw), nil
		}
		return Binary(p.result.Type, raw)
	}
	if p.name == "substring" || p.name == "substr" {
		lo, hi, err := binarySubstringRange(len(a.text), values[1:])
		if err != nil {
			return Value{}, err
		}
		return Binary(p.result.Type, a.text[lo:hi])
	}
	if p.name == "overlay" {
		start := values[2].integer
		if start <= 0 {
			return Value{}, failure("22011", "negative substring length not allowed")
		}
		count := int64(len(values[1].text))
		if p.arity == 4 {
			count = values[3].integer
		}
		end := start + count
		if end < math.MinInt32 || end > math.MaxInt32 {
			return Value{}, failure("22003", "integer out of range")
		}
		lo := min(start-1, int64(len(a.text)))
		hi := min(max(end, 1)-1, int64(len(a.text)))
		out := a.text[:lo] + values[1].text + a.text[hi:]
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		return Binary(p.result.Type, out)
	}
	if p.name == "position" {
		index, err := binaryPosition(ctx, a.text, values[1].text)
		if err != nil {
			return Value{}, err
		}
		return Integer(p.result.Type, int64(index+1))
	}
	switch p.name {
	case "crc32", "crc32c":
		return binaryChecksum(ctx, p.name, p.result.Type, a.text)
	case "md5", "sha224", "sha256", "sha384", "sha512":
		return binaryDigest(ctx, p.name, p.result.Type, a.text)
	}
	if p.name == "reverse" {
		text, err := stringReverse(ctx, a.text, true)
		if err != nil {
			return Value{}, err
		}
		return Binary(p.result.Type, text)
	}
	if p.arity == 1 {
		return integerLength(p.result.Type, int64(len(a.text)), p.name == "bit_length")
	}
	b := values[1]
	if p.name == "||" {
		if len(a.text) == 0 {
			return b, nil
		}
		if len(b.text) == 0 {
			return a, nil
		}
		if len(a.text) > int(^uint(0)>>1)-len(b.text) {
			return Value{}, fmt.Errorf("binary concatenation size overflow")
		}
		return Binary(p.result.Type, a.text+b.text)
	}
	if len(a.text) == 0 || len(b.text) == 0 {
		return a, nil
	}
	var set [256]bool
	for i := range len(b.text) {
		if i&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return Value{}, err
			}
		}
		set[b.text[i]] = true
	}
	start, end := 0, len(a.text)
	if p.name != "rtrim" {
		for start < end && set[a.text[start]] {
			if start&4095 == 0 {
				if err := ctx.Err(); err != nil {
					return Value{}, err
				}
			}
			start++
		}
	}
	if p.name != "ltrim" {
		for end > start && set[a.text[end-1]] {
			if end&4095 == 0 {
				if err := ctx.Err(); err != nil {
					return Value{}, err
				}
			}
			end--
		}
	}
	if start == 0 && end == len(a.text) {
		return a, nil
	}
	return Binary(p.result.Type, a.text[start:end])
}

// binarySubstringRange shares INT4 clipping for raw octets and canonical bits.
// Addition precedes lower-bound clipping; positive INT4 overflow means to-end.
func binarySubstringRange(size int, indexes []Value) (lo, hi int64, err error) {
	start := indexes[0].integer
	end := int64(size) + 1
	if len(indexes) == 2 {
		count := indexes[1].integer
		if count < 0 {
			return 0, 0, failure("22011", "negative substring length not allowed")
		}
		sum := start + count
		if sum >= math.MinInt32 && sum <= math.MaxInt32 {
			end = sum
		}
	}
	lo = min(max(start, 1)-1, int64(size))
	hi = min(max(end-1, lo), int64(size))
	return lo, hi, nil
}
