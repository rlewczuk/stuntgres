package engine

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Published definitions contain immutable typed nodes, never evaluated values
// or caller-owned syntax. Attribute keys survive descriptor replacements.
type columnDefault struct {
	expression BoundExpr
	sql        string
}

func (b *expressionBinder) bindDefault(raw parser.Expr, column ColumnDescriptor) (BoundExpr, error) {
	// Recursive binding carries this consumer policy through Boolean and
	// comparison children, preserving their ordinary diagnostic order.
	previous := b.defaultConsumer
	b.defaultConsumer = true
	defer func() { b.defaultConsumer = previous }()
	node, err := b.bind(raw, expressionPolicy{context: projectionContext, expected: column.Usage.Type.Usage()})
	if err != nil {
		return nil, err
	}
	if node.ResultUsage() != column.Usage {
		plan, err := sqltype.PlanCoercion(node.ResultUsage(), column.Usage, sqltype.Assignment)
		if err != nil {
			if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "42804" {
				d := sqlError("42804", fmt.Sprintf(`column "%s" is of type %s but default expression is of type %s`, column.Name, column.Usage.Type.DiagnosticName(), node.ResultUsage().Type.DiagnosticName())).(*sqlstate.Error)
				d.Hint = "You will need to rewrite or cast the expression."
				return nil, d
			}
			return nil, err
		}
		node = coercionExpression{b.info(column.Usage, parser.Position{}, coercionProperties(node, plan)), node, plan, sqltype.Assignment}
	}
	return node, nil
}

func bindCreateDefaults(b *expressionBinder, columns []ColumnDescriptor, declarations []createColumnDeclaration) (map[int32]columnDefault, error) {
	var defaults map[int32]columnDefault
	view := CatalogRead{owner: b.engine, state: b.engine.state}
	for slot := range declarations {
		raw := declarations[slot].defaultExpr
		if raw == nil {
			continue
		}
		node, err := b.bindDefault(raw, columns[slot])
		if err != nil {
			return nil, err
		}
		canonical, err := defaultSQL(b.ctx, view, node)
		if err != nil {
			return nil, err
		}
		if defaults == nil {
			defaults = make(map[int32]columnDefault)
		}
		defaults[int32(slot+1)] = columnDefault{node, canonical}
	}
	return defaults, nil
}

// One recursive serializer validates persisted scalar trees without Eval.
// DEFAULT admits closed Immutable/Stable trees; CHECK admits its actual owner
// row and existing volatile providers. Assignment wrappers keep deferred input.
type scalarRenderer struct {
	owner           *relationDescriptor
	dependencies    *[]ObjectRef
	commentSnapshot *bool
	// TYPE reparsing transports concrete literals privately, without reinferring
	// unknown input or changing ordinary DEFAULT/CHECK serialization.
	checkLiterals *[]literalExpression
	// Final CHECK replay may compare a retained REAL literal with a widened
	// column. Preserve its exact promoted value, not shortest float32 input.
	checkReplayPrecision bool
}

func defaultSQL(ctx context.Context, view CatalogRead, node BoundExpr) (string, error) {
	return (scalarRenderer{}).sql(ctx, view, node)
}

