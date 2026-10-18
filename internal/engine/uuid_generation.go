package engine

import (
	"crypto/rand"
	"fmt"
	"sync"
	"time"

	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Session-owned, nontransactional clock. Only the clock update holds mu;
// argument evaluation, calendar arithmetic and cryptographic entropy never do.
// Both v7 overloads share this state; catalog replacement does not reset it.
type uuidClock struct {
	mu       sync.Mutex
	previous int64
}

func (s *uuidClock) now() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.previous = functions.UUIDAscendingTime(s.previous, time.Now().UnixNano(), functions.UUIDClockStep())
	return s.previous
}

func (c *EvalContext) FunctionUUIDReady() error {
	if c.liveScope() == nil || c.liveScope().op == nil || c.liveScope().op.session == nil {
		return fmt.Errorf("stuntgres: missing UUID session scope")
	}
	return nil
}
func (c *EvalContext) FunctionUUIDTime() int64                { return c.liveScope().op.session.uuid.now() }
func (c *EvalContext) FunctionUUIDEntropy(bytes []byte) error { _, err := rand.Read(bytes); return err }

func (b *expressionBinder) bindUUIDGeneration(raw parser.FunctionExpr, visible bool) (BoundExpr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	args, unknown, err := functions.AnalyzeDirectArguments[BoundExpr](b, raw.Args)
	if err != nil {
		return nil, err
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
		return nil, b.functionResolutionErrorForTypes(raw.Name, raw.Pos, types, false)
	}
	issue := functions.FunctionLabels(raw)
	if issue.Malformed {
		return nil, fmt.Errorf("invalid function argument labels")
	}
	if issue.Positional {
		return nil, expressionError("42601", "positional argument cannot follow named argument", issue.Position)
	}
	if issue.Duplicate != "" {
		return nil, expressionError("42601", fmt.Sprintf("argument name %q used more than once", issue.Duplicate), issue.Position)
	}
	if !functions.UUIDGenerationShape(raw, visible) {
		return signature()
	}
	properties := ExprProperties{Volatility: Volatile}
	var timestamp *sqltype.Descriptor
	if len(args) != 0 {
		target := b.typ(sqltype.Interval).Usage()
		argument := functions.Argument[BoundExpr]{Expression: args[0], Raw: raw.Args[0]}
		if !unknown[0] {
			argument.Usage = args[0].ResultUsage()
		}
		if !functions.UUIDShiftCompatible(argument.Usage, target) {
			return signature()
		}
		args[0], err = functions.CoerceUUIDShift(b, argument, target)
		if err != nil {
			return nil, err
		}
		properties = mergeExpressionProperties(properties, args[0].Properties())
		timestamp = b.typ(sqltype.Timestamptz)
	}
	node, err := functions.NewUUIDGeneration[BoundExpr, ExprContext](functions.Info[ExprProperties]{}, name, args, timestamp)
	if err != nil {
		return nil, err
	}
	var formal [1]sqltype.ID
	if len(args) != 0 {
		formal[0] = sqltype.Interval
	}
	info, err := b.builtinRoutineInfo(b.info(b.typ(sqltype.UUID).Usage(), raw.Pos, properties), uuidRoutineProvider, name, formal[:len(args)]...)
	if err != nil {
		return nil, err
	}
	node.Info = functionInfo(info)
	return node, nil
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
	case installedUUIDOsspFunctionExpression:
		return n.validShape()
	case functions.UUIDGeneration[BoundExpr, ExprContext, ExprProperties]:
		for _, arg := range n.Args {
			if arg.Properties().Volatility > Stable || !closedUUIDDefault(arg) {
				return false
			}
		}
		return true
	case rowExpression, fieldExpression:
		valid := true
		boundExpressionChildren(node, func(child BoundExpr) { valid = valid && closedUUIDDefault(child) })
		return valid
	case functions.DatetimeTrunc[BoundExpr, ExprContext, ExprProperties], functions.DatetimeBin[BoundExpr, ExprContext, ExprProperties]:
		valid := true
		boundExpressionChildren(node, func(child BoundExpr) { valid = valid && closedUUIDDefault(child) })
		return valid
	case functions.Posix[BoundExpr, ExprContext, ExprProperties], functions.PosixSubstring[BoundExpr, ExprContext, ExprProperties], functions.Similar[BoundExpr, ExprContext, ExprProperties], functions.SimilarEscape[BoundExpr, ExprContext, ExprProperties], functions.SimilarSubstring[BoundExpr, ExprContext, ExprProperties], functions.Regexp[BoundExpr, ExprContext, ExprProperties]:
		valid := true
		boundExpressionChildren(node, func(child BoundExpr) { valid = valid && closedUUIDDefault(child) })
		return valid
	case functions.UUIDExtract[BoundExpr, ExprContext, ExprProperties]:
		return closedUUIDDefault(n.Arg)
	case functions.NetworkFormat[BoundExpr, ExprContext, ExprProperties]:
		for _, arg := range n.Args[:n.Kind.Arity()] {
			if !closedUUIDDefault(arg) {
				return false
			}
		}
		return true
	case functions.ArrayRandom[BoundExpr, ExprContext, ExprProperties]:
		valid := true
		boundExpressionChildren(node, func(child BoundExpr) { valid = valid && closedUUIDDefault(child) })
		return valid
	case functions.ArrayTransform[BoundExpr, ExprContext, ExprProperties], functions.RangeOperator[BoundExpr, ExprContext, ExprProperties], functions.RangeInspect[BoundExpr, ExprContext, ExprProperties], functions.Enum[BoundExpr, ExprContext, ExprProperties], functions.ArraySearch[BoundExpr, ExprContext, ExprProperties], functions.ArrayInspect[BoundExpr, ExprContext, ExprProperties]:
		valid := true
		boundExpressionChildren(node, func(child BoundExpr) { valid = valid && closedUUIDDefault(child) })
		return valid
	case scalarArrayComparisonExpression:
		valid := true
		boundExpressionChildren(node, func(child BoundExpr) { valid = valid && closedUUIDDefault(child) })
		return valid
	case sqlJSONExpression, jsonPredicateExpression:
		valid := true
		boundExpressionChildren(node, func(child BoundExpr) { valid = valid && closedUUIDDefault(child) })
		return valid
	case functions.JSONBPathExists[BoundExpr, ExprContext, ExprProperties], functions.JSONBPathMatch[BoundExpr, ExprContext, ExprProperties], functions.JSONBPathQueryFirst[BoundExpr, ExprContext, ExprProperties], functions.JSONBPathQueryArray[BoundExpr, ExprContext, ExprProperties], functions.JSONBSet[BoundExpr, ExprContext, ExprProperties], functions.JSONStripNulls[BoundExpr, ExprContext, ExprProperties], functions.JSONExtract[BoundExpr, ExprContext, ExprProperties]:
		valid := true
		boundExpressionChildren(node, func(child BoundExpr) { valid = valid && closedUUIDDefault(child) })
		return valid
	case coercionExpression, collationExpression, comparisonExpression, arithmeticExpression, bitwiseExpression, likeExpression, functions.LikeEscape[BoundExpr, ExprContext, ExprProperties], nullTestExpression, truthTestExpression, logicalExpression, distinctExpression, conditionalExpression, functions.Binary[BoundExpr, ExprContext, ExprProperties], functions.DatetimeJustify[BoundExpr, ExprContext, ExprProperties], functions.DatetimeAgeFinite[BoundExpr, ExprContext, ExprProperties], functions.DatetimeZoneCalendar[BoundExpr, ExprContext, ExprProperties], functions.DatetimeMake[BoundExpr, ExprContext, ExprProperties], functions.DatetimeFormatting[BoundExpr, ExprContext, ExprProperties], functions.NumericFormatting[BoundExpr, ExprContext, ExprProperties], functions.String[BoundExpr, ExprContext, ExprProperties], functions.Timezone[BoundExpr, ExprContext, ExprProperties], functions.Math[BoundExpr, ExprContext, ExprProperties], functions.NullCount[BoundExpr, ExprContext, ExprProperties], functions.Format[BoundExpr, ExprContext, ExprProperties], functions.StringVariadic[BoundExpr, ExprContext, ExprProperties, quoteOrigin], functions.JSONCreate[BoundExpr, ExprContext, ExprProperties], caseExpression:
		valid := true
		boundExpressionChildren(node, func(child BoundExpr) { valid = valid && closedUUIDDefault(child) })
		return valid
	default:
		return p.Volatility <= Stable
	}
}
