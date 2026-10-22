package functions

import (
	"context"
	"fmt"
	"math"
	"runtime"
	"stuntgres/internal/engine/expr"
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

type UUIDGeneration struct {
	expr.Info
	Name      string
	Args      []expr.Expr
	Timestamp *sqltype.Descriptor
	Addition  sqltype.Arithmetic
}

func (e UUIDGeneration) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if value, ok := uuidOsspConstant(e.Name); ok {
		return sqltype.UUIDValue(e.Usage.Type, value)
	}
	if uuidOsspName(e.Name) {
		if err := e.replayFault(); err != nil {
			return sqltype.Value{}, err
		}
		return e.evalName(ctx, c)
	}
	if e.Name != "uuidv4" && e.Name != "uuidv7" && e.Name != "gen_random_uuid" && !uuidOsspTime(e.Name) {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid UUID selector")
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
	if uuidOsspTime(e.Name) {
		value, err := c.FunctionUUIDV1(e.Name == "uuid_generate_v1mc")
		if err != nil {
			return sqltype.Value{}, err
		}
		return sqltype.UUIDValue(e.Usage.Type, value)
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
func uuidOsspTime(name string) bool {
	return name == "uuid_generate_v1" || name == "uuid_generate_v1mc"
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
func CoerceUUIDShift(b ArgumentCoercer, argument Argument, target sqltype.Usage) (expr.Expr, error) {
	return b.CoerceArgument(argument, CoercionRequest{Intent: DirectFormalArgument, Target: target, AtRaw: true})
}
func NewUUIDGeneration(info expr.Info, name string, args []expr.Expr, timestamp *sqltype.Descriptor) (UUIDGeneration, error) {
	if uuidOsspName(name) {
		if timestamp != nil || len(args) != 2 || args[0] == nil || args[1] == nil || args[0].ResultUsage().Type == nil || args[1].ResultUsage().Type == nil || args[0].ResultUsage().Type.ID() != sqltype.UUID || args[1].ResultUsage().Type.ID() != sqltype.Text {
			return UUIDGeneration{}, fmt.Errorf("stuntgres: invalid UUID namespace/name selector shape")
		}
	} else if name != "uuidv4" && name != "uuidv7" && name != "gen_random_uuid" && !uuidOsspTime(name) {
		if _, ok := uuidOsspConstant(name); !ok || len(args) != 0 || timestamp != nil {
			return UUIDGeneration{}, fmt.Errorf("stuntgres: invalid UUID constant selector or shape")
		}
	}
	if uuidOsspTime(name) && (len(args) != 0 || timestamp != nil) {
		return UUIDGeneration{}, fmt.Errorf("stuntgres: invalid UUID time selector shape")
	}
	node := UUIDGeneration{Info: info, Name: name, Args: args, Timestamp: timestamp}
	if len(args) != 0 && !uuidOsspName(name) {
		var err error
		node.Addition, err = sqltype.BindArithmetic(sqltype.ArithmeticAdd, timestamp, args[0].ResultUsage().Type, timestamp)
		if err != nil {
			return node, err
		}
	}
	return node, nil
}
func (e UUIDGeneration) Reconstruct(info expr.Info, args []expr.Expr, timestamp *sqltype.Descriptor) (UUIDGeneration, error) {
	next, err := NewUUIDGeneration(info, e.Name, args, timestamp)
	if err != nil {
		return e, err
	}
	return next, nil
}

// UUIDGeneration implements expr.Composite. Rebuild keeps the bound timestamp
// descriptor; a call without arguments only takes the new metadata.
var _ expr.Composite = UUIDGeneration{}

func (e UUIDGeneration) Children() []expr.Expr { return e.Args }
func (e UUIDGeneration) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("UUIDGeneration", children, len(e.Args)); err != nil {
		return nil, err
	}
	if len(children) == 0 {
		e.Info = info
		return e, nil
	}
	return rebuilt(e.Reconstruct(info, children, e.Timestamp))
}

// IntrinsicVolatility excludes operand volatility so simplification can merge
// only the children that remain demanded.
func (e UUIDGeneration) IntrinsicVolatility() expr.Volatility {
	if uuidOsspName(e.Name) {
		return expr.Immutable
	}
	return e.Property.Volatility
}

// StrictNull eliminates demand even though the provider is volatile.
func (e UUIDGeneration) StrictNull() expr.Strict { return expr.StrictAfter }

func (e UUIDGeneration) PlanningFold() expr.Fold { return expr.FoldNever }

// Retag retains the timestamp descriptor generation.
func (e UUIDGeneration) Retag(info expr.Info, children []expr.Expr, changed bool, env expr.Retag) (expr.Expr, bool, error) {
	if len(children) == 0 {
		e.Info = info
		return e, changed, nil
	}
	timestamp := e.Timestamp
	if timestamp != nil {
		timestamp = env.Type(timestamp)
		changed = changed || timestamp != e.Timestamp
	}
	node, err := e.Reconstruct(info, children, timestamp)
	return retagged(node, changed, err)
}

var _ expr.Renderable = UUIDGeneration{}

// Render implements expr.Renderable.
func (e UUIDGeneration) Render() (expr.Rendering, error) {
	spec := callRendering("UUIDGeneration "+e.Name, e.Args, e.Name, e.Args)
	spec.Replay.Validate = e.replayFault
	// A NULL shift must keep its interval type to select the overload.
	transportArgs(spec.Args, expr.TransportIntervalNulls)
	return spec, nil
}

// replayFault validates the stored signature and its optional uuidv7 shift.
func (e UUIDGeneration) replayFault() error {
	if uuidOsspName(e.Name) {
		if e.Usage.Type == nil || e.Usage.Type.ID() != sqltype.UUID || e.Timestamp != nil || e.Addition != (sqltype.Arithmetic{}) {
			return fmt.Errorf("stuntgres: invalid stored UUID namespace/name signature")
		}
		_, err := NewUUIDGeneration(e.Info, e.Name, e.Args, nil)
		return err
	}
	_, constant := uuidOsspConstant(e.Name)
	if e.Usage.Type == nil || e.Usage.Type.ID() != sqltype.UUID || (!constant && !uuidOsspTime(e.Name) && e.Name != "uuidv7" && e.Name != "uuidv4" && e.Name != "gen_random_uuid") || len(e.Args) > 1 || len(e.Args) != 0 && e.Name != "uuidv7" || uuidOsspTime(e.Name) && e.Timestamp != nil {
		return fmt.Errorf("stuntgres: invalid stored UUID generation signature")
	}
	if len(e.Args) != 0 && e.Args[0].ResultUsage().Type.ID() != sqltype.Interval {
		return fmt.Errorf("stuntgres: invalid stored UUID shift")
	}
	return nil
}

// SameGroup implements expr.GroupIdentity.
func (e UUIDGeneration) SameGroup(other expr.Expr, same func(a, b expr.Expr) bool, definition bool) bool {
	o, ok := other.(UUIDGeneration)
	if !ok || e.Name != o.Name || len(e.Args) != len(o.Args) {
		return false
	}
	for i := range e.Args {
		if !same(e.Args[i], o.Args[i]) {
			return false
		}
	}
	return true
}

// LooksLikeFunction implements expr.FunctionSyntax.
func (e UUIDGeneration) LooksLikeFunction() bool { return true }

// Substitute implements expr.Substitutable.
func (e UUIDGeneration) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("UUIDGeneration", children, len(e.Args)); err != nil {
		return nil, err
	}
	e.Info, e.Args = info, children
	return e, nil
}

func (e UUIDGeneration) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	args := e.Children()
	return expr.CaptureOperands{Args: args, Count: len(args)}, true, nil
}

func namedUUIDGeneration(b CallTypeContext, raw parser.FunctionExpr, args []Argument, declared []sqltype.Usage) ([]sqltype.Usage, []sqltype.Usage, bool) {
	if !UUIDGenerationShape(raw, true) || len(args) != 1 || !declaredTypes(declared, []sqltype.ID{sqltype.Interval}) || !UUIDShiftCompatible(args[0].Usage, declared[0]) {
		return nil, nil, false
	}
	return namedInputs(args, declared, []int{0})
}
