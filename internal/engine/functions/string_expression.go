package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type String struct {
	expr.Info
	Plan    sqltype.StringPlan
	Args    []expr.Expr
	Textual [4]bool
	Syntax  parser.StringSyntax
}

func (e String) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if e.Plan.Name() == "string_to_table" {
		return sqltype.Value{}, fmt.Errorf("stuntgres: unlowered string_to_table expression")
	}
	if e.Plan.Strict() {
		for _, arg := range e.Args {
			if c.FunctionLiteralNull(arg) {
				return sqltype.Null(e.Usage.Type), nil
			}
		}
	}
	var values [4]sqltype.Value
	for i, arg := range e.Args {
		value, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = value
	}
	return e.Plan.Evaluate(ctx, values, c.FunctionInput(), c.FunctionStringOutput())
}
func (e String) Reconstruct(info expr.Info, args []expr.Expr, text sqltype.Usage) (String, error) {
	var usages [4]sqltype.Usage
	for i, arg := range args {
		usages[i] = arg.ResultUsage()
	}
	plan, err := sqltype.BindString(e.Plan.Name(), info.Usage, text, usages[:len(args)], e.Textual, e.Plan.CollationIndeterminate())
	if err != nil {
		return e, err
	}
	e.Info, e.Args, e.Plan = info, args, plan
	return e, nil
}
func (e String) Validate() error {
	if len(e.Args) < 1 || len(e.Args) > 4 {
		return fmt.Errorf("stuntgres: invalid stored string arity")
	}
	var usages [4]sqltype.Usage
	for i, arg := range e.Args {
		usages[i] = arg.ResultUsage()
	}
	plan, err := sqltype.BindString(e.Plan.Name(), e.Usage, e.Plan.TextUsage(), usages[:len(e.Args)], e.Textual, e.Plan.CollationIndeterminate())
	if err != nil || plan != e.Plan {
		return fmt.Errorf("stuntgres: invalid stored string provider")
	}
	return nil
}
func NewString(info expr.Info, name string, text sqltype.Usage, args []expr.Expr, textual [4]bool, syntax parser.StringSyntax, conflict bool) (String, error) {
	var usages [4]sqltype.Usage
	for i, arg := range args {
		usages[i] = arg.ResultUsage()
	}
	plan, err := sqltype.BindString(name, info.Usage, text, usages[:len(args)], textual, conflict)
	return String{info, plan, args, textual, syntax}, err
}
func StringIntrinsic(name string, textual [4]bool) (stable, stage bool) {
	return name == "||" && (!textual[0] || !textual[1]), name == "string_to_table"
}
func StringOperationCollation(name string) bool {
	return name == "position" || name == "strpos" || name == "starts_with"
}
func StringCatalogOutput(name string, textual bool, usage sqltype.Usage) bool {
	return name == "||" && !textual && usage.Type.ContainsCatalogIdentifier()
}

type StringTargets struct{ Text, Integer, Name, Boolean sqltype.Usage }

func (t StringTargets) usage(id sqltype.ID) sqltype.Usage {
	switch id {
	case sqltype.Text:
		return t.Text
	case sqltype.Int4:
		return t.Integer
	case sqltype.Name:
		return t.Name
	case sqltype.Bool:
		return t.Boolean
	}
	return sqltype.Usage{}
}

type StringSelection struct {
	Arguments [4]sqltype.ID
	Textual   [4]bool
	Result    sqltype.ID
	Array     bool
}

