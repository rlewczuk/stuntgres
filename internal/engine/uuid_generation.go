package engine

import (
	"crypto/rand"
	"fmt"
	"math"
	"runtime"
	"sync"
	"time"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

const uuidEpochMicroseconds int64 = 946684800000000
const uuidMaxUnixMicroseconds int64 = ((1 << 48) - 1) * 1000

// Session-owned, nontransactional clock. Only the clock update holds mu;
// argument evaluation, calendar arithmetic and cryptographic entropy never do.
// Both v7 overloads share this state; catalog replacement does not reset it.
type uuidClock struct {
	mu       sync.Mutex
	previous int64
}

func uuidClockStep() int64 {
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		return 977
	}
	return 245
}
func uuidAscendingTime(previous, observed, step int64) int64 {
	if previous+step >= observed {
		return previous + step
	}
	return observed
}
func (s *uuidClock) now() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.previous = uuidAscendingTime(s.previous, time.Now().UnixNano(), uuidClockStep())
	return s.previous
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
	d := sqlError("22008", "timestamp out of range for UUID version 7").(*sqlstate.Error)
	d.Detail = "UUID version 7 supports timestamps from 1970-01-01 to approximately year 10889."
	return d
}
func uuidShiftTimestamp(us, remainder int64) (uint64, int64, error) {
	// Compare in PostgreSQL units before adding the epoch, avoiding overflow.
	if us < -uuidEpochMicroseconds || us > uuidMaxUnixMicroseconds-uuidEpochMicroseconds {
		return 0, 0, uuidTimestampError()
	}
	unix := us + uuidEpochMicroseconds
	return uint64(unix / 1000), unix%1000*1000 + remainder, nil
}
func uuidInfiniteInterval(months, days int32, micros int64) bool {
	return months == math.MinInt32 && days == math.MinInt32 && micros == math.MinInt64 || months == math.MaxInt32 && days == math.MaxInt32 && micros == math.MaxInt64
}

type uuidGenerationExpression struct {
	expressionInfo
	name      string
	args      []BoundExpr
	timestamp *sqltype.Descriptor
	addition  sqltype.Arithmetic
}

func (e uuidGenerationExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	var shift sqltype.Value
	if len(e.args) != 0 {
		var err error
		shift, err = e.args[0].Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		if shift.IsNull() {
			return sqltype.Null(e.usage.Type), nil
		}
	}
	if c.liveScope() == nil || c.liveScope().op == nil || c.liveScope().op.session == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing UUID session scope")
	}
	var ms uint64
	var fraction int64
	if e.name == "uuidv7" {
		ns := c.liveScope().op.session.uuid.now()
		ms = uint64(ns / 1000000)
		fraction = ns % 1000000
		if len(e.args) != 0 {
			months, days, micros, err := shift.Interval()
			if err != nil {
				return sqltype.Value{}, err
			}
			if uuidInfiniteInterval(months, days, micros) {
				d := sqlError("22008", "interval out of range for UUID version 7").(*sqlstate.Error)
				d.Detail = "UUID version 7 does not support infinite intervals."
				return sqltype.Value{}, d
			}
			stamp, err := sqltype.TimestampValue(e.timestamp, ns/1000-uuidEpochMicroseconds)
			if err != nil {
				return sqltype.Value{}, err
			}
			shifted, err := e.addition.Binary(c.Context, stamp, shift, c.Input)
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
	}
	var entropy [16]byte
	randomBytes := entropy[:]
	if e.name == "uuidv7" {
		randomBytes = entropy[8:]
	}
	if _, err := rand.Read(randomBytes); err != nil {
		return sqltype.Value{}, sqlError("XX000", "could not generate random values")
	}
	if e.name == "uuidv7" {
		return sqltype.UUIDValue(e.usage.Type, uuidPackV7(ms, fraction, entropy, uuidClockStep() == 977))
	}
	return sqltype.UUIDValue(e.usage.Type, uuidPackV4(entropy))
}

