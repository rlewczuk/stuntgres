package engine

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type pgRenderer struct {
	view               CatalogRead
	relation           *relationDescriptor
	pretty             bool
	path               []ObjectAddress
	output             sqltype.OutputEnvironment
	nonstandardStrings bool
	showImplicit       bool
}

func pgIdentifier(name string) string {
	if parser.IdentifierNeedsQuotes(name) {
		return quoteIdentifier(name)
	}
	return name
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
	if n.usage.Type == nil || n.usage.Type.ID() != typ || n.usage != n.usage.Type.Usage() ||
		n.properties != (ExprProperties{Volatility: Stable}) || n.unknownNull {
		return fmt.Errorf("stuntgres: invalid deparse identity metadata")
	}
	return nil
}

func pgType(u sqltype.Usage) (string, error) {
	declaration, err := u.Declaration()
	if err != nil {
		return "", err
	}
	if u.Type.IsDomain() {
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
	if !u.Type.IsDomain() {
		return pgType(u)
	}
	d, ok := r.view.state.descriptors[ObjectAddress{ClassID: TypeClassID, ObjectID: OID(u.Type.ID())}].(*typeDescriptor)
	if !ok || d.typ != u.Type {
		return "", fmt.Errorf("stuntgres: invalid deparse domain authority")
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

func (r pgRenderer) literal(ctx context.Context, n literalExpression) (string, error) {
	u := n.ResultUsage()
	v := n.value
	if n.unknownNull && (!v.IsNull() || u.Type == nil || u.Type.ID() != sqltype.Text) {
		return "", fmt.Errorf("stuntgres: invalid unknown NULL provenance")
	}
	typ, err := r.typeSQL(u)
	if err != nil {
		return "", err
	}
	if v.IsNull() {
		if n.unknownNull {
			return "NULL::unknown", nil
		}
		return "NULL::" + typ, nil
	}
	text, err := v.Type().OutputWithEnvironment(ctx, v, r.output)
	if err != nil {
		return "", err
	}
	quote := func(s string) string {
		if r.nonstandardStrings {
			s = strings.ReplaceAll(s, "\\", "\\\\")
		}
		return "'" + strings.ReplaceAll(s, "'", "''") + "'"
	}
	if u.Type.IsDomain() {
		if u.Type.BaseType().ID() == sqltype.Regclass {
			id, err := strconv.ParseUint(text, 10, 32)
			if err != nil {
				return "", err
			}
			text = regclassSpelling(r.view, r.path, OID(id))
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
	case sqltype.Int2, sqltype.Int8, sqltype.Oid:
		return quote(text) + "::" + typ, nil
	case sqltype.Float4, sqltype.Float8:
		return quote(text) + "::" + typ, nil
	case sqltype.Regclass:
		if id, err := strconv.ParseUint(text, 10, 32); err == nil {
			text = regclassSpelling(r.view, r.path, OID(id))
		}
		return quote(text) + "::regclass", nil
	case sqltype.Text, sqltype.Name, sqltype.Char, sqltype.BPChar, sqltype.VarChar, sqltype.Bytea, sqltype.UUID, sqltype.Date, sqltype.Time, sqltype.Timetz, sqltype.Timestamp, sqltype.Timestamptz, sqltype.Interval, sqltype.NameArray:
		return quote(text) + "::" + typ, nil
	default:
		return "", fmt.Errorf("stuntgres: unsupported deparse constant type %d", u.Type.ID())
	}
}

// A zero parent is a top-level or syntax-separated expression. Operator parents
// additionally distinguish operand side so pretty output preserves the tree.
type pgRenderParent struct {
	precedence int
	operator   string
	right      bool
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
	if parent.precedence == 0 {
		return false
	}
	if !binary {
		return true
	}
	parentPriority := pgOperatorPriority(parent.operator)
	if parentPriority == 0 {
		return true
	}
	childPriority := pgOperatorPriority(op)
	return childPriority == 0 || childPriority < parentPriority || childPriority == parentPriority && parent.right
}

func (r pgRenderer) sql(ctx context.Context, node BoundExpr, parent pgRenderParent) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if node == nil || node.ResultUsage().Type == nil {
		return "", fmt.Errorf("stuntgres: missing deparse node")
	}
	showImplicit := r.showImplicit
	r.showImplicit = false
	child := func(n BoundExpr) (string, error) { return r.sql(ctx, n, pgRenderParent{}) }
	function := func(name string, args []BoundExpr, implicit bool) (string, error) {
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
	unary := func(n BoundExpr, suffix string) (string, error) {
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
	comparisonChild := func(node, other BoundExpr) (string, error) {
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
	binary := func(left, right BoundExpr, op string, precedence int, enclosing pgRenderParent) (string, error) {
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
	operator := func(namespace, spelling string) string {
		if namespace == "" || namespace == "pg_catalog" {
			return spelling
		}
		return "OPERATOR(" + pgIdentifier(namespace) + "." + spelling + ")"
	}
	switch n := node.(type) {
	case literalExpression:
		return r.literal(ctx, n)
	case identityExpression:
		if err := validateDeparseIdentity(n); err != nil {
			return "", err
		}
		if n.ordinaryCall {
			return pgIdentifier(n.kind.spelling()) + "()", nil
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
	case columnExpression:
		return deparseAttribute(r.relation, n.origin.Address.SubID)
	case collationExpression:
		text, err := child(n.child)
		if err != nil {
			return "", err
		}
		return "(" + text + " COLLATE " + pgIdentifier(builtinCollationName(n.usage.Collation.ID)) + ")", nil
	case coercionExpression:
		if literal, ok := n.child.(literalExpression); ok && n.mode == sqltype.Explicit &&
			literal.usage.Type == n.usage.Type && n.usage.Typmod >= 0 {
			literal.usage = n.usage
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
		if n.mode == sqltype.Assignment {
			return r.sql(ctx, n.child, parent)
		}
		text, err := child(n.child)
		if err != nil {
			return "", err
		}
		_, simpleColumn := n.child.(columnExpression)
		_, simpleLiteral := n.child.(literalExpression)
		return r.cast(text, n.ResultUsage(), simpleColumn || simpleLiteral)
	case arithmeticExpression:
		ops := [...]string{"+", "-", "*", "/", "%", "^"}
		if int(n.operator) >= len(ops) {
			return "", fmt.Errorf("stuntgres: invalid deparse arithmetic operator")
		}
		op := operator(n.namespace, ops[n.operator])
		if n.right == nil {
			text, err := r.sql(ctx, n.left, pgRenderParent{precedence: 5, operator: op})
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
			text, err := r.sql(ctx, n.left, pgRenderParent{precedence: 5, operator: op})
			text = op + " " + text
			if !r.pretty || pgOperatorGrouped(op, false, parent) {
				text = "(" + text + ")"
			}
			return text, err
		}
		return binary(n.left, n.right, op, 5, parent)
	case likeExpression:
		return binary(n.left, n.right, operator(n.namespace, n.operator), 5, parent)
	case likeEscapeExpression:
		pattern, err := child(n.pattern)
		if err != nil {
			return "", err
		}
		escape, err := child(n.escape)
		if err != nil {
			return "", err
		}
		return "pg_catalog.like_escape(" + pattern + ", " + escape + ")", nil
	case comparisonExpression:
		return binary(n.left, n.right, operator(n.namespace, n.operator), 4, parent)
	case nullTestExpression:
		suffix := " IS NULL"
		if n.not {
			suffix = " IS NOT NULL"
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
	case mathExpression:
		return function(n.plan.Name(), n.args, true)
	case stringExpression:
		if n.plan.Name() == "||" {
			return binary(n.args[0], n.args[1], "||", 5, parent)
		}
		if n.syntax == parser.OrdinaryStringSyntax {
			name := n.plan.Name()
			if name == "normalize" {
				name = `"normalize"`
			} else if name == "position" {
				name = pgIdentifier(name)
			}
			parts := make([]string, len(n.args))
			for i, arg := range n.args {
				argRenderer := r
				argRenderer.showImplicit = true
				text, err := argRenderer.sql(ctx, arg, pgRenderParent{})
				if err != nil {
					return "", err
				}
				// Present the provider's resolved formal conversion without
				// evaluating or adding a second runtime conversion.
				target := n.plan.ArgumentUsage(i)
				if n.textual[i] && arg.ResultUsage().Type != target.Type {
					simple := false
					switch arg.(type) {
					case columnExpression, literalExpression, coercionExpression:
						simple = true
					}
					text, err = r.cast(text, target, simple)
					if err != nil {
						return "", err
					}
				}
				parts[i] = text
			}
			return name + "(" + strings.Join(parts, ", ") + ")", nil
		}
		argRenderer := r
		argRenderer.showImplicit = false
		source, err := argRenderer.sql(ctx, n.args[0], pgRenderParent{})
		if err != nil {
			return "", err
		}
		set := ""
		if (n.syntax == parser.TrimStringSyntax || n.syntax == parser.PositionStringSyntax) && len(n.args) == 2 {
			set, err = argRenderer.sql(ctx, n.args[1], pgRenderParent{})
			if err != nil {
				return "", err
			}
		}
		return n.specialSQL(source, set)
	case binaryExpression:
		if err := n.validateSyntax(); err != nil {
			return "", err
		}
		if n.plan.Name() == "||" {
			return binary(n.args[0], n.args[1], "||", 5, parent)
		}
		if n.syntax == parser.SQLFunctionSyntax {
			return renderBinaryCall(n.plan.Name(), n.syntax, n.args, n.plan.Name(), child)
		}
		return function(pgIdentifier(n.plan.Name()), n.args, true)
	case randomExpression:
		return function(n.name, n.args, true)
	case nullCountExpression:
		name := "num_nulls"
		if n.kind == countNonnulls {
			name = "num_nonnulls"
		}
		return function(name, n.args, true)
	case visibilityInquiryExpression:
		u := n.ResultUsage()
		if n.kind.class() == 0 || u != u.Type.Usage() || u.Type.ID() != sqltype.Bool || n.arg == nil {
			return "", fmt.Errorf("stuntgres: invalid stored visibility inquiry signature")
		}
		argument := n.arg.ResultUsage()
		if argument.Type == nil || argument != argument.Type.Usage() || argument.Type.ID() != sqltype.Oid {
			return "", fmt.Errorf("stuntgres: invalid stored visibility inquiry argument")
		}
		properties := n.arg.Properties()
		properties.Volatility = max(Stable, properties.Volatility)
		if n.Properties() != properties {
			return "", fmt.Errorf("stuntgres: invalid stored visibility inquiry properties")
		}
		return function(n.kind.name(), []BoundExpr{n.arg}, true)
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
		return function("current_schemas", []BoundExpr{n.arg}, true)
	case sequenceCallExpression:
		return function(n.name, n.args, true)
	case timeZoneExpression:
		text, err := child(n.value)
		if err != nil {
			return "", err
		}
		if n.local {
			return "(" + text + " AT LOCAL)", nil
		}
		zone, err := child(n.zone)
		if err != nil {
			return "", err
		}
		return "(" + text + " AT TIME ZONE " + zone + ")", nil
	case deparseExpression:
		name := "pg_get_expr"
		if n.constraint {
			name = "pg_get_constraintdef"
		}
		return function(name, n.args, true)
	default:
		return "", fmt.Errorf("stuntgres: unsupported deparse node %T", node)
	}
}