func StringEncodingLength(name string, visible bool, args []Argument, encodingName sqltype.Usage) bool {
	if !visible || name != "length" || len(args) != 2 {
		return false
	}
	source, encoding := args[0].Usage.Type, args[1].Usage.Type
	if source != nil && source.BaseType().ID() != sqltype.Bytea {
		return false
	}
	if encoding != nil {
		if _, err := sqltype.PlanCoercion(args[1].Usage, encodingName, sqltype.Implicit); err != nil {
			return false
		}
	}
	return true
}
func StringBinaryCandidate(name string, variadic bool, args []Argument) bool {
	if BinaryBitCandidate(name, variadic, args) {
		return true
	}
	binary := (!variadic && len(args) == 2 && (name == "btrim" || name == "ltrim" || name == "rtrim")) ||
		(!variadic && len(args) == 1 && (name == "length" || name == "octet_length" || name == "bit_length")) ||
		(len(args) == 2 && name == "position") || ((name == "reverse" || name == "md5") && len(args) == 1) ||
		(name == "overlay" && (len(args) == 3 || len(args) == 4))
	known := false
	for i, arg := range args {
		if sqltype.StringIntegerArgument(name, i) {
			continue
		}
		if d := arg.Usage.Type; d != nil {
			binary = binary && d.BaseType().ID() == sqltype.Bytea
			known = known || d.BaseType().ID() == sqltype.Bytea
		}
	}
	return binary && known
}
func StringTargetDemand(name string, count int) (integer, encodingName bool) {
	for i := range count {
		integer = integer || sqltype.StringIntegerArgument(name, i)
	}
	if name == "to_ascii" && count > 1 {
		integer, encodingName = true, true
	}
	if name == "length" && count == 2 {
		encodingName = true
	}
	return
}
func SelectStringFunction(name string, visible, variadic bool, args []Argument, targets StringTargets) (StringSelection, Resolution) {
	encodingID := sqltype.Name
	if name == "to_ascii" && len(args) > 1 && args[1].Usage.Type != nil {
		if _, err := sqltype.PlanCoercion(args[1].Usage, targets.Integer, sqltype.Implicit); err == nil {
			encodingID = sqltype.Int4
		}
	}
	count := name == "length" || name == "char_length" || name == "character_length" || name == "octet_length" || name == "bit_length"
	search := name == "position" || name == "strpos"
	replacement := name == "replace" || name == "translate" || name == "starts_with" || name == "overlay"
	split := name == "split_part" || name == "string_to_array" || name == "string_to_table"
	slice := name == "left" || name == "right" || name == "substring" || name == "substr"
	valid := visible && (!variadic || slice || split || count || search || replacement || name == "lpad" || name == "rpad" || name == "repeat" || name == "reverse" || name == "md5")
	switch {
	case name == "left" || name == "right":
		valid = valid && len(args) == 2
	case name == "substring" || name == "substr":
		valid = valid && (len(args) == 2 || len(args) == 3)
	case name == "split_part":
		valid = valid && len(args) == 3
	case name == "string_to_array" || name == "string_to_table":
		valid = valid && (len(args) == 2 || len(args) == 3)
	case name == "ascii" || name == "chr" || name == "unistr" || name == "unicode_assigned":
		valid = valid && len(args) == 1
	case name == "lpad" || name == "rpad":
		valid = valid && (len(args) == 2 || len(args) == 3)
	case name == "repeat":
		valid = valid && len(args) == 2
	case name == "reverse" || name == "md5":
		valid = valid && len(args) == 1
	case name == "overlay":
		valid = valid && (len(args) == 3 || len(args) == 4)
	case name == "replace" || name == "translate":
		valid = valid && len(args) == 3
	case search || name == "starts_with":
		valid = valid && len(args) == 2
	case count || name == "lower" || name == "upper" || name == "casefold" || name == "initcap":
		valid = valid && len(args) == 1
	default:
		valid = valid && len(args) >= 1 && len(args) <= 2
	}
	for i, arg := range args {
		if d := arg.Usage.Type; d != nil {
			if name == "to_ascii" && i == 1 {
				_, err := sqltype.PlanCoercion(arg.Usage, targets.usage(encodingID), sqltype.Implicit)
				valid = valid && err == nil
				continue
			}
			if sqltype.StringIntegerArgument(name, i) {
				_, err := sqltype.PlanCoercion(arg.Usage, targets.Integer, sqltype.Implicit)
				valid = valid && err == nil
			} else {
				valid = valid && sqltype.StringTextInput(d)
			}
		}
	}
	if !valid {
		return StringSelection{}, Missing
	}
	selection := StringSelection{Result: sqltype.Text}
	for i := range args {
		selection.Arguments[i], selection.Textual[i] = sqltype.Text, true
		if sqltype.StringIntegerArgument(name, i) {
			selection.Arguments[i], selection.Textual[i] = sqltype.Int4, false
		}
		if name == "to_ascii" && i == 1 {
			selection.Arguments[i], selection.Textual[i] = encodingID, false
		}
	}
	if name == "string_to_array" {
		selection.Array = true
	}
	if name == "is_normalized" || name == "starts_with" || name == "unicode_assigned" {
		selection.Result = sqltype.Bool
	}
	if count || search || name == "ascii" {
		selection.Result = sqltype.Int4
	}
	return selection, Resolved
}
func CoerceStringArguments(b ArgumentCoercer, arguments []Argument, selection StringSelection, targets StringTargets) ([]expr.Expr, error) {
	args := make([]expr.Expr, len(arguments))
	for i, argument := range arguments {
		intent := FinalizeArgument
		if !selection.Textual[i] {
			intent = ExactFormalArgument
		}
		arg, err := b.CoerceArgument(argument, CoercionRequest{Intent: intent, Target: targets.usage(selection.Arguments[i]), PreserveProperties: true})
		if err != nil {
			return nil, err
		}
		args[i] = arg
	}
	return args, nil
}
func StringPrefixViable(visible bool, count int, types [2]*sqltype.Descriptor) bool {
	valid := visible && count == 2
	for _, d := range types {
		valid = valid && (d == nil || sqltype.StringTextInput(d))
	}
	return valid
}
func SelectStringConcat(visible bool, types [2]*sqltype.Descriptor) (textual [2]bool, binary sqltype.ID, resolution Resolution) {
	bit := func(t *sqltype.Descriptor) bool {
		return t != nil && (t.BaseType().ID() == sqltype.Bit || t.BaseType().ID() == sqltype.VarBit)
	}
	if visible && (bit(types[0]) && (types[1] == nil || bit(types[1])) || bit(types[1]) && types[0] == nil) {
		return textual, sqltype.VarBit, Resolved
	}
	if visible && ((types[0] != nil && types[0].BaseType().ID() == sqltype.Bytea && (types[1] == nil || types[1].BaseType().ID() == sqltype.Bytea)) || (types[1] != nil && types[1].BaseType().ID() == sqltype.Bytea && types[0] == nil)) {
		return textual, sqltype.Bytea, Resolved
	}
	signatures := [3][2]bool{{true, true}, {true, false}, {false, true}}
	var candidates [3]int
	count := 0
	bestExact, bestPreferred := -1, -1
	if visible {
		for k, s := range signatures {
			viable := true
			exact, preferred := 0, 0
			for i, t := range types {
				if t == nil {
					continue
				}
				id := t.BaseType().ID()
				if s[i] {
					if !sqltype.StringTextInput(t) {
						viable = false
					}
					if id == sqltype.Text {
						exact++
					}
					if sqltype.StringCategory(id) {
						preferred++
					}
				} else if !sqltype.StringScalar(t) {
					viable = false
				}
			}
			if !viable {
				continue
			}
			if exact > bestExact || exact == bestExact && preferred > bestPreferred {
				count = 0
				bestExact = exact
				bestPreferred = preferred
			}
			if exact == bestExact && preferred == bestPreferred {
				candidates[count] = k
				count++
			}
		}
	}
	for i, t := range types {
		if t != nil || count < 2 {
			continue
		}
		preferText := false
		for _, k := range candidates[:count] {
			preferText = preferText || signatures[k][i]
		}
		if preferText {
			n := 0
			for _, k := range candidates[:count] {
				if signatures[k][i] {
					candidates[n] = k
					n++
				}
			}
			count = n
		}
	}
	if count != 1 {
		if count == 0 {
			return textual, 0, Missing
		}
		return textual, 0, Ambiguous
	}
	return signatures[candidates[0]], 0, Resolved
}