func (r scalarRenderer) sql(ctx context.Context, view CatalogRead, node BoundExpr) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if node == nil {
		return "", fmt.Errorf("stuntgres: missing default node")
	}
	u, p := node.ResultUsage(), node.Properties()
	if u.Type == nil || !u.Valid() || !u.Type.Concrete() || r.owner == nil && (p.Volatility > Stable || p.RowDependent) || p.ParameterDependent || p.StageDependent {
		return "", fmt.Errorf("stuntgres: invalid stored default properties")
	}
	typ, ok := view.LookupType(u.Type.ID())
	if !ok || typ != u.Type {
		return "", fmt.Errorf("stuntgres: foreign default type")
	}
	r.record(ObjectRef{owner: view.owner.owner, Epoch: view.Epoch(), Address: ObjectAddress{ClassID: TypeClassID, ObjectID: OID(u.Type.ID())}})
	switch n := node.(type) {
	case columnExpression:
		if r.owner == nil || n.origin.owner != view.owner.owner || n.origin.Epoch != view.Epoch() || n.origin.Address.ClassID != r.owner.address.ClassID || n.origin.Address.ObjectID != r.owner.address.ObjectID || n.scope != 1 || n.frame != 1 || n.slot < 0 || n.slot >= len(r.owner.columns) || r.owner.attributes[n.slot] != n.origin.Address.SubID || u != r.owner.columns[n.slot].Usage {
			return "", fmt.Errorf("stuntgres: invalid stored row origin")
		}
		r.record(n.origin)
		return quoteIdentifier(r.owner.columns[n.slot].Name), nil
	case randomExpression:
		if r.owner == nil {
			return "", fmt.Errorf("stuntgres: volatile stored default")
		}
		argumentType := sqltype.Float8
		valid := false
		switch n.name {
		case "random":
			if len(n.args) == 0 {
				valid = u.Type.ID() == sqltype.Float8
			}
			if len(n.args) == 2 {
				argumentType = u.Type.ID()
				valid = argumentType == sqltype.Int4 || argumentType == sqltype.Int8 || argumentType == sqltype.Numeric
			}
		case "random_normal":
			valid = len(n.args) <= 2 && u.Type.ID() == sqltype.Float8
		case "setseed":
			valid = len(n.args) == 1 && u.Type.ID() == sqltype.Void
		}
		if !valid {
			return "", fmt.Errorf("stuntgres: invalid stored random signature")
		}
		for _, arg := range n.args {
			if arg == nil || arg.ResultUsage().Type == nil || arg.ResultUsage().Type.ID() != argumentType {
				return "", fmt.Errorf("stuntgres: invalid stored random argument")
			}
		}
		return r.functionSQL(ctx, view, "pg_catalog."+n.name, n.args)
	case mathExpression:
		if len(n.args) == 0 || len(n.args) > 2 {
			return "", fmt.Errorf("stuntgres: invalid stored math arity")
		}
		var usages [2]sqltype.Usage
		for i, arg := range n.args {
			if arg == nil {
				return "", fmt.Errorf("stuntgres: missing math operand")
			}
			usages[i] = arg.ResultUsage()
		}
		plan, err := sqltype.BindMath(n.plan.Name(), u, usages[:len(n.args)]...)
		if err != nil || plan != n.plan {
			return "", fmt.Errorf("stuntgres: invalid stored math provider")
		}
		return r.functionSQL(ctx, view, "pg_catalog."+n.plan.Name(), n.args)
	case nullCountExpression:
		if len(n.args) == 0 || len(n.args) > 100 || u.Type.ID() != sqltype.Int4 {
			return "", fmt.Errorf("stuntgres: invalid stored null count signature")
		}
		name := "num_nulls"
		if n.kind == countNonnulls {
			name = "num_nonnulls"
		} else if n.kind != countNulls {
			return "", fmt.Errorf("stuntgres: invalid stored null count")
		}
		return r.functionSQL(ctx, view, "pg_catalog."+name, n.args)
	case commentInquiryExpression:
		if r.owner == nil {
			return "", fmt.Errorf("stuntgres: comment inquiries are unsupported in stored defaults")
		}
		name, arity, second := "obj_description", 2, sqltype.Name
		switch n.kind {
		case columnDescription:
			name, second = "col_description", sqltype.Int4
		case objectDescription:
		case legacyDescription:
			arity = 1
		case sharedDescription:
			name = "shobj_description"
		default:
			return "", fmt.Errorf("stuntgres: invalid stored comment inquiry kind")
		}
		if len(n.args) != arity || u != u.Type.Usage() || u.Type.ID() != sqltype.Text {
			return "", fmt.Errorf("stuntgres: invalid stored comment inquiry signature")
		}
		properties := ExprProperties{Volatility: Stable}
		for i, arg := range n.args {
			id := sqltype.Oid
			if i == 1 {
				id = second
			}
			if arg == nil || arg.ResultUsage().Type == nil || arg.ResultUsage() != arg.ResultUsage().Type.Usage() || arg.ResultUsage().Type.ID() != id {
				return "", fmt.Errorf("stuntgres: invalid stored comment inquiry argument")
			}
			child := arg.Properties()
			properties.Volatility = max(properties.Volatility, child.Volatility)
			properties.RowDependent = properties.RowDependent || child.RowDependent
			properties.ParameterDependent = properties.ParameterDependent || child.ParameterDependent
			properties.StageDependent = properties.StageDependent || child.StageDependent
		}
		if p != properties {
			return "", fmt.Errorf("stuntgres: invalid stored comment inquiry properties")
		}
		if r.commentSnapshot != nil {
			*r.commentSnapshot = true
		}
		return r.functionSQL(ctx, view, "pg_catalog."+name, n.args)
	case currentSchemasExpression:
		if n.arg == nil || n.arg.ResultUsage().Type == nil || n.arg.ResultUsage().Type.ID() != sqltype.Bool || u.Type.ID() != sqltype.NameArray {
			return "", fmt.Errorf("stuntgres: invalid stored current_schemas signature")
		}
		return r.functionSQL(ctx, view, "pg_catalog.current_schemas", []BoundExpr{n.arg})
	case sequenceCallExpression:
		if r.owner == nil {
			return "", fmt.Errorf("stuntgres: sequence calls are unsupported in stored defaults")
		}
		args := n.args
		want := 1
		switch n.name {
		case "nextval", "currval":
		case "lastval":
			want = 0
		case "setval":
			want = 2
		default:
			return "", fmt.Errorf("stuntgres: invalid stored sequence function")
		}
		if len(args) != want && !(n.name == "setval" && len(args) == 3) || u.Type.ID() != sqltype.Int8 {
			return "", fmt.Errorf("stuntgres: invalid stored sequence signature")
		}
		for i, arg := range args {
			id := sqltype.Regclass
			if i == 1 {
				id = sqltype.Int8
			}
			if i == 2 {
				id = sqltype.Bool
			}
			if arg == nil || arg.ResultUsage().Type == nil || arg.ResultUsage().Type.ID() != id {
				return "", fmt.Errorf("stuntgres: invalid stored sequence argument")
			}
		}
		if n.ref != (ObjectRef{}) {
			if len(args) == 0 {
				return "", fmt.Errorf("stuntgres: stored sequence identity lacks argument")
			}
			if _, err := r.sql(ctx, view, args[0]); err != nil {
				return "", err
			}
			target, ok := view.Descriptor(n.ref.Address)
			if !ok || n.ref.owner != view.owner.owner || n.ref.Epoch != view.Epoch() {
				return "", fmt.Errorf("stuntgres: invalid stored sequence reference")
			}
			sequence, ok := target.(*relationDescriptor)
			if !ok || sequence.relationKind != sequenceRelation {
				return "", fmt.Errorf("stuntgres: invalid stored sequence target")
			}
			r.record(n.ref)
			namespace, ok := view.Descriptor(sequence.namespace)
			if !ok {
				return "", fmt.Errorf("stuntgres: missing stored sequence namespace")
			}
			name := quoteIdentifier(namespace.Name()) + "." + quoteIdentifier(sequence.name)
			parts := []string{"CAST('" + strings.ReplaceAll(name, "'", "''") + "' AS regclass)"}
			for _, arg := range args[1:] {
				text, err := r.sql(ctx, view, arg)
				if err != nil {
					return "", err
				}
				parts = append(parts, text)
			}
			return "pg_catalog." + n.name + "(" + strings.Join(parts, ", ") + ")", nil
		}
		return r.functionSQL(ctx, view, "pg_catalog."+n.name, args)
	case caseExpression, conditionalExpression:
		return r.conditionalSQL(ctx, view, node)
	case identityExpression:
		if n.ordinaryCall {
			return "pg_catalog." + n.kind.spelling() + "()", nil
		}
		return n.kind.spelling(), nil
	case literalExpression:
		if r.checkLiterals != nil {
			*r.checkLiterals = append(*r.checkLiterals, n)
			return fmt.Sprintf("$%d", len(*r.checkLiterals)), nil
		}
		if r.checkReplayPrecision && u.Type.ID() == sqltype.Float4 && !n.value.IsNull() {
			value, err := n.value.Float()
			if err != nil {
				return "", err
			}
			if !math.IsNaN(value) && !math.IsInf(value, 0) {
				return "'" + strconv.FormatFloat(value, 'g', -1, 64) + "'", nil
			}
		}
		return literalSQL(ctx, n)
	case timeZoneExpression:
		value, err := r.sql(ctx, view, n.value)
		if err != nil {
			return "", err
		}
		if n.local {
			return "(" + value + " AT LOCAL)", nil
		}
		zone, err := r.sql(ctx, view, n.zone)
		if err != nil {
			return "", err
		}
		return "(" + value + " AT TIME ZONE " + zone + ")", nil
	case coercionExpression:
		if n.child == nil {
			return "", fmt.Errorf("stuntgres: missing default coercion child")
		}
		switch n.mode {
		case sqltype.Implicit, sqltype.Assignment, sqltype.Explicit:
		default:
			return "", fmt.Errorf("stuntgres: invalid default coercion mode")
		}
		plan, err := planExpressionCoercion(n.child.ResultUsage(), u, n.mode)
		if err != nil || plan != n.plan {
			return "", fmt.Errorf("stuntgres: invalid default coercion")
		}
		child, err := r.sql(ctx, view, n.child)
		if err != nil || n.mode != sqltype.Explicit {
			return child, err
		}
		// Bare quoted TEXT becomes unknown on replay and can move a deferred
		// conversion into CREATE-time input decoding. Keep that child typed.
		if n.child.ResultUsage().Type.ID() == sqltype.Text {
			inner, explicit := n.child.(coercionExpression)
			if !explicit || inner.mode != sqltype.Explicit {
				declaration, err := n.child.ResultUsage().Declaration()
				if err != nil {
					return "", err
				}
				child = "CAST(" + child + " AS " + declaration + ")"
			}
		}
		declaration, err := u.Declaration()
		if err != nil {
			return "", err
		}
		return "CAST(" + child + " AS " + declaration + ")", nil
	case bitwiseExpression:
		if !integralBitwiseType(u.Type) || u != u.Type.Usage() || n.left == nil || n.left.ResultUsage() != u {
			return "", fmt.Errorf("stuntgres: invalid default bitwise operands")
		}
		var rightType *sqltype.Descriptor
		if n.right != nil {
			rightType = n.right.ResultUsage().Type
			if rightType == nil || n.right.ResultUsage() != rightType.Usage() {
				return "", fmt.Errorf("stuntgres: invalid default bitwise right usage")
			}
		}
		provider, err := sqltype.BindBitwise(n.operator, u.Type, rightType)
		if err != nil || provider != n.provider {
			return "", fmt.Errorf("stuntgres: invalid default bitwise binding")
		}
		operator := [...]string{"&", "|", "#", "~", "<<", ">>"}[n.operator]
		if n.namespace != "" {
			operator = "OPERATOR(" + quoteIdentifier(n.namespace) + "." + operator + ")"
		}
		operandSQL := func(child BoundExpr) (string, error) {
			text, err := r.sql(ctx, view, child)
			if err != nil {
				return "", err
			}
			cu := child.ResultUsage()
			if literal, ok := child.(literalExpression); ok && (cu.Type.ID() != sqltype.Int4 || literal.value.IsNull()) {
				declaration, err := cu.Declaration()
				if err != nil {
					return "", err
				}
				text = "CAST(" + text + " AS " + declaration + ")"
			}
			return text, nil
		}
		left, err := operandSQL(n.left)
		if err != nil {
			return "", err
		}
		if n.right == nil {
			return "(" + operator + " " + left + ")", nil
		}
		right, err := operandSQL(n.right)
		if err != nil {
			return "", err
		}
		return "(" + left + " " + operator + " " + right + ")", nil
	case arithmeticExpression:
		if !integerArithmeticType(u.Type) || u != u.Type.Usage() || n.left == nil || n.left.ResultUsage() != u || n.right != nil && n.right.ResultUsage() != u {
			return "", fmt.Errorf("stuntgres: invalid default arithmetic operands")
		}
		provider, err := sqltype.BindArithmetic(n.operator, u.Type, n.right == nil)
		if err != nil || provider != n.provider {
			return "", fmt.Errorf("stuntgres: invalid default arithmetic binding")
		}
		var operator string
		switch n.operator {
		case sqltype.ArithmeticAdd:
			operator = "+"
		case sqltype.ArithmeticSubtract:
			operator = "-"
		case sqltype.ArithmeticMultiply:
			operator = "*"
		case sqltype.ArithmeticDivide:
			operator = "/"
		case sqltype.ArithmeticRemainder:
			operator = "%"
		default:
			return "", fmt.Errorf("stuntgres: invalid default arithmetic operator")
		}
		if n.namespace != "" {
			operator = "OPERATOR(" + quoteIdentifier(n.namespace) + "." + operator + ")"
		}
		operandSQL := func(child BoundExpr) (string, error) {
			text, err := r.sql(ctx, view, child)
			if err != nil {
				return "", err
			}
			// A known BIGINT or typed NULL leaf must not become an INT4 or
			// unknown operand on replay, changing width or unary resolution.
			if literal, ok := child.(literalExpression); ok && (u.Type.ID() == sqltype.Int8 || literal.value.IsNull()) {
				declaration, err := u.Declaration()
				if err != nil {
					return "", err
				}
				text = "CAST(" + text + " AS " + declaration + ")"
			}
			return text, nil
		}
		left, err := operandSQL(n.left)
		if err != nil {
			return "", err
		}
		if n.right == nil {
			return "(" + operator + " " + left + ")", nil
		}
		right, err := operandSQL(n.right)
		if err != nil {
			return "", err
		}
		return "(" + left + " " + operator + " " + right + ")", nil
	case comparisonExpression:
		if u.Type.ID() != sqltype.Bool || n.left == nil || n.right == nil {
			return "", fmt.Errorf("stuntgres: invalid default predicate")
		}
		var operator sqltype.ComparisonOperator
		switch n.operator {
		case "=":
			operator = sqltype.CompareEqual
		case "<>":
			operator = sqltype.CompareNotEqual
		case "<":
			operator = sqltype.CompareLess
		case "<=":
			operator = sqltype.CompareLessEqual
		case ">":
			operator = sqltype.CompareGreater
		case ">=":
			operator = sqltype.CompareGreaterEqual
		default:
			return "", fmt.Errorf("stuntgres: invalid default comparison")
		}
		predicate, err := sqltype.BindComparisonPredicate(operator, n.left.ResultUsage().Type, n.right.ResultUsage().Type, u.Type)
		if err != nil || predicate != n.predicate {
			return "", fmt.Errorf("stuntgres: invalid default predicate binding")
		}
		left, err := r.comparisonOperandSQL(ctx, view, n.left)
		if err != nil {
			return "", err
		}
		right, err := r.comparisonOperandSQL(ctx, view, n.right)
		if err != nil {
			return "", err
		}
		spelling := n.operator
		if n.namespace != "" {
			spelling = "OPERATOR(" + quoteIdentifier(n.namespace) + "." + spelling + ")"
		}
		return "(" + left + " " + spelling + " " + right + ")", nil
	case nullTestExpression:
		if u.Type.ID() != sqltype.Bool || n.child == nil {
			return "", fmt.Errorf("stuntgres: invalid default null test")
		}
		child, err := r.sql(ctx, view, n.child)
		if err != nil {
			return "", err
		}
		operator := "IS NULL"
		if n.not {
			operator = "IS NOT NULL"
		}
		return "(" + child + " " + operator + ")", nil
	case truthTestExpression:
		if u.Type.ID() != sqltype.Bool || n.child == nil || n.child.ResultUsage().Type != u.Type {
			return "", fmt.Errorf("stuntgres: invalid default truth test")
		}
		var truth string
		switch n.truth {
		case True:
			truth = "TRUE"
		case False:
			truth = "FALSE"
		case Unknown:
			truth = "UNKNOWN"
		default:
			return "", fmt.Errorf("stuntgres: invalid default truth value")
		}
		child, err := r.sql(ctx, view, n.child)
		if err != nil {
			return "", err
		}
		operator := "IS"
		if n.not {
			operator = "IS NOT"
		}
		return "(" + child + " " + operator + " " + truth + ")", nil
	case distinctExpression:
		if u.Type.ID() != sqltype.Bool || n.left == nil || n.right == nil {
			return "", fmt.Errorf("stuntgres: invalid default distinct test")
		}
		predicate, err := sqltype.BindComparisonPredicate(sqltype.CompareEqual, n.left.ResultUsage().Type, n.right.ResultUsage().Type, u.Type)
		if err != nil || predicate != n.equality {
			return "", fmt.Errorf("stuntgres: invalid default distinct binding")
		}
		left, err := r.sql(ctx, view, n.left)
		if err != nil {
			return "", err
		}
		right, err := r.sql(ctx, view, n.right)
		if err != nil {
			return "", err
		}
		operator := "IS DISTINCT FROM"
		if n.not {
			operator = "IS NOT DISTINCT FROM"
		}
		return "(" + left + " " + operator + " " + right + ")", nil
	case logicalExpression:
		if u.Type.ID() != sqltype.Bool || n.left == nil || n.left.ResultUsage().Type.ID() != sqltype.Bool || n.op == parser.LogicalNot && n.right != nil || n.op != parser.LogicalNot && (n.right == nil || n.right.ResultUsage().Type.ID() != sqltype.Bool) {
			return "", fmt.Errorf("stuntgres: invalid default logical binding")
		}
		left, err := r.sql(ctx, view, n.left)
		if err != nil {
			return "", err
		}
		if n.op == parser.LogicalNot {
			return "(NOT " + left + ")", nil
		}
		right, err := r.sql(ctx, view, n.right)
		if err != nil {
			return "", err
		}
		operator := "AND"
		if n.op == parser.LogicalOr {
			operator = "OR"
		} else if n.op != parser.LogicalAnd {
			return "", fmt.Errorf("stuntgres: invalid default logical operator")
		}
		return "(" + left + " " + operator + " " + right + ")", nil
	default:
		return "", fmt.Errorf("stuntgres: unsupported stored default node")
	}
}

