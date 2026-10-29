package functions

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// FTSInspection owns checked operands and a fixed OUT descriptor, never invocation state.
type FTSInspection struct {
	expr.Info
	Args  [2]expr.Expr
	Order [2]int
	Count int
	Stat  bool
}

type FTSInspectionRuntime interface {
	DebugTextSearch(context.Context, bool, sqltype.Value, string, *sqltype.Descriptor) ([]sqltype.Value, error)
	StatTextSearch(context.Context, string, string, *sqltype.Descriptor) ([]sqltype.Value, error)
}

func (e FTSInspection) name() string {
	if e.Stat {
		return "ts_stat"
	}
	return "ts_debug"
}
func (e FTSInspection) labels() []string {
	if e.Stat {
		if e.Count == 2 {
			return []string{"query", "weights"}
		}
		return []string{"query"}
	}
	if e.Count == 2 {
		return []string{"config", "document"}
	}
	return []string{"document"}
}
func (e FTSInspection) formal(slot int) sqltype.ID {
	if !e.Stat && e.Count == 2 && slot == 0 {
		return sqltype.Regconfig
	}
	return sqltype.Text
}
func (e FTSInspection) Children() []expr.Expr { return e.Args[:e.Count] }
func (e FTSInspection) Validate() error {
	if e.Count < 1 || e.Count > 2 {
		return fmt.Errorf("stuntgres: invalid inspection arity")
	}
	names, ids := inspectionOutputs(e.Stat)
	d := e.Usage.Type
	if d == nil || d.ID() != 2249 || !d.IsComposite() || d.CompositeLen() != len(names) {
		return fmt.Errorf("stuntgres: invalid inspection record")
	}
	for i, name := range names {
		m := d.CompositeMember(i)
		if m.Attribute != int32(i+1) || m.Name != name || m.Usage.Type == nil || m.Usage.Type.ID() != ids[i] {
			return fmt.Errorf("stuntgres: invalid inspection member")
		}
	}
	var seen [2]bool
	for i, arg := range e.Children() {
		slot := e.Order[i]
		if arg == nil || slot < 0 || slot >= e.Count || seen[slot] || arg.ResultUsage().Type == nil || arg.ResultUsage().Type.ID() != e.formal(slot) {
			return fmt.Errorf("stuntgres: invalid inspection operand")
		}
		seen[slot] = true
	}
	return nil
}
func inspectionOutputs(stat bool) ([]string, []sqltype.ID) {
	if stat {
		return []string{"word", "ndoc", "nentry"}, []sqltype.ID{sqltype.Text, sqltype.Int4, sqltype.Int4}
	}
	return []string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"}, []sqltype.ID{sqltype.Text, sqltype.Text, sqltype.Text, 3770, sqltype.Regdictionary, 1009}
}
func (e FTSInspection) Eval(expr.Context) (sqltype.Value, error) {
	return sqltype.Value{}, fmt.Errorf("stuntgres: inspection set used as scalar")
}
func (e FTSInspection) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("FTSInspection", children, e.Count); err != nil {
		return nil, err
	}
	e.Info = info
	copy(e.Args[:], children)
	return e, nil
}
func (e FTSInspection) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	n, err := e.Substitute(info, children)
	if err != nil {
		return nil, err
	}
	return n, n.(FTSInspection).Validate()
}
func (e FTSInspection) SourceAdmission() (bool, bool) { return true, true }
func (e FTSInspection) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(FTSInspection)
	return ok && e.Stat == o.Stat && e.Count == o.Count && e.Order == o.Order && sqltype.SameRecordUsage(e.Usage, o.Usage)
}
func (e FTSInspection) OpenSource(c expr.Context) (expr.SourceCursor, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	if err = e.Validate(); err != nil {
		return expr.SourceCursor{}, err
	}
	var values [2]sqltype.Value
	for i, arg := range e.Children() {
		v, err := arg.Eval(c)
		if err != nil {
			return expr.SourceCursor{}, err
		}
		values[e.Order[i]] = v
	}
	for _, v := range values[:e.Count] {
		if v.IsNull() {
			return expr.OwnedSource(e.Usage.Type, nil), nil
		}
	}
	runtime, ok := c.(FTSInspectionRuntime)
	if !ok {
		return expr.SourceCursor{}, fmt.Errorf("stuntgres: missing inspection authority")
	}
	var rows []sqltype.Value
	if e.Stat {
		query, textErr := values[0].Text()
		if textErr != nil {
			return expr.SourceCursor{}, textErr
		}
		weights := ""
		if e.Count == 2 {
			weights, err = values[1].Text()
			if err != nil {
				return expr.SourceCursor{}, err
			}
		}
		rows, err = runtime.StatTextSearch(ctx, query, weights, e.Usage.Type)
	} else {
		text, errText := values[e.Count-1].Text()
		if errText != nil {
			return expr.SourceCursor{}, errText
		}
		rows, err = runtime.DebugTextSearch(ctx, e.Count == 2, values[0], text, e.Usage.Type)
	}
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.OwnedSource(e.Usage.Type, rows), nil
}
func (e FTSInspection) IntrinsicVolatility() expr.Volatility {
	if e.Stat {
		return expr.Volatile
	}
	return expr.Stable
}
func (FTSInspection) PlanningFold() expr.Fold                 { return expr.FoldNever }
func (FTSInspection) PreflightFold() expr.Preflight           { return expr.PreflightChildren }
func (FTSInspection) LooksLikeFunction() bool                 { return true }
func (FTSInspection) ClosedDefault(func(expr.Expr) bool) bool { return false }
func (e FTSInspection) ValidateStoredProvider() error         { return e.Validate() }
func (FTSInspection) ClosureTransparent()                     {}
func (FTSInspection) SnapshotDemand(func(expr.Expr) bool) (bool, bool, bool) {
	return true, true, false
}
func (e FTSInspection) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(FTSInspection)
	if !ok || e.Stat != o.Stat || e.Count != o.Count || e.Order != o.Order {
		return false
	}
	for i, arg := range e.Children() {
		if !same(arg, o.Args[i]) {
			return false
		}
	}
	return true
}
func (e FTSInspection) Render() (expr.Rendering, error) {
	if err := e.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	args := renderArgs(e.Children())
	for i := range args {
		if e.Order[i] != i {
			for j := range args {
				args[j].Label = e.labels()[e.Order[j]]
			}
			break
		}
	}
	r := writtenCall("text search inspection", e.Children(), e.name(), args, false)
	r.Namespace = "pg_catalog"
	r.Replay.QuoteNamespace = true
	r.Replay.Validate = e.Validate
	return r, nil
}
func (e FTSInspection) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	return expr.CaptureOperands{Count: e.Count, Indexed: true}, true, nil
}
func (e FTSInspection) CaptureOperand(i int) (expr.Expr, error) {
	if i >= 0 && i < e.Count {
		return e.Args[i], nil
	}
	return nil, expr.ErrorAt("XX000", "invalid inspection capture operand", e.Pos)
}
