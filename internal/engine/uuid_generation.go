package engine

import (
	"crypto/rand"
	"fmt"
	"sync"
	"time"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
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

// Only selected UUID leaves may introduce volatility into a closed DEFAULT.
// Shift arguments themselves must be closed Immutable/Stable trees. This does
// not extend the separate fixed-nextval allowance or admit random()/setseed().
func closedUUIDDefault(node expr.Expr) bool {
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
	case functions.UUIDGeneration:
		for _, arg := range n.Args {
			if arg.Properties().Volatility > expr.Stable || !closedUUIDDefault(arg) {
				return false
			}
		}
		return true
	case rowExpression, fieldExpression:
		valid := true
		boundExpressionChildren(node, func(child expr.Expr) { valid = valid && closedUUIDDefault(child) })
		return valid
	case functions.DatetimeTrunc, functions.DatetimeBin, functions.DatetimeZoneCalendar:
		// Looked through here only; the transaction-time check closes them by
		// volatility.
		valid := true
		boundExpressionChildren(node, func(child expr.Expr) { valid = valid && closedUUIDDefault(child) })
		return valid
	case scalarArrayComparisonExpression:
		valid := true
		boundExpressionChildren(node, func(child expr.Expr) { valid = valid && closedUUIDDefault(child) })
		return valid
	case coercionExpression, collationExpression, comparisonExpression, arithmeticExpression, bitwiseExpression, likeExpression, nullTestExpression, truthTestExpression, logicalExpression, distinctExpression, conditionalExpression, caseExpression:
		valid := true
		boundExpressionChildren(node, func(child expr.Expr) { valid = valid && closedUUIDDefault(child) })
		return valid
	case expr.ClosureTransparent:
		// Function nodes the closure looks through.
		valid := true
		boundExpressionChildren(node, func(child expr.Expr) { valid = valid && closedUUIDDefault(child) })
		return valid
	default:
		return p.Volatility <= expr.Stable
	}
}
