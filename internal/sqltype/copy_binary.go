package sqltype

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"strconv"
	"stuntgres/internal/sqlstate"
)

// binaryReceive reports consumed bytes separately: array/record members require
// exact consumption, while PostgreSQL range bounds deliberately permit padding.
func (d *Descriptor) BinaryInputWithEnvironment(ctx context.Context, p []byte, typmod int32, env InputEnvironment) (Value, error) {
	v, n, err := d.binaryReceive(ctx, p, typmod, env)
	if err == nil && n != len(p) {
		err = failure("22P03", "incorrect binary data format")
	}
	return v, err
}
func binaryShort() error     { return failure("08P01", "insufficient data left in message") }
func binaryByteShort() error { return failure("08P01", "no data left in message") }
func (d *Descriptor) binaryReceive(ctx context.Context, p []byte, typmod int32, env InputEnvironment) (v Value, n int, err error) {
	if err = ctx.Err(); err != nil {
		return
	}
	if d == nil || !d.Concrete() || !d.validTypmod(typmod) {
		return v, 0, fmt.Errorf("invalid binary input usage")
	}
	if d.IsDomain() {
		base := d.Usage().BaseUsage()
		v, n, err = base.Type.binaryReceive(ctx, p, base.Typmod, env)
		if err == nil {
			v, err = d.Wrap(v)
		}
		return
	}
	if d.IsArray() {
		return d.binaryArray(ctx, p, typmod, env)
	}
	if d.IsComposite() {
		return d.binaryComposite(ctx, p, env)
	}
	if isRange(d.id) {
		return d.binaryRange(ctx, p, env)
	}
	if isMultirange(d.id) {
		return d.binaryMultirange(ctx, p, env)
	}
	if d.IsEnum() {
		text := string(p)
		if err = ValidateTextInput(text); err != nil {
			return
		}
		v, err = d.enumInput(text, env)
		return v, len(p), err
	}
	size := 0
	switch d.id {
	case Bool, Char:
		size = 1
	case Int2:
		size = 2
	case Int4, Oid, Xid, Cid, Regclass, Regtype, Regrole, Regoper, Regoperator, Float4, Date:
		size = 4
	case Int8, Xid8, Float8, Money, Time, Timetz, Timestamp, Timestamptz:
		size = 8
	case UUID, Interval:
		size = 16
	}
	if size > len(p) {
		if size == 1 {
			return v, 0, binaryByteShort()
		}
		return v, 0, binaryShort()
	}
	v = Value{typ: d}
	n = size
	switch d.id {
	case Bool:
		v.boolean = p[0] != 0
	case Char:
		if p[0] != 0 {
			v.text = string(p[:1])
		}
	case Int2:
		v.integer = int64(int16(binary.BigEndian.Uint16(p)))
	case Int4:
		v.integer = int64(int32(binary.BigEndian.Uint32(p)))
	case Int8, Xid8, Money:
		v.integer = int64(binary.BigEndian.Uint64(p))
	case Oid, Xid, Cid, Regtype, Regrole, Regoper, Regoperator:
		v.oid = binary.BigEndian.Uint32(p)
		if d.id == Regtype || d.id == Regrole || IsOperatorAlias(d.id) {
			v.integer = 1
		}
	case Regclass:
		v.text = strconv.FormatUint(uint64(binary.BigEndian.Uint32(p)), 10)
	case Float4:
		v.floating = float64(math.Float32frombits(binary.BigEndian.Uint32(p)))
	case Float8:
		v.floating = math.Float64frombits(binary.BigEndian.Uint64(p))
	case UUID:
		copy(v.uuid[:], p[:16])
	case Date:
		days := int64(int32(binary.BigEndian.Uint32(p)))
		if days == math.MaxInt32 {
			days = TimestampPositiveInfinity
		} else if days == math.MinInt32 {
			days = TimestampNegativeInfinity
		}
		v, err = DateValue(d, days)
	case Time:
		v, err = TimeValue(d, int64(binary.BigEndian.Uint64(p)))
		if err == nil {
			v.integer = roundTimestamp(v.integer, typmod)
		}
	case Timetz:
		// Receive validates the clock before reading the narrower binary zone.
		// Generic TimetzValue also admits explicitly configured POSIX offsets.
		v, err = TimeValue(d, int64(binary.BigEndian.Uint64(p)))
		if err != nil {
			return
		}
		if len(p) < 12 {
			return v, n, binaryShort()
		}
		west := int32(binary.BigEndian.Uint32(p[8:]))
		if west <= -16*60*60 || west >= 16*60*60 {
			return v, n, failure("22009", "time zone displacement out of range")
		}
		v.west = west
		v.integer = roundTimestamp(v.integer, typmod)
		n = 12
	case Timestamp, Timestamptz:
		v, err = TimestampValue(d, int64(binary.BigEndian.Uint64(p)))
		if err == nil {
			v.integer = roundTimestamp(v.integer, typmod)
		}
	case Interval:
		v.integer = int64(binary.BigEndian.Uint64(p))
		v.days = int32(binary.BigEndian.Uint32(p[8:]))
		v.months = int32(binary.BigEndian.Uint32(p[12:]))
		v, err = adjustInterval(v, typmod)
	case Bytea:
		v.text = string(p)
		n = len(p)
	case Text, VarChar, BPChar, Name, JSON, JSONB, JSONPath:
		data := p
		if d.id == JSONB || d.id == JSONPath {
			if len(p) == 0 {
				return v, 0, binaryShort()
			}
			if p[0] != 1 {
				message := fmt.Sprintf("unsupported jsonb version number %d", p[0])
				if d.id == JSONPath {
					message = fmt.Sprintf("unsupported jsonpath version number: %d", p[0])
				}
				return v, 0, failure("XX000", message)
			}
			data = p[1:]
		}
		text := string(data)
		if err = ValidateTextInput(text); err != nil {
			return
		}
		switch d.id {
		case Name:
			if len(text) >= 64 {
				return v, 0, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42622", Message: "identifier too long", Detail: "Identifier must be less than 64 characters."}
			}
			v.text = text
		case Text, VarChar, BPChar:
			v, err = sizeCharacter(String(d, text), typmod, false)
		case JSON, JSONB:
			v, err = d.jsonInput(ctx, text)
		case JSONPath:
			v, err = d.jsonpathInput(ctx, text)
		}
		n = len(p)
	case Numeric:
		return d.binaryNumeric(p, typmod)
	case Bit, VarBit:
		return d.binaryBits(p, typmod)
	case Inet, Cidr:
		return d.binaryNetwork(p)
	case PgNodeTree:
		return v, 0, failure("0A000", "cannot accept a value of type pg_node_tree")
	case Void:
		n = 0
	default:
		return v, 0, fmt.Errorf("missing binary input provider for %s", d.name)
	}
	return
}
func binary16(dst []byte, n uint16) []byte { return binary.BigEndian.AppendUint16(dst, n) }
func binary32(dst []byte, n uint32) []byte { return binary.BigEndian.AppendUint32(dst, n) }
func binary64(dst []byte, n uint64) []byte { return binary.BigEndian.AppendUint64(dst, n) }
func (d *Descriptor) AppendBinaryWithEnvironment(ctx context.Context, dst []byte, v Value, env OutputEnvironment) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return dst, err
	}
	if d == nil || !d.Concrete() || v.typ != d || v.null {
		return dst, fmt.Errorf("invalid binary output value")
	}
	if d.IsDomain() {
		base := d.Usage().BaseUsage()
		v.typ = base.Type
		return base.Type.AppendBinaryWithEnvironment(ctx, dst, v, env)
	}
	if d.IsArray() {
		return d.appendBinaryArray(ctx, dst, v, env)
	}
	if d.IsComposite() {
		return d.appendBinaryComposite(ctx, dst, v, env)
	}
	if isRange(d.id) {
		return d.appendBinaryRange(ctx, dst, v.rangeDatum, env)
	}
	if isMultirange(d.id) {
		return d.appendBinaryMultirange(ctx, dst, v, env)
	}
	if d.IsEnum() {
		text, err := d.enumOutput(v)
		return append(dst, text...), err
	}
	switch d.id {
	case Bool:
		if v.boolean {
			return append(dst, 1), nil
		}
		return append(dst, 0), nil
	case Char:
		b := byte(0)
		if v.text != "" {
			b = v.text[0]
		}
		return append(dst, b), nil
	case Int2:
		return binary16(dst, uint16(v.integer)), nil
	case Int4:
		return binary32(dst, uint32(v.integer)), nil
	case Int8, Xid8, Money, Time, Timestamp, Timestamptz:
		return binary64(dst, uint64(v.integer)), nil
	case Date:
		n := v.integer
		if n == TimestampPositiveInfinity {
			n = math.MaxInt32
		} else if n == TimestampNegativeInfinity {
			n = math.MinInt32
		}
		return binary32(dst, uint32(n)), nil
	case Timetz:
		return binary32(binary64(dst, uint64(v.integer)), uint32(v.west)), nil
	case Interval:
		return binary32(binary32(binary64(dst, uint64(v.integer)), uint32(v.days)), uint32(v.months)), nil
	case Oid, Xid, Cid, Regtype, Regproc, Regrole, Regoper, Regoperator:
		return binary32(dst, v.oid), nil
	case Regclass:
		n, err := strconv.ParseUint(v.text, 10, 32)
		return binary32(dst, uint32(n)), err
	case Float4:
		return binary32(dst, math.Float32bits(float32(v.floating))), nil
	case Float8:
		return binary64(dst, math.Float64bits(v.floating)), nil
	case UUID:
		return append(dst, v.uuid[:]...), nil
	case Text, VarChar, BPChar, Name, Bytea, JSON:
		return append(dst, v.text...), nil
	case JSONB, JSONPath:
		dst = append(dst, 1)
		text, err := d.OutputWithEnvironment(ctx, v, env)
		return append(dst, text...), err
	case Numeric:
		return appendBinaryNumeric(dst, v), nil
	case Bit, VarBit:
		return appendBinaryBits(dst, v), nil
	case Inet, Cidr:
		return appendBinaryNetwork(dst, v), nil
	case PgNodeTree:
		text, err := d.OutputWithEnvironment(ctx, v, env)
		return append(dst, text...), err
	case Void:
		return dst, nil
	}
	return dst, fmt.Errorf("missing binary output provider for %s", d.name)
}