func StringArgumentAnalysis(name string, index int) AnalysisRequest {
	if sqltype.StringIntegerArgument(name, index) {
		return AnalysisRequest{Intent: StringOtherArgument}
	}
	return AnalysisRequest{Intent: StringTextArgument}
}

// String implements expr.Composite. Rebuild keeps the bound text usage.
var _ expr.Composite = String{}

func (e String) Children() []expr.Expr { return e.Args }
func (e String) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("String", children, len(e.Args)); err != nil {
		return nil, err
	}
	if err := checkPresent("stuntgres: invalid remapped string argument", children); err != nil {
		return nil, err
	}
	return rebuilt(e.Reconstruct(info, children, e.Plan.TextUsage()))
}

func (e String) IntrinsicVolatility() expr.Volatility { return stableIf(e.Plan.SessionDependent()) }

func (e String) PlanningFold() expr.Fold {
	if e.Plan.Name() == "string_to_table" {
		return expr.FoldNever
	}
	return expr.FoldConstant
}
func (e String) StrictNull() expr.Strict {
	if e.Plan.Strict() {
		return expr.StrictAfter
	}
	return expr.StrictNone
}

func (e String) ValidateStoredProvider() error { return nil }

// Retag retains and remaps the bound text usage. A text usage change alone is
// revalidated but does not count as a change.
func (e String) Retag(info expr.Info, children []expr.Expr, changed bool, env expr.Retag) (expr.Expr, bool, error) {
	if err := checkPresent("stuntgres: invalid remapped string argument", children); err != nil {
		return nil, false, err
	}
	text := e.Plan.TextUsage()
	text.Type = env.Type(text.Type)
	var err error
	if text.Collation, err = env.Collation(text.Collation); err != nil {
		return nil, false, err
	}
	if !changed && text == e.Plan.TextUsage() {
		return e, false, nil
	}
	node, err := e.Reconstruct(info, children, text)
	return retagged(node, changed, err)
}