func (r scalarRenderer) functionSQL(ctx context.Context, view CatalogRead, name string, args []BoundExpr) (string, error) {
	parts := make([]string, len(args))
	for i, arg := range args {
		text, err := r.sql(ctx, view, arg)
		if err != nil {
			return "", err
		}
		parts[i] = text
	}
	return name + "(" + strings.Join(parts, ", ") + ")", nil
}

func (r scalarRenderer) record(ref ObjectRef) {
	if r.dependencies == nil {
		return
	}
	for _, old := range *r.dependencies {
		if old == ref {
			return
		}
	}
	*r.dependencies = append(*r.dependencies, ref)
}

// Grouped predicate operands arise from BETWEEN lowering, but ordinary
// comparisons intentionally retain scalar-only operands in the replay grammar.
// CASE is the existing scalar identity bridge; serialize/validate its child
// first, without evaluating the stored default or widening the grammar.
func (r scalarRenderer) comparisonOperandSQL(ctx context.Context, view CatalogRead, child BoundExpr) (string, error) {
	text, err := r.sql(ctx, view, child)
	if err != nil {
		return "", err
	}
	switch child.(type) {
	case comparisonExpression, nullTestExpression, truthTestExpression, distinctExpression, logicalExpression:
		return predicateOperandSQL(text), nil
	default:
		return text, nil
	}
}

