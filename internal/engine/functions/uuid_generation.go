package functions

import (
	"context"
	"math"
	"runtime"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

const uuidEpochMicroseconds int64 = 946684800000000
const uuidMaxUnixMicroseconds int64 = ((1 << 48) - 1) * 1000

func UUIDClockStep() int64 {
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		return 977
	}
	return 245
}
func UUIDAscendingTime(previous, observed, step int64) int64 {
	if previous+step >= observed {
		return previous + step
	}
	return observed
}
func uuidPackV4(entropy [16]byte) [16]byte {
	entropy[6] = entropy[6]&0x0f | 0x40
	entropy[8] = entropy[8]&0x3f | 0x80
	return entropy
}
func uuidPackV7(ms uint64, fractionNS int64, entropy [16]byte, coarse bool) [16]byte {
	for i := range 6 {
		entropy[5-i] = byte(ms)
		ms >>= 8
	}
	fraction := uint16(fractionNS * 4096 / 1000000)
	if coarse {
		fraction ^= uint16(entropy[8] >> 6)
	}
	entropy[6] = 0x70 | byte(fraction>>8)
	entropy[7] = byte(fraction)
	entropy[8] = entropy[8]&0x3f | 0x80
	return entropy
}
func uuidTimestampError() error {
	diagnostic := sqlError("22008", "timestamp out of range for UUID version 7").(*sqlstate.Error)
	diagnostic.Detail = "UUID version 7 supports timestamps from 1970-01-01 to approximately year 10889."
	return diagnostic
}
func uuidShiftTimestamp(us, remainder int64) (uint64, int64, error) {
	if us < -uuidEpochMicroseconds || us > uuidMaxUnixMicroseconds-uuidEpochMicroseconds {
		return 0, 0, uuidTimestampError()
	}
	unix := us + uuidEpochMicroseconds
	return uint64(unix / 1000), unix%1000*1000 + remainder, nil
}
func uuidInfiniteInterval(months, days int32, micros int64) bool {
	return months == math.MinInt32 && days == math.MinInt32 && micros == math.MinInt64 || months == math.MaxInt32 && days == math.MaxInt32 && micros == math.MaxInt64
}

type UUIDGenerationContext interface {
	Context
	FunctionInput() sqltype.InputEnvironment
	FunctionUUIDReady() error
	FunctionUUIDTime() int64
	FunctionUUIDEntropy([]byte) error
}
type UUIDGeneration[E Expression[C], C UUIDGenerationContext, P any] struct {
	Info[P]
	Name      string
	Args      []E
	Timestamp *sqltype.Descriptor
	Addition  sqltype.Arithmetic
}

func (e UUIDGeneration[E, C, P]) Eval(c C) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	var shift sqltype.Value
	if len(e.Args) != 0 {
		shift, err = e.Args[0].Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		if shift.IsNull() {
			return sqltype.Null(e.Usage.Type), nil
		}
	}
	if err := c.FunctionUUIDReady(); err != nil {
		return sqltype.Value{}, err
	}
	if e.Name != "uuidv7" {
		return uuidGenerateV4(ctx, c, e.Usage.Type)
	}
	var ms uint64
	var fraction int64
	ns := c.FunctionUUIDTime()
	ms = uint64(ns / 1000000)
	fraction = ns % 1000000
	if len(e.Args) != 0 {
		months, days, micros, err := shift.Interval()
		if err != nil {
			return sqltype.Value{}, err
		}
		if uuidInfiniteInterval(months, days, micros) {
			diagnostic := sqlError("22008", "interval out of range for UUID version 7").(*sqlstate.Error)
			diagnostic.Detail = "UUID version 7 does not support infinite intervals."
			return sqltype.Value{}, diagnostic
		}
		stamp, err := sqltype.TimestampValue(e.Timestamp, ns/1000-uuidEpochMicroseconds)
		if err != nil {
			return sqltype.Value{}, err
		}
		shifted, err := e.Addition.Binary(ctx, stamp, shift, c.FunctionInput())
		if err != nil {
			return sqltype.Value{}, err
		}
		us, err := shifted.Timestamp()
		if err != nil {
			return sqltype.Value{}, err
		}
		ms, fraction, err = uuidShiftTimestamp(us, ns%1000)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	var entropy [16]byte
	if err := c.FunctionUUIDEntropy(entropy[8:]); err != nil {
		return sqltype.Value{}, sqlError("XX000", "could not generate random values")
	}
	return sqltype.UUIDValue(e.Usage.Type, uuidPackV7(ms, fraction, entropy, UUIDClockStep() == 977))
}

// Core V4 and the installed pgcrypto alias use one entropy/packing path.
func uuidGenerateV4(ctx context.Context, source interface{ FunctionUUIDEntropy([]byte) error }, result *sqltype.Descriptor) (sqltype.Value, error) {
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	var entropy [16]byte
	if err := source.FunctionUUIDEntropy(entropy[:]); err != nil {
		if canceled := ctx.Err(); canceled != nil {
			return sqltype.Value{}, canceled
		}
		return sqltype.Value{}, sqlError("XX000", "could not generate random values")
	}
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.UUIDValue(result, uuidPackV4(entropy))
}
func UUIDGenerationShape(raw parser.FunctionExpr, visible bool) bool {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	valid := visible && !raw.Variadic && (len(raw.Args) == 0 || name == "uuidv7" && len(raw.Args) == 1)
	for _, label := range raw.ArgNames {
		valid = valid && (label.Text == "" || name == "uuidv7" && label.Text == "shift")
	}
	return valid
}
func UUIDShiftCompatible(usage, target sqltype.Usage) bool {
	if usage.Type == nil {
		return true
	}
	_, err := sqltype.PlanCoercion(usage, target, sqltype.Implicit)
	return err == nil
}
func CoerceUUIDShift[E any, B ArgumentCoercer[E]](b B, argument Argument[E], target sqltype.Usage) (E, error) {
	return b.CoerceArgument(argument, CoercionRequest{Intent: DirectFormalArgument, Target: target, AtRaw: true})
}
func NewUUIDGeneration[E Expression[C], C UUIDGenerationContext, P any](info Info[P], name string, args []E, timestamp *sqltype.Descriptor) (UUIDGeneration[E, C, P], error) {
	node := UUIDGeneration[E, C, P]{Info: info, Name: name, Args: args, Timestamp: timestamp}
	if len(args) != 0 {
		var err error
		node.Addition, err = sqltype.BindArithmetic(sqltype.ArithmeticAdd, timestamp, args[0].ResultUsage().Type, timestamp)
		if err != nil {
			return node, err
		}
	}
	return node, nil
}
func (e UUIDGeneration[E, C, P]) Reconstruct(info Info[P], args []E, timestamp *sqltype.Descriptor) (UUIDGeneration[E, C, P], error) {
	next, err := NewUUIDGeneration(info, e.Name, args, timestamp)
	if err != nil {
		return e, err
	}
	return next, nil
}