var _ expr.Renderable = String{}

// Render implements expr.Renderable.
func (e String) Render() (expr.Rendering, error) {
	// Corrupted operands have no form; stored SQL reports them.
	switch {
	case len(e.Args) < 1 || len(e.Args) > 4:
		return expr.Rendering{Tree: "String " + e.Plan.Name(), Replay: expr.Replay{Validate: refuseReplay("stuntgres: invalid stored string arity")}}, nil
	case missingArgument(e.Args):
		return expr.Rendering{Tree: "String " + e.Plan.Name(), Replay: expr.Replay{Validate: refuseReplay("stuntgres: missing string operand")}}, nil
	}
	spec, err := e.rendering()
	if err != nil {
		return spec, err
	}
	spec.Replay.Validate = e.replayFault
	for i, arg := range e.Args {
		// A captured NAME literal would otherwise replay as unknown and
		// resolve to text; base conversions select their stored integer
		// overload, including small BIGINT values and typed NULLs.
		spec.Args[i].Transport = expr.TransportName
		if sqltype.StringBaseConversion(e.Plan.Name()) {
			spec.Args[i].Transport, spec.Args[i].TransportType = expr.TransportCast, e.Plan.ArgumentUsage(i)
		}
		if !e.Textual[i] && catalogArgument(arg) {
			spec.Replay.CatalogSnapshot = true
		}
	}
	return spec, nil
}

// replayFault validates the stored provider and the prefix operator syntax.
func (e String) replayFault() error {
	if err := e.Validate(); err != nil {
		return err
	}
	if e.Syntax == parser.PrefixStringSyntax && (e.Plan.Name() != "starts_with" || len(e.Args) != 2) {
		return fmt.Errorf("stuntgres: invalid prefix syntax provider")
	}
	return nil
}

