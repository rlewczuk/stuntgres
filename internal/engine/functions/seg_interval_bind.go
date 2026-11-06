package functions

import (
	"strings"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

var segIntervalNames = [...]string{"seg_left", "seg_right", "seg_over_left", "seg_over_right", "seg_same", "seg_overlap", "seg_contains", "seg_contained"}

// InstalledSegRoutine exposes the actual installed scalar descriptor. Empty
// signature resolution is specific to this family, not wildcard overload lookup.
type InstalledSegRoutine interface {
	InstalledRoutine
	SegType() *sqltype.Descriptor
}

func bindSegInterval(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var operands [2]Argument
	var nameStorage [2]string
	names := nameStorage[:min(len(raw.Args), len(nameStorage))]
	if len(raw.Args) > len(nameStorage) {
		names = make([]string, len(raw.Args))
	}
	for i, arg := range raw.Args {
		o, err := b.AnalyzeArgument(arg, AnalysisRequest{Intent: StringOtherArgument})
		if err != nil {
			return nil, err
		}
		if i < len(operands) {
			operands[i] = o
		}
		name := "unknown"
		if o.Usage.Type != nil {
			name = b.DiagnosticTypeName(o.Usage.Type)
		}
		names[i] = NamedArgumentType(raw, i, name)
	}
	if err := CallLabelError(raw); err != nil {
		return nil, err
	}
	routine, err := b.InstalledRoutine(raw.Name, SegExtension, "")
	if err != nil {
		return nil, err
	}
	installed, ok := routine.(InstalledSegRoutine)
	name := strings.ToLower(raw.Name.Parts[len(raw.Name.Parts)-1].Text)
	op, selected := sqltype.SegIntervalKind(name)
	if !ok || !selected || len(raw.Args) != 2 || raw.Variadic || len(raw.ArgNames) != 0 || raw.Distinct || raw.All || raw.Star || raw.Window != nil || raw.Filter != nil || len(raw.Order) != 0 {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	if err := installed.Validate(); err != nil {
		return nil, err
	}
	target := installed.SegType().Usage()
	var args [2]expr.Expr
	properties := expr.Properties{Volatility: expr.Immutable}
	for i, o := range operands {
		if o.Usage.Type != nil && !b.CanCoerce(o.Usage, target, sqltype.Implicit) {
			return nil, b.SignatureError(raw.Name, raw.Pos, names)
		}
		args[i], err = b.CoerceArgument(o, CoercionRequest{Intent: ConditionalArgument, Target: target, Label: name})
		if err != nil {
			return nil, err
		}
		properties = expr.MergeProperties(properties, args[i].Properties())
	}
	call, err := NewSegInterval(b.Info(b.Type(op.ResultID()).Usage(), raw.Pos, properties), op, args)
	if err != nil {
		return nil, err
	}
	return installed.Attach(call)
}
