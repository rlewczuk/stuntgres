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
			err = d.CheckDomainInput(ctx, v, env)
		}
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
	case Int4, Oid, Xid, Cid, Regclass, Regtype, Regrole, Regoper, Regoperator, Regproc, Regprocedure, Regcollation, Regnamespace, Regconfig, Regdictionary, Float4, Date:
		size = 4
	case Int8, Xid8, Float8, Money, Time, Timetz, Timestamp, Timestamptz:
		size = 8
	case UUID, Interval:
		size = 16
	case Circle:
		size = 24
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
	case Oid, Xid, Cid, Regtype, Regrole, Regoper, Regoperator, Regproc, Regprocedure, Regcollation, Regnamespace, Regconfig, Regdictionary:
		v.oid = binary.BigEndian.Uint32(p)
		if d.id == Regtype || d.id == Regrole || IsOperatorAlias(d.id) || IsRoutineAlias(d.id) || IsRegObjectAlias(d.id) || IsRegTS(d.id) {
			v.integer = 1
		}
	case Regclass:
		v.oid, v.integer = binary.BigEndian.Uint32(p), 1
		v.text = strconv.FormatUint(uint64(v.oid), 10)
	case Float4:
		v = float4FromBits(d, binary.BigEndian.Uint32(p))
	case Float8:
		v.floating = math.Float64frombits(binary.BigEndian.Uint64(p))
	case Circle:
		v.circle = circleDatum{
			math.Float64frombits(binary.BigEndian.Uint64(p)),
			math.Float64frombits(binary.BigEndian.Uint64(p[8:])),
			math.Float64frombits(binary.BigEndian.Uint64(p[16:])),
		}
		if v.circle.radius < 0 {
			err = failure("22P03", "invalid radius in external \"circle\" value")
		}
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
		return v, 0, d.missingBinaryProvider(false)
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
	case Oid, Xid, Cid, Regtype, Regproc, Regprocedure, Regrole, Regoper, Regoperator, Regcollation, Regnamespace, Regconfig, Regdictionary:
		return binary32(dst, v.oid), nil
	case Regclass:
		if v.integer == 1 {
			return binary32(dst, v.oid), nil
		}
		n, err := strconv.ParseUint(v.text, 10, 32)
		return binary32(dst, uint32(n)), err
	case Float4:
		return binary32(dst, v.float4Bits()), nil
	case Float8:
		return binary64(dst, math.Float64bits(v.floating)), nil
	case Circle:
		dst = binary64(dst, math.Float64bits(v.circle.x))
		dst = binary64(dst, math.Float64bits(v.circle.y))
		return binary64(dst, math.Float64bits(v.circle.radius)), nil
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
	return dst, d.missingBinaryProvider(true)
}

// CheckBinaryInputDeclaration initializes only the declared receive function.
// Every domain has the generic domain receiver; its base provider is resolved
// when that receiver is actually invoked, including invocation on SQL NULL.
func (d *Descriptor) CheckBinaryInputDeclaration() error {
	if d == nil {
		return fmt.Errorf("invalid binary input type")
	}
	if d.IsDomain() {
		return nil
	}
	return d.unavailableBinaryProvider(false)
}

// CheckBinaryInput prepares receiver invocation, including domain base lookup,
// without converting a fabricated datum. NULL input must do this before domain
// checks, but must not execute nonnull record/node-tree or member conversion.
func (d *Descriptor) CheckBinaryInput() error {
	return d.checkBinaryProvider(false)
}

// CheckBinaryOutput initializes the declared send provider for result metadata
// already admitted by the planner. It does not admit values or grant codecs to
// lookup-only descriptors. Generic containers defer member providers to demand.
func (d *Descriptor) CheckBinaryOutput() error {
	return d.checkBinaryProvider(true)
}

func (d *Descriptor) checkBinaryProvider(output bool) error {
	if d == nil {
		direction := "input"
		if output {
			direction = "output"
		}
		return fmt.Errorf("invalid binary %s type", direction)
	}
	if d.IsDomain() {
		return d.Usage().BaseUsage().Type.checkBinaryProvider(output)
	}
	return d.unavailableBinaryProvider(output)
}

func (d *Descriptor) unavailableBinaryProvider(output bool) error {
	direction := "input"
	if output {
		direction = "output"
	}
	// The same actual codec boundary serves metadata initialization and the
	// binary receive/send dispatchers, not a second supported-type inventory.
	if d.id == ACLItem || d.IsQueryInt() || d.IsCube() || d.IsCitext() || d.IsEan13() {
		return failure("42883", fmt.Sprintf("no binary %s function available for type %s", direction, d.name))
	}
	if d.id == PgSnapshot || d.id == Tid || d.id == Macaddr || d.id == Macaddr8 || d.id == Lseg {
		return d.unsupportedBinaryProvider(output)
	}
	return nil
}

func (d *Descriptor) unsupportedBinaryProvider(output bool) error {
	direction := "input"
	if output {
		direction = "output"
	}
	return failure("0A000", fmt.Sprintf("binary %s is not supported for type %s", direction, d.name))
}

func (d *Descriptor) missingBinaryProvider(output bool) error {
	if err := d.unavailableBinaryProvider(output); err != nil {
		return err
	}
	return d.unsupportedBinaryProvider(output)
}