func predicateOperandSQL(text string) string {
	return "(CASE WHEN TRUE THEN " + text + " END)"
}

// Replacement can retain TEMP sequence builtin generations. Rebind persisted
// scalar trees to those types without evaluating them or changing captured
// values, positions or expression properties.
func retagBoundTypes(node BoundExpr, retainedType func(*sqltype.Descriptor) *sqltype.Descriptor) (BoundExpr, bool, error) {
	if node == nil {
		return nil, false, nil
	}
	info := expressionInfo{usage: node.ResultUsage(), pos: node.SourcePosition(), properties: node.Properties()}
	typ := retainedType(info.usage.Type)
	changed := typ != info.usage.Type
	info.usage.Type = typ
	var err error
	child := func(old BoundExpr) BoundExpr {
		if err != nil {
			return old
		}
		var next BoundExpr
		var replaced bool
		next, replaced, err = retagBoundTypes(old, retainedType)
		changed = changed || replaced
		return next
	}
	var rebound BoundExpr
	switch n := node.(type) {
	case randomExpression:
		var args []BoundExpr
		for i, old := range n.args {
			next := child(old)
			if changed && args == nil {
				args = append([]BoundExpr(nil), n.args...)
			}
			if args != nil {
				args[i] = next
			}
		}
		if args != nil {
			n.args = args
		}
		n.expressionInfo = info
		rebound = n
	case columnExpression:
		n.expressionInfo = info
		rebound = n
	case groupExpression:
		n.expressionInfo = info
		rebound = n
	case aggregateExpression:
		n.expressionInfo = info
		rebound = n
	case cachedOrderExpression:
		n.expressionInfo = info
		rebound = n
	case currentSchemasExpression:
		n.arg = child(n.arg)
		n.expressionInfo = info
		rebound = n
	case sequenceCallExpression:
		n.args = append([]BoundExpr(nil), n.args...)
		for i := range n.args {
			n.args[i] = child(n.args[i])
		}
		n.expressionInfo = info
		rebound = n
	case mathExpression:
		n.args = append([]BoundExpr(nil), n.args...)
		var usages [2]sqltype.Usage
		for i := range n.args {
			n.args[i] = child(n.args[i])
			usages[i] = n.args[i].ResultUsage()
		}
		if err != nil {
			return node, false, err
		}
		if !changed {
			return node, false, nil
		}
		n.plan, err = sqltype.BindMath(n.plan.Name(), info.usage, usages[:len(n.args)]...)
		n.expressionInfo = info
		rebound = n
	case commentInquiryExpression:
		n.args = append([]BoundExpr(nil), n.args...)
		for i := range n.args {
			n.args[i] = child(n.args[i])
		}
		n.expressionInfo = info
		rebound = n
	case nullCountExpression:
		n.args = append([]BoundExpr(nil), n.args...)
		for i := range n.args {
			n.args[i] = child(n.args[i])
		}
		n.expressionInfo = info
		rebound = n
	case caseExpression:
		n.test, n.otherwise = child(n.test), child(n.otherwise)
		var arms []caseBranch
		for i, old := range n.arms {
			arm := old
			arm.condition, arm.result = child(arm.condition), child(arm.result)
			if n.test != nil && err == nil {
				arm.testUsage.Type = retainedType(arm.testUsage.Type)
				arm.testPlan, err = sqltype.PlanCoercion(n.test.ResultUsage(), arm.testUsage, sqltype.Implicit)
				if err == nil {
					arm.equality, err = sqltype.BindEqualityComparison(arm.testUsage.Type, arm.condition.ResultUsage().Type)
				}
			}
			if changed && arms == nil {
				arms = append([]caseBranch(nil), n.arms...)
			}
			if arms != nil {
				arms[i] = arm
			}
		}
		if !changed {
			return node, false, err
		}
		if arms != nil {
			n.arms = arms
		}
		n.expressionInfo = info
		rebound = n
	case conditionalExpression:
		var args []BoundExpr
		for i, old := range n.args {
			next := child(old)
			if changed && args == nil {
				args = append([]BoundExpr(nil), n.args...)
			}
			if args != nil {
				args[i] = next
			}
		}
		if !changed {
			return node, false, err
		}
		if args != nil {
			n.args = args
		}
		if err == nil && n.kind != parser.Coalesce {
			left, right := typ, typ
			if n.kind == parser.NullIf {
				left, right = n.args[0].ResultUsage().Type, n.args[1].ResultUsage().Type
			}
			n.comparison, err = sqltype.BindEqualityComparison(left, right)
		}
		n.expressionInfo = info
		rebound = n
	case identityExpression:
		n.expressionInfo = info
		rebound = n
	case literalExpression:
		if !changed {
			return node, false, nil
		}
		n.value, err = n.value.Retag(typ)
		n.expressionInfo = info
		rebound = n
	case timeZoneExpression:
		n.value = child(n.value)
		if !n.local {
			n.zone = child(n.zone)
		}
		if !changed {
			return node, false, err
		}
		n.expressionInfo = info
		rebound = n
	case coercionExpression:
		n.child = child(n.child)
		if !changed {
			return node, false, err
		}
		if changed && err == nil {
			n.plan, err = planExpressionCoercion(n.child.ResultUsage(), info.usage, n.mode)
		}
		n.expressionInfo = info
		rebound = n
	case bitwiseExpression:
		n.left, n.right = child(n.left), child(n.right)
		if !changed {
			return node, false, err
		}
		if err == nil {
			var rt *sqltype.Descriptor
			if n.right != nil {
				rt = n.right.ResultUsage().Type
			}
			n.provider, err = sqltype.BindBitwise(n.operator, n.left.ResultUsage().Type, rt)
		}
		n.expressionInfo = info
		rebound = n
	case arithmeticExpression:
		n.left, n.right = child(n.left), child(n.right)
		if !changed {
			return node, false, err
		}
		if changed && err == nil {
			n.provider, err = sqltype.BindArithmetic(n.operator, typ, n.right == nil)
		}
		n.expressionInfo = info
		rebound = n
	case comparisonExpression:
		n.left, n.right = child(n.left), child(n.right)
		if !changed {
			return node, false, err
		}
		if changed && err == nil {
			var operator sqltype.ComparisonOperator
			switch n.operator {
			case "=":
				operator = sqltype.CompareEqual
			case "<>":
				operator = sqltype.CompareNotEqual
			case "<":
				operator = sqltype.CompareLess
			case "<=":
				operator = sqltype.CompareLessEqual
			case ">":
				operator = sqltype.CompareGreater
			case ">=":
				operator = sqltype.CompareGreaterEqual
			default:
				return nil, false, fmt.Errorf("stuntgres: invalid default comparison")
			}
			n.predicate, err = sqltype.BindComparisonPredicate(operator, n.left.ResultUsage().Type, n.right.ResultUsage().Type, typ)
		}
		n.expressionInfo = info
		rebound = n
	case nullTestExpression:
		n.child = child(n.child)
		if !changed {
			return node, false, err
		}
		n.expressionInfo = info
		rebound = n
	case truthTestExpression:
		n.child = child(n.child)
		if !changed {
			return node, false, err
		}
		n.expressionInfo = info
		rebound = n
	case distinctExpression:
		n.left, n.right = child(n.left), child(n.right)
		if !changed {
			return node, false, err
		}
		if changed && err == nil {
			n.equality, err = sqltype.BindComparisonPredicate(sqltype.CompareEqual, n.left.ResultUsage().Type, n.right.ResultUsage().Type, typ)
		}
		n.expressionInfo = info
		rebound = n
	case logicalExpression:
		n.left, n.right = child(n.left), child(n.right)
		if !changed {
			return node, false, err
		}
		n.expressionInfo = info
		rebound = n
	default:
		return nil, false, fmt.Errorf("stuntgres: unsupported stored default node")
	}
	if err != nil {
		return nil, false, err
	}
	return rebound, true, nil
}

