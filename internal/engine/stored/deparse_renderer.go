package stored

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type Deparser struct {
	View               catalog.Read
	Relation           *catalog.Relation
	Pretty             bool
	Path               []ids.ObjectAddress
	Output             sqltype.OutputEnvironment
	NonstandardStrings bool
	ShowImplicit       bool
	unknownTextLiteral bool
	ColumnSQL          func(plan.ColumnExpr) (string, error)
	NodeSQL            func(expr.Expr) (string, bool, error)
}

func (r Deparser) Identifier(name string) string {
	if r.Output.QuoteAllIdentifiers {
		return catalog.QuoteIdentifier(name)
	}
	return catalog.Identifier(name)
}

func (r Deparser) Attribute(relation *catalog.Relation, attr int32) (string, error) {
	text, err := DeparseAttribute(relation, attr)
	if err == nil && r.Output.QuoteAllIdentifiers && !strings.HasPrefix(text, "\"") {
		text = catalog.QuoteIdentifier(text)
	}
	return text, err
}

func (r Deparser) typeSQL(u sqltype.Usage) (string, error) {
	if !u.Type.IsDomain() && u.Type.IsArray() {
		element := u
		element.Type = u.Type.Element()
		typ, err := r.typeSQL(element)
		if err != nil {
			return "", err
		}
		return typ + "[]", nil
	}
	if !u.Type.IsUserDefined() && !u.Type.IsDomain() {
		text, err := analyze.PGType(u)
		if err == nil && r.Output.QuoteAllIdentifiers {
			switch u.Type.ID() {
			case sqltype.Text, sqltype.Name, sqltype.Bytea, sqltype.Oid, sqltype.Regclass, sqltype.Regtype, sqltype.UUID, sqltype.JSON, sqltype.JSONB:
				text = r.Identifier(text)
			}
		}
		return text, err
	}
	d, ok := r.View.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(u.Type.ID())})
	if !ok || d.SQLType != u.Type {
		return "", fmt.Errorf("stuntgres: invalid deparse domain authority")
	}
	if r.Output.QuoteAllIdentifiers {
		name := r.Identifier(d.Name())
		for _, ns := range r.Path {
			if a, ok := r.View.LookupName(ids.NameKey{ClassID: ids.TypeClassID, Namespace: ns, Name: d.Name()}); ok {
				if a == d.Address() {
					return name, nil
				}
				break
			}
		}
		return r.Identifier(r.View.Get(d.Namespace()).Name()) + "." + name, nil
	}
	return catalog.TypeDiagnosticNameAt(r.View, d, r.Path), nil
}

func (r Deparser) cast(child string, usage sqltype.Usage, simple bool) (string, error) {
	typ, err := r.typeSQL(usage)
	if err != nil {
		return "", err
	}
	if !r.Pretty || !simple {
		child = "(" + child + ")"
	}
	return child + "::" + typ, nil
}

// Captured literals can already carry a cast in their canonical presentation.
// Numeric's emitted annotation is authoritative: integer-spelled, sized and
// special values are annotated, while genuinely bare decimal literals are not.
func pgLiteralSimple(n plan.LiteralExpr, rendered string) bool {
	if n.Value.IsNull() || n.Usage.Type.IsUserDefined() || n.Usage.Type.IsDomain() {
		return false
	}
	switch n.Usage.Type.ID() {
	case sqltype.Bool:
		return true
	case sqltype.Int4:
		value, err := n.Value.Integer()
		return err == nil && value >= 0
	case sqltype.Numeric:
		return !strings.Contains(rendered, "::")
	default:
		return false
	}
}

