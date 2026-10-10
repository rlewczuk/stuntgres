package engine

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type Volatility uint8

const (
	Immutable Volatility = iota
	Stable
	Volatile
)

type ExprProperties struct {
	Volatility                                       Volatility
	RowDependent, ParameterDependent, StageDependent bool
}

// Dependence and volatility propagate independently through bound expressions.
type BoundExpr interface {
	boundExpression()
	ResultUsage() sqltype.Usage
	CollationIndeterminate() bool
	ImplicitCollationConflict() (sqltype.CollationRef, sqltype.CollationRef, parser.Position)
	// ExplicitCollation returns the annotation origin; zero means implicit.
	ExplicitCollation() parser.Position
	SourcePosition() parser.Position
	Properties() ExprProperties
	Eval(*EvalContext) (sqltype.Value, error)
}
type EvalContext struct {
	Context        context.Context
	Scope, Frame   uint32
	Row            []sqltype.Value
	OldRow         []sqltype.Value
	Parameters     []sqltype.Value
	orderValues    []sqltype.Value
	statementScope *StatementScope
	Input          sqltype.InputEnvironment
	Output         sqltype.OutputEnvironment
	Outer          *EvalContext
	query          *queryState
}
type expressionInfo struct {
	usage                         sqltype.Usage
	pos                           parser.Position
	properties                    ExprProperties
	unknownNull                   bool
	collationConflict             bool
	collationExplicit             parser.Position
	collationLeft, collationRight sqltype.CollationRef
	collationConflictPos          parser.Position
}

func (expressionInfo) boundExpression()                     {}
func (e expressionInfo) ResultUsage() sqltype.Usage         { return e.usage }
func (e expressionInfo) SourcePosition() parser.Position    { return e.pos }
func (e expressionInfo) Properties() ExprProperties         { return e.properties }
func (e expressionInfo) CollationIndeterminate() bool       { return e.collationConflict }
func (e expressionInfo) ExplicitCollation() parser.Position { return e.collationExplicit }
func (e expressionInfo) ImplicitCollationConflict() (sqltype.CollationRef, sqltype.CollationRef, parser.Position) {
	return e.collationLeft, e.collationRight, e.collationConflictPos
}

// Preserve diagnostic provenance when an existing wrapper copies metadata.
func boundExpressionInfo(node BoundExpr) expressionInfo {
	left, right, position := node.ImplicitCollationConflict()
	return expressionInfo{usage: node.ResultUsage(), pos: node.SourcePosition(), properties: node.Properties(), collationConflict: node.CollationIndeterminate(), collationExplicit: node.ExplicitCollation(), collationLeft: left, collationRight: right, collationConflictPos: position}
}
func evaluationContext(c *EvalContext) error {
	if c == nil || c.Context == nil {
		return fmt.Errorf("stuntgres: missing expression context")
	}
	return c.Context.Err()
}

type literalExpression struct {
	expressionInfo
	value sqltype.Value
}

func (e literalExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	if e.usage.Type == nil || !e.usage.Type.Concrete() || e.value.Type() != e.usage.Type ||
		e.unknownNull && (!e.value.IsNull() || e.usage.Type.ID() != sqltype.Text) {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid literal expression")
	}
	return e.value, nil
}

type columnExpression struct {
	expressionInfo
	origin       ObjectRef
	scope, frame uint32
	slot         int
}

func (e columnExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	for c != nil && (c.Scope != e.scope || c.Frame != e.frame) {
		c = c.Outer
	}
	if c == nil || e.slot < 0 || e.slot >= len(c.Row) {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid expression row frame")
	}
	v := c.Row[e.slot]
	if e.usage.Type == nil || !e.usage.Type.Concrete() || v.Type() != e.usage.Type {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid expression column type")
	}
	return v, nil
}

type parameterExpression struct {
	expressionInfo
	number int
}

func (e parameterExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	if e.number <= 0 || e.number > len(c.Parameters) {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid expression parameter frame")
	}
	v := c.Parameters[e.number-1]
	if e.usage.Type == nil || !e.usage.Type.Concrete() || v.Type() != e.usage.Type {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid expression parameter type")
	}
	return v, nil
}

type coercionExpression struct {
	expressionInfo
	child BoundExpr
	plan  sqltype.CoercionPlan
	mode  sqltype.CoercionContext
}

// COLLATE changes analyzed usage only; values retain the same type and datum.
type collationExpression struct {
	expressionInfo
	child BoundExpr
}

func (e collationExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	return e.child.Eval(c)
}

func (e coercionExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	v, err := e.child.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	output := c.Output
	output.Regclass = c.statementScope
	return e.plan.Apply(c.Context, v, c.Input, output)
}
func coercionProperties(child BoundExpr, plan sqltype.CoercionPlan) ExprProperties {
	properties := child.Properties()
	if plan.SessionDependent() && properties.Volatility < Stable {
		properties.Volatility = Stable
	}
	return properties
}

type comparisonExpression struct {
	expressionInfo
	left, right BoundExpr
	predicate   sqltype.ComparisonPredicate
	operator    string
	namespace   string
}

func (e comparisonExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	a, err := e.left.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	b, err := e.right.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	return e.predicate.Evaluate(c.Context, a, b, c.Input)
}

// preflightCoercions materializes immutable conversions from the imported
// execution frame, never rows or stages. Only changed branches are copied;
// analyzed nodes and their conservative dependence metadata remain immutable.
func preflightCoercions(c *EvalContext, expression BoundExpr) (BoundExpr, bool, error) {
	return preflightCoercionsWithPolicy(c, expression, false)
}

