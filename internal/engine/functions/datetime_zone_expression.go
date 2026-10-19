package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

type DatetimeZoneCalendar struct {
	expr.Info
	Name     string
	Args     [3]expr.Expr
	Count    int
	Provider sqltype.Arithmetic
}

func (e DatetimeZoneCalendar) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	for _, arg := range e.Args[:e.Count] {
		if c.FunctionLiteralNull(arg) {
			return sqltype.Null(e.Usage.Type), nil
		}
	}
	var values [3]sqltype.Value
	null := false
	for i, arg := range e.Args[:e.Count] {
		v, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i], null = v, null || v.IsNull()
	}
	if null {
		return sqltype.Null(e.Usage.Type), nil
	}
	if e.Count == 2 {
		return e.Provider.Binary(ctx, values[0], values[1], c.FunctionInput())
	}
	name, err := values[2].Text()
	if err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.CalendarArithmetic(ctx, e.Provider, values[0], values[1], name, c.FunctionInput())
}

func (e DatetimeZoneCalendar) IntrinsicStable() bool { return e.Count == 2 }

type datetimeZoneSignature struct {
	name   string
	args   [3]sqltype.ID
	count  int
	result sqltype.ID
}

var datetimeZoneSignatures = [...]datetimeZoneSignature{
	{"timezone", [3]sqltype.ID{sqltype.Text, sqltype.Timestamp}, 2, sqltype.Timestamptz},
	{"timezone", [3]sqltype.ID{sqltype.Interval, sqltype.Timestamp}, 2, sqltype.Timestamptz},
	{"timezone", [3]sqltype.ID{sqltype.Text, sqltype.Timestamptz}, 2, sqltype.Timestamp},
	{"timezone", [3]sqltype.ID{sqltype.Interval, sqltype.Timestamptz}, 2, sqltype.Timestamp},
	{"timezone", [3]sqltype.ID{sqltype.Text, sqltype.Timetz}, 2, sqltype.Timetz},
	{"timezone", [3]sqltype.ID{sqltype.Interval, sqltype.Timetz}, 2, sqltype.Timetz},
	{"timezone", [3]sqltype.ID{sqltype.Timestamp}, 1, sqltype.Timestamptz},
	{"timezone", [3]sqltype.ID{sqltype.Timestamptz}, 1, sqltype.Timestamp},
	{"timezone", [3]sqltype.ID{sqltype.Timetz}, 1, sqltype.Timetz},
	{"date_add", [3]sqltype.ID{sqltype.Timestamptz, sqltype.Interval}, 2, sqltype.Timestamptz},
	{"date_add", [3]sqltype.ID{sqltype.Timestamptz, sqltype.Interval, sqltype.Text}, 3, sqltype.Timestamptz},
	{"date_subtract", [3]sqltype.ID{sqltype.Timestamptz, sqltype.Interval}, 2, sqltype.Timestamptz},
	{"date_subtract", [3]sqltype.ID{sqltype.Timestamptz, sqltype.Interval, sqltype.Text}, 3, sqltype.Timestamptz},
}

func datetimeZoneCategory(id sqltype.ID) byte {
	if sqltype.IsCharacter(id) {
		return 'S'
	}
	if id == sqltype.Timetz {
		return 'D'
	}
	return ageFiniteCategory(id)
}
func datetimeZonePreferred(id sqltype.ID) bool {
	return id == sqltype.Text || id == sqltype.Timestamptz || id == sqltype.Interval
}

type DatetimeZoneSelection struct {
	Name       string
	Args       [3]sqltype.ID
	Count      int
	Result     sqltype.ID
	Resolution Resolution
}
type DatetimeZonePreparation struct {
	candidates []datetimeZoneSignature
	Demand     TemporalDemand
}

func PrepareDatetimeZone(name string, visible, variadic bool, args []Argument) DatetimeZonePreparation {
	if !visible || variadic {
		return DatetimeZonePreparation{}
	}
	p := DatetimeZonePreparation{}
	for _, signature := range datetimeZoneSignatures {
		if signature.name != name || signature.count != len(args) {
			continue
		}
		p.candidates = datetimeZoneSignatures[:]
		for i, arg := range args {
			if d := arg.Usage.Type; d != nil && d.BaseType().ID() != signature.args[i] {
				p.Demand |= temporalDemand(signature.args[i])
			}
		}
	}
	return p
}