func (r Deparser) Literal(ctx context.Context, n plan.LiteralExpr) (string, error) {
	u := n.ResultUsage()
	v := n.Value
	if n.UnknownNull && (!v.IsNull() || u.Type == nil || u.Type.ID() != sqltype.Text) {
		return "", fmt.Errorf("stuntgres: invalid unknown NULL provenance")
	}
	typ, err := r.typeSQL(u)
	if err != nil {
		return "", err
	}
	if v.IsNull() {
		if n.UnknownNull {
			return "NULL::unknown", nil
		}
		return "NULL::" + typ, nil
	}
	if regobj.ExactFloat4Dump(ctx) && regobj.ContainsFloat4(u.Type) {
		return regobj.Literal(ctx, r.View, n)
	}
	if v.Type().BaseType().ID() == sqltype.Path {
		// Use the same exact persistence route as row dumps, not NaN-canonical
		// SQL display. PATH coordinate text contains no quoting metacharacters.
		literal, err := v.Type().SQLLiteral(ctx, v)
		if err != nil {
			return "", err
		}
		return literal + "::" + typ, nil
	}
	output := r.Output
	// Persisted literal text must remain lossless even when SQL presentation
	// requests rounded floats, including nested array and domain leaves.
	output.FloatPrecisionSet = false
	if v.Type().BaseType().IsArray() && v.Type().ContainsRegclass() {
		output.Regclass = regobj.ClassOutput{View: r.View, Path: r.Path}
	}
	if v.Type().ContainsRegtype() {
		output.Regtype = regobj.TypeOutput{View: r.View, Path: r.Path}
	}
	if v.Type().ContainsRegObject() {
		if output.Regcollation == nil {
			output.Regcollation = regobj.DumpAliasOutput{View: r.View}
		}
		if output.Regnamespace == nil {
			output.Regnamespace = regobj.DumpAliasOutput{View: r.View}
		}
	}
	if v.Type().ContainsRegrole() && output.Regrole == nil {
		output.Regrole = regobj.RoleContextFormatter(ctx)
	}
	if v.Type().ContainsOperatorAlias() {
		output.Operator = regobj.DumpOperatorOutput{}
	}
	if v.Type().ContainsRegproc() {
		output.Regproc = regobj.RoutineOutput{View: r.View, Path: r.Path}
	}
	if v.Type().ContainsRegTS() {
		output.RegTS = regobj.DumpTextSearchOutput{View: r.View}
	}
	if v.Type().ContainsACLItem() && output.ACLItem == nil {
		output.ACLItem = regobj.ACLItemContextFormatter(ctx)
	}
	if regobj.ContainsEmptyACL(v) || v.RequiresCircleConstructorReplay() {
		return regobj.LiteralWithACL(ctx, r.View, n, output.ACLItem)
	}
	if sqltype.IsRoutineAlias(v.Type().BaseType().ID()) {
		text, err := regobj.RoutineLiteral(r.View, v)
		if err != nil {
			return "", err
		}
		if u.Type.IsDomain() {
			text = "(" + text + ")::" + typ
		}
		return text, nil
	}
	if sqltype.IsOperatorAlias(v.Type().BaseType().ID()) && !r.Pretty {
		return regobj.OperatorLiteral(r.View, v)
	}
	if v.Type().ContainsRegproc() || v.Type().ContainsOperatorAlias() {
		return regobj.Literal(ctx, r.View, n)
	}
	text, err := v.Type().OutputWithEnvironment(ctx, v, output)
	if err != nil {
		return "", err
	}
	quote := func(s string) string {
		if r.NonstandardStrings {
			s = strings.ReplaceAll(s, "\\", "\\\\")
		}
		return "'" + strings.ReplaceAll(s, "'", "''") + "'"
	}
	if u.Type.IsArray() {
		return quote(text) + "::" + typ, nil
	}
	if u.Type.IsUserDefined() || u.Type.IsDomain() {
		if u.Type.BaseType().ID() == sqltype.Regclass {
			id, err := strconv.ParseUint(text, 10, 32)
			if err != nil {
				return "", err
			}
			text = regobj.ClassSpelling(r.View, r.Path, ids.OID(id))
		}
		return quote(text) + "::" + typ, nil
	}
	switch u.Type.ID() {
	case sqltype.TSVector, sqltype.TSQuery, sqltype.Box, sqltype.Point, sqltype.Line, sqltype.Lseg:
		return quote(text) + "::" + typ, nil
	case sqltype.Bool:
		value, err := v.Boolean()
		if err != nil {
			return "", err
		}
		return strconv.FormatBool(value), nil
	case sqltype.Int4:
		if strings.HasPrefix(text, "-") {
			return quote(text) + "::integer", nil
		}
		return text, nil
	case sqltype.Numeric:
		if text == "" || text[0] < '0' || text[0] > '9' || !strings.ContainsAny(text, ".eE") {
			return quote(text) + "::" + typ, nil
		}
		if u.Typmod >= 0 {
			return text + "::" + typ, nil
		}
		return text, nil
	case sqltype.Int2, sqltype.Int8, sqltype.Oid, sqltype.Xid, sqltype.Cid, sqltype.Xid8:
		return quote(text) + "::" + typ, nil
	case sqltype.Float4, sqltype.Float8:
		return quote(text) + "::" + typ, nil
	case sqltype.Regclass:
		if id, err := strconv.ParseUint(text, 10, 32); err == nil {
			text = regobj.ClassSpelling(r.View, r.Path, ids.OID(id))
		}
		return quote(text) + "::regclass", nil
	case sqltype.Regtype:
		// Output above resolves the typed identity through the captured
		// catalog/path, including reminted user types after Restore.
		return quote(text) + "::regtype", nil
	case sqltype.Regrole:
		return quote(text) + "::regrole", nil
	case sqltype.Regoper, sqltype.Regoperator, sqltype.Regcollation, sqltype.Regnamespace, sqltype.Regconfig, sqltype.Regdictionary:
		return quote(text) + "::" + typ, nil
	case sqltype.Timetz:
		return datetimeZoneTimetzDeparse(ctx, v, typ, text, quote)
	case sqltype.Text:
		if r.unknownTextLiteral {
			return quote(text), nil
		}
		return quote(text) + "::" + typ, nil
	case sqltype.Tid, sqltype.Macaddr, sqltype.Macaddr8:
		return quote(text) + "::" + typ, nil
	case sqltype.ACLItem, sqltype.Inet, sqltype.Cidr, sqltype.Name, sqltype.Char, sqltype.BPChar, sqltype.VarChar, sqltype.Bytea, sqltype.UUID, sqltype.JSON, sqltype.JSONB, sqltype.Date, sqltype.Time, sqltype.Timestamp, sqltype.Timestamptz, sqltype.Interval:
		return quote(text) + "::" + typ, nil
	default:
		return "", fmt.Errorf("stuntgres: unsupported deparse constant type %d", u.Type.ID())
	}
}

