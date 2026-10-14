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
	node, err := functions.NewUUIDGeneration[BoundExpr, *EvalContext](functions.Info[ExprProperties]{}, name, args, timestamp)
	if err != nil {
		return nil, err
	}
	node.Info = functionInfo(b.info(b.typ(sqltype.UUID).Usage(), raw.Pos, properties))
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
	case functions.UUIDGeneration[BoundExpr, *EvalContext, ExprProperties]:
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
	case functions.DatetimeTrunc[BoundExpr, *EvalContext, ExprProperties], functions.DatetimeBin[BoundExpr, *EvalContext, ExprProperties]:
		valid := true
		boundExpressionChildren(node, func(child BoundExpr) { valid = valid && closedUUIDDefault(child) })
		return valid
	case functions.Posix[BoundExpr, *EvalContext, ExprProperties], functions.PosixSubstring[BoundExpr, *EvalContext, ExprProperties], functions.Similar[BoundExpr, *EvalContext, ExprProperties], functions.SimilarEscape[BoundExpr, *EvalContext, ExprProperties], functions.SimilarSubstring[BoundExpr, *EvalContext, ExprProperties], functions.Regexp[BoundExpr, *EvalContext, ExprProperties]:
		valid := true
		boundExpressionChildren(node, func(child BoundExpr) { valid = valid && closedUUIDDefault(child) })
		return valid
	case functions.RangeInspect[BoundExpr, *EvalContext, ExprProperties], functions.Enum[BoundExpr, *EvalContext, ExprProperties], functions.ArraySearch[BoundExpr, *EvalContext, ExprProperties], functions.ArrayInspect[BoundExpr, *EvalContext, ExprProperties]:
		valid := true
		boundExpressionChildren(node, func(child BoundExpr) { valid = valid && closedUUIDDefault(child) })
		return valid
	case scalarArrayComparisonExpression:
		valid := true
		boundExpressionChildren(node, func(child BoundExpr) { valid = valid && closedUUIDDefault(child) })
		return valid
	case functions.JSONBSet[BoundExpr, *EvalContext, ExprProperties]:
		valid := true
		boundExpressionChildren(node, func(child BoundExpr) { valid = valid && closedUUIDDefault(child) })
		return valid
	case coercionExpression, collationExpression, comparisonExpression, arithmeticExpression, bitwiseExpression, likeExpression, functions.LikeEscape[BoundExpr, *EvalContext, ExprProperties], nullTestExpression, truthTestExpression, logicalExpression, distinctExpression, conditionalExpression, functions.Binary[BoundExpr, *EvalContext, ExprProperties], functions.DatetimeJustify[BoundExpr, *EvalContext, ExprProperties], functions.DatetimeAgeFinite[BoundExpr, *EvalContext, ExprProperties], functions.DatetimeZoneCalendar[BoundExpr, *EvalContext, ExprProperties], functions.DatetimeMake[BoundExpr, *EvalContext, ExprProperties], functions.DatetimeFormatting[BoundExpr, *EvalContext, ExprProperties], functions.NumericFormatting[BoundExpr, *EvalContext, ExprProperties], functions.String[BoundExpr, *EvalContext, ExprProperties], functions.Timezone[BoundExpr, *EvalContext, ExprProperties], functions.Math[BoundExpr, *EvalContext, ExprProperties], functions.NullCount[BoundExpr, *EvalContext, ExprProperties], functions.Format[BoundExpr, *EvalContext, ExprProperties], functions.StringVariadic[BoundExpr, *EvalContext, ExprProperties, quoteOrigin], functions.JSONCreate[BoundExpr, *EvalContext, ExprProperties], caseExpression:
		valid := true
		boundExpressionChildren(node, func(child BoundExpr) { valid = valid && closedUUIDDefault(child) })
		return valid
	default:
		return p.Volatility <= Stable
	}
}
