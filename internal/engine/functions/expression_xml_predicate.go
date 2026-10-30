package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type XMLDocumentPredicate struct {
	expr.Info
	Child   expr.Expr
	Negated bool
	Syntax  parser.XMLDocumentPredicateExpr
}

func (e XMLDocumentPredicate) Eval(c expr.Context) (sqltype.Value, error) {
	v, err := e.Child.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if v.IsNull() {
		return sqltype.Null(e.Usage.Type), nil
	}
	ok, err := sqltype.XMLIsDocument(ctx, v)
	if err != nil {
		return sqltype.Value{}, err
	}
	if e.Negated {
		ok = !ok
	}
	return sqltype.Boolean(e.Usage.Type, ok), nil
}
func (e XMLDocumentPredicate) Children() []expr.Expr { return []expr.Expr{e.Child} }
func (e XMLDocumentPredicate) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := e.ValidateStoredProvider(); err != nil {
		return nil, err
	}
	if len(children) != 1 {
		return nil, fmt.Errorf("invalid XML document predicate children")
	}
	e.Info, e.Child = info, children[0]
	return e, e.ValidateStoredProvider()
}
func (XMLDocumentPredicate) IntrinsicVolatility() expr.Volatility { return expr.Immutable }
func (XMLDocumentPredicate) PlanningFold() expr.Fold              { return expr.FoldNever }
func (XMLDocumentPredicate) ExecutionDemand()                     {}
func (XMLDocumentPredicate) PreflightFold() expr.Preflight        { return expr.PreflightChildren }
func (XMLDocumentPredicate) StrictNull() expr.Strict              { return expr.StrictAfter }
func (XMLDocumentPredicate) ClosureTransparent()                  {}
func (e XMLDocumentPredicate) ValidateStoredProvider() error {
	if !xmlPredicateOperand(e.Child, sqltype.XML) || e.Usage.Type == nil || e.Usage.Type.ID() != sqltype.Bool {
		return fmt.Errorf("invalid XML document predicate recipe")
	}
	return nil
}
func (e XMLDocumentPredicate) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	return expr.CaptureOperands{Count: 1, Inline: [2]expr.Expr{e.Child}, XMLSources: true}, kind == expr.CaptureXMLDocument, nil
}
func (e XMLDocumentPredicate) Render() (expr.Rendering, error) {
	suffix := " IS DOCUMENT)"
	if e.Negated {
		suffix = " IS NOT DOCUMENT)"
	}
	return expr.Rendering{Tree: "IS DOCUMENT", Inputs: e.Children(), Form: expr.FormTemplate, Args: []expr.RenderArg{{Expr: e.Child, HideImplicit: true, XMLSource: e.Pos}}, Pieces: []expr.Piece{expr.TextPiece("("), expr.ArgPiece(0), expr.TextPiece(suffix)}}, e.ValidateStoredProvider()
}
func (e XMLDocumentPredicate) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(XMLDocumentPredicate)
	return ok && e.Negated == o.Negated && same(e.Child, o.Child)
}

type XMLExists struct {
	expr.Info
	Path, Context expr.Expr
}

func (e XMLExists) Eval(c expr.Context) (sqltype.Value, error) {
	path, err := e.Path.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	value, err := e.Context.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if path.IsNull() || value.IsNull() {
		return sqltype.Null(e.Usage.Type), nil
	}
	text, err := path.Text()
	if err != nil {
		return sqltype.Value{}, err
	}
	ok, err := sqltype.XMLExists(ctx, text, value, c.FunctionInput())
	if err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.Boolean(e.Usage.Type, ok), nil
}
func (e XMLExists) Children() []expr.Expr { return []expr.Expr{e.Path, e.Context} }
func (e XMLExists) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := e.ValidateStoredProvider(); err != nil {
		return nil, err
	}
	if len(children) != 2 {
		return nil, fmt.Errorf("invalid XMLEXISTS children")
	}
	e.Info, e.Path, e.Context = info, children[0], children[1]
	return e, e.ValidateStoredProvider()
}
func (XMLExists) IntrinsicVolatility() expr.Volatility { return expr.Immutable }
func (XMLExists) PlanningFold() expr.Fold              { return expr.FoldNever }
func (XMLExists) ExecutionDemand()                     {}
func (XMLExists) PreflightFold() expr.Preflight        { return expr.PreflightChildren }
func (XMLExists) StrictNull() expr.Strict              { return expr.StrictAfter }
func (XMLExists) LooksLikeFunction() bool              { return true }
func (XMLExists) ClosureTransparent()                  {}
func xmlPredicateOperand(e expr.Expr, id sqltype.ID) bool {
	return e != nil && e.ResultUsage().Type != nil && e.ResultUsage().Type.BaseType().ID() == id
}
func (e XMLExists) ValidateStoredProvider() error {
	if !xmlPredicateOperand(e.Path, sqltype.Text) || !xmlPredicateOperand(e.Context, sqltype.XML) || e.Usage.Type == nil || e.Usage.Type.ID() != sqltype.Bool {
		return fmt.Errorf("invalid XMLEXISTS recipe")
	}
	return nil
}
func (e XMLExists) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	return expr.CaptureOperands{Count: 2, Inline: [2]expr.Expr{e.Path, e.Context}}, kind == expr.CaptureFunction, nil
}
func (e XMLExists) Render() (expr.Rendering, error) {
	return expr.Rendering{Tree: "xmlexists", Inputs: e.Children(), Form: expr.FormCall, Name: "xmlexists", Args: []expr.RenderArg{{Expr: e.Path}, {Expr: e.Context}}}, e.ValidateStoredProvider()
}
func (e XMLExists) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(XMLExists)
	return ok && same(e.Path, o.Path) && same(e.Context, o.Context)
}