// A zero parent is a top-level or syntax-separated expression. Operator parents
// additionally distinguish operand side and unary parents to preserve the tree.
type DeparseParent struct {
	Precedence int
	Operator   string
	Right      bool
	unary      bool
	// Function arguments need no grouping around COLLATE or a cast chain.
	functionArgument bool
	// Ordered-set direct argument subtrees own contextual typed literals;
	// ordinary function/aggregate operands do not acquire this context.
	orderedSetDirect bool
}

func pgOperatorPriority(op string) int {
	switch op {
	case "+", "-":
		return 1
	case "*", "/", "%":
		return 2
	case "^":
		return 3
	}
	return 0
}

func OperatorGrouped(op string, binary bool, parent DeparseParent) bool {
	// Every binary scalar operator binds more tightly than AND/OR/NOT.
	if parent.Precedence == 0 || binary && parent.Precedence < 4 {
		return false
	}
	if !binary || parent.unary {
		return true
	}
	parentPriority := pgOperatorPriority(parent.Operator)
	if parentPriority == 0 {
		return true
	}
	childPriority := pgOperatorPriority(op)
	return childPriority == 0 || childPriority < parentPriority || childPriority == parentPriority && parent.Right
}

func (r Deparser) SQL(ctx context.Context, node expr.Expr, parent DeparseParent) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if node == nil || node.ResultUsage().Type == nil {
		return "", fmt.Errorf("stuntgres: missing deparse node")
	}
	if r.NodeSQL != nil {
		if text, handled, err := r.NodeSQL(node); handled || err != nil {
			return text, err
		}
	}
	showImplicit := r.ShowImplicit
	r.ShowImplicit = false
	child := func(n expr.Expr) (string, error) { return r.SQL(ctx, n, DeparseParent{}) }
	function := func(name string, args []expr.Expr, implicit bool) (string, error) {
		if r.Output.QuoteAllIdentifiers && !strings.HasPrefix(name, "\"") {
			name = r.Identifier(name)
		}
		parts := make([]string, len(args))
		for i, arg := range args {
			argRenderer := r
			argRenderer.ShowImplicit = implicit
			text, err := argRenderer.SQL(ctx, arg, DeparseParent{})
			if err != nil {
				return "", err
			}
			parts[i] = text
		}
		return name + "(" + strings.Join(parts, ", ") + ")", nil
	}
	unary := func(n expr.Expr, suffix string) (string, error) {
		text, err := r.SQL(ctx, n, DeparseParent{Precedence: 5})
		if err != nil {
			return "", err
		}
		text += suffix
		if !r.Pretty || parent.Precedence > 4 {
			text = "(" + text + ")"
		}
		return text, nil
	}
	// Numeric comparison providers accept mixed scalar types without storing
	// coercion nodes. Reconstruct the contextual Const cast that PostgreSQL
	// records for integer operands of numeric/floating comparisons.
	comparisonChild := func(node, other expr.Expr) (string, error) {
		text, err := r.SQL(ctx, node, DeparseParent{Precedence: 5})
		if err != nil {
			return "", err
		}
		if _, constant := node.(plan.LiteralExpr); constant {
			id, target := node.ResultUsage().Type.ID(), other.ResultUsage().Type.Usage()
			if (id == sqltype.Int2 || id == sqltype.Int4 || id == sqltype.Int8) &&
				(target.Type.ID() == sqltype.Numeric || target.Type.ID() == sqltype.Float8) {
				return r.cast(text, target, true)
			}
		}
		return text, nil
	}
	binary := func(left, right expr.Expr, op string, precedence int, enclosing DeparseParent) (string, error) {
		var a, b string
		var err error
		if precedence == 4 {
			a, err = comparisonChild(left, right)
		} else {
			a, err = r.SQL(ctx, left, DeparseParent{Precedence: precedence, Operator: op})
		}
		if err != nil {
			return "", err
		}
		if precedence == 4 {
			b, err = comparisonChild(right, left)
		} else {
			b, err = r.SQL(ctx, right, DeparseParent{Precedence: precedence, Operator: op, Right: true})
		}
		if err != nil {
			return "", err
		}
		text := a + " " + op + " " + b
		grouped := !r.Pretty
		if r.Pretty {
			grouped = enclosing.Precedence > precedence
			if precedence == 5 {
				grouped = OperatorGrouped(op, true, enclosing)
			}
		}
		if grouped {
			text = "(" + text + ")"
		}
		return text, nil
	}
	logicalNot := func(text string) string {
		text = "NOT " + text
		if !r.Pretty || parent.Precedence > 3 || parent.Operator == "NOT" {
			text = "(" + text + ")"
		}
		return text
	}
	operator := r.operatorSpelling
	switch n := node.(type) {
	case plan.LiteralExpr:
		return r.Literal(ctx, n)
	case plan.ParameterExpr:
		if n.Number <= 0 {
			return "", fmt.Errorf("stuntgres: invalid parameter number")
		}
		return "$" + strconv.Itoa(n.Number), nil
	case plan.MergeActionExpr:
		return "merge_action()", nil
	case plan.EarthCheckExpr:
		return earthCheckSQL(r.View, n)
	case plan.DomainValueExpr:
		return "VALUE", nil
	case plan.ColumnExpr:
		if r.ColumnSQL != nil {
			return r.ColumnSQL(n)
		}
		return r.Attribute(r.Relation, n.Origin.Address.SubID)
	case plan.TableOIDExpr:
		return "tableoid", nil
	case plan.RowExpr:
		parts := make([]string, len(n.Fields))
		for i, field := range n.Fields {
			text, err := child(field)
			if err != nil {
				return "", err
			}
			parts[i] = text
		}
		text := "ROW(" + strings.Join(parts, ", ") + ")"
		if n.Usage.Type.ID() != 2249 {
			typ, err := r.typeSQL(n.Usage)
			if err != nil {
				return "", err
			}
			// ROW(...) already delimits its arguments and is a simple cast
			// operand, including in non-pretty catalog expressions.
			return text + "::" + typ, nil
		}
		return text, nil
	case plan.FieldExpr:
		text, err := child(n.Base)
		if err != nil {
			return "", err
		}
		index, ok := n.Base.ResultUsage().Type.BaseType().CompositeAttributeIndex(n.Attribute)
		if !ok {
			return "", fmt.Errorf("stuntgres: retired deparse field")
		}
		return "(" + text + ")." + catalog.Identifier(n.Base.ResultUsage().Type.BaseType().CompositeMember(index).Name), nil
	case plan.ArrayExpr:
		parts := make([]string, len(n.Elements))
		for i, element := range n.Elements {
			text, err := child(element)
			if err != nil {
				return "", err
			}
			parts[i] = text
		}
		text := "ARRAY[" + strings.Join(parts, ", ") + "]"
		if len(parts) == 0 {
			return r.cast(text, n.ResultUsage(), true)
		}
		return text, nil
	case plan.SubscriptExpr:
		text, err := child(n.Base)
		if err != nil {
			return "", err
		}
		text = "(" + text + ")"
		for _, s := range n.Subscripts {
			text += "["
			if s.Lower != nil {
				part, err := child(s.Lower)
				if err != nil {
					return "", err
				}
				text += part
			}
			if s.Slice {
				text += ":"
			}
			if s.Upper != nil {
				part, err := child(s.Upper)
				if err != nil {
					return "", err
				}
				text += part
			}
			text += "]"
		}
		return text, nil
	case plan.CollationExpr:
		text, err := child(n.Child)
		if err != nil {
			return "", err
		}
		text += " COLLATE " + CollationDisplayName(r.View, n.Usage.Collation.ID, r.Path)
		if r.Pretty && parent.functionArgument {
			return text, nil
		}
		return "(" + text + ")", nil
	case plan.CoercionExpr:
		if n.Plan.IsJSONReturning() {
			spec, supported, err := jsonReturningRendering(n.Child)
			if err != nil {
				return "", err
			}
			if supported {
				return r.renderingWithReturning(ctx, n.Child, spec, parent, n.Usage)
			}
		}
		// Identifier leaves already retain their scalar annotation. A transparent
		// relabel must not duplicate it around a genuine enclosing conversion.
		if n.Child != nil && sqltype.IsTransactionIdentifier(n.Usage.Type.ID()) && n.Child.ResultUsage() == n.Usage {
			return r.SQL(ctx, n.Child, parent)
		}
		if literal, ok := n.Child.(plan.LiteralExpr); ok && n.Mode == sqltype.Explicit &&
			literal.Usage.Type == n.Usage.Type && n.Usage.Typmod >= 0 {
			literal.Usage = n.Usage
			return r.Literal(ctx, literal)
		}
		if n.Mode == sqltype.Implicit && !showImplicit {
			// Const coercions have type-owned annotations; casts of Vars for operator
			// resolution are invisible in PostgreSQL presentation.
			if _, ok := n.Child.(plan.LiteralExpr); !ok {
				return r.SQL(ctx, n.Child, parent)
			}
			if n.Child.ResultUsage().Type.ID() == n.ResultUsage().Type.ID() {
				return r.SQL(ctx, n.Child, parent)
			}
		}
		if n.Mode == sqltype.Assignment && !sqlJSONResult(n.Child) {
			return r.SQL(ctx, n.Child, parent)
		}
		text, err := child(n.Child)
		if err != nil {
			return "", err
		}
		_, simpleColumn := n.Child.(plan.ColumnExpr)
		_, simpleLiteral := n.Child.(plan.LiteralExpr)
		_, castChild := n.Child.(plan.CoercionExpr)
		simpleFunctionCall := false
		if call, ok := n.Child.(expr.Renderable); ok {
			spec, err := call.Render()
			simpleFunctionCall = err == nil && spec.SimpleCast
		}
		return r.cast(text, n.ResultUsage(), simpleColumn || simpleLiteral || simpleFunctionCall || r.Pretty && parent.functionArgument && castChild)
	case plan.SelectedOperatorExpr:
		d, ok := r.View.Operator(n.Ref.Address)
		if !ok {
			return "", fmt.Errorf("stuntgres: missing selected operator during deparse")
		}
		namespace, ok := r.View.Namespace(d.Namespace())
		if !ok {
			return "", fmt.Errorf("stuntgres: missing selected operator namespace")
		}
		op := "OPERATOR(" + catalog.QuoteIdentifier(namespace.Name()) + "." + d.Name() + ")"
		var operands [2]expr.Expr
		count := 0
		plan.ExpressionChildren(n.Provider, func(arg expr.Expr) {
			if arg != nil && count < len(operands) {
				operands[count] = arg
				count++
			}
		})
		if d.Left == 0 {
			if count != 1 {
				return "", fmt.Errorf("stuntgres: invalid selected prefix operands")
			}
			text, err := child(operands[0])
			return "(" + op + " " + text + ")", err
		}
		if count != 2 {
			return "", fmt.Errorf("stuntgres: invalid selected binary operands")
		}
		return binary(operands[0], operands[1], op, 5, parent)
	case plan.ArithmeticExpr:
		ops := [...]string{"+", "-", "*", "/", "%", "^"}
		if int(n.Operator) >= len(ops) {
			return "", fmt.Errorf("stuntgres: invalid deparse arithmetic operator")
		}
		op := operator(n.Namespace, ops[n.Operator])
		if n.Right == nil {
			text, err := r.SQL(ctx, n.Left, DeparseParent{Precedence: 5, Operator: op, unary: true})
			text = op + " " + text
			if !r.Pretty || OperatorGrouped(op, false, parent) {
				text = "(" + text + ")"
			}
			return text, err
		}
		return binary(n.Left, n.Right, op, 5, parent)
	case plan.BitwiseExpr:
		ops := [...]string{"&", "|", "#", "~", "<<", ">>"}
		if int(n.Operator) >= len(ops) {
			return "", fmt.Errorf("stuntgres: invalid deparse bitwise operator")
		}
		op := operator(n.Namespace, ops[n.Operator])
		if n.Right == nil {
			text, err := r.SQL(ctx, n.Left, DeparseParent{Precedence: 5, Operator: op, unary: true})
			text = op + " " + text
			if !r.Pretty || OperatorGrouped(op, false, parent) {
				text = "(" + text + ")"
			}
			return text, err
		}
		return binary(n.Left, n.Right, op, 5, parent)
	case plan.LikeExpr:
		return binary(n.Left, n.Right, operator(n.Namespace, n.Operator), 5, parent)
	case plan.RowComparisonExpr:
		spelling := n.Operator
		if !n.Distinct() {
			if len(n.Comparisons) == 0 {
				return "", fmt.Errorf("stuntgres: invalid row comparison")
			}
			spelling = operator(n.Comparisons[0].Namespace, n.Operator)
		}
		return renderRowComparison(n, child, spelling)
	case plan.ScalarArrayComparisonExpr:
		return renderScalarArrayComparison(n, child, r.Identifier)
	case plan.UnnestExpr:
		if n.Procedure == nil {
			spec, err := n.Render()
			if err != nil {
				return "", err
			}
			return r.rendering(ctx, node, spec, parent)
		}
		argRenderer := r
		argRenderer.ShowImplicit = true
		arg, err := argRenderer.SQL(ctx, n.Args[0], DeparseParent{})
		if err != nil {
			return "", err
		}
		name := catalog.Identifier("unnest")
		if n.Procedure != nil {
			if err := extension.ValidateIntaggIdentity(r.View, n.Procedure, n.Ref, "int_array_enum"); err != nil {
				return "", err
			}
			namespace, ok := r.View.Namespace(n.Procedure.Namespace())
			if !ok {
				return "", fmt.Errorf("stuntgres: missing intagg namespace")
			}
			name = catalog.Identifier(namespace.Name()) + "." + catalog.Identifier(n.Procedure.Name())
		}
		return name + "(" + arg + ")", nil
	case plan.ComparisonExpr:
		return binary(n.Left, n.Right, operator(n.Namespace, n.Operator), 4, parent)
	case plan.NullTestExpr:
		suffix := " IS NULL"
		if n.Not {
			suffix = " IS NOT NULL"
		}
		if n.Whole && n.Child.ResultUsage().Type.BaseType().IsComposite() {
			suffix = " IS NOT DISTINCT FROM NULL"
			if n.Not {
				suffix = " IS DISTINCT FROM NULL"
			}
		}
		return unary(n.Child, suffix)
	case plan.TruthTestExpr:
		truth := [...]string{"FALSE", "TRUE", "UNKNOWN"}
		if int(n.Truth) >= len(truth) {
			return "", fmt.Errorf("stuntgres: invalid deparse truth")
		}
		suffix := " IS "
		if n.Not {
			suffix += "NOT "
		}
		return unary(n.Child, suffix+truth[n.Truth])
	case plan.DistinctExpr:
		enclosing := parent
		if n.Not {
			enclosing = DeparseParent{Precedence: 3, Operator: "NOT"}
		}
		text, err := binary(n.Left, n.Right, "IS DISTINCT FROM", 4, enclosing)
		if n.Not && err == nil {
			text = logicalNot(text)
		}
		return text, err
	case plan.LogicalExpr:
		if n.Op == parser.LogicalNot {
			text, err := r.SQL(ctx, n.Left, DeparseParent{Precedence: 3, Operator: "NOT"})
			if err != nil {
				return "", err
			}
			return logicalNot(text), nil
		}
		op, precedence := "AND", 2
		if n.Op == parser.LogicalOr {
			op, precedence = "OR", 1
		}
		return binary(n.Left, n.Right, op, precedence, parent)
	case plan.ConditionalExpr:
		names := [...]string{"COALESCE", "NULLIF", "GREATEST", "LEAST"}
		if int(n.Kind) >= len(names) {
			return "", fmt.Errorf("stuntgres: invalid deparse conditional")
		}
		return function(names[n.Kind], n.Args, false)
	case plan.CaseExpr:
		var text strings.Builder
		text.WriteString("\nCASE")
		if n.Test != nil {
			test, err := child(n.Test)
			if err != nil {
				return "", err
			}
			text.WriteByte(' ')
			text.WriteString(test)
		}
		for _, arm := range n.Arms {
			condition, err := child(arm.Condition)
			if err != nil {
				return "", err
			}
			result, err := child(arm.Result)
			if err != nil {
				return "", err
			}
			text.WriteString("\n    WHEN " + condition + " THEN " + result)
		}
		otherwise, err := child(n.Otherwise)
		if err != nil {
			return "", err
		}
		text.WriteString("\n    ELSE " + otherwise)
		text.WriteString("\nEND")
		return text.String(), nil
	case plan.SQLRoutineExpr:
		namespace, ok := r.View.Namespace(n.Procedure.Namespace())
		if !ok {
			return "", fmt.Errorf("stuntgres: missing SQL routine namespace")
		}
		return routineCallSQL(n, r.Identifier(namespace.Name())+"."+r.Identifier(n.Procedure.Name()), r.Identifier, func(arg expr.Expr) (string, error) {
			argRenderer := r
			argRenderer.ShowImplicit = true
			return argRenderer.SQL(ctx, arg, DeparseParent{})
		})
	case plan.ExtensionPathsExpr:
		args := [1]expr.Expr{n.Arg}
		return function("pg_extension_update_paths", args[:], true)
	case plan.InstalledCubeExpr:
		if err := ValidateInstalledCube(n, r.View); err != nil {
			return "", err
		}
		namespace, ok := r.View.Namespace(n.Procedure.Namespace())
		if !ok {
			return "", fmt.Errorf("stuntgres: missing cube function namespace")
		}
		return function(r.Identifier(namespace.Name())+"."+r.Identifier(n.Procedure.Name()), n.Args, true)
	case plan.InstalledSoundexExpr:
		namespace, ok := r.View.Namespace(n.Procedure.Namespace())
		if !ok {
			return "", fmt.Errorf("stuntgres: missing Soundex function namespace")
		}
		return function(r.Identifier(namespace.Name())+"."+r.Identifier(n.Procedure.Name()), n.Args, true)
	case plan.InstalledMetaphoneExpr:
		namespace, ok := r.View.Namespace(n.Procedure.Namespace())
		if !ok {
			return "", fmt.Errorf("stuntgres: missing Metaphone function namespace")
		}
		return function(r.Identifier(namespace.Name())+"."+r.Identifier(n.Procedure.Name()), n.Args, true)
	case plan.InstalledLevenshteinExpr:
		namespace, ok := r.View.Namespace(n.Procedure.Namespace())
		if !ok {
			return "", fmt.Errorf("stuntgres: missing Levenshtein function namespace")
		}
		return function(r.Identifier(namespace.Name())+"."+r.Identifier(n.Procedure.Name()), n.Args, true)
	case plan.InstalledUUIDOsspExpr:
		if err := ValidateInstalledUUIDOssp(n, r.View); err != nil {
			return "", err
		}
		namespace, ok := r.View.Namespace(n.Procedure.Namespace())
		if !ok {
			return "", fmt.Errorf("stuntgres: missing uuid-ossp function namespace")
		}
		return function(r.Identifier(namespace.Name())+"."+r.Identifier(n.Procedure.Name()), n.Args, true)
	case plan.InstalledCryptoExpr:
		namespace, ok := r.View.Namespace(n.Procedure.Namespace())
		if !ok {
			return "", fmt.Errorf("stuntgres: missing crypto function namespace")
		}
		return function(r.Identifier(namespace.Name())+"."+r.Identifier(n.Procedure.Name()), n.Args, true)
	case plan.InstalledCubeSpatialExpr:
		if err := extension.ValidateCubeSpatialCall(r.View, n.Procedure, n.CubeSpatial); err != nil {
			return "", err
		}
		namespace, ok := r.View.Namespace(n.Procedure.Namespace())
		if !ok {
			return "", fmt.Errorf("stuntgres: missing cube spatial function namespace")
		}
		return function(r.Identifier(namespace.Name())+"."+r.Identifier(n.Procedure.Name()), n.Args[:], true)
	case plan.InstalledTextExpr:
		namespace, ok := r.View.Namespace(n.Procedure.Namespace())
		if !ok {
			return "", fmt.Errorf("stuntgres: missing installed function namespace")
		}
		return function(r.Identifier(namespace.Name())+"."+r.Identifier(n.Procedure.Name()), []expr.Expr{n.Arg}, true)
	case plan.InstalledPgrowlocksExpr:
		if err := ValidateInstalledPgrowlocks(n, r.View); err != nil {
			return "", err
		}
		namespace, ok := r.View.Namespace(n.Procedure.Namespace())
		if !ok {
			return "", fmt.Errorf("stuntgres: missing pgrowlocks namespace")
		}
		return function(r.Identifier(namespace.Name())+"."+r.Identifier(n.Procedure.Name()), []expr.Expr{n.Arg}, true)
	case expr.Renderable:
		// Function nodes describe themselves; engine wrappers that embed one
		// keep their explicit cases above.
		spec, err := n.Render()
		if err != nil {
			return "", err
		}
		return r.rendering(ctx, node, spec, parent)
	default:
		return "", fmt.Errorf("stuntgres: unsupported deparse node %T", node)
	}
}

