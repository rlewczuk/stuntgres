package engine

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type pgRenderer struct {
	view               CatalogRead
	relation           *catalog.Relation
	pretty             bool
	path               []ids.ObjectAddress
	output             sqltype.OutputEnvironment
	nonstandardStrings bool
	showImplicit       bool
	unknownTextLiteral bool
	columnSQL          func(columnExpression) (string, error)
	nodeSQL            func(expr.Expr) (string, bool, error)
}

func pgIdentifier(name string) string {
	if parser.IdentifierNeedsQuotes(name) {
		return quoteIdentifier(name)
	}
	return name
}

func (r pgRenderer) identifier(name string) string {
	if r.output.QuoteAllIdentifiers {
		return quoteIdentifier(name)
	}
	return pgIdentifier(name)
}

func (r pgRenderer) attribute(relation *catalog.Relation, attr int32) (string, error) {
	text, err := deparseAttribute(relation, attr)
	if err == nil && r.output.QuoteAllIdentifiers && !strings.HasPrefix(text, "\"") {
		text = quoteIdentifier(text)
	}
	return text, err
}

func validateDeparseIdentity(n identityExpression) error {
	if n.kind > identityCurrentSchema {
		return fmt.Errorf("stuntgres: invalid deparse identity kind")
	}
	if n.ordinaryCall {
		switch n.kind {
		case identityDatabase, identityCurrentUser, identitySessionUser, identitySystemUser, identityCurrentSchema:
		default:
			return fmt.Errorf("stuntgres: invalid deparse identity call")
		}
	} else if n.kind == identityDatabase {
		return fmt.Errorf("stuntgres: invalid deparse identity keyword")
	}
	typ := sqltype.Name
	if n.kind == identitySystemUser {
		typ = sqltype.Text
	}
	if n.Usage.Type == nil || n.Usage.Type.ID() != typ || n.Usage != n.Usage.Type.Usage() ||
		n.Property != (expr.Properties{Volatility: expr.Stable}) || n.UnknownNull {
		return fmt.Errorf("stuntgres: invalid deparse identity metadata")
	}
	return nil
}

func pgType(u sqltype.Usage) (string, error) {
	declaration, err := u.Declaration()
	if err != nil {
		return "", err
	}
	if u.Type.IsUserDefined() || u.Type.IsDomain() {
		return declaration, nil
	}
	switch u.Type.ID() {
	case sqltype.Text:
		return "text", nil
	case sqltype.Bool:
		return "boolean", nil
	case sqltype.Time, sqltype.Timestamp:
		return strings.ToLower(declaration) + " without time zone", nil
	case sqltype.VarChar:
		return strings.Replace(strings.ToLower(declaration), "varchar", "character varying", 1), nil
	case sqltype.Char:
		return `"char"`, nil
	case sqltype.BPChar:
		if u.Typmod < 0 {
			return "bpchar", nil
		}
	}
	return strings.ToLower(declaration), nil
}

func (r pgRenderer) typeSQL(u sqltype.Usage) (string, error) {
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
		text, err := pgType(u)
		if err == nil && r.output.QuoteAllIdentifiers {
			switch u.Type.ID() {
			case sqltype.Text, sqltype.Name, sqltype.Bytea, sqltype.Oid, sqltype.Regclass, sqltype.Regtype, sqltype.UUID, sqltype.JSON, sqltype.JSONB:
				text = r.identifier(text)
			}
		}
		return text, err
	}
	d, ok := r.view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(u.Type.ID())})
	if !ok || d.SQLType != u.Type {
		return "", fmt.Errorf("stuntgres: invalid deparse domain authority")
	}
	if r.output.QuoteAllIdentifiers {
		name := r.identifier(d.Name())
		for _, ns := range r.path {
			if a, ok := r.view.LookupName(ids.NameKey{ClassID: ids.TypeClassID, Namespace: ns, Name: d.Name()}); ok {
				if a == d.Address() {
					return name, nil
				}
				break
			}
		}
		return r.identifier(r.view.Get(d.Namespace()).Name()) + "." + name, nil
	}
	return typeDiagnosticNameAt(r.view, d, r.path), nil
}

func (r pgRenderer) cast(child string, usage sqltype.Usage, simple bool) (string, error) {
	typ, err := r.typeSQL(usage)
	if err != nil {
		return "", err
	}
	if !r.pretty || !simple {
		child = "(" + child + ")"
	}
	return child + "::" + typ, nil
}

