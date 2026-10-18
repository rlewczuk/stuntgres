package engine

import (
	"errors"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type advisoryAction uint8

const (
	advisoryAcquire advisoryAction = iota
	advisoryTry
	advisoryUnlock
	advisoryUnlockAll
)

type advisoryLifetime uint8

const (
	advisorySession advisoryLifetime = iota
	advisoryTransaction
)

func advisoryDefinition(name string) (mode relationLockMode, action advisoryAction, lifetime advisoryLifetime, id sqltype.ID, ok bool) {
	mode, action, id, ok = advisoryExclusive, advisoryAcquire, sqltype.Void, true
	switch name {
	case "pg_advisory_lock":
	case "pg_advisory_lock_shared":
		mode = advisoryShared
	case "pg_try_advisory_lock":
		action = advisoryTry
	case "pg_try_advisory_lock_shared":
		mode, action = advisoryShared, advisoryTry
	case "pg_advisory_xact_lock":
		lifetime = advisoryTransaction
	case "pg_advisory_xact_lock_shared":
		lifetime, mode = advisoryTransaction, advisoryShared
	case "pg_try_advisory_xact_lock":
		lifetime, action = advisoryTransaction, advisoryTry
	case "pg_try_advisory_xact_lock_shared":
		lifetime, mode, action = advisoryTransaction, advisoryShared, advisoryTry
	case "pg_advisory_unlock":
		action = advisoryUnlock
	case "pg_advisory_unlock_shared":
		mode, action = advisoryShared, advisoryUnlock
	case "pg_advisory_unlock_all":
		action = advisoryUnlockAll
	default:
		return 0, 0, 0, 0, false
	}
	if action == advisoryTry || action == advisoryUnlock {
		id = sqltype.Bool
	}
	return
}

func (x advisoryExpression) validStored() bool {
	mode, action, lifetime, id, ok := advisoryDefinition(x.name)
	if !ok || x.mode != mode || x.action != action || x.lifetime != lifetime || x.Usage.Type == nil || x.Usage.Type.ID() != id {
		return false
	}
	if action == advisoryUnlockAll {
		return len(x.args) == 0
	}
	if len(x.args) != 1 && len(x.args) != 2 {
		return false
	}
	argumentID := sqltype.Int8
	if len(x.args) == 2 {
		argumentID = sqltype.Int4
	}
	for _, arg := range x.args {
		if arg == nil || arg.ResultUsage().Type == nil || arg.ResultUsage().Type.ID() != argumentID {
			return false
		}
	}
	return true
}

type advisoryExpression struct {
	expressionInfo
	name     string
	args     []BoundExpr
	mode     relationLockMode
	action   advisoryAction
	lifetime advisoryLifetime
}

func (x advisoryExpression) Eval(ctx ExprContext) (sqltype.Value, error) {
	c := evalContext(ctx)
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	var values [2]sqltype.Value
	for i, arg := range x.args {
		v, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = v
	}
	for i := range x.args {
		if values[i].IsNull() {
			return sqltype.Null(x.Usage.Type), nil
		}
	}
	s := c.liveScope()
	if s == nil || s.op == nil || s.op.session == nil || s.op.session.advisory.ticket == nil {
		return sqltype.Value{}, errors.New("stuntgres: missing advisory session scope")
	}
	ticket := s.op.session.advisory.ticket
	if x.action == advisoryUnlockAll {
		if err := s.op.session.Engine().locks.unlockAdvisoryAll(ticket); err != nil {
			return sqltype.Value{}, err
		}
		return sqltype.VoidValue(x.Usage.Type), nil
	}
	first, err := values[0].Integer()
	if err != nil {
		return sqltype.Value{}, err
	}
	ref := lockResource{kind: advisoryBigintResource, high: uint32(uint64(first) >> 32), low: uint32(first)}
	if len(x.args) == 2 {
		second, err := values[1].Integer()
		if err != nil {
			return sqltype.Value{}, err
		}
		ref = lockResource{kind: advisoryPairResource, high: uint32(first), low: uint32(second)}
	}
	if x.action == advisoryUnlock {
		unlocked, err := s.op.session.Engine().locks.unlockAdvisory(ticket, ref, x.mode)
		if err != nil {
			return sqltype.Value{}, err
		}
		if !unlocked {
			message := "you don't own a lock of type ExclusiveLock"
			if x.mode == advisoryShared {
				message = "you don't own a lock of type ShareLock"
			}
			s.op.emitNotice(levelWarning, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01000", Message: message})
		}
		return sqltype.Boolean(x.Usage.Type, unlocked), nil
	}
	try := x.action == advisoryTry
	acquired, err := s.acquireAdvisory(c.Context, ref, x.mode, x.lifetime, try)
	if err != nil {
		return sqltype.Value{}, err
	}
	if try {
		return sqltype.Boolean(x.Usage.Type, acquired), nil
	}
	return sqltype.VoidValue(x.Usage.Type), nil
}

func (b *expressionBinder) bindAdvisory(raw parser.FunctionExpr, visible bool) (BoundExpr, error) {
	if b.defaultConsumer {
		return nil, expressionError("0A000", "functions are unsupported", raw.Pos)
	}
	var args []BoundExpr
	var unknown [2]bool
	if len(raw.Args) <= 100 {
		args = make([]BoundExpr, len(raw.Args))
	}
	for i, child := range raw.Args {
		if l, ok := child.(parser.LiteralExpr); ok && (l.Value.Kind == parser.StringValue || l.Value.Kind == parser.NullValue) {
			if args != nil {
				args[i] = literalExpression{expressionInfo: expressionInfo{Pos: child.SourcePosition()}}
				if i < len(unknown) {
					unknown[i] = true
				}
			}
			continue
		}
		x, err := b.bind(child, expressionPolicy{context: projectionContext})
		if err != nil {
			return nil, err
		}
		if args != nil {
			args[i] = x
			if i < len(unknown) {
				unknown[i] = x.ResultUsage().Type == nil
			}
		}
	}
	if len(raw.Args) > 100 {
		return nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	mode, action, lifetime, id, defined := advisoryDefinition(name)
	valid := defined && visible && !raw.Variadic && len(raw.ArgNames) == 0
	if action == advisoryUnlockAll {
		valid = valid && len(args) == 0
	} else {
		valid = valid && (len(args) == 1 || len(args) == 2)
	}
	signatureError := func() error {
		names := make([]string, len(args))
		for i, arg := range args {
			names[i] = functionNamedType(raw, i, functionArgumentTypeName(raw.Args[i], arg))
		}
		return b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
	}
	if !valid {
		return nil, signatureError()
	}
	argumentID := sqltype.Int8
	if len(args) == 2 {
		argumentID = sqltype.Int4
	}
	usage := b.typ(argumentID).Usage()
	var coercions [2]sqltype.CoercionPlan
	for i, arg := range args {
		if unknown[i] {
			continue
		}
		var err error
		coercions[i], err = sqltype.PlanCoercion(arg.ResultUsage(), usage, sqltype.Implicit)
		if err != nil {
			return nil, signatureError()
		}
	}
	properties := ExprProperties{Volatility: Volatile}
	for i, arg := range args {
		if unknown[i] {
			var err error
			args[i], err = b.bind(raw.Args[i], expressionPolicy{context: functionArgumentContext, expected: usage, mode: sqltype.Implicit})
			if err != nil {
				return nil, err
			}
		} else if arg.ResultUsage() != usage {
			args[i] = coercionExpression{b.info(usage, raw.Args[i].SourcePosition(), arg.Properties()), arg, coercions[i], sqltype.Implicit, false}
		}
		properties = mergeExpressionProperties(properties, args[i].Properties())
	}
	// Constant-NULL folding belongs after demanded child simplification, not
	// here: a constant bad cast must still report its own error first.
	formal := [2]sqltype.ID{argumentID, argumentID}
	info, err := b.builtinRoutineInfo(b.info(b.typ(id).Usage(), raw.Pos, properties), advisoryRoutineProvider, name, formal[:len(args)]...)
	if err != nil {
		return nil, err
	}
	return advisoryExpression{expressionInfo: info, name: name, args: args, mode: mode, action: action, lifetime: lifetime}, nil
}

func foldAdvisoryNull(x advisoryExpression) (BoundExpr, bool) {
	for _, arg := range x.args {
		if randomLiteralNull(arg) {
			return literalExpression{expressionInfo: expressionInfo{Usage: x.Usage, Pos: x.Pos}, value: sqltype.Null(x.Usage.Type)}, true
		}
	}
	return x, false
}