func ResolveDatetimeZone(prepared DatetimeZonePreparation, name string, operands []Argument, types TemporalTypes) DatetimeZoneSelection {
	if len(prepared.candidates) == 0 {
		return DatetimeZoneSelection{Resolution: Missing}
	}

	var viable [13]bool
	bestExact, bestPreferred := -1, -1
	for j, signature := range prepared.candidates {
		if signature.name != name || signature.count != len(operands) {
			continue
		}
		ok, exact, preferred := true, 0, 0
		for i, o := range operands {
			if o.Usage.Type == nil {
				continue
			}
			source, target := o.Usage.Type.BaseType().ID(), signature.args[i]
			if source == target {
				exact++
				continue
			}
			if _, err := sqltype.PlanCoercion(o.Usage, types.Usage(target), sqltype.Implicit); err != nil {
				ok = false
				break
			}
			if datetimeZoneCategory(source) == datetimeZoneCategory(target) && datetimeZonePreferred(target) {
				preferred++
			}
		}
		if !ok {
			continue
		}
		if exact > bestExact || exact == bestExact && preferred > bestPreferred {
			viable = [13]bool{}
			bestExact, bestPreferred = exact, preferred
		}
		if exact == bestExact && preferred == bestPreferred {
			viable[j] = true
		}
	}
	for i, o := range operands {
		if o.Usage.Type != nil {
			continue
		}
		category, conflict, preferred := byte(0), false, false
		for j, signature := range prepared.candidates {
			if !viable[j] {
				continue
			}
			cat := datetimeZoneCategory(signature.args[i])
			if cat == 'S' {
				category = 'S'
				break
			}
			if category != 0 && category != cat {
				conflict = true
			}
			category = cat
		}
		if category != 'S' && conflict {
			continue
		}
		for j, signature := range prepared.candidates {
			if viable[j] && datetimeZoneCategory(signature.args[i]) == category {
				preferred = preferred || datetimeZonePreferred(signature.args[i])
			}
		}
		for j, signature := range prepared.candidates {
			if viable[j] && (datetimeZoneCategory(signature.args[i]) != category || preferred && !datetimeZonePreferred(signature.args[i])) {
				viable[j] = false
			}
		}
	}
	selected, matches := 0, 0
	for j, ok := range viable {
		if ok {
			selected, matches = j, matches+1
		}
	}
	if matches != 1 {
		if matches > 1 {
			return DatetimeZoneSelection{Resolution: Ambiguous}
		}
		return DatetimeZoneSelection{Resolution: Missing}
	}
	signature := prepared.candidates[selected]
	return DatetimeZoneSelection{Name: signature.name, Args: signature.args, Count: signature.count, Result: signature.result}

}

func (s DatetimeZoneSelection) IntrinsicStable() bool {
	return s.Name != "timezone" && s.Count == 2 || s.Name == "timezone" && (s.Count == 1 || s.Args[1] == sqltype.Timetz && s.Args[0] == sqltype.Text)
}

func (e DatetimeZoneCalendar) Reconstruct(info expr.Info, args [3]expr.Expr) (DatetimeZoneCalendar, bool, error) {
	op := sqltype.ArithmeticAdd
	if e.Name == "date_subtract" {
		op = sqltype.ArithmeticSubtract
	}
	provider, err := sqltype.BindArithmetic(op, args[0].ResultUsage().Type, args[1].ResultUsage().Type, info.Usage.Type)
	if err != nil {
		return e, false, err
	}
	changed := provider != e.Provider
	e.Info, e.Args, e.Provider = info, args, provider
	return e, changed, nil
}

// DatetimeZoneCalendar implements expr.Composite. Rebuild rebinds the
// arithmetic provider from the children and keeps trailing argument slots.
var _ expr.Composite = DatetimeZoneCalendar{}

func (e DatetimeZoneCalendar) Children() []expr.Expr { return e.Args[:e.Count] }
func (e DatetimeZoneCalendar) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("DatetimeZoneCalendar", children, e.Count); err != nil {
		return nil, err
	}
	args := e.Args
	copy(args[:], children)
	node, _, err := e.Reconstruct(info, args)
	return rebuilt(node, err)
}

func (e DatetimeZoneCalendar) IntrinsicVolatility() expr.Volatility {
	return stableIf(e.IntrinsicStable())
}

func (e DatetimeZoneCalendar) StrictNull() expr.Strict { return expr.StrictBefore }

func (e DatetimeZoneCalendar) ValidateStoredProvider() error { return nil }

// Retag rebinds the arithmetic provider; a provider change counts as a change.
func (e DatetimeZoneCalendar) Retag(info expr.Info, children []expr.Expr, changed bool, _ expr.Retag) (expr.Expr, bool, error) {
	args := e.Args
	copy(args[:], children)
	node, providerChanged, err := e.Reconstruct(info, args)
	return retagged(node, changed || providerChanged, err)
}

var _ expr.Renderable = DatetimeZoneCalendar{}

// Render implements expr.Renderable.
func (e DatetimeZoneCalendar) Render() (expr.Rendering, error) {
	return callRendering("Datetime "+e.Name, e.Args[:e.Count], e.Name, e.Args[:e.Count]), nil
}

// SameGroup implements expr.GroupIdentity.
func (e DatetimeZoneCalendar) SameGroup(other expr.Expr, same func(a, b expr.Expr) bool, definition bool) bool {
	o, ok := other.(DatetimeZoneCalendar)
	if !ok || e.Name != o.Name || e.Count != o.Count {
		return false
	}
	for i, arg := range e.Args[:e.Count] {
		if !same(arg, o.Args[i]) {
			return false
		}
	}
	return true
}

// PreflightFold implements expr.Preflighting.
func (e DatetimeZoneCalendar) PreflightFold() expr.Preflight { return expr.PreflightNullFirst }

// LooksLikeFunction implements expr.FunctionSyntax.
func (e DatetimeZoneCalendar) LooksLikeFunction() bool { return true }

// Substitute implements expr.Substitutable.
func (e DatetimeZoneCalendar) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("DatetimeZoneCalendar", children, e.Count); err != nil {
		return nil, err
	}
	e.Info = info
	copy(e.Args[:], children)
	return e, nil
}

func (e DatetimeZoneCalendar) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	return captureShape(e.Count, len(e.Args))
}

func (e DatetimeZoneCalendar) CaptureOperand(written int) (expr.Expr, error) {
	return captureAt(e.Args[:], e.Count, written)
}