// Captured literals can already carry a cast in their canonical presentation.
// Numeric's emitted annotation is authoritative: integer-spelled, sized and
// special values are annotated, while genuinely bare decimal literals are not.
func pgLiteralSimple(n literalExpression, rendered string) bool {
	if n.value.IsNull() || n.Usage.Type.IsUserDefined() || n.Usage.Type.IsDomain() {
		return false
	}
	switch n.Usage.Type.ID() {
	case sqltype.Bool:
		return true
	case sqltype.Int4:
		value, err := n.value.Integer()
		return err == nil && value >= 0
	case sqltype.Numeric:
		return !strings.Contains(rendered, "::")
	default:
		return false
	}
}

func (r pgRenderer) literal(ctx context.Context, n literalExpression) (string, error) {
	u := n.ResultUsage()
	v := n.value
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
	output := r.output
	// Persisted literal text must remain lossless even when SQL presentation
	// requests rounded floats, including nested array and domain leaves.
	output.FloatPrecisionSet = false
	if v.Type().BaseType().IsArray() && v.Type().ContainsRegclass() {
		output.Regclass = catalogRegclassOutput{view: r.view, path: r.path}
	}
	if v.Type().ContainsRegtype() {
		output.Regtype = catalogRegtypeOutput{view: r.view, path: r.path}
	}
	if v.Type().ContainsRegObject() {
		if output.Regcollation == nil {
			output.Regcollation = dumpCatalogAliasOutput{view: r.view}
		}
		if output.Regnamespace == nil {
			output.Regnamespace = dumpCatalogAliasOutput{view: r.view}
		}
	}
	if v.Type().ContainsRegrole() && output.Regrole == nil {
		output.Regrole = regroleContextFormatter(ctx)
	}
	if v.Type().ContainsOperatorAlias() {
		output.Operator = dumpOperatorOutput{}
	}
	if v.Type().ContainsRegproc() {
		output.Regproc = catalogRoutineOutput{view: r.view, path: r.path}
	}
	if v.Type().ContainsRegTS() {
		output.RegTS = dumpRegTSOutput{view: r.view}
	}
	if v.Type().ContainsACLItem() && output.ACLItem == nil {
		output.ACLItem = aclItemContextFormatter(ctx)
	}
	if containsEmptyACL(v) {
		return catalogLiteralWithACL(ctx, r.view, n, output.ACLItem)
	}
	if sqltype.IsRoutineAlias(v.Type().BaseType().ID()) {
		text, err := routineLiteralSQL(r.view, v)
		if err != nil {
			return "", err
		}
		if u.Type.IsDomain() {
			text = "(" + text + ")::" + typ
		}
		return text, nil
	}
	if sqltype.IsOperatorAlias(v.Type().BaseType().ID()) && !r.pretty {
		return operatorLiteralSQL(r.view, v)
	}
	if v.Type().ContainsRegproc() || v.Type().ContainsOperatorAlias() {
		return catalogLiteralSQL(ctx, r.view, n)
	}
	text, err := v.Type().OutputWithEnvironment(ctx, v, output)
	if err != nil {
		return "", err
	}
	quote := func(s string) string {
		if r.nonstandardStrings {
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
			text = regclassSpelling(r.view, r.path, ids.OID(id))
		}
		return quote(text) + "::" + typ, nil
	}
	switch u.Type.ID() {
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
			text = regclassSpelling(r.view, r.path, ids.OID(id))
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
	case sqltype.Tid:
		return quote(text) + "::" + typ, nil
	case sqltype.ACLItem, sqltype.Inet, sqltype.Cidr, sqltype.Name, sqltype.Char, sqltype.BPChar, sqltype.VarChar, sqltype.Bytea, sqltype.UUID, sqltype.JSON, sqltype.JSONB, sqltype.Date, sqltype.Time, sqltype.Timestamp, sqltype.Timestamptz, sqltype.Interval:
		return quote(text) + "::" + typ, nil
	default:
		return "", fmt.Errorf("stuntgres: unsupported deparse constant type %d", u.Type.ID())
	}
}