func renderScalarArrayComparison(n plan.ScalarArrayComparisonExpr, child func(expr.Expr) (string, error), identifier func(string) string) (string, error) {
	left, err := child(n.Left)
	if err != nil {
		return "", err
	}
	var right string
	if n.Array != nil {
		right, err = child(n.Array)
		if err != nil {
			return "", err
		}
	} else {
		items := make([]string, len(n.Items))
		for i, item := range n.Items {
			items[i], err = child(item)
			if err != nil {
				return "", err
			}
		}
		if n.Left.ResultUsage().Type.IsCube() && n.RoutineRef != (ids.ObjectRef{}) && (n.Operator == "=" && !n.All || n.Operator == "<>" && n.All) {
			membership := "IN"
			if n.All {
				membership = "NOT IN"
			}
			return "(" + left + " " + membership + " (" + strings.Join(items, ", ") + "))", nil
		}
		right = "ARRAY[" + strings.Join(items, ", ") + "]"
	}
	operator := n.Operator
	if n.Namespace != "" {
		operator = "OPERATOR(" + identifier(n.Namespace) + "." + operator + ")"
	}
	quantifier := "ANY"
	if n.All {
		quantifier = "ALL"
	}
	return "(" + left + " " + operator + " " + quantifier + " (" + right + "))", nil
}

func renderRowComparison(e plan.RowComparisonExpr, operand func(expr.Expr) (string, error), operator string) (string, error) {
	if len(e.Comparisons) == 0 && !e.Distinct() {
		return "", fmt.Errorf("stuntgres: invalid stored row comparison")
	}
	left, right := make([]string, len(e.Comparisons)), make([]string, len(e.Comparisons))
	for i, pair := range e.Comparisons {
		var err error
		left[i], err = operand(pair.Left)
		if err != nil {
			return "", err
		}
		right[i], err = operand(pair.Right)
		if err != nil {
			return "", err
		}
	}
	constructor := ""
	if len(e.Comparisons) < 2 {
		constructor = "ROW"
	}
	return "(" + constructor + "(" + strings.Join(left, ", ") + ") " + operator + " " + constructor + "(" + strings.Join(right, ", ") + "))", nil
}