// Serialize captured scalars through their owning type, without evaluating a
// stored expression or converting a temporal introducer into an unsupported cast.
func literalSQL(ctx context.Context, n literalExpression) (string, error) {
	u := n.ResultUsage()
	if n.value.Type() != u.Type {
		return "", fmt.Errorf("stuntgres: invalid stored literal")
	}
	literal, err := u.Type.SQLLiteral(ctx, n.value)
	if err == nil && u.Type.ID() == sqltype.Oid {
		return "CAST(" + literal + " AS OID)", nil
	}
	if err != nil || n.value.IsNull() {
		return literal, err
	}
	switch u.Type.ID() {
	case sqltype.Interval:
		literalUsage := u
		if u.Typmod >= 0 && (u.Typmod>>16)&sqltype.IntervalSecond == 0 {
			// Non-SECOND precision needs a generic column declaration, not
			// invalid keyword literal syntax. The destination restores that
			// exact typmod; this already-adjusted scalar keeps its field mask.
			literalUsage.Typmod |= sqltype.IntervalFullPrecision
		}
		declaration, err := literalUsage.Declaration()
		if err != nil {
			return "", err
		}
		if strings.HasPrefix(declaration, "INTERVAL(") {
			return declaration + strings.TrimPrefix(literal, "INTERVAL"), nil
		}
		return literal + strings.TrimPrefix(declaration, "INTERVAL"), nil
	case sqltype.Date, sqltype.Time, sqltype.Timestamp, sqltype.Timestamptz, sqltype.Timetz:
		// The endpoint encoding can already carry its required precision.
		if strings.HasPrefix(literal, "TIMESTAMP") {
			return literal, nil
		}
		declaration, err := u.Declaration()
		if err != nil {
			return "", err
		}
		return declaration + " " + literal, nil
	case sqltype.Numeric:
		text, err := u.Type.Output(ctx, n.value)
		if err != nil {
			return "", err
		}
		if text == "NaN" || text == "Infinity" || text == "-Infinity" {
			return literal, nil
		}
		if !strings.ContainsAny(text, ".eE") {
			// Keep NUMERIC identity without adding a display-scale digit.
			text += "e0"
		}
		return text, nil
	}
	return literal, nil
}