func (e String) rendering() (expr.Rendering, error) {
	name := e.Plan.Name()
	tree := "String " + name
	if name == "||" {
		// Replay always qualifies the operator; display shows it bare.
		return infixRendering(tree, e.Args, "pg_catalog", "||", e.Args[0], e.Args[1]), nil
	}
	if e.Syntax == parser.OrdinaryStringSyntax || e.Syntax == parser.PrefixStringSyntax {
		return e.callRendering(tree)
	}
	spec := expr.Rendering{Tree: tree, Inputs: e.Args, Form: expr.FormTemplate, Args: hiddenArgs(e.Args...)}
	text, arg := expr.TextPiece, expr.ArgPiece
	var err error
	switch e.Syntax {
	case parser.TrimStringSyntax:
		spec.Pieces, err = TrimPieces(name, len(e.Args))
	case parser.PositionStringSyntax:
		if name != "position" || len(e.Args) != 2 {
			return expr.Rendering{}, fmt.Errorf("stuntgres: invalid POSITION syntax provider")
		}
		spec.Pieces, err = SyntaxPieces(name, len(e.Args))
	case parser.OverlayStringSyntax, parser.SubstringStringSyntax:
		spec.Pieces, err = SyntaxPieces(name, len(e.Args))
	case parser.NormalizeStringSyntax:
		spec.Pieces = []expr.Piece{text("NORMALIZE("), arg(0)}
		if len(e.Args) == 2 {
			spec.Pieces = append(spec.Pieces, text(", "), expr.ValuePiece(1))
		}
		spec.Pieces = append(spec.Pieces, text(")"))
	case parser.NormalizedStringSyntax:
		spec.Pieces = []expr.Piece{text("("), arg(0), text(" IS ")}
		if len(e.Args) == 2 {
			spec.Pieces = append(spec.Pieces, expr.ValuePiece(1), text(" "))
		}
		spec.Pieces = append(spec.Pieces, text("NORMALIZED)"))
	default:
		err = fmt.Errorf("stuntgres: invalid special string syntax")
	}
	if err != nil {
		return expr.Rendering{}, err
	}
	return spec, nil
}

// callRendering displays an ordinary call or the ^@ prefix operator. Textual
// arguments display the provider's resolved formal conversion.
func (e String) callRendering(tree string) (expr.Rendering, error) {
	name := e.Plan.Name()
	args := renderArgs(e.Args)
	for i, arg := range e.Args {
		args[i].Listed = e.Syntax == parser.OrdinaryStringSyntax
		if target := e.Plan.ArgumentUsage(i); e.Textual[i] && arg.ResultUsage().Type != target.Type {
			args[i].Cast, args[i].CastForm = target, expr.CastLeafOrCoercion
		}
		if name == "parse_ident" && !e.Textual[0] {
			args[i].Label = "strict"
			if e.Textual[i] {
				args[i].Label = "str"
			}
		}
	}
	if e.Syntax == parser.PrefixStringSyntax {
		return expr.Rendering{Tree: tree, Inputs: e.Args, Form: expr.FormInfix, Name: "^@", Namespace: "pg_catalog", Args: args}, nil
	}
	quote := expr.QuoteVerbatim
	var replay string
	switch name {
	case "normalize", "overlay":
		replay = name
		name = `"` + name + `"`
	case "left", "right", "substring":
		name = `"` + name + `"`
	case "position":
		quote = expr.QuoteIdentifier
	}
	return expr.Rendering{Tree: tree, Inputs: e.Args, Form: expr.FormCall, Name: name, Quote: quote, LabelQuote: expr.QuoteVerbatim, Args: args, Replay: expr.Replay{Name: replay}}, nil
}

// SameGroup implements expr.GroupIdentity.
func (e String) SameGroup(other expr.Expr, same func(a, b expr.Expr) bool, definition bool) bool {
	o, ok := other.(String)
	if !ok || e.Plan != o.Plan || len(e.Args) != len(o.Args) {
		return false
	}
	for i := range e.Args {
		if !same(e.Args[i], o.Args[i]) {
			return false
		}
	}
	return true
}

// LooksLikeFunction implements expr.FunctionSyntax.
func (e String) LooksLikeFunction() bool { return e.Plan.Name() != "||" }

// Substitute implements expr.Substitutable.
func (e String) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("String", children, len(e.Args)); err != nil {
		return nil, err
	}
	e.Info, e.Args = info, children
	return e, nil
}

// ClosureTransparent implements expr.ClosureTransparent.
func (e String) ClosureTransparent() {}

func (e String) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction && kind != expr.CaptureOperator {
		return expr.CaptureOperands{}, false, nil
	}
	args := e.Children()
	return expr.CaptureOperands{Args: args, Count: len(args)}, true, nil
}
