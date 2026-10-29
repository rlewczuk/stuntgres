package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// publicationInfo retains no catalog facts: each execution uses statement capture.
type publicationInfo struct {
	expr.Info
	Arg    expr.Expr
	Tables bool
}

func publicationInfoSignature(raw parser.FunctionExpr) (sqltype.ID, bool, bool) {
	if len(raw.Name.Parts) == 0 {
		return 0, false, false
	}
	switch raw.Name.Parts[len(raw.Name.Parts)-1].Text {
	case "pg_relation_is_publishable":
		if len(raw.Args) != 1 || raw.Variadic {
			return 0, false, false
		}
		_, valid := namedSlots(raw, []string{""}, 1)
		return sqltype.Regclass, false, valid
	case "pg_get_publication_tables":
		if !raw.Variadic && len(raw.ArgNames) == 0 {
			return 1009, true, len(raw.Args) > 0
		}
		if len(raw.Args) != 1 {
			return 0, false, false
		}
		_, valid := namedSlots(raw, []string{"pubname"}, 1)
		return 1009, true, valid && raw.Variadic
	}
	return 0, false, false
}

func bindPublicationInfo(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var operand Argument
	id, tables, valid := publicationInfoSignature(raw)
	names := make([]string, len(raw.Args))
	if tables && valid && !raw.Variadic {
		var err error
		operand, err = b.AnalyzeArgument(parser.ArrayExpr{Elements: raw.Args, Pos: raw.Pos}, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return nil, err
		}
		for i := range raw.Args {
			names[i] = NamedArgumentType(raw, i, b.DiagnosticTypeName(operand.Usage.Type.Element()))
		}
	} else {
		for i, child := range raw.Args {
			arg, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
			if err != nil {
				return nil, err
			}
			operand = arg
			name := "unknown"
			if arg.Usage.Type != nil {
				name = b.DiagnosticTypeName(arg.Usage.Type)
			}
			names[i] = NamedArgumentType(raw, i, name)
		}
	}
	if err := ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	if !visible || !valid || raw.ResolutionOnly {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	target := b.Type(id).Usage()
	if err := b.CaptureTypeInput(target.Type); err != nil {
		return nil, err
	}
	arg, err := b.CoerceArgument(operand, CoercionRequest{Intent: FinalizeArgument, Target: target})
	if err != nil {
		return nil, err
	}
	if arg.ResultUsage() != target {
		coercion, failure := sqltype.PlanCoercion(arg.ResultUsage(), target, sqltype.Implicit)
		if failure != nil {
			if diagnostic, ok := failure.(*sqlstate.Error); ok && (diagnostic.Code == "42804" || diagnostic.Code == "42846") {
				return nil, b.SignatureError(raw.Name, raw.Pos, names)
			}
			return nil, failure
		}
		arg, err = b.CoerceArgument(Argument{Expression: arg, Raw: raw.Args[0], Usage: arg.ResultUsage()}, CoercionRequest{Intent: BareFormalArgument, Target: target, Plan: coercion, Planned: true})
		if err != nil {
			return nil, err
		}
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	result := b.Type(sqltype.Bool).Usage()
	if tables {
		if err := b.AdmitSetReturning(raw.Pos); err != nil {
			return nil, err
		}
		shape, err := sqltype.RecordShape([]sqltype.CompositeMember{
			{Attribute: 1, Name: "pubid", Usage: b.Type(sqltype.Oid).Usage()},
			{Attribute: 2, Name: "relid", Usage: b.Type(sqltype.Oid).Usage()},
			{Attribute: 3, Name: "attrs", Usage: b.Type(22).Usage()},
			{Attribute: 4, Name: "qual", Usage: b.Type(sqltype.PgNodeTree).Usage()},
		})
		if err != nil {
			return nil, err
		}
		result = shape.Usage()
	}
	properties := arg.Properties()
	properties.Volatility = max(expr.Stable, properties.Volatility)
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	info, err := b.BuiltinRoutineInfo(b.Info(result, raw.Pos, properties), CatalogRoutineProvider, name, id)
	if err != nil {
		return nil, err
	}
	b.NoteCatalogSnapshot()
	b.NoteCatalogText()
	return publicationInfo{Info: info, Arg: arg, Tables: tables}, nil
}

func namedPublicationInfo(b CallTypeContext, raw parser.FunctionExpr, args []Argument, declared []sqltype.Usage) ([]sqltype.Usage, []sqltype.Usage, bool) {
	id, _, valid := publicationInfoSignature(raw)
	if !valid || len(args) != 1 || !declaredTypes(declared, []sqltype.ID{id}) {
		return nil, nil, false
	}
	return namedInputs(b, args, declared, []int{0})
}

func (n publicationInfo) Validate() error {
	id := sqltype.Regclass
	if n.Tables {
		id = 1009
	}
	if n.Arg == nil || n.Arg.ResultUsage().Type == nil || n.Arg.ResultUsage().Type.ID() != id || n.Usage.Type == nil {
		return fmt.Errorf("stuntgres: invalid publication inquiry signature")
	}
	if !n.Tables {
		if n.Usage.Type.ID() != sqltype.Bool {
			return fmt.Errorf("stuntgres: invalid publication inquiry result")
		}
		return nil
	}
	if n.Usage.Type.CompositeLen() != 4 {
		return fmt.Errorf("stuntgres: invalid publication table result")
	}
	for i, field := range [...]struct {
		name string
		id   sqltype.ID
	}{{"pubid", sqltype.Oid}, {"relid", sqltype.Oid}, {"attrs", 22}, {"qual", sqltype.PgNodeTree}} {
		member := n.Usage.Type.CompositeMember(i)
		if member.Name != field.name || member.Usage.Type == nil || member.Usage.Type.ID() != field.id {
			return fmt.Errorf("stuntgres: invalid publication table field")
		}
	}
	return nil
}
func (n publicationInfo) Eval(ctx expr.Context) (sqltype.Value, error) {
	if n.Tables {
		return sqltype.Value{}, fmt.Errorf("stuntgres: publication table set used as scalar")
	}
	value, err := partitionInfoInput(ctx, n.Arg)
	if err != nil {
		return sqltype.Value{}, err
	}
	if value.IsNull() {
		return sqltype.Null(n.Usage.Type), nil
	}
	oid, err := partitionInfoOID(value)
	if err != nil {
		return sqltype.Value{}, err
	}
	runtime, err := catalogInquiryRuntime(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	return runtime.RelationIsPublishable(oid, n.Usage.Type)
}
func (n publicationInfo) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	if !n.Tables {
		value, err := n.Eval(ctx)
		if err != nil {
			return expr.SourceCursor{}, err
		}
		return expr.ScalarSource(value), nil
	}
	value, err := partitionInfoInput(ctx, n.Arg)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	if value.IsNull() {
		return expr.NewSourceCursor(expr.SourceCursor{Type: n.Usage.Type, Exhausted: true}), nil
	}
	runtime, err := catalogInquiryRuntime(ctx)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return runtime.PublicationTables(value, n.Usage.Type)
}
func (publicationInfo) IntrinsicVolatility() expr.Volatility { return expr.Stable }
func (publicationInfo) PlanningFold() expr.Fold              { return expr.FoldNever }
func (n publicationInfo) StrictNull() expr.Strict {
	if n.Tables {
		return expr.StrictNone
	}
	return expr.StrictAfter
}
func (publicationInfo) PreflightFold() expr.Preflight           { return expr.PreflightChildren }
func (publicationInfo) LooksLikeFunction() bool                 { return true }
func (publicationInfo) CallCost() int                           { return 1 }
func (publicationInfo) ClosedDefault(func(expr.Expr) bool) bool { return false }
func (n publicationInfo) SourceAdmission() (bool, bool)         { return true, n.Tables }
func (n publicationInfo) Children() []expr.Expr                 { return []expr.Expr{n.Arg} }
func (n publicationInfo) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(publicationInfo)
	return ok && n.Tables == o.Tables && sqltype.SameRecordUsage(n.ResultUsage(), o.ResultUsage())
}
func (n publicationInfo) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, _ bool) bool {
	o, ok := other.(publicationInfo)
	return ok && n.Tables == o.Tables && same(n.Arg, o.Arg)
}
func (n publicationInfo) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != 1 || children[0] == nil {
		return nil, fmt.Errorf("stuntgres: invalid publication inquiry arguments")
	}
	n.Info, n.Arg = info, children[0]
	return n, nil
}
func (n publicationInfo) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	next, err := n.Substitute(info, children)
	if err != nil {
		return nil, err
	}
	rebuilt := next.(publicationInfo)
	return rebuilt, rebuilt.Validate()
}
func (n publicationInfo) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	name := "pg_relation_is_publishable"
	if n.Tables {
		name = "pg_get_publication_tables"
	}
	rendering := partitionInfoRendering("PublicationInfo", name, n.Arg)
	rendering.Variadic = n.Tables
	return rendering, nil
}
func (n publicationInfo) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	return partitionInfoCapture(kind, n.Arg)
}
func (n publicationInfo) ValidateStoredProvider() error { return n.Validate() }
func (publicationInfo) SnapshotDemand(func(expr.Expr) bool) (bool, bool, bool) {
	return true, true, false
}