// A zero parent is a top-level or syntax-separated expression. Operator parents
// additionally distinguish operand side and unary parents to preserve the tree.
type pgRenderParent struct {
	precedence int
	operator   string
	right      bool
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

func pgOperatorGrouped(op string, binary bool, parent pgRenderParent) bool {
	// Every binary scalar operator binds more tightly than AND/OR/NOT.
	if parent.precedence == 0 || binary && parent.precedence < 4 {
		return false
	}
	if !binary || parent.unary {
		return true
	}
	parentPriority := pgOperatorPriority(parent.operator)
	if parentPriority == 0 {
		return true
	}
	childPriority := pgOperatorPriority(op)
	return childPriority == 0 || childPriority < parentPriority || childPriority == parentPriority && parent.right
}

func (r pgRenderer) sql(ctx context.Context, node expr.Expr, parent pgRenderParent) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if node == nil || node.ResultUsage().Type == nil {
		return "", fmt.Errorf("stuntgres: missing deparse node")
	}
	if r.nodeSQL != nil {
		if text, handled, err := r.nodeSQL(node); handled || err != nil {
			return text, err
		}
	}
	showImplicit := r.showImplicit
	r.showImplicit = false
	child := func(n expr.Expr) (string, error) { return r.sql(ctx, n, pgRenderParent{}) }
	function := func(name string, args []expr.Expr, implicit bool) (string, error) {
		if r.output.QuoteAllIdentifiers && !strings.HasPrefix(name, "\"") {
			name = r.identifier(name)
		}
		parts := make([]string, len(args))
		for i, arg := range args {
			argRenderer := r
			argRenderer.showImplicit = implicit
			text, err := argRenderer.sql(ctx, arg, pgRenderParent{})
			if err != nil {
				return "", err
			}
			parts[i] = text
		}
		return name + "(" + strings.Join(parts, ", ") + ")", nil
	}
	unary := func(n expr.Expr, suffix string) (string, error) {
		text, err := r.sql(ctx, n, pgRenderParent{precedence: 5})
		if err != nil {
			return "", err
		}
		text += suffix
		if !r.pretty || parent.precedence > 4 {
			text = "(" + text + ")"
		}
		return text, nil
	}
	// Numeric comparison providers accept mixed scalar types without storing
	// coercion nodes. Reconstruct the contextual Const cast that PostgreSQL
	// records for integer operands of numeric/floating comparisons.
	comparisonChild := func(node, other expr.Expr) (string, error) {
		text, err := r.sql(ctx, node, pgRenderParent{precedence: 5})
		if err != nil {
			return "", err
		}
		if _, constant := node.(literalExpression); constant {
			id, target := node.ResultUsage().Type.ID(), other.ResultUsage().Type.Usage()
			if (id == sqltype.Int2 || id == sqltype.Int4 || id == sqltype.Int8) &&
				(target.Type.ID() == sqltype.Numeric || target.Type.ID() == sqltype.Float8) {
				return r.cast(text, target, true)
			}
		}
		return text, nil
	}
	binary := func(left, right expr.Expr, op string, precedence int, enclosing pgRenderParent) (string, error) {
		var a, b string
		var err error
		if precedence == 4 {
			a, err = comparisonChild(left, right)
		} else {
			a, err = r.sql(ctx, left, pgRenderParent{precedence: precedence, operator: op})
		}
		if err != nil {
			return "", err
		}
		if precedence == 4 {
			b, err = comparisonChild(right, left)
		} else {
			b, err = r.sql(ctx, right, pgRenderParent{precedence: precedence, operator: op, right: true})
		}
		if err != nil {
			return "", err
		}
		text := a + " " + op + " " + b
		grouped := !r.pretty
		if r.pretty {
			grouped = enclosing.precedence > precedence
			if precedence == 5 {
				grouped = pgOperatorGrouped(op, true, enclosing)
			}
		}
		if grouped {
			text = "(" + text + ")"
		}
		return text, nil
	}
	logicalNot := func(text string) string {
		text = "NOT " + text
		if !r.pretty || parent.precedence > 3 || parent.operator == "NOT" {
			text = "(" + text + ")"
		}
		return text
	}
	operator := r.operatorSpelling
	switch n := node.(type) {
	case literalExpression:
		return r.literal(ctx, n)
	case identityExpression:
		if err := validateDeparseIdentity(n); err != nil {
			return "", err
		}
		if n.ordinaryCall {
			return r.identifier(n.kind.spelling()) + "()", nil
		}
		switch n.kind {
		case identityCatalog:
			return "CURRENT_CATALOG", nil
		case identityRole:
			return "CURRENT_ROLE", nil
		case identityCurrentUser:
			return "CURRENT_USER", nil
		case identitySessionUser:
			return "SESSION_USER", nil
		case identityUser:
			return "USER", nil
		case identitySystemUser:
			return "SYSTEM_USER", nil
		case identityCurrentSchema:
			return "CURRENT_SCHEMA", nil
		default:
			return "", fmt.Errorf("stuntgres: invalid deparse identity keyword")
		}
	case domainValueExpression:
		return "VALUE", nil
	case columnExpression:
		if r.columnSQL != nil {
			return r.columnSQL(n)
		}
		return r.attribute(r.relation, n.origin.Address.SubID)
	case tableOIDExpression:
		return "tableoid", nil
	case rowExpression:
		parts := make([]string, len(n.fields))
		for i, field := range n.fields {
			text, err := child(field)
			if err != nil {
				return "", err
			}
			parts[i] = text
		}
		text := "ROW(" + strings.Join(parts, ", ") + ")"
		if n.Usage.Type.ID() != 2249 {
			return r.cast(text, n.Usage, true)
		}
		return text, nil
	case fieldExpression:
		text, err := child(n.base)
		if err != nil {
			return "", err
		}
		index, ok := n.base.ResultUsage().Type.BaseType().CompositeAttributeIndex(n.attribute)
		if !ok {
			return "", fmt.Errorf("stuntgres: retired deparse field")
		}
		return "(" + text + ")." + pgIdentifier(n.base.ResultUsage().Type.BaseType().CompositeMember(index).Name), nil
	case arrayExpression:
		parts := make([]string, len(n.elements))
		for i, element := range n.elements {
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
	case subscriptExpression:
		text, err := child(n.base)
		if err != nil {
			return "", err
		}
		text = "(" + text + ")"
		for _, s := range n.subscripts {
			text += "["
			if s.lower != nil {
				part, err := child(s.lower)
				if err != nil {
					return "", err
				}
				text += part
			}
			if s.slice {
				text += ":"
			}
			if s.upper != nil {
				part, err := child(s.upper)
				if err != nil {
					return "", err
				}
				text += part
			}
			text += "]"
		}
		return text, nil
	case collationExpression:
		text, err := child(n.child)
		if err != nil {
			return "", err
		}
		text += " COLLATE " + collationDisplayName(r.view, n.Usage.Collation.ID, r.path)
		if r.pretty && parent.functionArgument {
			return text, nil
		}
		return "(" + text + ")", nil
	case coercionExpression:
		if constructor, ok := n.child.(functions.SQLJSON); ok && n.plan.IsJSONReturning() {
			return r.sqlJSON(ctx, constructor, n.Usage)
		}
		// Identifier leaves already retain their scalar annotation. A transparent
		// relabel must not duplicate it around a genuine enclosing conversion.
		if n.child != nil && sqltype.IsTransactionIdentifier(n.Usage.Type.ID()) && n.child.ResultUsage() == n.Usage {
			return r.sql(ctx, n.child, parent)
		}
		if literal, ok := n.child.(literalExpression); ok && n.mode == sqltype.Explicit &&
			literal.Usage.Type == n.Usage.Type && n.Usage.Typmod >= 0 {
			literal.Usage = n.Usage
			return r.literal(ctx, literal)
		}
		if n.mode == sqltype.Implicit && !showImplicit {
			// Const coercions have type-owned annotations; casts of Vars for operator
			// resolution are invisible in PostgreSQL presentation.
			if _, ok := n.child.(literalExpression); !ok {
				return r.sql(ctx, n.child, parent)
			}
			if n.child.ResultUsage().Type.ID() == n.ResultUsage().Type.ID() {
				return r.sql(ctx, n.child, parent)
			}
		}
		if n.mode == sqltype.Assignment && !sqlJSONResult(n.child) {
			return r.sql(ctx, n.child, parent)
		}
		text, err := child(n.child)
		if err != nil {
			return "", err
		}
		_, simpleColumn := n.child.(columnExpression)
		_, simpleLiteral := n.child.(literalExpression)
		_, castChild := n.child.(coercionExpression)
		simpleFunctionCall := false
		if call, ok := n.child.(expr.Renderable); ok {
			spec, err := call.Render()
			simpleFunctionCall = err == nil && spec.SimpleCast
		}
		return r.cast(text, n.ResultUsage(), simpleColumn || simpleLiteral || simpleFunctionCall || r.pretty && parent.functionArgument && castChild)
	case selectedOperatorExpression:
		d, ok := r.view.Operator(n.ref.Address)
		if !ok {
			return "", fmt.Errorf("stuntgres: missing selected operator during deparse")
		}
		namespace, ok := r.view.Namespace(d.Namespace())
		if !ok {
			return "", fmt.Errorf("stuntgres: missing selected operator namespace")
		}
		op := "OPERATOR(" + quoteIdentifier(namespace.Name()) + "." + d.Name() + ")"
		var operands [2]expr.Expr
		count := 0
		boundExpressionChildren(n.provider, func(arg expr.Expr) {
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
	case arithmeticExpression:
		ops := [...]string{"+", "-", "*", "/", "%", "^"}
		if int(n.operator) >= len(ops) {
			return "", fmt.Errorf("stuntgres: invalid deparse arithmetic operator")
		}
		op := operator(n.namespace, ops[n.operator])
		if n.right == nil {
			text, err := r.sql(ctx, n.left, pgRenderParent{precedence: 5, operator: op, unary: true})
			text = op + " " + text
			if !r.pretty || pgOperatorGrouped(op, false, parent) {
				text = "(" + text + ")"
			}
			return text, err
		}
		return binary(n.left, n.right, op, 5, parent)
	case bitwiseExpression:
		ops := [...]string{"&", "|", "#", "~", "<<", ">>"}
		if int(n.operator) >= len(ops) {
			return "", fmt.Errorf("stuntgres: invalid deparse bitwise operator")
		}
		op := operator(n.namespace, ops[n.operator])
		if n.right == nil {
			text, err := r.sql(ctx, n.left, pgRenderParent{precedence: 5, operator: op, unary: true})
			text = op + " " + text
			if !r.pretty || pgOperatorGrouped(op, false, parent) {
				text = "(" + text + ")"
			}
			return text, err
		}
		return binary(n.left, n.right, op, 5, parent)
	case likeExpression:
		return binary(n.left, n.right, operator(n.namespace, n.operator), 5, parent)
	case rowComparisonExpression:
		spelling := n.operator
		if !n.distinct() {
			if len(n.comparisons) == 0 {
				return "", fmt.Errorf("stuntgres: invalid row comparison")
			}
			spelling = operator(n.comparisons[0].namespace, n.operator)
		}
		return renderRowComparison(n, child, spelling)
	case scalarArrayComparisonExpression:
		return renderScalarArrayComparison(n, child, r.identifier)
	case seriesExpression:
		argRenderer := r
		argRenderer.showImplicit = true
		args := make([]string, len(n.args))
		for i, arg := range n.args {
			var err error
			args[i], err = argRenderer.sql(ctx, arg, pgRenderParent{})
			if err != nil {
				return "", err
			}
		}
		return pgIdentifier("generate_series") + "(" + strings.Join(args, ", ") + ")", nil
	case subscriptsExpression:
		argRenderer := r
		argRenderer.showImplicit = true
		args := make([]string, len(n.args))
		for i, arg := range n.args {
			var err error
			args[i], err = argRenderer.sql(ctx, arg, pgRenderParent{})
			if err != nil {
				return "", err
			}
		}
		return pgIdentifier("generate_subscripts") + "(" + strings.Join(args, ", ") + ")", nil
	case unnestExpression:
		argRenderer := r
		argRenderer.showImplicit = true
		arg, err := argRenderer.sql(ctx, n.args[0], pgRenderParent{})
		if err != nil {
			return "", err
		}
		name := pgIdentifier("unnest")
		if n.procedure != nil {
			if err := validateIntaggIdentity(r.view, n.procedure, n.ref, "int_array_enum"); err != nil {
				return "", err
			}
			namespace, ok := r.view.Namespace(n.procedure.Namespace())
			if !ok {
				return "", fmt.Errorf("stuntgres: missing intagg namespace")
			}
			name = pgIdentifier(namespace.Name()) + "." + pgIdentifier(n.procedure.Name())
		}
		return name + "(" + arg + ")", nil
	case comparisonExpression:
		return binary(n.left, n.right, operator(n.namespace, n.operator), 4, parent)
	case nullTestExpression:
		suffix := " IS NULL"
		if n.not {
			suffix = " IS NOT NULL"
		}
		if n.whole && n.child.ResultUsage().Type.BaseType().IsComposite() {
			suffix = " IS NOT DISTINCT FROM NULL"
			if n.not {
				suffix = " IS DISTINCT FROM NULL"
			}
		}
		return unary(n.child, suffix)
	case truthTestExpression:
		truth := [...]string{"FALSE", "TRUE", "UNKNOWN"}
		if int(n.truth) >= len(truth) {
			return "", fmt.Errorf("stuntgres: invalid deparse truth")
		}
		suffix := " IS "
		if n.not {
			suffix += "NOT "
		}
		return unary(n.child, suffix+truth[n.truth])
	case distinctExpression:
		enclosing := parent
		if n.not {
			enclosing = pgRenderParent{precedence: 3, operator: "NOT"}
		}
		text, err := binary(n.left, n.right, "IS DISTINCT FROM", 4, enclosing)
		if n.not && err == nil {
			text = logicalNot(text)
		}
		return text, err
	case logicalExpression:
		if n.op == parser.LogicalNot {
			text, err := r.sql(ctx, n.left, pgRenderParent{precedence: 3, operator: "NOT"})
			if err != nil {
				return "", err
			}
			return logicalNot(text), nil
		}
		op, precedence := "AND", 2
		if n.op == parser.LogicalOr {
			op, precedence = "OR", 1
		}
		return binary(n.left, n.right, op, precedence, parent)
	case conditionalExpression:
		names := [...]string{"COALESCE", "NULLIF", "GREATEST", "LEAST"}
		if int(n.kind) >= len(names) {
			return "", fmt.Errorf("stuntgres: invalid deparse conditional")
		}
		return function(names[n.kind], n.args, false)
	case caseExpression:
		var text strings.Builder
		text.WriteString("\nCASE")
		if n.test != nil {
			test, err := child(n.test)
			if err != nil {
				return "", err
			}
			text.WriteByte(' ')
			text.WriteString(test)
		}
		for _, arm := range n.arms {
			condition, err := child(arm.condition)
			if err != nil {
				return "", err
			}
			result, err := child(arm.result)
			if err != nil {
				return "", err
			}
			text.WriteString("\n    WHEN " + condition + " THEN " + result)
		}
		otherwise, err := child(n.otherwise)
		if err != nil {
			return "", err
		}
		text.WriteString("\n    ELSE " + otherwise)
		text.WriteString("\nEND")
		return text.String(), nil
	case catalogUserNameExpression:
		args := [1]expr.Expr{n.arg}
		return function("pg_get_userbyid", args[:], true)
	case catalogForeignKeysExpression:
		return function("pg_get_catalog_foreign_keys", nil, true)
	case transactionIdentityExpression:
		if n.arg == nil {
			return function(n.kind.name(), nil, true)
		}
		args := [1]expr.Expr{n.arg}
		return function(n.kind.name(), args[:], true)
	case commitTimestampExpression:
		if n.arg == nil {
			return function(n.kind.name(), nil, true)
		}
		args := [1]expr.Expr{n.arg}
		return function(n.kind.name(), args[:], true)
	case advisoryExpression:
		return function(n.name, n.args, true)
	case installedSoundexFunctionExpression:
		namespace, ok := r.view.Namespace(n.procedure.Namespace())
		if !ok {
			return "", fmt.Errorf("stuntgres: missing Soundex function namespace")
		}
		return function(r.identifier(namespace.Name())+"."+r.identifier(n.procedure.Name()), n.Args, true)
	case installedMetaphoneFunctionExpression:
		namespace, ok := r.view.Namespace(n.procedure.Namespace())
		if !ok {
			return "", fmt.Errorf("stuntgres: missing Metaphone function namespace")
		}
		return function(r.identifier(namespace.Name())+"."+r.identifier(n.procedure.Name()), n.Args, true)
	case installedLevenshteinFunctionExpression:
		namespace, ok := r.view.Namespace(n.procedure.Namespace())
		if !ok {
			return "", fmt.Errorf("stuntgres: missing Levenshtein function namespace")
		}
		return function(r.identifier(namespace.Name())+"."+r.identifier(n.procedure.Name()), n.Args, true)
	case installedUUIDOsspFunctionExpression:
		if err := n.validate(r.view); err != nil {
			return "", err
		}
		namespace, ok := r.view.Namespace(n.procedure.Namespace())
		if !ok {
			return "", fmt.Errorf("stuntgres: missing uuid-ossp function namespace")
		}
		return function(r.identifier(namespace.Name())+"."+r.identifier(n.procedure.Name()), nil, true)
	case installedCryptoFunctionExpression:
		namespace, ok := r.view.Namespace(n.procedure.Namespace())
		if !ok {
			return "", fmt.Errorf("stuntgres: missing crypto function namespace")
		}
		return function(r.identifier(namespace.Name())+"."+r.identifier(n.procedure.Name()), n.Args, true)
	case installedFunctionExpression:
		namespace, ok := r.view.Namespace(n.procedure.Namespace())
		if !ok {
			return "", fmt.Errorf("stuntgres: missing installed function namespace")
		}
		return function(r.identifier(namespace.Name())+"."+r.identifier(n.procedure.Name()), []expr.Expr{n.Arg}, true)
	case functions.SQLJSON:
		return r.sqlJSON(ctx, n, sqltype.Usage{})
	case visibilityInquiryExpression:
		u := n.ResultUsage()
		if n.kind.class() == 0 || u.Type == nil || u != u.Type.Usage() || u.Type.ID() != n.kind.result() || n.arg == nil {
			return "", fmt.Errorf("stuntgres: invalid stored visibility inquiry signature")
		}
		argument := n.arg.ResultUsage()
		if argument.Type == nil || argument != argument.Type.Usage() || argument.Type.ID() != n.kind.formal() {
			return "", fmt.Errorf("stuntgres: invalid stored visibility inquiry argument")
		}
		properties := n.arg.Properties()
		properties.Volatility = max(n.kind.volatility(), properties.Volatility)
		if n.Properties() != properties {
			return "", fmt.Errorf("stuntgres: invalid stored visibility inquiry properties")
		}
		return function(n.kind.name(), []expr.Expr{n.arg}, true)
	case commentInquiryExpression:
		name := "obj_description"
		if n.kind == columnDescription {
			name = "col_description"
		}
		if n.kind == sharedDescription {
			name = "shobj_description"
		}
		return function(name, n.args, true)
	case currentSchemasExpression:
		return function("current_schemas", []expr.Expr{n.arg}, true)
	case objectAddressExpression:
		return n.sql(r.identifier(n.op.name()), func(arg expr.Expr) (string, error) {
			argRenderer := r
			argRenderer.showImplicit = true
			return argRenderer.sql(ctx, arg, pgRenderParent{})
		})
	case aclValueExpression:
		return n.sql(func(arg expr.Expr) (string, error) {
			argRenderer := r
			argRenderer.showImplicit = true
			return argRenderer.sql(ctx, arg, pgRenderParent{})
		})
	case objectACLExpression:
		return n.sql(r.identifier("pg_get_acl"), func(arg expr.Expr) (string, error) {
			argRenderer := r
			argRenderer.showImplicit = true
			return argRenderer.sql(ctx, arg, pgRenderParent{})
		})
	case accessInquiryExpression:
		if err := n.validateStored(); err != nil {
			return "", err
		}
		return function(n.name(), n.arguments(), true)
	case namespaceInquiryExpression:
		if err := n.validateStored(); err != nil {
			return "", err
		}
		return function(n.kind.name(), n.arguments(), true)
	case blockingInquiryExpression:
		if err := n.validateStored(); err != nil {
			return "", err
		}
		return function(n.kind.name(), n.arguments(), true)
	case sequenceCallExpression:
		return function(n.name, n.args, true)
	case indexPropertyExpression:
		if err := validateStoredIndexPropertyExpression(n); err != nil {
			return "", err
		}
		return function(n.kind.name(), n.children(), true)
	case deparseExpression:
		if err := validateDeparseExpression(n); err != nil {
			return "", err
		}
		return function(deparseSignatures[n.signature].name, n.args, true)
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