func (b *expressionBinder) bindUUIDGeneration(raw parser.FunctionExpr, visible bool) (BoundExpr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	// Analyze every child before the generic argument limit/signature error.
	var args []BoundExpr
	var unknown []bool
	if len(raw.Args) <= 100 {
		args = make([]BoundExpr, len(raw.Args))
		unknown = make([]bool, len(raw.Args))
	}
	for i, r := range raw.Args {
		if l, ok := r.(parser.LiteralExpr); ok && (l.Value.Kind == parser.StringValue || l.Value.Kind == parser.NullValue) {
			if args != nil {
				unknown[i] = true
				args[i] = literalExpression{expressionInfo: expressionInfo{pos: r.SourcePosition()}}
			}
			continue
		}
		x, err := b.bind(r, expressionPolicy{context: projectionContext})
		if err != nil {
			return nil, err
		}
		if args != nil {
			args[i] = x
			unknown[i] = x.ResultUsage().Type == nil
		}
	}
	if len(raw.Args) > 100 {
		return nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	signature := func() (BoundExpr, error) {
		types := make([]string, len(args))
		for i, arg := range args {
			typ := "unknown"
			if !unknown[i] && arg.ResultUsage().Type != nil {
				typ = arg.ResultUsage().Type.DiagnosticName()
			}
			types[i] = functionNamedType(raw, i, typ)
		}
		return nil, functionResolutionErrorForTypes(raw.Name, raw.Pos, types, false)
	}
	if len(raw.ArgNames) != 0 && len(raw.ArgNames) != len(raw.Args) {
		return nil, fmt.Errorf("invalid function argument labels")
	}
	named := false
	for i, label := range raw.ArgNames {
		if label.Text == "" {
			if named {
				return nil, expressionError("42601", "positional argument cannot follow named argument", raw.Args[i].SourcePosition())
			}
			continue
		}
		named = true
		for _, previous := range raw.ArgNames[:i] {
			if previous.Text == label.Text {
				return nil, expressionError("42601", fmt.Sprintf("argument name %q used more than once", label.Text), label.Pos)
			}
		}
	}
	valid := visible && !raw.Variadic && (len(args) == 0 || name == "uuidv7" && len(args) == 1)
	for _, label := range raw.ArgNames {
		valid = valid && (label.Text == "" || name == "uuidv7" && label.Text == "shift")
	}
	if !valid {
		return signature()
	}
	properties := ExprProperties{Volatility: Volatile}
	e := uuidGenerationExpression{name: name, args: args}
	if len(args) != 0 {
		target := b.typ(sqltype.Interval).Usage()
		if !unknown[0] {
			if _, err := sqltype.PlanCoercion(args[0].ResultUsage(), target, sqltype.Implicit); err != nil {
				return signature()
			}
		}
		var err error
		if unknown[0] {
			args[0], err = b.bind(raw.Args[0], expressionPolicy{context: functionArgumentContext, expected: target, mode: sqltype.Implicit})
		} else if args[0].ResultUsage() != target {
			plan, failure := sqltype.PlanCoercion(args[0].ResultUsage(), target, sqltype.Implicit)
			err = failure
			if err == nil {
				args[0] = coercionExpression{b.info(target, raw.Args[0].SourcePosition(), coercionProperties(args[0], plan)), args[0], plan, sqltype.Implicit}
			}
		}
		if err != nil {
			return nil, err
		}
		properties = mergeExpressionProperties(properties, args[0].Properties())
		e.timestamp = b.typ(sqltype.Timestamptz)
		e.addition, err = sqltype.BindArithmetic(sqltype.ArithmeticAdd, e.timestamp, target.Type, e.timestamp)
		if err != nil {
			return nil, err
		}
	}
	e.expressionInfo = b.info(b.typ(sqltype.UUID).Usage(), raw.Pos, properties)
	return e, nil
}

// Only selected UUID leaves may introduce volatility into a closed DEFAULT.
// Shift arguments themselves must be closed Immutable/Stable trees. This does
// not extend the separate fixed-nextval allowance or admit random()/setseed().
func closedUUIDDefault(node BoundExpr) bool {
	if node == nil {
		return true
	}
	p := node.Properties()
	if p.RowDependent || p.ParameterDependent || p.StageDependent {
		return false
	}
	switch n := node.(type) {
	case uuidGenerationExpression:
		for _, arg := range n.args {
			if arg.Properties().Volatility > Stable || !closedUUIDDefault(arg) {
				return false
			}
		}
		return true
	case datetimeTruncExpression, datetimeBinExpression:
		valid := true
		boundExpressionChildren(node, func(child BoundExpr) { valid = valid && closedUUIDDefault(child) })
		return valid
	case posixExpression, posixSubstringExpression, similarExpression, similarEscapeExpression, similarSubstringExpression, regexpExpression:
		valid := true
		boundExpressionChildren(node, func(child BoundExpr) { valid = valid && closedUUIDDefault(child) })
		return valid
	case enumFunctionExpression:
		valid := true
		boundExpressionChildren(node, func(child BoundExpr) { valid = valid && closedUUIDDefault(child) })
		return valid
	case coercionExpression, collationExpression, comparisonExpression, arithmeticExpression, bitwiseExpression, likeExpression, likeEscapeExpression, nullTestExpression, truthTestExpression, logicalExpression, distinctExpression, conditionalExpression, binaryExpression, datetimeJustifyExpression, datetimeAgeFiniteExpression, datetimeZoneCalendarExpression, datetimeMakeExpression, datetimeFormattingExpression, numericFormattingExpression, stringExpression, timeZoneExpression, mathExpression, nullCountExpression, formatExpression, stringVariadicExpression, caseExpression:
		valid := true
		boundExpressionChildren(node, func(child BoundExpr) { valid = valid && closedUUIDDefault(child) })
		return valid
	default:
		return p.Volatility <= Stable
	}
}