type XMLValidator struct {
	expr.Info
	Child expr.Expr
	Name  string
}

func (e XMLValidator) Eval(c expr.Context) (sqltype.Value, error) {
	v, err := e.Child.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if v.IsNull() {
		return sqltype.Null(e.Usage.Type), nil
	}
	text, err := v.Text()
	if err != nil {
		return sqltype.Value{}, err
	}
	mode := c.FunctionInput().XMLMode
	switch e.Name {
	case "xml_is_well_formed_document":
		mode = sqltype.XMLDocument
	case "xml_is_well_formed_content":
		mode = sqltype.XMLContent
	}
	ok, err := sqltype.XMLIsWellFormed(ctx, text, mode)
	if err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.Boolean(e.Usage.Type, ok), nil
}
func (e XMLValidator) Children() []expr.Expr { return []expr.Expr{e.Child} }
func (e XMLValidator) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := e.ValidateStoredProvider(); err != nil {
		return nil, err
	}
	if len(children) != 1 {
		return nil, fmt.Errorf("invalid XML validator children")
	}
	e.Info, e.Child = info, children[0]
	return e, e.ValidateStoredProvider()
}
func (e XMLValidator) IntrinsicVolatility() expr.Volatility {
	if e.Name == "xml_is_well_formed" {
		return expr.Stable
	}
	return expr.Immutable
}
func (XMLValidator) PlanningFold() expr.Fold       { return expr.FoldNever }
func (XMLValidator) ExecutionDemand()              {}
func (XMLValidator) PreflightFold() expr.Preflight { return expr.PreflightChildren }
func (XMLValidator) StrictNull() expr.Strict       { return expr.StrictAfter }
func (XMLValidator) LooksLikeFunction() bool       { return true }
func (XMLValidator) ClosureTransparent()           {}
func (e XMLValidator) ValidateStoredProvider() error {
	if !xmlPredicateOperand(e.Child, sqltype.Text) || e.Usage.Type == nil || e.Usage.Type.ID() != sqltype.Bool {
		return fmt.Errorf("invalid XML validator recipe")
	}
	switch e.Name {
	case "xml_is_well_formed", "xml_is_well_formed_document", "xml_is_well_formed_content":
		return nil
	}
	return fmt.Errorf("invalid XML validator name")
}
func (e XMLValidator) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	return expr.CaptureOperands{Count: 1, Inline: [2]expr.Expr{e.Child}}, kind == expr.CaptureFunction, nil
}
func (e XMLValidator) Render() (expr.Rendering, error) {
	return expr.Rendering{Tree: e.Name, Inputs: e.Children(), Form: expr.FormCall, Name: e.Name, Args: []expr.RenderArg{{Expr: e.Child}}}, e.ValidateStoredProvider()
}
func (e XMLValidator) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(XMLValidator)
	return ok && e.Name == o.Name && same(e.Child, o.Child)
}

func bindXMLPredicateCall(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	formals := [2]sqltype.ID{sqltype.Text, sqltype.XML}
	count := 1
	if name == "xmlexists" {
		count = 2
	}
	if len(raw.Args) != count || raw.Variadic || raw.ResolutionOnly {
		return nil, b.CallSignatureError(raw)
	}
	var operands [2]Argument
	for i, rawChild := range raw.Args {
		operand, err := b.AnalyzeArgument(rawChild, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return nil, err
		}
		operands[i] = operand
	}
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	if !visible {
		return nil, b.CallSignatureError(raw)
	}
	var children [2]expr.Expr
	properties := expr.Properties{}
	for i, id := range formals[:count] {
		children[i], err = coerceInquiryFormal(b, operands[i], raw.Args[i], b.Type(id).Usage(), func() error { return b.CallSignatureError(raw) })
		if err != nil {
			return nil, err
		}
		properties = expr.MergeProperties(properties, children[i].Properties())
	}
	if raw.Star {
		return nil, expr.ErrorAt("42809", name+"(*) specified, but "+name+" is not an aggregate function", raw.Pos)
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	if name == "xml_is_well_formed" {
		properties.Volatility = max(properties.Volatility, expr.Stable)
	}
	info, err := b.BuiltinRoutineInfo(b.Info(b.Type(sqltype.Bool).Usage(), raw.Pos, properties), CatalogRoutineProvider, name, formals[:count]...)
	if err != nil {
		return nil, err
	}
	if name == "xmlexists" {
		node := XMLExists{Info: info, Path: children[0], Context: children[1]}
		return node, node.ValidateStoredProvider()
	}
	node := XMLValidator{Info: info, Child: children[0], Name: name}
	return node, node.ValidateStoredProvider()
}