// Only consumed stored defaults use decisive constant planning. Ordinary source,
// DELETE and supplied-value preflight retain their existing traversal demand.
func preflightCoercionsWithPolicy(c *EvalContext, expression BoundExpr, decisiveDefaults bool) (BoundExpr, bool, error) {
	if err := evaluationContext(c); err != nil {
		return nil, false, err
	}
	switch e := expression.(type) {
	case visibilityInquiryExpression:
		arg, changed, err := preflightCoercionsWithPolicy(c, e.arg, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		if changed {
			e.arg = arg
		}
		return e, changed, nil
	case currentSchemasExpression:
		arg, changed, err := preflightCoercionsWithPolicy(c, e.arg, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		if changed {
			e.arg = arg
		}
		return e, changed, nil
	case caseExpression:
		return simplifyConditional(c, expression, decisiveDefaults)
	case conditionalExpression:
		if e.kind == parser.Coalesce {
			return simplifyConditional(c, expression, decisiveDefaults)
		}
		var args []BoundExpr
		for i, arg := range e.args {
			next, changed, err := preflightCoercionsWithPolicy(c, arg, decisiveDefaults)
			if err != nil {
				return nil, false, err
			}
			if changed {
				if args == nil {
					args = append([]BoundExpr(nil), e.args...)
				}
				args[i] = next
			}
		}
		if args != nil {
			e.args = args
		}
		next, changed, err := simplifyConditional(c, e, decisiveDefaults)
		return next, changed || args != nil, err
	case timeZoneExpression:
		value, changed, err := preflightCoercionsWithPolicy(c, e.value, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		e.value = value
		if !e.local {
			zone, zoneChanged, err := preflightCoercionsWithPolicy(c, e.zone, decisiveDefaults)
			if err != nil {
				return nil, false, err
			}
			e.zone = zone
			changed = changed || zoneChanged
		}
		return e, changed, nil
	case collationExpression:
		child, changed, err := preflightCoercionsWithPolicy(c, e.child, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		e.child = child
		return e, changed, nil
	case coercionExpression:
		child, changed, err := preflightCoercionsWithPolicy(c, e.child, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		e.child = child
		p := e.Properties()
		if p.Volatility == Immutable && !p.RowDependent && !p.StageDependent {
			value, err := e.Eval(c)
			return preflightValue(c, e.expressionInfo, value, err)
		}
		if changed {
			return e, true, nil
		}
	case likeEscapeExpression:
		pattern, pc, err := preflightCoercionsWithPolicy(c, e.pattern, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		escape, ec, err := preflightCoercionsWithPolicy(c, e.escape, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		e.pattern, e.escape = pattern, escape
		p := e.Properties()
		if p.Volatility == Immutable && !p.RowDependent && !p.StageDependent {
			value, err := e.Eval(c)
			return preflightValue(c, e.expressionInfo, value, err)
		}
		return e, pc || ec, nil
	case likeExpression:
		left, lc, err := preflightCoercionsWithPolicy(c, e.left, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		right, rc, err := preflightCoercionsWithPolicy(c, e.right, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		e.left, e.right = left, right
		if randomLiteralNull(left) || randomLiteralNull(right) {
			return preflightValue(c, e.expressionInfo, sqltype.Null(e.usage.Type), nil)
		}
		p := e.Properties()
		if p.Volatility == Immutable && !p.RowDependent && !p.StageDependent {
			value, err := e.Eval(c)
			return preflightValue(c, e.expressionInfo, value, err)
		}
		return e, lc || rc, nil
	case comparisonExpression:
		left, leftChanged, err := preflightCoercionsWithPolicy(c, e.left, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		right, rightChanged, err := preflightCoercionsWithPolicy(c, e.right, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		if decisiveDefaults {
			_, leftConstant := left.(literalExpression)
			_, rightConstant := right.(literalExpression)
			if leftConstant && rightConstant {
				e.left, e.right = left, right
				value, err := e.Eval(c)
				return preflightValue(c, e.expressionInfo, value, err)
			}
		}
		if leftChanged || rightChanged {
			e.left, e.right = left, right
			return e, true, nil
		}
	case bitwiseExpression:
		left, lc, err := preflightCoercionsWithPolicy(c, e.left, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		var right BoundExpr
		var rc bool
		if e.right != nil {
			right, rc, err = preflightCoercionsWithPolicy(c, e.right, decisiveDefaults)
			if err != nil {
				return nil, false, err
			}
		}
		e.left, e.right = left, right
		if bitwiseConstantNull(left) || bitwiseConstantNull(right) {
			return preflightValue(c, e.expressionInfo, sqltype.Null(e.usage.Type), nil)
		}
		if decisiveDefaults {
			_, l := left.(literalExpression)
			_, r := right.(literalExpression)
			p := e.Properties()
			if l && (right == nil || r) && p == (ExprProperties{}) {
				v, err := e.Eval(c)
				return preflightValue(c, e.expressionInfo, v, err)
			}
		}
		if lc || rc {
			return e, true, nil
		}
	case arithmeticExpression:
		left, leftChanged, err := preflightCoercionsWithPolicy(c, e.left, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		var right BoundExpr
		var rightChanged bool
		if e.right != nil {
			right, rightChanged, err = preflightCoercionsWithPolicy(c, e.right, decisiveDefaults)
			if err != nil {
				return nil, false, err
			}
		}
		if decisiveDefaults {
			_, leftConstant := left.(literalExpression)
			_, rightConstant := right.(literalExpression)
			p := e.Properties()
			if leftConstant && (e.right == nil || rightConstant) && p.Volatility == Immutable && !p.RowDependent && !p.ParameterDependent && !p.StageDependent {
				e.left, e.right = left, right
				value, err := e.Eval(c)
				return preflightValue(c, e.expressionInfo, value, err)
			}
		}
		// Ordinary consumers only materialize casts; arithmetic stays row-owned.
		if leftChanged || rightChanged {
			e.left, e.right = left, right
			return e, true, nil
		}
	case nullTestExpression:
		child, changed, err := preflightCoercionsWithPolicy(c, e.child, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		if decisiveDefaults {
			if _, constant := child.(literalExpression); constant {
				e.child = child
				value, err := e.Eval(c)
				return preflightValue(c, e.expressionInfo, value, err)
			}
		}
		if changed {
			e.child = child
			return e, true, nil
		}
	case truthTestExpression:
		child, changed, err := preflightCoercionsWithPolicy(c, e.child, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		if decisiveDefaults {
			if _, constant := child.(literalExpression); constant {
				e.child = child
				value, err := e.Eval(c)
				return preflightValue(c, e.expressionInfo, value, err)
			}
		}
		if changed {
			e.child = child
			return e, true, nil
		}
	case distinctExpression:
		left, leftChanged, err := preflightCoercionsWithPolicy(c, e.left, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		right, rightChanged, err := preflightCoercionsWithPolicy(c, e.right, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		if decisiveDefaults {
			_, leftConstant := left.(literalExpression)
			_, rightConstant := right.(literalExpression)
			if leftConstant && rightConstant {
				e.left, e.right = left, right
				value, err := e.Eval(c)
				return preflightValue(c, e.expressionInfo, value, err)
			}
		}
		if leftChanged || rightChanged {
			e.left, e.right = left, right
			return e, true, nil
		}
	case logicalExpression:
		left, leftChanged, err := preflightCoercionsWithPolicy(c, e.left, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		var leftConstant bool
		if decisiveDefaults {
			_, leftConstant = left.(literalExpression)
			e.left = left
			value, decisive, err := decisiveLogicalValue(&e)
			if err != nil {
				return nil, false, err
			}
			if decisive {
				return preflightValue(c, e.expressionInfo, value, nil)
			}
		}
		var right BoundExpr
		var rightChanged bool
		if e.right != nil {
			right, rightChanged, err = preflightCoercionsWithPolicy(c, e.right, decisiveDefaults)
			if err != nil {
				return nil, false, err
			}
		}
		if decisiveDefaults && leftConstant {
			_, rightConstant := right.(literalExpression)
			if e.op == parser.LogicalNot || rightConstant {
				e.left, e.right = left, right
				value, err := e.Eval(c)
				return preflightValue(c, e.expressionInfo, value, err)
			}
		}
		if leftChanged || rightChanged {
			e.left, e.right = left, right
			return e, true, nil
		}
	case sequenceCallExpression:
		var args []BoundExpr
		for i, arg := range e.args {
			folded, changed, err := preflightCoercions(c, arg)
			if err != nil {
				return nil, false, err
			}
			if changed {
				if args == nil {
					args = append([]BoundExpr(nil), e.args...)
				}
				args[i] = folded
			}
		}
		if args != nil {
			e.args = args
			return e, true, nil
		}
	case binaryExpression:
		var args []BoundExpr
		for i, arg := range e.args {
			folded, changed, err := preflightCoercionsWithPolicy(c, arg, decisiveDefaults)
			if err != nil {
				return nil, false, err
			}
			if changed {
				if args == nil {
					args = append([]BoundExpr(nil), e.args...)
				}
				args[i] = folded
			}
		}
		if args != nil {
			e.args = args
		}
		next, changed, err := simplifyConditional(c, e, decisiveDefaults)
		return next, changed || args != nil, err
	case datetimeAgeFiniteExpression:
		anyChanged := false
		for i, arg := range e.children() {
			folded, changed, err := preflightCoercionsWithPolicy(c, arg, decisiveDefaults)
			if err != nil {
				return nil, false, err
			}
			if changed {
				e.args[i], anyChanged = folded, true
			}
		}
		next, changed, err := simplifyConditional(c, e, decisiveDefaults)
		return next, changed || anyChanged, err
	case datetimeMakeExpression:
		anyChanged := false
		for i, arg := range e.children() {
			folded, changed, err := preflightCoercionsWithPolicy(c, arg, decisiveDefaults)
			if err != nil {
				return nil, false, err
			}
			if changed {
				e.args[i] = folded
				anyChanged = true
			}
		}
		next, changed, err := simplifyConditional(c, e, decisiveDefaults)
		return next, changed || anyChanged, err
	case datetimeFormattingExpression:
		anyChanged := false
		for i, arg := range e.args {
			folded, changed, err := preflightCoercionsWithPolicy(c, arg, decisiveDefaults)
			if err != nil {
				return nil, false, err
			}
			if changed {
				e.args[i] = folded
				anyChanged = true
			}
		}
		if template, ok := e.args[1].(literalExpression); ok {
			plan, changed, err := e.plan.WithConstantTemplate(c.Context, template.value)
			if err != nil {
				return nil, false, err
			}
			if changed {
				e.plan = plan
				anyChanged = true
			}
		}
		next, changed, err := simplifyConditional(c, e, decisiveDefaults)
		return next, changed || anyChanged, err
	case stringExpression:
		var args []BoundExpr
		for i, arg := range e.args {
			folded, changed, err := preflightCoercionsWithPolicy(c, arg, decisiveDefaults)
			if err != nil {
				return nil, false, err
			}
			if changed {
				if args == nil {
					args = append([]BoundExpr(nil), e.args...)
				}
				args[i] = folded
			}
		}
		if args != nil {
			e.args = args
		}
		next, changed, err := simplifyConditional(c, e, decisiveDefaults)
		return next, changed || args != nil, err
	case mathExpression:
		var args []BoundExpr
		for i, arg := range e.args {
			folded, changed, err := preflightCoercionsWithPolicy(c, arg, decisiveDefaults)
			if err != nil {
				return nil, false, err
			}
			if changed {
				if args == nil {
					args = append([]BoundExpr(nil), e.args...)
				}
				args[i] = folded
			}
		}
		if args != nil {
			e.args = args
		}
		next, changed, err := simplifyConditional(c, e, decisiveDefaults)
		return next, changed || args != nil, err
	case randomExpression:
		var args []BoundExpr
		for i, arg := range e.args {
			folded, changed, err := preflightCoercionsWithPolicy(c, arg, decisiveDefaults)
			if err != nil {
				return nil, false, err
			}
			if changed {
				if args == nil {
					args = append([]BoundExpr(nil), e.args...)
				}
				args[i] = folded
			}
		}
		if args != nil {
			e.args = args
			return e, true, nil
		}
	case indexPropertyExpression:
		did := false
		e.properties = ExprProperties{Volatility: Stable}
		for i, arg := range e.children() {
			folded, changed, err := preflightCoercionsWithPolicy(c, arg, decisiveDefaults)
			if err != nil {
				return nil, false, err
			}
			if changed {
				e.args[i] = folded
				did = true
			}
			e.properties = mergeExpressionProperties(e.properties, e.args[i].Properties())
		}
		if did {
			return e, true, nil
		}
	case deparseExpression:
		var args []BoundExpr
		for i, arg := range e.args {
			folded, changed, err := preflightCoercionsWithPolicy(c, arg, decisiveDefaults)
			if err != nil {
				return nil, false, err
			}
			if changed {
				if args == nil {
					args = append([]BoundExpr(nil), e.args...)
				}
				args[i] = folded
			}
		}
		if args != nil {
			e.args = args
			return e, true, nil
		}
	case commentInquiryExpression:
		var args []BoundExpr
		for i, arg := range e.args {
			folded, changed, err := preflightCoercionsWithPolicy(c, arg, decisiveDefaults)
			if err != nil {
				return nil, false, err
			}
			if changed {
				if args == nil {
					args = append([]BoundExpr(nil), e.args...)
				}
				args[i] = folded
			}
		}
		if args != nil {
			e.args = args
			return e, true, nil
		}
	case nullCountExpression:
		var args []BoundExpr
		for i, arg := range e.args {
			folded, changed, err := preflightCoercionsWithPolicy(c, arg, decisiveDefaults)
			if err != nil {
				return nil, false, err
			}
			if changed {
				if args == nil {
					args = append([]BoundExpr(nil), e.args...)
				}
				args[i] = folded
			}
		}
		if args != nil {
			e.args = args
			return e, true, nil
		}
	case formatExpression:
		var args []BoundExpr
		for i, arg := range e.args {
			folded, changed, err := preflightCoercionsWithPolicy(c, arg, decisiveDefaults)
			if err != nil {
				return nil, false, err
			}
			if changed {
				if args == nil {
					args = append([]BoundExpr(nil), e.args...)
				}
				args[i] = folded
			}
		}
		if args != nil {
			e.args = args
			return e, true, nil
		}
	}
	return expression, false, nil
}

func preflightValue(c *EvalContext, info expressionInfo, value sqltype.Value, err error) (BoundExpr, bool, error) {
	if err != nil {
		return nil, false, err
	}
	if err := evaluationContext(c); err != nil {
		return nil, false, err
	}
	return literalExpression{expressionInfo: info, value: value}, true, nil
}

type Truth uint8

const (
	False Truth = iota
	True
	Unknown
)

func TruthOf(v sqltype.Value) (Truth, error) {
	if v.Type() != nil && v.Type().IsDomain() {
		var err error
		v, err = v.BaseValue()
		if err != nil {
			return False, err
		}
	}
	if v.Type() == nil || v.Type().ID() != sqltype.Bool {
		return False, fmt.Errorf("stuntgres: predicate is not typed boolean")
	}
	if v.IsNull() {
		return Unknown, nil
	}
	b, err := v.Boolean()
	if err != nil {
		return False, err
	}
	if b {
		return True, nil
	}
	return False, nil
}

type expressionContext uint8

const (
	projectionContext expressionContext = iota
	filterContext
	assignmentContext
	functionArgumentContext
)

type expressionPolicy struct {
	context          expressionContext
	expected         sqltype.Usage
	mode             sqltype.CoercionContext
	column           string
	booleanConstruct string
	// String-family textual roles alone admit internal CHAR annotations.
	stringInput bool
	// Selected string INSERT rows analyze occurrences before target assignment.
	deferAssignment bool
	// Operand skips the enclosing WHERE BOOL check while binding predicate children.
	operand bool
}
type expressionDependency struct {
	ref      ObjectRef
	revision DefinitionRevision
}
type expressionBinder struct {
	engine                     *Engine
	ctx                        context.Context
	relation                   *relationDescriptor
	ref                        ObjectRef
	parameters                 map[int]sqltype.Usage
	updateParameters           bool
	updateInputParameters      map[int]sqltype.Usage
	uses                       []int
	dependencies               []expressionDependency
	input                      sqltype.InputEnvironment
	op                         *Operation
	expected                   *StatementDescription
	temporals                  []temporalConstant
	anyParameters              []parameterExpression
	deleteTarget               bool
	deleteUsingSources         bool
	targetName                 string
	targetAlias                string
	returningAliases           *returningAliases
	defaultConsumer            bool
	checkConsumer              bool
	indexConsumer              bool
	generatedOwner             *relationDescriptor
	visibilitySnapshot         bool
	sequenceConstants          []sequenceConstant
	regclassConcat             bool
	aggregateClause            string
	aggregates                 []aggregateCall
	queryAuth                  *AuthorizationView
	querySources               []queryLeaf
	queryLockAdmission         *queryLockAdmission
	queryNamespace             *queryNamespace
	queryHidden                []queryQualifier
	queryNonLateral            bool
	queryFrame, nextQueryFrame uint32
	queryScopes                []queryBindingScope
	resolvedQueryFrame         uint32
	queryRowDiagnosticMode     relationLockMode // Clause diagnostics only; tuple authority is queryRowMark.
	subqueryDemand             bool
	groupSubquery              bool
	viewValuesCount            int
	noParameters               bool
	// Only internal TYPE CHECK reparsing supplies these immutable literal nodes.
	// Generated markers are not prepared parameters and never enter inference.
	checkLiterals  []literalExpression
	viewLiterals   map[viewLiteralPath]literalExpression
	viewCastUsages map[viewLiteralPath]sqltype.Usage
	viewColumns    map[viewLiteralPath]viewColumn
	viewStars      map[viewLiteralPath][]viewColumn
	viewJoinCount  int
	viewNamespaces []viewNamespace
}

func newExpressionBinder(ctx context.Context, e *Engine, input sqltype.InputEnvironment, op *Operation, expected *StatementDescription) *expressionBinder {
	return &expressionBinder{engine: e, ctx: ctx, parameters: make(map[int]sqltype.Usage), input: input, op: op, expected: expected}
}

// Binding must retain the caller's catalog/path authority, including a private
// CREATE candidate. Execution still opens providers through live authorization.
func (b *expressionBinder) bindingAuthorization() (AuthorizationView, error) {
	if b.queryAuth != nil {
		return *b.queryAuth, nil
	}
	return b.engine.authorizationLocked(b.op)
}
func (b *expressionBinder) typ(id sqltype.ID) *sqltype.Descriptor {
	d, _ := (CatalogRead{owner: b.engine, state: b.engine.state}).LookupType(id)
	return d
}
func (b *expressionBinder) dependency(ref ObjectRef, revision DefinitionRevision) {
	for _, d := range b.dependencies {
		if d.ref == ref {
			return
		}
	}
	b.dependencies = append(b.dependencies, expressionDependency{ref, revision})
}
func (b *expressionBinder) info(u sqltype.Usage, pos parser.Position, p ExprProperties) expressionInfo {
	view := CatalogRead{owner: b.engine, state: b.engine.state}
	if b.queryAuth != nil {
		view = b.queryAuth.catalog
	}
	address := ObjectAddress{ClassID: TypeClassID, ObjectID: OID(u.Type.ID())}
	revision := DefinitionRevision(1)
	if descriptor, ok := view.Descriptor(address); ok {
		revision = descriptor.Revision()
	}
	b.dependency(ObjectRef{owner: b.engine.owner, Epoch: view.Epoch(), Address: address}, revision)
	return expressionInfo{usage: u, pos: pos, properties: p}
}
func expressionError(code, message string, pos parser.Position) error {
	d := sqlError(code, message).(*sqlstate.Error)
	if pos.Character > 0 {
		d.Position = strconv.Itoa(pos.Character)
	}
	return d
}
func (b *expressionBinder) constrain(n int, u sqltype.Usage, pos parser.Position) error {
	if n <= 0 {
		return expressionError("42P02", fmt.Sprintf("there is no parameter $%d", n), pos)
	}
	if old, ok := b.parameters[n]; ok && old.Type != nil && old.Type != u.Type {
		if b.updateParameters {
			// UPDATE uses occurrence-sensitive inference diagnostics; other
			// assignment consumers retain their selected 0A000 boundary.
			_, err := b.coerceParameter(parameterExpression{expressionInfo: expressionInfo{pos: pos}, number: n}, u)
			return err
		}
		return sqlError("0A000", "parameter reused with different destination types")
	}
	b.parameters[n] = u.Type.Usage()
	return nil
}
func (b *expressionBinder) finish() (ParameterInfo, error) {
	// Retained unknown ANY and null-test occurrences share binding order.
	// Diagnose occurrence mismatches before gaps or unresolved shared slots.
	for _, p := range b.anyParameters {
		if b.parameters[p.number].Type != nil {
			return ParameterInfo{}, expressionError("42P08", fmt.Sprintf("could not determine data type of parameter $%d", p.number), p.pos)
		}
	}
	keys := make([]int, 0, len(b.parameters))
	for n := range b.parameters {
		keys = append(keys, n)
	}
	sort.Ints(keys)
	for i, n := range keys {
		if n != i+1 || b.parameters[n].Type == nil {
			return ParameterInfo{}, sqlError("42P18", fmt.Sprintf("could not determine data type of parameter $%d", i+1))
		}
	}
	info := ParameterInfo{Count: len(keys), Types: make([]sqltype.Usage, len(keys))}
	for i, n := range keys {
		info.Types[i] = b.parameters[n]
		if info.Types[i].Type.BaseType().IsEnum() {
			if info.DiagnosticNames == nil {
				info.DiagnosticNames = make([]string, len(keys))
			}
			info.DiagnosticNames[i] = b.diagnosticTypeName(info.Types[i].Type.BaseType())
		}
	}
	return info, nil
}

// Output fallback belongs to an unresolved occurrence, not its subsequently
// constrained shared slot. These private provisional nodes never escape describe.
func (b *expressionBinder) finalizeProjection(node BoundExpr) (BoundExpr, error) {
	p, ok := node.(parameterExpression)
	if !ok || p.usage.Type != nil {
		return node, nil
	}
	return b.coerceParameter(p, b.typ(sqltype.Text).Usage())
}
func (b *expressionBinder) column(raw parser.ColumnExpr) (BoundExpr, error) {
	if b.generatedOwner != nil {
		name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
		owner := b.generatedOwner
		qualified := len(raw.Name.Parts) == 1 || len(raw.Name.Parts) == 2 && raw.Name.Parts[0].Text == owner.name || len(raw.Name.Parts) == 3 && raw.Name.Parts[1].Text == owner.name && raw.Name.Parts[0].Text == b.queryNamespace.qualifiers[0].schema
		if qualified {
			if name == "tableoid" {
				ref := b.querySources[0].ref
				return tableOIDExpression{b.info(b.typ(sqltype.Oid).Usage(), raw.Pos, ExprProperties{RowDependent: true}), ref}, nil
			}
			if isSystemColumn(name) {
				return nil, expressionError("42P10", fmt.Sprintf(`cannot use system column "%s" in column generation expression`, name), raw.Pos)
			}
		}
		node, err := b.queryColumn(raw)
		if err != nil {
			// A bare relation name is a whole-row reference only after ordinary
			// column lookup has failed; a same-named attribute takes precedence.
			if name == owner.name && len(raw.Name.Parts) == 1 {
				diagnostic := expressionError("42P17", "cannot use whole-row variable in column generation expression", raw.Pos).(*sqlstate.Error)
				diagnostic.Detail = "This would cause the generated column to depend on its own value."
				return nil, diagnostic
			}
			return nil, err
		}
		if column, ok := node.(columnExpression); ok {
			if _, generated := owner.generations[column.origin.Address.SubID]; generated {
				diagnostic := expressionError("42P17", fmt.Sprintf(`cannot use generated column "%s" in column generation expression`, name), raw.Pos).(*sqlstate.Error)
				diagnostic.Detail = "A generated column cannot reference another generated column."
				return nil, diagnostic
			}
		}
		return node, nil
	}
	if b.checkConsumer && len(raw.Name.Parts) > 0 {
		name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
		switch name {
		case "ctid", "xmin", "cmin", "xmax", "cmax":
			return nil, expressionError("42P10", `system column "`+name+`" reference in check constraint is invalid`, raw.Pos)
		}
	}
	if b.returningAliases != nil {
		return b.returningColumn(raw)
	}
	if b.queryNamespace != nil {
		return b.queryColumn(raw)
	}
	if len(raw.Name.Parts) == 2 && !b.deleteTarget {
		return nil, expressionError("0A000", "qualified column references are unsupported", raw.Pos)
	}
	if len(raw.Name.Parts) == 0 || len(raw.Name.Parts) > 2 {
		return nil, fmt.Errorf("stuntgres: column outside expression scope")
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	if b.relation == nil {
		return nil, expressionError("42703", fmt.Sprintf(`column "%s" does not exist`, name), raw.Pos)
	}
	if len(raw.Name.Parts) == 2 {
		qualifier := raw.Name.Parts[0].Text
		visible := b.targetName
		if b.targetAlias != "" {
			visible = b.targetAlias
		}
		if qualifier != visible {
			if qualifier == b.targetName && b.targetAlias != "" {
				err := expressionError("42P01", fmt.Sprintf(`invalid reference to FROM-clause entry for table "%s"`, qualifier), raw.Pos).(*sqlstate.Error)
				err.Hint = fmt.Sprintf(`Perhaps you meant to reference the table alias "%s".`, b.targetAlias)
				return nil, err
			}
			return nil, expressionError("42P01", fmt.Sprintf(`missing FROM-clause entry for table "%s"`, qualifier), raw.Pos)
		}
	}
	i, ok := b.relation.index[name]
	if !ok {
		if len(raw.Name.Parts) == 2 {
			return nil, expressionError("42703", fmt.Sprintf("column %s.%s does not exist", raw.Name.Parts[0].Text, name), raw.Pos)
		}
		return nil, expressionError("42703", fmt.Sprintf(`column "%s" does not exist`, name), raw.Pos)
	}
	return b.columnAt(i, raw.Pos), nil
}
func (b *expressionBinder) useColumn(i int) ObjectRef {
	return b.useColumnOf(b.relation, i)
}

func (b *expressionBinder) useColumnOf(relation *relationDescriptor, i int) ObjectRef {
	ref := b.ref
	ref.Address.SubID = relation.attributes[i]
	b.dependency(ref, relation.revision)
	used := false
	for _, slot := range b.uses {
		if slot == i {
			used = true
			break
		}
	}
	if !used {
		b.uses = append(b.uses, i)
	}
	b.info(relation.columns[i].Usage, parser.Position{Offset: -1}, ExprProperties{})
	return ref
}
func (b *expressionBinder) columnAt(i int, pos parser.Position) BoundExpr {
	return b.columnAtOf(b.relation, i, pos)
}

func (b *expressionBinder) columnAtOf(relation *relationDescriptor, i int, pos parser.Position) BoundExpr {
	ref := b.useColumnOf(relation, i)
	return columnExpression{expressionInfo: expressionInfo{usage: relation.columns[i].Usage, pos: pos, properties: ExprProperties{RowDependent: true}}, origin: ref, scope: 1, frame: 1, slot: i}
}
func (b *expressionBinder) bind(raw parser.Expr, policy expressionPolicy) (BoundExpr, error) {
	if policy.context == filterContext && !policy.operand && policy.booleanConstruct == "" {
		return b.bindBoolean(raw, policy.context, "WHERE")
	}
	if err := b.ctx.Err(); err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, fmt.Errorf("stuntgres: missing scalar expression")
	}
	var node BoundExpr
	switch r := raw.(type) {
	case parser.IndexSubqueryExpr:
		return nil, expressionError("0A000", "cannot use subquery in index expression", r.Pos)
	case parser.SubqueryExpr:
		var err error
		node, err = b.bindSubquery(r)
		if err != nil {
			return nil, err
		}
	case parser.RowExpr:
		return nil, expressionError("0A000", "record values are unsupported", r.Pos)
	case parser.IdentityExpr:
		var err error
		node, err = b.bindIdentity(r.Name, r.Pos)
		if err != nil {
			return nil, err
		}
	case parser.CurrentDateTimeExpr:
		var err error
		node, err = b.bindCurrentDateTime(r)
		if err != nil {
			return nil, err
		}
	case parser.ForbiddenGenerationExpr:
		return nil, expressionError(r.Code, r.Message, r.Pos)
	case parser.AggregateExpr:
		if b.indexConsumer {
			return nil, expressionError("42803", "aggregate functions are not allowed in index expressions", r.Pos)
		}
		if b.aggregateClause == "VALUES" || b.aggregateClause == "INSERT VALUES" && r.Name == "string_agg" {
			return nil, expressionError("42803", "aggregate functions are not allowed in VALUES", r.Pos)
		}
		if r.Name == "sum" {
			if b.aggregateClause != "RETURNING" {
				return nil, expressionError("0A000", "functions are unsupported", r.Pos)
			}
			if r.Star || len(r.Args) != 1 {
				return nil, expressionError("0A000", "aggregate call is unsupported", r.Pos)
			}
			arg, err := b.bind(r.Args[0], expressionPolicy{context: projectionContext})
			if err != nil {
				return nil, err
			}
			if arg.ResultUsage().Type == nil {
				return nil, expressionError("0A000", "untyped aggregate arguments are unsupported", r.Pos)
			}
			if literal, ok := r.Args[0].(parser.LiteralExpr); ok && (literal.Value.Kind == parser.StringValue || literal.Value.Kind == parser.NullValue) {
				return nil, expressionError("0A000", "untyped aggregate arguments are unsupported", r.Pos)
			}
			name := parser.ObjectName{Parts: []parser.Identifier{{Text: r.Name, Pos: r.Pos}}}
			if r.Schema != nil {
				name.Parts = []parser.Identifier{*r.Schema, {Text: r.Name, Pos: r.Pos}}
			}
			visible, err := b.builtinVisible(name, functionLookup)
			if err != nil {
				return nil, err
			}
			eligible := false
			switch arg.ResultUsage().Type.ID() {
			case sqltype.Int2, sqltype.Int4, sqltype.Int8, sqltype.Float4, sqltype.Float8, sqltype.Numeric, sqltype.Interval:
				eligible = true
			}
			if !visible || !eligible {
				return nil, functionSignatureErrorForTypes(name, r.Pos, []string{arg.ResultUsage().Type.DiagnosticName()})
			}
			return nil, expressionError("42803", "aggregate functions are not allowed in RETURNING", r.Pos)
		}
		if b.checkConsumer {
			return nil, expressionError("42803", "aggregate functions are not allowed in check constraints", r.Pos)
		}
		name := parser.ObjectName{Parts: []parser.Identifier{{Text: r.Name, Pos: r.Pos}}}
		if r.Schema != nil {
			name.Parts = []parser.Identifier{*r.Schema, {Text: r.Name, Pos: r.Pos}}
		}
		var err error
		if r.Name == "bool_and" || r.Name == "bool_or" || r.Name == "every" {
			node, err = b.bindBooleanAggregate(r, name)
		} else if r.Name == "avg" {
			node, err = b.bindAvg(r, name)
		} else if r.Name == "any_value" {
			node, err = b.bindAnyValue(r, name)
		} else if r.Name == "min" || r.Name == "max" {
			node, err = b.bindExtrema(r, name)
		} else if r.Name == "string_agg" {
			node, err = b.bindStringAggregate(r, name)
		} else {
			node, err = b.bindCount(r, name)
		}
		if err != nil {
			return nil, err
		}
		node, err = b.ownAggregate(node)
		if err != nil {
			return nil, err
		}
	case parser.CaseExpr:
		var err error
		node, err = b.bindCase(r)
		if err != nil {
			return nil, err
		}
	case parser.ConditionalExpr:
		var err error
		node, err = b.bindConditional(r)
		if err != nil {
			return nil, err
		}
	case parser.FunctionExpr:
		var err error
		node, err = b.bindFunction(r)
		if err != nil {
			return nil, err
		}
	case parser.CallExpr:
		var err error
		node, err = b.bindSequenceCall(r)
		if err != nil {
			return nil, err
		}
	case parser.RegclassExpr:
		var err error
		node, _, err = b.bindRegclass(r.Child)
		if err != nil {
			return nil, err
		}
	case parser.ColumnExpr:
		if b.defaultConsumer {
			return nil, expressionError("0A000", "cannot use column reference in DEFAULT expression", r.Pos)
		}
		if policy.context == assignmentContext {
			return nil, expressionError("0A000", "column references outside SELECT are unsupported", r.Pos)
		}
		var err error
		node, err = b.column(r)
		if err != nil {
			return nil, err
		}
	case parser.ParameterExpr:
		if b.checkLiterals != nil {
			if r.Number <= 0 || r.Number > len(b.checkLiterals) {
				return nil, expressionError("42P02", fmt.Sprintf("there is no parameter $%d", r.Number), r.Pos)
			}
			node = b.checkLiterals[r.Number-1]
			break
		}
		if b.noParameters || b.defaultConsumer || r.Number <= 0 {
			return nil, expressionError("42P02", fmt.Sprintf("there is no parameter $%d", r.Number), r.Pos)
		}
		u := policy.expected
		if established := b.updateInputParameters[r.Number]; established.Type != nil {
			u = established
		}
		// A retained prepared parameter is an input contract, not the current
		// destination column's type. Reanalysis adds ordinary coercion from
		// that immutable source to the fresh destination.
		if b.expected != nil && r.Number <= len(b.expected.Parameters.Types) {
			u = b.expected.Parameters.Types[r.Number-1]
			// Native replacement remints builtin descriptors. Keep the SQL input
			// identity/modifiers, but bind values/providers to this catalog generation.
			if u.Type != nil {
				current := b.typ(u.Type.ID())
				if current == nil && u.Type.IsUserDefined() {
					return nil, sqlError("0A000", "cached plan must not change parameter type")
				}
				u.Type = current
			}
		}
		if u.Type == nil {
			u = b.parameters[r.Number]
		}
		if u.Type == nil {
			b.parameters[r.Number] = sqltype.Usage{}
			node = parameterExpression{expressionInfo{pos: r.Pos, properties: ExprProperties{ParameterDependent: true}}, r.Number}
		} else {
			if err := b.constrain(r.Number, u, r.Pos); err != nil {
				return nil, err
			}
			node = parameterExpression{b.info(u.Type.Usage(), r.Pos, ExprProperties{ParameterDependent: true}), r.Number}
		}
	case parser.LiteralExpr:
		v := r.Value
		u := policy.expected
		var value sqltype.Value
		var err error
		switch v.Kind {
		case parser.NullValue:
			if u.Type == nil {
				u = b.typ(sqltype.Text).Usage()
			}
			value = sqltype.Null(u.Type)
		case parser.BitStringValue:
			value, err = b.inputConstant(v.Text, b.typ(sqltype.Bit).Usage(), v.Pos)
		case parser.StringValue:
			if u.Type == nil {
				u = b.typ(sqltype.Text).Usage()
			}
			inputUsage := u.Type.Usage()
			if u.Type.ID() == sqltype.Interval {
				inputUsage = u
			}
			if u.Type.IsDomain() {
				inputUsage = u.BaseUsage()
				if inputUsage.Type.ID() != sqltype.Interval {
					inputUsage = inputUsage.Type.Usage()
				}
			}
			value, err = b.inputConstant(v.Text, inputUsage, v.Pos)
		case parser.NumericValue:
			value, err = b.inputConstant(v.Text, b.typ(sqltype.Numeric).Usage(), v.Pos)
		case parser.IntegerValue:
			id := sqltype.Int4
			if v.Integer < -2147483648 || v.Integer > 2147483647 {
				id = sqltype.Int8
			}
			value, err = sqltype.Integer(b.typ(id), v.Integer)
		case parser.BooleanValue:
			value = sqltype.Boolean(b.typ(sqltype.Bool), v.Boolean)
		default:
			return nil, fmt.Errorf("stuntgres: invalid scalar literal")
		}
		if err != nil {
			return nil, literalInputError(err, r)
		}
		literalUsage := value.Type().Usage()
		if v.Kind == parser.StringValue && u.Type.BaseType().ID() == sqltype.Interval {
			literalUsage = u.BaseUsage()
		}
		node = literalExpression{b.info(literalUsage, v.Pos, ExprProperties{}), value}
	case parser.TypedLiteralExpr:
		rawType := r.TypeName
		saved, found, err := b.captureAt(r.Pos.Offset)
		if err != nil {
			return nil, err
		}
		if found {
			rawType.Modifiers = nil
		}
		u, err := b.engine.resolveType(rawType, b.op)
		if err != nil {
			return nil, err
		}
		if found {
			u.Typmod = saved.typmod
			if saved.typed {
				u.Typmod = saved.typedTypmod
			}
		}
		inputUsage := u.BaseUsage()
		if inputUsage.Type.ID() != sqltype.Interval {
			inputUsage = inputUsage.Type.Usage()
		}
		value, err := b.inputConstant(r.Text, inputUsage, r.Pos)
		if err != nil {
			return nil, inputErrorAt(err, r.Pos)
		}
		if b.expected == nil {
			for i := range b.temporals {
				if b.temporals[i].offset == r.Pos.Offset && b.temporals[i].typeID == u.Type.ID() {
					b.temporals[i].typed = true
					b.temporals[i].typedTypmod = u.Typmod
				}
			}
		}
		node = literalExpression{b.info(inputUsage, r.Pos, ExprProperties{}), value}
		if inputUsage != u {
			plan, err := sqltype.PlanCoercion(inputUsage, u, sqltype.Explicit)
			if err != nil {
				return nil, err
			}
			node = coercionExpression{b.info(u, r.Pos, coercionProperties(node, plan)), node, plan, sqltype.Explicit}
		}
	case parser.CollateExpr:
		child, err := b.bind(r.Child, policy)
		if err != nil {
			return nil, err
		}
		name := r.Name
		name.Parts = append([]parser.Identifier(nil), name.Parts...)
		name.Parts[0].Pos = r.Pos
		usage, err := b.engine.resolveColumnCollation(child.ResultUsage(), &name, b.op)
		if err != nil {
			return nil, err
		}
		info := b.info(usage, r.Pos, child.Properties())
		info.collationExplicit = r.Pos
		node = collationExpression{info, child}
	case parser.CastExpr:
		var err error
		node, err = b.cast(r, policy)
		if err != nil {
			return nil, err
		}
	case parser.TimeZoneExpr:
		var err error
		node, err = b.bindTimeZone(r, policy)
		if err != nil {
			return nil, err
		}
	case parser.LogicalExpr:
		var err error
		node, err = b.bindLogical(r, policy.context)
		if err != nil {
			return nil, err
		}
	case parser.PredicateExpr:
		if policy.context == assignmentContext {
			return nil, fmt.Errorf("stuntgres: predicate in assignment scalar")
		}
		var err error
		node, err = b.bindPredicate(r, policy.context)
		if err != nil {
			return nil, err
		}
	case parser.OperatorExpr:
		var err error
		if subquery, ok := rowSubqueryComparison(r); ok {
			node, err = b.bindSubquery(subquery)
		} else if len(r.Name.Parts) > 0 && r.Name.Parts[len(r.Name.Parts)-1].Text == "||" {
			node, err = b.bindStringConcat(r)
		} else if len(r.Name.Parts) > 0 && r.Name.Parts[len(r.Name.Parts)-1].Text == "^@" {
			node, err = b.bindStringPrefix(r)
		} else if len(r.Name.Parts) > 0 && likeName(r.Name.Parts[len(r.Name.Parts)-1].Text) {
			node, err = b.bindLike(r)
		} else if len(r.Name.Parts) > 0 && bitwiseName(r.Name.Parts[len(r.Name.Parts)-1].Text) {
			node, err = b.bindBitwise(r)
		} else if len(r.Name.Parts) > 0 && (arithmeticName(r.Name.Parts[len(r.Name.Parts)-1].Text) || symbolicArithmeticName(r.Name.Parts[len(r.Name.Parts)-1].Text)) {
			node, err = b.bindArithmetic(r)
		} else {
			if policy.context == assignmentContext {
				return nil, fmt.Errorf("stuntgres: comparison in assignment scalar")
			}
			node, err = b.bindComparison(r)
		}
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("stuntgres: unsupported scalar node")
	}
	if policy.booleanConstruct != "" && node.ResultUsage().Type.IsDomain() && node.ResultUsage().Type.BaseType().ID() == sqltype.Bool {
		var err error
		node, err = b.coerceConditional(comparisonOperand{node: node}, b.typ(sqltype.Bool).Usage(), policy.booleanConstruct)
		if err != nil {
			return nil, err
		}
	}
	if policy.booleanConstruct != "" && node.ResultUsage().Type.ID() != sqltype.Bool {
		return nil, expressionError("42804", fmt.Sprintf("argument of %s must be type boolean, not type %s", policy.booleanConstruct, node.ResultUsage().Type.DiagnosticName()), parser.ExpressionStart(raw))
	}
	if !policy.deferAssignment && (policy.context == assignmentContext || policy.context == functionArgumentContext) && node.ResultUsage() != policy.expected {
		if policy.expected.Type.ContainsRegclass() {
			if err := b.captureElementInput(); err != nil {
				return nil, err
			}
		}
		plan, err := sqltype.PlanCoercion(node.ResultUsage(), policy.expected, policy.mode)
		if err != nil {
			// Retain the resolved source for the assignment caller's diagnostic.
			if d, ok := err.(*sqlstate.Error); ok && d.Code == "42804" && policy.column != "" {
				copy := *d
				copy.Message = fmt.Sprintf("column \"%s\" is of type %s but expression is of type %s", policy.column, b.diagnosticTypeName(policy.expected.Type), b.diagnosticTypeName(node.ResultUsage().Type))
				copy.Hint = "You will need to rewrite or cast the expression."
				copy.Position = strconv.Itoa(parser.ExpressionStart(raw).Character)
				return node, &copy
			}
			return node, err
		}
		info, err := collatedInfo(b.info(policy.expected, raw.SourcePosition(), coercionProperties(node, plan)), node)
		if err != nil {
			return nil, err
		}
		node = coercionExpression{info, node, plan, policy.mode}
	}
	return node, nil
}

const functionCastHint = "No function matches the given name and argument types. You might need to add explicit type casts."

func functionCastError(raw parser.CastExpr, source string, ambiguous bool) error {
	code, ending, hint := "42883", "does not exist", functionCastHint
	if ambiguous {
		code, ending = "42725", "is not unique"
		hint = "Could not choose a best candidate function. You might need to add explicit type casts."
	}
	name := raw.Callee
	if raw.TypeName.Schema != nil {
		name = raw.TypeName.Schema.Text + "." + name
	}
	d := expressionError(code, fmt.Sprintf("function %s(%s) %s", name, source, ending), raw.Pos).(*sqlstate.Error)
	d.Hint = hint
	return d
}

func (b *expressionBinder) cast(raw parser.CastExpr, outerPolicy expressionPolicy) (BoundExpr, error) {
	if !b.defaultConsumer && b.queryNamespace == nil && b.returningAliases == nil {
		if err := qualifiedCastChild(raw.Child); err != nil {
			return nil, err
		}
	}
	visible := true
	if raw.Syntax == parser.FunctionCast {
		name := parser.ObjectName{Parts: []parser.Identifier{{Text: raw.Callee, Pos: raw.Pos}}}
		if raw.TypeName.Schema != nil {
			name.Parts = []parser.Identifier{*raw.TypeName.Schema, {Text: raw.Callee, Pos: raw.Pos}}
		}
		var err error
		visible, err = b.builtinVisible(name, functionLookup)
		if err != nil {
			return nil, err
		}
	}
	if raw.Syntax == parser.FunctionCast && (!visible || raw.Callee != "oid" && raw.Callee != "int4" && raw.Callee != "int8" && raw.Callee != "text" && raw.Callee != "bool") {
		source := "unknown"
		if literal, ok := raw.Child.(parser.LiteralExpr); !ok || (literal.Value.Kind != parser.StringValue && literal.Value.Kind != parser.NullValue) {
			child, err := b.bind(raw.Child, expressionPolicy{context: projectionContext})
			if err != nil {
				return nil, err
			}
			if child.ResultUsage().Type != nil {
				source = child.ResultUsage().Type.DiagnosticName()
			}
		}
		return nil, functionCastError(raw, source, false)
	}
	rawNull, isNull := raw.Child.(parser.LiteralExpr)
	unknownNull := raw.Syntax != parser.FunctionCast && isNull && rawNull.Value.Kind == parser.NullValue
	var target sqltype.Usage
	var err error
	if outerPolicy.stringInput {
		target, err = b.engine.resolveStringAnnotationType(raw.TypeName, b.op, unknownNull)
	} else {
		target, err = b.engine.resolveAnnotationType(raw.TypeName, b.op, unknownNull)
	}
	if err != nil {
		return nil, err
	}
	if target.Type.ID() == sqltype.Unknown {
		// UNKNOWN is syntax provenance only, never an executable datum type.
		return b.bind(raw.Child, outerPolicy)
	}
	if target.Type.ID() == sqltype.Regclass {
		node, _, err := b.bindRegclass(raw.Child)
		return node, err
	}
	if !target.Type.IsUserDefined() && !sqltype.StoredScalarCastType(target.Type.ID()) && !(outerPolicy.stringInput && target.Type.ID() == sqltype.Char) && target.Type.ID() != sqltype.PgNodeTree && !target.Type.IsArray() {
		return nil, expressionError("0A000", "explicit casts are supported only for int4, int8, text and bool", raw.Pos)
	}
	if target.Type.ContainsRegclass() {
		if err := b.captureElementInput(); err != nil {
			return nil, err
		}
	}
	policy := expressionPolicy{context: projectionContext}
	switch child := raw.Child.(type) {
	case parser.LiteralExpr:
		if child.Value.Kind == parser.StringValue || child.Value.Kind == parser.NullValue {
			policy.expected = target
			if target.Type.IsDomain() {
				policy.expected = target.Type.BaseType().Usage()
				if policy.expected.Type.ID() == sqltype.Interval {
					policy.expected = target.BaseUsage()
				}
			}
		}
	case parser.CastExpr:
		if unknownNullSyntax(child) {
			policy.expected = target
		}
	case parser.ParameterExpr:
		if b.checkLiterals != nil {
			break // bind resolves the captured literal without parameter inference.
		}
		if b.noParameters || b.defaultConsumer {
			return nil, expressionError("42P02", fmt.Sprintf("there is no parameter $%d", child.Number), child.Pos)
		}
		if b.parameters[child.Number].Type == nil {
			if raw.Syntax == parser.FunctionCast && target.Type.ID() != sqltype.Text {
				if child.Number <= 0 {
					return nil, expressionError("42P02", fmt.Sprintf("there is no parameter $%d", child.Number), child.Pos)
				}
				return nil, functionCastError(raw, "unknown", true)
			}
			policy.expected = target
		}
	}
	child, err := b.bind(raw.Child, policy)
	if err != nil {
		return nil, err
	}
	source := child.ResultUsage()
	if source.Type.ContainsRegclass() && target.Type.BaseType().ID() == sqltype.Text {
		b.regclassConcat = true
	}
	if target.Collation.ID != 0 && source.Collation.ID != 0 {
		target.Collation = source.Collation
	}
	if outerPolicy.stringInput && target.Type.ID() == sqltype.Char && source == target {
		// The literal/input path already applied the CHAR codec. Retain that
		// resolved datum without admitting CHAR to general explicit coercion.
		return child, nil
	}
	plan, err := sqltype.PlanCoercion(source, target, sqltype.Explicit)
	if err != nil {
		if !source.Type.BaseType().IsEnum() && !target.Type.BaseType().IsEnum() && source.Type.BaseType().ID() != sqltype.JSON && target.Type.BaseType().ID() != sqltype.JSON && source.Type.BaseType().ID() != sqltype.JSONB && target.Type.BaseType().ID() != sqltype.JSONB && source.Type.ID() != sqltype.PgNodeTree && target.Type.ID() != sqltype.PgNodeTree && (!sqltype.BaselineCastType(source.Type.ID()) || !sqltype.BaselineCastType(target.Type.ID())) && !sqltype.OIDCastPair(source.Type.ID(), target.Type.ID()) && !sqltype.TemporalCastPair(source.Type.ID(), target.Type.ID()) {
			return nil, expressionError("0A000", "explicit casts are supported only for int4, int8, text and bool", raw.Pos)
		}
		if raw.Syntax == parser.FunctionCast {
			return nil, functionCastError(raw, source.Type.DiagnosticName(), false)
		}
		return nil, expressionError("42846", fmt.Sprintf("cannot cast type %s to %s", b.diagnosticTypeName(source.Type), b.diagnosticTypeName(target.Type)), raw.Pos)
	}
	if source == target {
		return child, nil
	}
	pos := raw.Pos
	if start := child.SourcePosition(); start.Character > 0 && (pos.Character <= 0 || start.Character < pos.Character) {
		pos = start
	}
	info, err := collatedInfo(b.info(target, pos, coercionProperties(child, plan)), child)
	if err != nil {
		return nil, err
	}
	return coercionExpression{info, child, plan, sqltype.Explicit}, nil
}

// Strong column/function names survive standard casts; weak literal names do not.
func expressionName(raw parser.Expr, usage sqltype.Usage, bound BoundExpr) (string, bool) {
	for {
		coercion, ok := bound.(coercionExpression)
		if !ok {
			break
		}
		bound = coercion.child
	}
	switch r := raw.(type) {
	case parser.SubqueryExpr:
		if r.Kind == parser.SubqueryExists {
			return "exists", true
		}
		if r.Kind == parser.SubqueryScalar {
			if child, ok := bound.(subqueryExpression); ok && len(child.queryPlan.Root.Schema()) == 1 {
				name := child.queryPlan.Root.Schema()[0].Name
				return name, name != "?column?" || len(r.Query.Labels) > 0 && r.Query.Labels[0].Text != ""
			}
		}
		return "?column?", false
	case parser.IdentityExpr:
		return r.Name, true
	case parser.CurrentDateTimeExpr:
		return r.Kind.Name(), true
	case parser.AggregateExpr:
		return r.Name, true
	case parser.ConditionalExpr:
		return conditionalName(r), true
	case parser.CaseExpr:
		if r.Else != nil {
			var otherwise BoundExpr
			if expr, ok := bound.(caseExpression); ok {
				otherwise = expr.otherwise
			}
			if name, strong := expressionName(r.Else, usage, otherwise); strong {
				return name, true
			}
		}
		return "case", false
	case parser.ColumnExpr:
		return r.Name.Parts[len(r.Name.Parts)-1].Text, true
	case parser.FunctionExpr:
		return r.Name.Parts[len(r.Name.Parts)-1].Text, true
	case parser.CallExpr:
		return r.Name.Parts[len(r.Name.Parts)-1].Text, true
	case parser.RegclassExpr:
		return "regclass", false
	case parser.TimeZoneExpr:
		return "timezone", true
	case parser.CollateExpr:
		if expr, ok := bound.(collationExpression); ok {
			bound = expr.child
		}
		return expressionName(r.Child, usage, bound)
	case parser.CastExpr:
		if r.Syntax == parser.FunctionCast {
			return r.Callee, true
		}
		if name, strong := expressionName(r.Child, usage, bound); strong {
			return name, true
		}
		return usage.Type.Name(), false
	case parser.TypedLiteralExpr:
		return usage.Type.Name(), false
	default:
		return "?column?", false
	}
}
func inputErrorAt(err error, pos parser.Position) error {
	if d, ok := err.(*sqlstate.Error); ok && (d.Code == "0A000" || d.Code == "22P02" || d.Code == "22P05" || d.Code == "2202E" || d.Code == "22003" || d.Code == "22007" || d.Code == "22008" || d.Code == "22009" || d.Code == "22015" || d.Code == "22023" || d.Code == "42601") {
		copy := *d
		if pos.Character > 0 {
			copy.Position = strconv.Itoa(pos.Character)
		}
		return &copy
	}
	return err
}
func literalInputError(err error, raw parser.LiteralExpr) error {
	return inputErrorAt(err, raw.Value.Pos)
}

// Explicit typed literals use this only to discover their frozen modifier
// before resolving the raw declaration again. Scalar reconstruction must use
// captured with the complete contextual usage.
func (b *expressionBinder) captureAt(offset int) (temporalConstant, bool, error) {
	if b.expected != nil {
		captures := b.expected.temporals
		i := sort.Search(len(captures), func(i int) bool { return captures[i].offset >= offset })
		if i < len(captures) && captures[i].offset == offset {
			saved := captures[i]
			for i++; i < len(captures) && captures[i].offset == offset; i++ {
				if captures[i].typeID != saved.typeID || captures[i].typmod != saved.typmod ||
					captures[i].typed != saved.typed || captures[i].typedTypmod != saved.typedTypmod {
					return temporalConstant{}, false, fmt.Errorf("stuntgres: ambiguous captured temporal metadata")
				}
			}
			return saved, true, nil
		}
	}
	return temporalConstant{}, false, nil
}

func (b *expressionBinder) captured(offset int, usage sqltype.Usage) (temporalConstant, bool, error) {
	if b.expected != nil {
		captures := b.expected.temporals
		i := sort.Search(len(captures), func(i int) bool { return captures[i].offset >= offset })
		if i < len(captures) && captures[i].offset == offset {
			for ; i < len(captures) && captures[i].offset == offset; i++ {
				capture := captures[i]
				if capture.typeID == usage.Type.ID() && capture.typmod == usage.Typmod {
					return capture, true, nil
				}
			}
			return temporalConstant{}, false, fmt.Errorf("stuntgres: captured temporal usage changed")
		}
	}
	return temporalConstant{}, false, nil
}
func (b *expressionBinder) inputConstant(text string, u sqltype.Usage, pos parser.Position) (sqltype.Value, error) {
	if u.Type.ID() == sqltype.Regclass {
		node, _, err := b.bindRegclass(parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: text, Pos: pos}})
		if err != nil {
			return sqltype.Value{}, err
		}
		return node.(literalExpression).value, nil
	}
	if u.Type.BaseType().IsArray() {
		c, found, err := b.captured(pos.Offset, u)
		if err != nil {
			return sqltype.Value{}, err
		}
		if found {
			return c.value.Retag(u.Type)
		}
		if u.Type.ContainsRegclass() {
			if err := b.captureElementInput(); err != nil {
				return sqltype.Value{}, err
			}
		}
		value, err := u.Type.InputWithEnvironment(b.ctx, text, -1, b.input)
		if err != nil {
			if diagnostic, ok := err.(*sqlstate.Error); ok && pos.Character > 0 {
				copy := *diagnostic
				copy.Position = strconv.Itoa(pos.Character)
				return sqltype.Value{}, &copy
			}
			return sqltype.Value{}, err
		}
		if b.expected == nil {
			i := sort.Search(len(b.temporals), func(i int) bool { return b.temporals[i].offset >= pos.Offset })
			b.temporals = append(b.temporals, temporalConstant{})
			copy(b.temporals[i+1:], b.temporals[i:])
			b.temporals[i] = temporalConstant{offset: pos.Offset, typmod: u.Typmod, typeID: u.Type.ID(), value: value}
		}
		return value, nil
	}
	if u.Type.IsEnum() {
		value, err := u.Type.InputWithEnvironment(b.ctx, text, -1, b.input)
		if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "22P02" {
			copy := *diagnostic
			copy.Message = fmt.Sprintf("invalid input value for enum %s: \"%s\"", b.diagnosticTypeName(u.Type), text)
			return sqltype.Value{}, &copy
		}
		return value, err
	}
	if u.Type.ID() != sqltype.Interval && u.Type.ID() != sqltype.Timestamp && u.Type.ID() != sqltype.Time && u.Type.ID() != sqltype.Date && u.Type.ID() != sqltype.Timestamptz && u.Type.ID() != sqltype.Timetz {
		return u.Type.InputWithEnvironment(b.ctx, text, -1, b.input)
	}
	c, found, err := b.captured(pos.Offset, u)
	if err != nil {
		return sqltype.Value{}, err
	}
	if found {
		if c.typeID == sqltype.Timetz {
			return sqltype.TimetzValue(u.Type, c.scalar, c.west)
		}
		if c.typeID == sqltype.Interval {
			return sqltype.IntervalValue(u.Type, c.months, c.days, c.scalar)
		}
		if c.typeID == sqltype.Time {
			return sqltype.TimeValue(u.Type, c.scalar)
		}
		if c.typeID == sqltype.Date {
			return sqltype.DateValue(u.Type, c.scalar)
		}
		return sqltype.TimestampValue(u.Type, c.scalar)
	}
	inputTypmod := int32(-1)
	if u.Type.ID() == sqltype.Interval {
		inputTypmod = u.Typmod
	}
	v, err := u.Type.InputWithEnvironment(b.ctx, text, inputTypmod, b.input)
	if err == nil && u.Typmod >= 0 {
		var p sqltype.CoercionPlan
		p, err = sqltype.PlanCoercion(u.Type.Usage(), u, sqltype.Assignment)
		if err == nil {
			v, err = p.Apply(b.ctx, v, b.input, sqltype.OutputEnvironment{Zone: b.input.Zone, DateStyle: b.input.DateStyle, IntervalStyle: b.input.IntervalStyle})
		}
	}
	if err != nil {
		return sqltype.Value{}, err
	}
	if b.expected == nil {
		var n int64
		var months, days int32
		var err error
		if u.Type.ID() == sqltype.Interval {
			months, days, n, err = v.Interval()
		} else if u.Type.ID() == sqltype.Time || u.Type.ID() == sqltype.Timetz {
			n, err = v.Time()
		} else if u.Type.ID() == sqltype.Date {
			n, err = v.Date()
		} else {
			n, err = v.Timestamp()
		}
		if err != nil {
			return sqltype.Value{}, err
		}
		// Symmetric comparison can contextualize an earlier unknown literal
		// after its right operand. Keep captures ordered for rebind lookup.
		i := sort.Search(len(b.temporals), func(i int) bool { return b.temporals[i].offset >= pos.Offset })
		b.temporals = append(b.temporals, temporalConstant{})
		copy(b.temporals[i+1:], b.temporals[i:])
		west := int32(0)
		if u.Type.ID() == sqltype.Timetz {
			west, err = v.TimeZoneOffset()
			if err != nil {
				return sqltype.Value{}, err
			}
		}
		b.temporals[i] = temporalConstant{offset: pos.Offset, scalar: n, typmod: u.Typmod, typeID: u.Type.ID(), west: west, months: months, days: days}
	}
	return v, nil
}

func (b *expressionBinder) captureElementInput() error {
	if b.input.Regclass != nil {
		return nil
	}
	auth, err := b.bindingAuthorization()
	if err != nil {
		return err
	}
	b.input.Regclass, err = b.engine.captureRegclassInput(auth)
	return err
}
func operatorExpressionError(err error, left, right *sqltype.Descriptor, raw parser.OperatorExpr) error {
	d, ok := err.(*sqlstate.Error)
	if !ok || d.Code != "42883" && d.Code != "42725" {
		return err
	}
	copy := *d
	name := func(t *sqltype.Descriptor) string {
		if t == nil {
			return "unknown"
		}
		return t.DiagnosticName()
	}
	if len(raw.Args) == 1 {
		copy.Message = fmt.Sprintf("operator does not exist: %s %s", objectNameText(raw.Name), name(left))
		copy.Hint = "No operator matches the given name and argument type. You might need to add an explicit type cast."
	} else {
		copy.Message = fmt.Sprintf("operator does not exist: %s %s %s", name(left), objectNameText(raw.Name), name(right))
		copy.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
	}
	if copy.Code == "42725" {
		copy.Message = strings.Replace(copy.Message, "operator does not exist:", "operator is not unique:", 1)
		copy.Hint = "Could not choose a best candidate operator. You might need to add explicit type casts."
	}
	copy.Position = strconv.Itoa(raw.Pos.Character)
	return &copy
}

// Unknown state exists only during analysis. It never becomes an executable Value.
type comparisonOperand struct {
	node BoundExpr
	raw  parser.Expr
}

func (o comparisonOperand) descriptor() *sqltype.Descriptor {
	if o.node == nil {
		return nil
	}
	return o.node.ResultUsage().Type
}

func (b *expressionBinder) comparisonOperand(raw parser.Expr) (comparisonOperand, error) {
	return b.comparisonOperandPolicy(raw, expressionPolicy{context: projectionContext})
}

func (b *expressionBinder) comparisonOperandPolicy(raw parser.Expr, policy expressionPolicy) (comparisonOperand, error) {
	if literal, ok := raw.(parser.LiteralExpr); ok && (literal.Value.Kind == parser.StringValue || literal.Value.Kind == parser.NullValue) {
		return comparisonOperand{raw: raw}, nil
	}
	if annotation, ok := raw.(parser.CastExpr); ok && unknownNullSyntax(raw) {
		var target sqltype.Usage
		var err error
		if policy.stringInput {
			target, err = b.engine.resolveStringAnnotationType(annotation.TypeName, b.op, true)
		} else {
			target, err = b.engine.resolveAnnotationType(annotation.TypeName, b.op, true)
		}
		if err != nil {
			return comparisonOperand{}, err
		}
		if target.Type.ID() == sqltype.Unknown {
			return comparisonOperand{raw: annotation.Child}, nil
		}
	}
	switch raw.(type) {
	case parser.CurrentDateTimeExpr, parser.IdentityExpr, parser.ColumnExpr, parser.ParameterExpr, parser.LiteralExpr, parser.TypedLiteralExpr, parser.CastExpr, parser.CollateExpr, parser.TimeZoneExpr, parser.OperatorExpr, parser.CallExpr, parser.RegclassExpr, parser.FunctionExpr, parser.AggregateExpr, parser.CaseExpr, parser.ConditionalExpr, parser.LogicalExpr, parser.PredicateExpr, parser.IndexSubqueryExpr, parser.SubqueryExpr:
	default:
		return comparisonOperand{}, fmt.Errorf("stuntgres: invalid comparison operand")
	}
	node, err := b.bind(raw, policy)
	return comparisonOperand{node: node, raw: raw}, err
}

func (b *expressionBinder) coerceParameter(p parameterExpression, u sqltype.Usage) (BoundExpr, error) {
	if existing := b.parameters[p.number]; existing.Type != nil && existing.Type != u.Type {
		d := expressionError("42P08", fmt.Sprintf("inconsistent types deduced for parameter $%d", p.number), p.pos).(*sqlstate.Error)
		d.Detail = fmt.Sprintf("%s versus %s", existing.Type.DiagnosticName(), u.Type.DiagnosticName())
		return nil, d
	}
	b.parameters[p.number] = u.Type.Usage()
	return parameterExpression{b.info(u.Type.Usage(), p.pos, p.properties), p.number}, nil
}

func (b *expressionBinder) finalizeOperand(o comparisonOperand, typ *sqltype.Descriptor) (BoundExpr, error) {
	if o.descriptor() != nil {
		return o.node, nil
	}
	if p, ok := o.node.(parameterExpression); ok {
		return b.coerceParameter(p, typ.Usage())
	}
	if o.raw == nil {
		return nil, fmt.Errorf("stuntgres: missing unknown comparison operand")
	}
	return b.bind(o.raw, expressionPolicy{context: projectionContext, expected: typ.Usage()})
}

// Apply existing implicit integer→OID and TIME→TIMETZ edges after
// occurrence-sensitive unknown inference. Coercion owns NULL/range/environment
// semantics and volatility; the comparison provider remains homogeneous.
func (b *expressionBinder) coerceComparisonOperands(left, right BoundExpr) (BoundExpr, BoundExpr, error) {
	lt, rt := left.ResultUsage().Type, right.ResultUsage().Type
	integer := func(t *sqltype.Descriptor) bool {
		return t.ID() == sqltype.Int2 || t.ID() == sqltype.Int4 || t.ID() == sqltype.Int8
	}
	var child BoundExpr
	var target sqltype.Usage
	switch {
	case lt.ID() == sqltype.Oid && integer(rt):
		child, target = right, lt.Usage()
	case rt.ID() == sqltype.Oid && integer(lt):
		child, target = left, rt.Usage()
	case lt.ID() == sqltype.Timetz && rt.ID() == sqltype.Time:
		child, target = right, lt.Usage()
	case rt.ID() == sqltype.Timetz && lt.ID() == sqltype.Time:
		child, target = left, rt.Usage()
	default:
		return left, right, nil
	}
	plan, err := sqltype.PlanCoercion(child.ResultUsage(), target, sqltype.Implicit)
	if err != nil {
		return nil, nil, err
	}
	converted := coercionExpression{b.info(target, child.SourcePosition(), coercionProperties(child, plan)), child, plan, sqltype.Implicit}
	if lt.ID() == sqltype.Oid || lt.ID() == sqltype.Timetz {
		right = converted
	} else {
		left = converted
	}
	return left, right, nil
}

func (b *expressionBinder) bindComparison(raw parser.OperatorExpr) (BoundExpr, error) {
	if len(raw.Args) != 2 || len(raw.Name.Parts) < 1 || len(raw.Name.Parts) > 2 {
		return nil, fmt.Errorf("stuntgres: invalid comparison expression")
	}
	var operator sqltype.ComparisonOperator
	switch raw.Name.Parts[len(raw.Name.Parts)-1].Text {
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
		return nil, fmt.Errorf("stuntgres: invalid comparison operator")
	}
	// Copy each occurrence's then-known parameter type before resolving either side.
	left, err := b.comparisonOperand(raw.Args[0])
	if err != nil {
		return nil, err
	}
	right, err := b.comparisonOperand(raw.Args[1])
	if err != nil {
		return nil, err
	}
	return b.bindComparisonOperands(raw, operator, left, right)
}

func (b *expressionBinder) bindComparisonOperands(raw parser.OperatorExpr, operator sqltype.ComparisonOperator, left, right comparisonOperand) (BoundExpr, error) {
	lt, rt := left.descriptor(), right.descriptor()
	visible, err := b.builtinVisible(raw.Name, operatorLookup)
	if err != nil {
		return nil, err
	}
	if !visible {
		return nil, b.comparisonError(sqlError("42883", "operator does not exist"), lt, rt, raw)
	}
	// Admit enum operators against the actual operands before conditional
	// coercion erases domain identity. Unknown operands retain normal inference.
	if lt != nil && rt != nil && (lt.BaseType().IsEnum() || rt.BaseType().IsEnum()) {
		if _, err := sqltype.BindComparison(lt, rt); err != nil {
			return nil, b.comparisonError(err, lt, rt, raw)
		}
	}
	if lt != nil {
		lt = lt.BaseType()
	}
	if rt != nil {
		rt = rt.BaseType()
	}
	if lt == nil {
		lt = rt
	}
	if rt == nil {
		rt = lt
	}
	if lt == nil {
		lt, rt = b.typ(sqltype.Text), b.typ(sqltype.Text)
	}
	l, err := b.coerceConditional(left, lt.Usage(), raw.Name.Parts[len(raw.Name.Parts)-1].Text)
	if err != nil {
		return nil, err
	}
	r, err := b.coerceConditional(right, rt.Usage(), raw.Name.Parts[len(raw.Name.Parts)-1].Text)
	if err != nil {
		return nil, err
	}
	l, r, err = b.coerceComparisonOperands(l, r)
	if err != nil {
		return nil, err
	}
	predicate, err := sqltype.BindComparisonPredicate(operator, l.ResultUsage().Type, r.ResultUsage().Type, b.typ(sqltype.Bool))
	if err != nil {
		return nil, b.comparisonError(err, left.descriptor(), right.descriptor(), raw)
	}
	p, rp := l.Properties(), r.Properties()
	p.RowDependent = p.RowDependent || rp.RowDependent
	p.ParameterDependent = p.ParameterDependent || rp.ParameterDependent
	p.StageDependent = p.StageDependent || rp.StageDependent
	if rp.Volatility > p.Volatility {
		p.Volatility = rp.Volatility
	}
	if predicate.SessionDependent() && p.Volatility < Stable {
		p.Volatility = Stable
	}
	namespace := ""
	if len(raw.Name.Parts) == 2 {
		namespace = raw.Name.Parts[0].Text
	}
	return comparisonExpression{b.info(b.typ(sqltype.Bool).Usage(), raw.Pos, p), l, r, predicate, raw.Name.Parts[len(raw.Name.Parts)-1].Text, namespace}, nil
}

type arithmeticExpression struct {
	expressionInfo
	left, right BoundExpr
	provider    sqltype.Arithmetic
	operator    sqltype.ArithmeticOperator
	namespace   string
}

func (e arithmeticExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	a, err := e.left.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if e.right == nil {
		return e.provider.Unary(c.Context, a, c.Input)
	}
	v, err := e.right.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	return e.provider.Binary(c.Context, a, v, c.Input)
}
func arithmeticName(name string) bool {
	return name == "+" || name == "-" || name == "*" || name == "/" || name == "%" || name == "^"
}
func symbolicArithmeticName(name string) bool {
	if len(name) < 2 {
		return false
	}
	for _, c := range name {
		if c != '+' && c != '-' && c != '*' && c != '/' && c != '%' && c != '^' {
			return false
		}
	}
	return true
}
func (b *expressionBinder) bindArithmetic(raw parser.OperatorExpr) (BoundExpr, error) {
	if len(raw.Name.Parts) < 1 || len(raw.Name.Parts) > 2 || len(raw.Args) < 1 || len(raw.Args) > 2 {
		return nil, fmt.Errorf("stuntgres: invalid arithmetic expression")
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	// Only syntactic signed constants normalize. Other unary expressions keep
	// their provider tree and runtime overflow/error phase.
	if (name == "+" || name == "-") && len(raw.Args) == 1 && len(raw.Name.Parts) == 1 {
		if literal, ok := raw.Args[0].(parser.LiteralExpr); ok {
			switch literal.Value.Kind {
			case parser.IntegerValue:
				if name == "-" && literal.Value.Integer != -1<<63 {
					literal.Value.Integer = -literal.Value.Integer
				} else if name == "-" {
					break
				}
				literal.Value.Pos = raw.Pos
				return b.bind(literal, expressionPolicy{context: projectionContext})
			case parser.NumericValue:
				if name == "-" {
					if strings.HasPrefix(literal.Value.Text, "-") {
						literal.Value.Text = strings.TrimPrefix(literal.Value.Text, "-")
					} else {
						literal.Value.Text = "-" + literal.Value.Text
					}
				}
				return b.bind(literal, expressionPolicy{context: projectionContext})
			}
		}
	}
	operator := sqltype.ArithmeticAdd
	switch name {
	case "-":
		operator = sqltype.ArithmeticSubtract
	case "*":
		operator = sqltype.ArithmeticMultiply
	case "/":
		operator = sqltype.ArithmeticDivide
	case "%":
		operator = sqltype.ArithmeticRemainder
	case "^":
		operator = sqltype.ArithmeticPower
	}
	left, err := b.comparisonOperand(raw.Args[0])
	if err != nil {
		return nil, err
	}
	var right comparisonOperand
	if len(raw.Args) == 2 {
		right, err = b.comparisonOperand(raw.Args[1])
		if err != nil {
			return nil, err
		}
	}
	lt, rt := left.descriptor(), right.descriptor()
	if lt != nil {
		lt = lt.BaseType()
	}
	if rt != nil {
		rt = rt.BaseType()
	}
	visible, err := b.builtinVisible(raw.Name, operatorLookup)
	if err != nil {
		return nil, err
	}
	if !visible {
		return nil, operatorExpressionError(sqlError("42883", "operator does not exist"), lt, rt, raw)
	}
	if symbolicArithmeticName(name) {
		return nil, operatorExpressionError(&sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883"}, lt, rt, raw)
	}
	signature, err := sqltype.ResolveArithmetic(operator, lt, rt, len(raw.Args) == 1)
	if err != nil {
		return nil, operatorExpressionError(err, lt, rt, raw)
	}
	typ := b.typ(signature.Result)
	if signature.Result == sqltype.Numeric {
		return nil, expressionError("0A000", fmt.Sprintf("arithmetic for %s operands is unsupported", typ.DiagnosticName()), raw.Pos)
	}
	leftType := b.typ(signature.Left)
	var rightType *sqltype.Descriptor
	if len(raw.Args) == 2 {
		rightType = b.typ(signature.Right)
	}
	provider, err := sqltype.BindArithmetic(operator, leftType, rightType, typ)
	if err != nil {
		return nil, err
	}
	finish := func(o comparisonOperand, target *sqltype.Descriptor) (BoundExpr, error) {
		source := o.descriptor()
		if source == nil {
			source = target
		}
		n, err := b.finalizeOperand(o, source)
		if err != nil {
			return nil, err
		}
		if source != target {
			plan, err := sqltype.PlanCoercion(n.ResultUsage(), target.Usage(), sqltype.Implicit)
			if err != nil {
				return nil, err
			}
			n = coercionExpression{b.info(target.Usage(), n.SourcePosition(), coercionProperties(n, plan)), n, plan, sqltype.Implicit}
		}
		return n, nil
	}
	l, err := finish(left, leftType)
	if err != nil {
		return nil, err
	}
	var r BoundExpr
	p := l.Properties()
	if len(raw.Args) == 2 {
		r, err = finish(right, rightType)
		if err != nil {
			return nil, err
		}
		rp := r.Properties()
		if rp.Volatility > p.Volatility {
			p.Volatility = rp.Volatility
		}
		p.RowDependent = p.RowDependent || rp.RowDependent
		p.ParameterDependent = p.ParameterDependent || rp.ParameterDependent
		p.StageDependent = p.StageDependent || rp.StageDependent
	}
	if provider.SessionDependent() && p.Volatility < Stable {
		p.Volatility = Stable
	}
	// Contextual consumers use the cached expression start; signature errors
	// above retain the raw operator token.
	pos := raw.Pos
	for _, child := range [...]BoundExpr{l, r} {
		if child != nil {
			start := child.SourcePosition()
			if start.Character > 0 && (pos.Character <= 0 || start.Character < pos.Character) {
				pos = start
			}
		}
	}
	namespace := ""
	if len(raw.Name.Parts) == 2 {
		namespace = raw.Name.Parts[0].Text
	}
	return arithmeticExpression{b.info(typ.Usage(), pos, p), l, r, provider, operator, namespace}, nil
}
