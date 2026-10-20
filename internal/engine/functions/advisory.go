package functions

import (
	"errors"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func advisoryDefinition(name string) (mode AdvisoryMode, action AdvisoryAction, lifetime AdvisoryLifetime, id sqltype.ID, ok bool) {
	mode, action, id, ok = AdvisoryExclusive, AdvisoryAcquire, sqltype.Void, true
	switch name {
	case "pg_advisory_lock":
	case "pg_advisory_lock_shared":
		mode = AdvisoryShare
	case "pg_try_advisory_lock":
		action = AdvisoryTry
	case "pg_try_advisory_lock_shared":
		mode, action = AdvisoryShare, AdvisoryTry
	case "pg_advisory_xact_lock":
		lifetime = AdvisoryTransaction
	case "pg_advisory_xact_lock_shared":
		lifetime, mode = AdvisoryTransaction, AdvisoryShare
	case "pg_try_advisory_xact_lock":
		lifetime, action = AdvisoryTransaction, AdvisoryTry
	case "pg_try_advisory_xact_lock_shared":
		lifetime, mode, action = AdvisoryTransaction, AdvisoryShare, AdvisoryTry
	case "pg_advisory_unlock":
		action = AdvisoryUnlock
	case "pg_advisory_unlock_shared":
		mode, action = AdvisoryShare, AdvisoryUnlock
	case "pg_advisory_unlock_all":
		action = AdvisoryUnlockAll
	default:
		return 0, 0, 0, 0, false
	}
	if action == AdvisoryTry || action == AdvisoryUnlock {
		id = sqltype.Bool
	}
	return
}

func (x Advisory) ValidStored() bool {
	mode, action, lifetime, id, ok := advisoryDefinition(x.Name)
	if !ok || x.Mode != mode || x.Action != action || x.Lifetime != lifetime || x.Usage.Type == nil || x.Usage.Type.ID() != id {
		return false
	}
	if action == AdvisoryUnlockAll {
		return len(x.Args) == 0
	}
	if len(x.Args) != 1 && len(x.Args) != 2 {
		return false
	}
	argumentID := sqltype.Int8
	if len(x.Args) == 2 {
		argumentID = sqltype.Int4
	}
	for _, arg := range x.Args {
		if arg == nil || arg.ResultUsage().Type == nil || arg.ResultUsage().Type.ID() != argumentID {
			return false
		}
	}
	return true
}

type Advisory struct {
	expr.Info
	Name     string
	Args     []expr.Expr
	Mode     AdvisoryMode
	Action   AdvisoryAction
	Lifetime AdvisoryLifetime
}

func (x Advisory) Eval(ctx expr.Context) (sqltype.Value, error) {
	if ctx == nil {
		return sqltype.Value{}, errors.New("stuntgres: missing expression context")
	}
	c := ctx
	if _, err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	var values [2]sqltype.Value
	for i, arg := range x.Args {
		v, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = v
	}
	for i := range x.Args {
		if values[i].IsNull() {
			return sqltype.Null(x.Usage.Type), nil
		}
	}
	var first, second int64
	if x.Action != AdvisoryUnlockAll {
		var err error
		first, err = values[0].Integer()
		if err != nil {
			return sqltype.Value{}, err
		}
		if len(x.Args) == 2 {
			second, err = values[1].Integer()
			if err != nil {
				return sqltype.Value{}, err
			}
		}
	}
	mode := x.Mode
	runtime, ok := ctx.(AdvisoryRuntime)
	if !ok {
		return sqltype.Value{}, errors.New("stuntgres: missing advisory session scope")
	}
	return runtime.Advisory(AdvisoryAction(x.Action), mode, AdvisoryLifetime(x.Lifetime), first, second, len(x.Args) == 2, x.Usage.Type)
}

func bindAdvisory(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	if b.DefaultConsumer() {
		return nil, expr.ErrorAt("0A000", "functions are unsupported", raw.Pos)
	}
	var args []expr.Expr
	var unknown [2]bool
	if len(raw.Args) <= 100 {
		args = make([]expr.Expr, len(raw.Args))
	}
	for i, child := range raw.Args {
		argument, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: DirectDeferredArgument})
		if err != nil {
			return nil, err
		}
		if args != nil {
			args[i] = argument.Expression
			if i < len(unknown) {
				unknown[i] = argument.Usage.Type == nil
			}
		}
	}
	if len(raw.Args) > 100 {
		return nil, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	mode, action, lifetime, id, defined := advisoryDefinition(name)
	valid := defined && visible && !raw.Variadic && len(raw.ArgNames) == 0
	if action == AdvisoryUnlockAll {
		valid = valid && len(args) == 0
	} else {
		valid = valid && (len(args) == 1 || len(args) == 2)
	}
	signatureError := func() error {
		names := make([]string, len(args))
		for i, arg := range args {
			name := "unknown"
			if arg != nil && arg.ResultUsage().Type != nil {
				name = arg.ResultUsage().Type.DiagnosticName()
			}
			names[i] = NamedArgumentType(raw, i, name)
		}
		return b.SignatureError(raw.Name, raw.Pos, names)
	}
	if !valid {
		return nil, signatureError()
	}
	argumentID := sqltype.Int8
	if len(args) == 2 {
		argumentID = sqltype.Int4
	}
	usage := b.Type(argumentID).Usage()
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
	properties := expr.Properties{Volatility: expr.Volatile}
	for i, arg := range args {
		if unknown[i] {
			var err error
			args[i], err = b.CoerceArgument(Argument{Expression: arg, Raw: raw.Args[i]}, CoercionRequest{Intent: DirectFormalArgument, Target: usage})
			if err != nil {
				return nil, err
			}
		} else if arg.ResultUsage() != usage {
			var err error
			args[i], err = b.CoerceArgument(Argument{Expression: arg, Raw: raw.Args[i], Usage: arg.ResultUsage()}, CoercionRequest{Intent: BareFormalArgument, Target: usage, Plan: coercions[i], Planned: true, PreserveProperties: true})
			if err != nil {
				return nil, err
			}
		}
		properties = expr.MergeProperties(properties, args[i].Properties())
	}
	// Constant-NULL folding belongs after demanded child simplification, not
	// here: a constant bad cast must still report its own error first.
	formal := [2]sqltype.ID{argumentID, argumentID}
	info, err := b.BuiltinRoutineInfo(b.Info(b.Type(id).Usage(), raw.Pos, properties), AdvisoryRoutineProvider, name, formal[:len(args)]...)
	if err != nil {
		return nil, err
	}
	return Advisory{Info: info, Name: name, Args: args, Mode: mode, Action: action, Lifetime: lifetime}, nil
}
