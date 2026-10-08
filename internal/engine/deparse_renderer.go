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
	view     CatalogRead
	relation *relationDescriptor
	pretty   bool
}

func pgIdentifier(name string) string {
	if parser.IdentifierNeedsQuotes(name) {
		return quoteIdentifier(name)
	}
	return name
}

func pgType(u sqltype.Usage) (string, error) {
	declaration, err := u.Declaration()
	if err != nil {
		return "", err
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

func (r pgRenderer) cast(child string, usage sqltype.Usage, simple bool) (string, error) {
	typ, err := pgType(usage)
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
	typ, err := pgType(u)
	if err != nil {
		return "", err
	}
	if v.IsNull() {
		return "NULL::" + typ, nil
	}
	text, err := v.Type().Output(ctx, v)
	if err != nil {
		return "", err
	}
	quote := func(s string) string {
		if strings.Contains(s, "\\") {
			return commentLiteral(s)
		}
		return "'" + strings.ReplaceAll(s, "'", "''") + "'"
	}
	switch u.Type.ID() {
	case sqltype.Bool:
		return text, nil
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
			if relation, ok := r.view.state.descriptors[ObjectAddress{ClassID: RelationClassID, ObjectID: OID(id)}].(*relationDescriptor); ok {
				text = pgIdentifier(relation.name)
				if namespace := r.view.state.descriptors[relation.namespace]; namespace != nil && namespace.Name() != "public" {
					text = pgIdentifier(namespace.Name()) + "." + text
				}
			}
		}
		return quote(text) + "::regclass", nil
	case sqltype.Text, sqltype.Name, sqltype.Char, sqltype.BPChar, sqltype.VarChar, sqltype.Bytea, sqltype.UUID, sqltype.Date, sqltype.Time, sqltype.Timetz, sqltype.Timestamp, sqltype.Timestamptz, sqltype.Interval, sqltype.NameArray:
		return quote(text) + "::" + typ, nil
	default:
		return "", fmt.Errorf("stuntgres: unsupported deparse constant type %d", u.Type.ID())
	}
}

// Parent contexts only affect pretty presentation. Arithmetic operators remain
// parenthesized like ruleutils even in pretty mode; Boolean precedence is explicit.
func (r pgRenderer) sql(ctx context.Context, node BoundExpr, parent int) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if node == nil || node.ResultUsage().Type == nil {
		return "", fmt.Errorf("stuntgres: missing deparse node")
	}
	child := func(n BoundExpr) (string, error) { return r.sql(ctx, n, 0) }
	function := func(name string, args []BoundExpr) (string, error) {
		parts := make([]string, len(args))
		for i, arg := range args {
			text, err := child(arg)
			if err != nil {
				return "", err
			}
			parts[i] = text
		}
		return name + "(" + strings.Join(parts, ", ") + ")", nil
	}
	unary := func(n BoundExpr, suffix string) (string, error) {
		text, err := r.sql(ctx, n, 4)
		if err != nil {
			return "", err
		}
		text += suffix
		if !r.pretty || parent > 4 {
			text = "(" + text + ")"
		}
		return text, nil
	}
	binary := func(left, right BoundExpr, op string, precedence int, always bool) (string, error) {
		a, err := r.sql(ctx, left, precedence)
		if err != nil {
			return "", err
		}
		b, err := r.sql(ctx, right, precedence)
		if err != nil {
			return "", err
		}
		text := a + " " + op + " " + b
		if always || !r.pretty || parent > precedence {
			text = "(" + text + ")"
		}
		return text, nil
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
	case columnExpression:
		return deparseAttribute(r.relation, n.origin.Address.SubID)
	case coercionExpression:
		text, err := child(n.child)
		if err != nil {
			return "", err
		}
		if n.mode == sqltype.Implicit {
			// Const coercions have type-owned annotations; casts of Vars for operator
			// resolution are invisible in PostgreSQL presentation.
			if _, ok := n.child.(literalExpression); !ok {
				return text, nil
			}
			if n.child.ResultUsage().Type.ID() == n.ResultUsage().Type.ID() {
				return text, nil
			}
		}
		if n.mode == sqltype.Assignment {
			return text, nil
		}
		_, simpleColumn := n.child.(columnExpression)
		_, simpleLiteral := n.child.(literalExpression)
		return r.cast(text, n.ResultUsage(), simpleColumn || simpleLiteral)
	case arithmeticExpression:
		ops := [...]string{"+", "-", "*", "/", "%"}
		if int(n.operator) >= len(ops) {
			return "", fmt.Errorf("stuntgres: invalid deparse arithmetic operator")
		}
		op := operator(n.namespace, ops[n.operator])
		if n.right == nil {
			text, err := child(n.left)
			return "(" + op + " " + text + ")", err
		}
		return binary(n.left, n.right, op, 5, true)
	case bitwiseExpression:
		ops := [...]string{"&", "|", "#", "~", "<<", ">>"}
		if int(n.operator) >= len(ops) {
			return "", fmt.Errorf("stuntgres: invalid deparse bitwise operator")
		}
		op := operator(n.namespace, ops[n.operator])
		if n.right == nil {
			text, err := child(n.left)
			return "(" + op + " " + text + ")", err
		}
		return binary(n.left, n.right, op, 5, true)
	case comparisonExpression:
		return binary(n.left, n.right, operator(n.namespace, n.operator), 4, false)
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
		op := "IS DISTINCT FROM"
		if n.not {
			op = "IS NOT DISTINCT FROM"
		}
		return binary(n.left, n.right, op, 4, false)
	case logicalExpression:
		if n.op == parser.LogicalNot {
			text, err := r.sql(ctx, n.left, 3)
			if err != nil {
				return "", err
			}
			text = "NOT " + text
			if !r.pretty || parent > 3 {
				text = "(" + text + ")"
			}
			return text, nil
		}
		op, precedence := "AND", 2
		if n.op == parser.LogicalOr {
			op, precedence = "OR", 1
		}
		return binary(n.left, n.right, op, precedence, false)
	case conditionalExpression:
		names := [...]string{"COALESCE", "NULLIF", "GREATEST", "LEAST"}
		if int(n.kind) >= len(names) {
			return "", fmt.Errorf("stuntgres: invalid deparse conditional")
		}
		return function(names[n.kind], n.args)
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
		if !structuralNull(n.otherwise) {
			otherwise, err := child(n.otherwise)
			if err != nil {
				return "", err
			}
			text.WriteString("\n    ELSE " + otherwise)
		}
		text.WriteString("\nEND")
		return text.String(), nil
	case mathExpression:
		return function(n.plan.Name(), n.args)
	case randomExpression:
		return function(n.name, n.args)
	case nullCountExpression:
		name := "num_nulls"
		if n.kind == countNonnulls {
			name = "num_nonnulls"
		}
		return function(name, n.args)
	case commentInquiryExpression:
		name := "obj_description"
		if n.kind == columnDescription {
			name = "col_description"
		}
		if n.kind == sharedDescription {
			name = "shobj_description"
		}
		return function(name, n.args)
	case currentSchemasExpression:
		return function("current_schemas", []BoundExpr{n.arg})
	case sequenceCallExpression:
		return function(n.name, n.args)
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
		return function(name, n.args)
	default:
		return "", fmt.Errorf("stuntgres: unsupported deparse node %T", node)
	}
}
