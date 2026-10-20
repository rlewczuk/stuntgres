package engine

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// A folded value retains conservative type/dependence metadata and the original
// binding retains its lifetime manifest, but the value is not a callable node.
func foldedExpressionInfo(node expr.Expr) expr.Info {
	info := expr.InfoOf(node)
	info.RoutineReference = nil
	return info
}

func (h *statementHost) FunctionArrayRandomOwner() (expr.RandomOwner, error) {
	scope := h.scope()
	if scope.op == nil || scope.op.session == nil {
		return nil, fmt.Errorf("stuntgres: missing array random session scope")
	}
	return &scope.op.session.arrayRandom, nil
}

func (h *statementHost) FunctionBinaryConversion(source, destination int) (*sqltype.EncodingConversionHandler, error) {
	procedure, err := h.scope().conversionProcedure(source, destination)
	if err != nil || procedure == nil {
		return nil, err
	}
	return &procedure.Conversion, nil
}

func coercionProperties(child expr.Expr, plan sqltype.CoercionPlan) expr.Properties {
	properties := child.Properties()
	properties.RuntimeDomainConversion = properties.RuntimeDomainConversion || plan.ChecksDomain()
	if plan.SessionDependent() && properties.Volatility < expr.Stable {
		properties.Volatility = expr.Stable
	}
	return properties
}

// preflightCoercions materializes immutable conversions from the imported
// execution frame, never rows or stages. Only changed branches are copied;
// analyzed nodes and their conservative dependence metadata remain immutable.
func preflightCoercions(c *exec.Context, expression expr.Expr) (expr.Expr, bool, error) {
	return preflightCoercionsWithPolicy(c, expression, false)
}

// Consumed stored defaults and standalone CHECK preparation use decisive
// constant planning. Ordinary source, DELETE and supplied-value preflight
// retain their existing traversal demand.
func preflightCoercionsWithPolicy(c *exec.Context, expression expr.Expr, decisiveDefaults bool) (expr.Expr, bool, error) {
	node, changed, err := preflightCoercionNode(c, expression, decisiveDefaults)
	if err != nil {
		return nil, false, err
	}
	node, folded, err := foldStrictParameterNull(c, node)
	return node, changed || folded, err
}

func preflightCoercionNode(c *exec.Context, expression expr.Expr, decisiveDefaults bool) (expr.Expr, bool, error) {
	if err := exec.EvaluationContext(c); err != nil {
		return nil, false, err
	}
	recipe, err := routine.ExpressionRecipeDescriptor(expression)
	if err != nil {
		return nil, false, err
	}
	if recipe != nil && (!c.RoutineGraft || c.RoutineConsumer != recipe) || recipe == nil && c.RoutineGraft {
		parent := c
		local := *c
		if err := selectExpressionRecipe(&local, recipe); err != nil {
			return nil, false, err
		}
		c = &local
		defer func() { parent.PreparedDomainChecks = c.PreparedDomainChecks }()
	}
	if c.SpecializeRuleParameters && expression != nil && expression.Properties().ParameterDependent {
		specialized, changed, err := specializeRuleParameterExpression(c, expression)
		if err != nil {
			return nil, false, err
		}
		if changed {
			folded, foldedChanged, err := preflightCoercionsWithPolicy(c, specialized, decisiveDefaults)
			return folded, changed || foldedChanged, err
		}
	}
	switch e := expression.(type) {
	case plan.SubqueryExpr:
		if !c.PlanStaticOperators || e.CTEEdge || e.QueryPlan == nil {
			return e, false, nil
		}
		input := e.QueryPlan.Root
		var presence *plan.ProjectNode
		if e.PresenceOnly {
			// Eligible EXISTS uses nextOccurrence, never its outer targets.
			// Keep actual child predicate and presence/sort preparation.
			presence = input.(*plan.ProjectNode)
			input = presence.Child
		}
		root, err := preflightQuery(c, input)
		if err != nil {
			return nil, false, err
		}
		if root == input {
			return e, false, nil
		}
		if presence != nil {
			copy := *presence
			copy.Child = root
			root = &copy
		}
		query := *e.QueryPlan
		query.Root = root
		e.QueryPlan = &query
		return e, true, nil
	case plan.DomainValueExpr:
		return e, false, nil
	case plan.InstalledSoundexExpr:
		var args []expr.Expr
		for i, arg := range e.Args {
			next, changed, err := preflightCoercionsWithPolicy(c, arg, decisiveDefaults)
			if err != nil {
				return nil, false, err
			}
			if changed {
				if args == nil {
					args = append([]expr.Expr(nil), e.Args...)
				}
				args[i] = next
			}
		}
		if args != nil {
			next, err := e.Reconstruct(e.Info, args)
			if err != nil {
				return nil, false, err
			}
			e.Soundex = next
		}
		return e, args != nil, nil
	case plan.InstalledMetaphoneExpr:
		var args []expr.Expr
		for i, arg := range e.Args {
			next, changed, err := preflightCoercionsWithPolicy(c, arg, decisiveDefaults)
			if err != nil {
				return nil, false, err
			}
			if changed {
				if args == nil {
					args = append([]expr.Expr(nil), e.Args...)
				}
				args[i] = next
			}
		}
		if args != nil {
			next, err := e.Reconstruct(e.Info, args)
			if err != nil {
				return nil, false, err
			}
			e.Metaphone = next
		}
		return e, args != nil, nil
	case plan.InstalledLevenshteinExpr:
		var args []expr.Expr
		for i, arg := range e.Args {
			next, changed, err := preflightCoercionsWithPolicy(c, arg, decisiveDefaults)
			if err != nil {
				return nil, false, err
			}
			if changed {
				if args == nil {
					args = append([]expr.Expr(nil), e.Args...)
				}
				args[i] = next
			}
		}
		if args != nil {
			next, err := e.Reconstruct(e.Info, args)
			if err != nil {
				return nil, false, err
			}
			e.Levenshtein = next
		}
		return e, args != nil, nil
	case plan.InstalledUUIDOsspExpr:
		return e, false, nil
	case plan.InstalledCryptoExpr:
		var args []expr.Expr
		for i, arg := range e.Args {
			next, changed, err := preflightCoercionsWithPolicy(c, arg, decisiveDefaults)
			if err != nil {
				return nil, false, err
			}
			if changed {
				if args == nil {
					args = append([]expr.Expr(nil), e.Args...)
				}
				args[i] = next
			}
		}
		if args != nil {
			next, err := e.Reconstruct(e.Info, args)
			if err != nil {
				return nil, false, err
			}
			e.Pgcrypto = next
		}
		return e, args != nil, nil
	case plan.AssignmentValueExpr:
		base, baseChanged, err := preflightCoercionsWithPolicy(c, e.Base, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		value, valueChanged, err := preflightCoercionsWithPolicy(c, e.Value, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		targets, err := preflightArrayTargetsWithPlanning(c, []*plan.ArrayAssignmentTarget{e.Target}, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		changed := baseChanged || valueChanged || targets[0] != e.Target
		if changed {
			e.Base, e.Value, e.Target = base, value, targets[0]
		}
		return e, changed, nil
	case plan.InstalledTextExpr:
		arg, changed, err := preflightCoercionsWithPolicy(c, e.Arg, decisiveDefaults)
		if changed {
			e.Arg = arg
		}
		return e, changed, err
	case plan.ViewQualificationExpr:
		return mapViewQualification(e, func(child expr.Expr) (expr.Expr, bool, error) {
			return preflightCoercionsWithPolicy(c, child, decisiveDefaults)
		})
	case plan.GroupingExpr:
		var args []expr.Expr
		for i, arg := range e.Args {
			next, changed, err := preflightCoercionsWithPolicy(c, arg, decisiveDefaults)
			if err != nil {
				return nil, false, err
			}
			if changed {
				if args == nil {
					args = append([]expr.Expr(nil), e.Args...)
				}
				args[i] = next
			}
		}
		if args != nil {
			e.Args = args
		}
		return e, args != nil, nil
	case plan.ExtensionPathsExpr:
		arg, changed, err := preflightCoercionsWithPolicy(c, e.Arg, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		e.Arg = arg
		return e, changed, nil
	case plan.CaseExpr:
		return simplifyConditional(c, expression, decisiveDefaults)
	case plan.ConditionalExpr:
		if err := e.ExtremaError(); err != nil {
			return nil, false, err
		}
		if e.Kind == parser.Coalesce {
			return simplifyConditional(c, expression, decisiveDefaults)
		}
		var args []expr.Expr
		for i, arg := range e.Args {
			next, changed, err := preflightCoercionsWithPolicy(c, arg, decisiveDefaults)
			if err != nil {
				return nil, false, err
			}
			if changed {
				if args == nil {
					args = append([]expr.Expr(nil), e.Args...)
				}
				args[i] = next
			}
		}
		if args != nil {
			e.Args = args
		}
		next, changed, err := simplifyConditional(c, e, decisiveDefaults)
		return next, changed || args != nil, err
	case plan.RowExpr:
		next, changed, err := mapRow(e, func(old expr.Expr) (expr.Expr, bool, error) {
			next, changed, err := simplifyConditional(c, old, decisiveDefaults)
			if err != nil {
				return nil, false, err
			}
			folded, did, err := preflightCoercionsWithPolicy(c, next, decisiveDefaults)
			return folded, changed || did, err
		})
		return next, changed, err
	case plan.FieldExpr:
		base, changed, err := preflightCoercionsWithPolicy(c, e.Base, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		e.Base = base
		return e, changed, nil
	case plan.ArrayExpr:
		var elements []expr.Expr
		for i, old := range e.Elements {
			next, changed, err := preflightCoercionsWithPolicy(c, old, decisiveDefaults)
			if err != nil {
				return nil, false, err
			}
			if changed {
				if elements == nil {
					elements = append([]expr.Expr(nil), e.Elements...)
				}
				elements[i] = next
			}
		}
		if elements != nil {
			e.Elements = elements
		}
		p := e.Properties()
		if p.Volatility == expr.Immutable && !p.RowDependent && !p.ParameterDependent && !p.StageDependent && !p.DomainDependent && !p.RuntimeDomainConversion {
			value, err := evaluatePlanningExpression(c, e)
			return preflightValue(c, e.Info, value, err)
		}
		return e, elements != nil, nil
	case plan.SubscriptExpr:
		next, changed, err := mapSubscript(e, func(old expr.Expr) (expr.Expr, bool, error) {
			return preflightCoercionsWithPolicy(c, old, decisiveDefaults)
		})
		return next, changed, err
	case plan.CollationExpr:
		child, changed, err := preflightCoercionsWithPolicy(c, e.Child, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		e.Child = child
		return e, changed, nil
	case plan.CoercionExpr:
		child, changed, err := preflightCoercionsWithPolicy(c, e.Child, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		e.Child = child
		if e.Plan.ChecksDomain() {
			if err := prepareDomainConversion(c, e.Child.ResultUsage().Type, e.Usage.Type); err != nil {
				return nil, false, err
			}
		}
		// A function-method coercion is subject to the same execution-known
		// strict NULL pruning as other selected scalars. Do this after child
		// input/domain preflight but before constant computation admits EXECUTE.
		if folded, did, err := foldStrictParameterNull(c, e); err != nil || did {
			return folded, did, err
		}
		p := e.Properties()
		if p.Volatility == expr.Immutable && !p.RowDependent && !p.StageDependent && !p.DomainDependent && !p.RuntimeDomainConversion {
			value, err := evaluatePlanningExpression(c, e)
			return preflightValue(c, e.Info, value, err)
		}
		if changed {
			return e, true, nil
		}
	case plan.LikeExpr:
		left, lc, err := preflightCoercionsWithPolicy(c, e.Left, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		right, rc, err := preflightCoercionsWithPolicy(c, e.Right, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		e.Left, e.Right = left, right
		if exec.RandomLiteralNull(left) || exec.RandomLiteralNull(right) {
			return preflightValue(c, e.Info, sqltype.Null(e.Usage.Type), nil)
		}
		p := e.Properties()
		if p.Volatility == expr.Immutable && !p.RowDependent && !p.StageDependent && !p.DomainDependent && !p.RuntimeDomainConversion {
			value, err := evaluatePlanningExpression(c, e)
			return preflightValue(c, e.Info, value, err)
		}
		return e, lc || rc, nil
	case plan.RowComparisonExpr:
		var pairs []plan.ComparisonExpr
		for i, pair := range e.Comparisons {
			left, leftChanged, err := preflightCoercionsWithPolicy(c, pair.Left, false)
			if err != nil {
				return nil, false, err
			}
			right, rightChanged, err := preflightCoercionsWithPolicy(c, pair.Right, false)
			if err != nil {
				return nil, false, err
			}
			// The pair belongs to the row's NULL-aware/lexicographic frontier,
			// not an independently foldable scalar comparison.
			if leftChanged || rightChanged {
				if pairs == nil {
					pairs = append([]plan.ComparisonExpr(nil), e.Comparisons...)
				}
				pair.Left, pair.Right = left, right
				pairs[i] = pair
			}
		}
		if pairs != nil {
			e.Comparisons = pairs
			return e, true, nil
		}
		return e, false, nil
	case plan.ScalarArrayComparisonExpr:
		next, changed, err := mapScalarArrayChildren(e, func(child expr.Expr) (expr.Expr, bool, error) {
			return preflightCoercionsWithPolicy(c, child, decisiveDefaults)
		})
		if err != nil {
			return nil, false, err
		}
		if decisiveDefaults && conditionalConstantInputs(next) {
			value, err := evaluatePlanningExpression(c, next)
			return preflightValue(c, next.Info, value, err)
		}
		if changed {
			return next, true, nil
		}
	case plan.ComparisonExpr:
		left, leftChanged, err := preflightCoercionsWithPolicy(c, e.Left, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		right, rightChanged, err := preflightCoercionsWithPolicy(c, e.Right, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		if decisiveDefaults {
			_, leftConstant := left.(plan.LiteralExpr)
			_, rightConstant := right.(plan.LiteralExpr)
			if leftConstant && rightConstant {
				e.Left, e.Right = left, right
				value, err := evaluatePlanningExpression(c, e)
				return preflightValue(c, e.Info, value, err)
			}
		}
		if leftChanged || rightChanged {
			e.Left, e.Right = left, right
			return e, true, nil
		}
	case plan.BitwiseExpr:
		left, lc, err := preflightCoercionsWithPolicy(c, e.Left, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		var right expr.Expr
		var rc bool
		if e.Right != nil {
			right, rc, err = preflightCoercionsWithPolicy(c, e.Right, decisiveDefaults)
			if err != nil {
				return nil, false, err
			}
		}
		e.Left, e.Right = left, right
		if bitwiseConstantNull(left) || bitwiseConstantNull(right) {
			return preflightValue(c, e.Info, sqltype.Null(e.Usage.Type), nil)
		}
		// Network operators follow the release's custom-plan Execute frontier:
		// imported parameters are available now, but retain their provenance.
		if e.Usage.Type.ID() == sqltype.Inet && networkOperatorExecutionConstant(e, c) {
			if folded, changed, err := foldStrictParameterNull(c, e); err != nil || changed {
				return folded, changed, err
			}
			value, err := evaluatePlanningExpression(c, e)
			return preflightValue(c, e.Info, value, err)
		}
		if decisiveDefaults || e.Usage.Type.ID() == sqltype.Bit {
			_, l := left.(plan.LiteralExpr)
			_, r := right.(plan.LiteralExpr)
			p := e.Properties()
			if l && (right == nil || r) && p == (expr.Properties{}) {
				v, err := evaluatePlanningExpression(c, e)
				return preflightValue(c, e.Info, v, err)
			}
		}
		if lc || rc {
			return e, true, nil
		}
	case plan.SelectedOperatorExpr:
		provider, changed, err := preflightCoercionsWithPolicy(c, e.Provider, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		if changed {
			e.Provider = provider
			return e, true, nil
		}
	case plan.ArithmeticExpr:
		left, leftChanged, err := preflightCoercionsWithPolicy(c, e.Left, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		var right expr.Expr
		var rightChanged bool
		if e.Right != nil {
			right, rightChanged, err = preflightCoercionsWithPolicy(c, e.Right, decisiveDefaults)
			if err != nil {
				return nil, false, err
			}
		}
		e.Left, e.Right = left, right
		if arithmeticSetConstantNull(expression.(plan.ArithmeticExpr), e) {
			return preflightValue(c, e.Info, sqltype.Null(e.Usage.Type), nil)
		}
		if (e.Left.ResultUsage().Type.ID() == sqltype.Inet || e.Right != nil && e.Right.ResultUsage().Type.ID() == sqltype.Inet) && networkOperatorExecutionConstant(e, c) {
			if folded, changed, err := foldStrictParameterNull(c, e); err != nil || changed {
				return folded, changed, err
			}
			value, err := evaluatePlanningExpression(c, e)
			return preflightValue(c, e.Info, value, err)
		}
		if decisiveDefaults || c.PlanStaticOperators {
			// Dependence belongs to the original analyzed recipe: an imported
			// cast may now be a literal without making its parameter static.
			p := expression.Properties()
			if p.Volatility == expr.Immutable && !p.RowDependent && !p.ParameterDependent && !p.StageDependent && !p.DomainDependent && !p.RuntimeDomainConversion && conditionalConstantInputs(e) {
				value, err := evaluatePlanningExpression(c, e)
				return preflightValue(c, e.Info, value, err)
			}
		}
		// Other consumers keep arithmetic at their existing row-owned frontier.
		if leftChanged || rightChanged {
			return e, true, nil
		}
	case plan.NullTestExpr:
		child, changed, err := preflightCoercionsWithPolicy(c, e.Child, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		if decisiveDefaults {
			if _, constant := child.(plan.LiteralExpr); constant {
				e.Child = child
				value, err := evaluatePlanningExpression(c, e)
				return preflightValue(c, e.Info, value, err)
			}
		}
		if changed {
			e.Child = child
			return e, true, nil
		}
	case plan.TruthTestExpr:
		child, changed, err := preflightCoercionsWithPolicy(c, e.Child, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		if decisiveDefaults {
			if _, constant := child.(plan.LiteralExpr); constant {
				e.Child = child
				value, err := evaluatePlanningExpression(c, e)
				return preflightValue(c, e.Info, value, err)
			}
		}
		if changed {
			e.Child = child
			return e, true, nil
		}
	case plan.DistinctExpr:
		left, leftChanged, err := preflightCoercionsWithPolicy(c, e.Left, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		right, rightChanged, err := preflightCoercionsWithPolicy(c, e.Right, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		if decisiveDefaults {
			_, leftConstant := left.(plan.LiteralExpr)
			_, rightConstant := right.(plan.LiteralExpr)
			if leftConstant && rightConstant {
				e.Left, e.Right = left, right
				value, err := evaluatePlanningExpression(c, e)
				return preflightValue(c, e.Info, value, err)
			}
		}
		if leftChanged || rightChanged {
			e.Left, e.Right = left, right
			return e, true, nil
		}
	case plan.LogicalExpr:
		left, leftChanged, err := preflightCoercionsWithPolicy(c, e.Left, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		var leftConstant bool
		if decisiveDefaults {
			_, leftConstant = left.(plan.LiteralExpr)
			e.Left = left
			value, decisive, err := plan.DecisiveLogicalValue(&e)
			if err != nil {
				return nil, false, err
			}
			if decisive {
				return preflightValue(c, e.Info, value, nil)
			}
		}
		var right expr.Expr
		var rightChanged bool
		if e.Right != nil {
			right, rightChanged, err = preflightCoercionsWithPolicy(c, e.Right, decisiveDefaults)
			if err != nil {
				return nil, false, err
			}
		}
		if decisiveDefaults && leftConstant {
			_, rightConstant := right.(plan.LiteralExpr)
			if e.Op == parser.LogicalNot || rightConstant {
				e.Left, e.Right = left, right
				value, err := evaluatePlanningExpression(c, e)
				return preflightValue(c, e.Info, value, err)
			}
		}
		if leftChanged || rightChanged {
			e.Left, e.Right = left, right
			return e, true, nil
		}
	case expr.Composite:
		// Function nodes name their fold; installed-routine wrappers keep
		// their cases above.
		return preflightComposite(c, e, decisiveDefaults)
	}
	return expression, false, nil
}

func preflightValue(c *exec.Context, info expr.Info, value sqltype.Value, err error) (expr.Expr, bool, error) {
	if err != nil {
		return nil, false, err
	}
	if err := exec.EvaluationContext(c); err != nil {
		return nil, false, err
	}
	info.RoutineReference = nil
	return plan.LiteralExpr{Info: info, Value: value}, true, nil
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
	// Selected textual function roles admit internal CHAR annotations.
	stringInput bool
	// Only width_bucket's threshold argument consumes existing enum arrays.
	// This is not permission for generic casts, storage or domain declarations.
	thresholdArrayInput bool
	// Selected string INSERT rows analyze occurrences before target assignment.
	deferAssignment bool
	// Operand skips the enclosing WHERE BOOL check while binding predicate children.
	operand bool
}

// expressionBinder binds the expressions of one statement. engine, ctx, op,
// expected and input are the host and statement inputs (12c replaces engine
// and op with a host seam); dependencies is the statement's dependency
// manifest. Everything else lives in the value groups of binder_groups.go.
type expressionBinder struct {
	host         analyzeHost
	ctx          context.Context
	expected     analyze.Expected
	input        sqltype.InputEnvironment
	dependencies []catalog.ExpressionDependency

	target   targetContext
	params   parameterState
	consumer consumerContext
	capture  viewCapture
	stored   storedSyntaxState
	prepared preparedCaptures
	clause   clauseState
	grouping groupingState
	query    queryScopeState
	cte      cteState
}

// newExpressionBinder is the root construction point: it binds through the
// engine's host for op.
func newExpressionBinder(ctx context.Context, e *Engine, input sqltype.InputEnvironment, op *Operation, expected *StatementDescription) *expressionBinder {
	return newHostedBinder(ctx, e.analysisHost(op), input, expectedOf(expected))
}

func newHostedBinder(ctx context.Context, host analyzeHost, input sqltype.InputEnvironment, expected analyze.Expected) *expressionBinder {
	b := &expressionBinder{host: host, ctx: ctx, params: parameterState{parameters: make(map[int]sqltype.Usage)}, input: input, expected: expected}
	host.CaptureEnumOperationSafety(&b.input)
	if expected.Held {
		b.seedParameters(expected.Parameters.Types)
		if expected.RefreshTemporals {
			for _, capture := range expected.Temporals {
				if !refreshableTemporalCapture(capture) {
					b.prepared.Temporals = append(b.prepared.Temporals, capture)
				}
			}
		}
	}
	return b
}

func (b *expressionBinder) seedParameters(types []sqltype.Usage) {
	b.params.formalParameters = types
	currentType := recordTypeMapper(func(t *sqltype.Descriptor) *sqltype.Descriptor { return b.typ(t.ID()) })
	for i, usage := range types {
		if usage.Type != nil {
			usage.Type = currentType(usage.Type)
		}
		b.params.parameters[i+1] = usage
	}
}

// Binding must retain the caller's catalog/path authority, including a private
// CREATE candidate. Execution still opens providers through live authorization.
func (b *expressionBinder) bindingAuthorization() (AuthorizationView, error) {
	if b.query.auth != nil {
		auth := *b.query.auth
		if definition := b.host.Definition(); definition != nil {
			auth.definition = definition
		}
		return auth, nil
	}
	return b.host.Authorization()
}
func (b *expressionBinder) typ(id sqltype.ID) *sqltype.Descriptor {
	d, _ := b.definitionType(id)
	return d
}
func (b *expressionBinder) dependency(ref ids.ObjectRef, revision ids.DefinitionRevision) {
	for _, d := range b.dependencies {
		if d.Ref == ref {
			return
		}
	}
	b.dependencies = append(b.dependencies, catalog.ExpressionDependency{ref, revision})
}
func (b *expressionBinder) bindingCatalog() catalog.Read {
	if b.query.auth != nil {
		return b.query.auth.Catalog
	}
	return b.host.CurrentCatalog()
}

func (b *expressionBinder) info(u sqltype.Usage, pos parser.Position, p expr.Properties) expr.Info {
	view := b.bindingCatalog()
	address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(u.Type.ID())}
	revision := ids.DefinitionRevision(1)
	if descriptor, ok := b.definitionDescriptor(address); ok {
		revision = descriptor.Revision()
	}
	b.dependency(b.host.CatalogOwner().Domain.Ref(view.Epoch(), address), revision)
	if u.Collation.ID != 0 {
		address := ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: ids.OID(u.Collation.ID)}
		if d, ok := b.definitionDescriptor(address); ok {
			b.dependency(b.host.CatalogOwner().Domain.Ref(view.Epoch(), address), d.Revision())
		}
	}
	return expr.Info{Usage: u, Pos: pos, Property: p}
}
func (b *expressionBinder) constrain(n int, u sqltype.Usage, pos parser.Position) error {
	if n <= 0 || b.params.closedRoutineFormals && n > len(b.params.formalParameters) {
		return expr.ErrorAt("42P02", fmt.Sprintf("there is no parameter $%d", n), pos)
	}
	if old, ok := b.params.parameters[n]; ok && old.Type != nil && old.Type != u.Type {
		if b.params.updateParameters {
			// UPDATE uses occurrence-sensitive inference diagnostics; other
			// assignment consumers retain their selected 0A000 boundary.
			_, err := b.coerceParameter(plan.ParameterExpr{Info: expr.Info{Pos: pos}, Number: n}, u)
			return err
		}
		return sqlError("0A000", "parameter reused with different destination types")
	}
	b.params.parameters[n] = u.Type.Usage()
	return nil
}
func (b *expressionBinder) finish() (ParameterInfo, error) {
	// Retained unknown ANY and null-test occurrences share binding order.
	// Diagnose occurrence mismatches before gaps or unresolved shared slots.
	for _, p := range b.params.anyParameters {
		if b.params.parameters[p.Number].Type != nil {
			return ParameterInfo{}, expr.ErrorAt("42P08", fmt.Sprintf("could not determine data type of parameter $%d", p.Number), p.Pos)
		}
	}
	keys := make([]int, 0, len(b.params.parameters))
	for n := range b.params.parameters {
		keys = append(keys, n)
	}
	sort.Ints(keys)
	for i, n := range keys {
		if n != i+1 || b.params.parameters[n].Type == nil {
			return ParameterInfo{}, sqlError("42P18", fmt.Sprintf("could not determine data type of parameter $%d", i+1))
		}
	}
	info := ParameterInfo{Count: len(keys), Types: make([]sqltype.Usage, len(keys))}
	for i, n := range keys {
		info.Types[i] = b.params.parameters[n]
		if err := b.captureTypeDiagnosticInput(info.Types[i].Type); err != nil {
			return ParameterInfo{}, err
		}
	}
	return info, nil
}

// Output fallback belongs to an unresolved occurrence, not its subsequently
// constrained shared slot. These private provisional nodes never escape describe.
func (b *expressionBinder) finalizeProjection(node expr.Expr) (expr.Expr, error) {
	p, ok := node.(plan.ParameterExpr)
	if !ok || p.Usage.Type != nil {
		return node, nil
	}
	return b.coerceParameter(p, b.typ(sqltype.Text).Usage())
}
func (b *expressionBinder) column(raw parser.ColumnExpr) (expr.Expr, error) {
	if b.clause.domainValueUsage != nil && len(raw.Name.Parts) == 1 && raw.Name.Parts[0].Text == "value" {
		return plan.DomainValueExpr{Info: b.info(*b.clause.domainValueUsage, raw.Pos, expr.Properties{DomainDependent: true})}, nil
	}
	if b.target.generatedOwner != nil {
		name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
		owner := b.target.generatedOwner
		qualified := len(raw.Name.Parts) == 1 || len(raw.Name.Parts) == 2 && raw.Name.Parts[0].Text == owner.Name() || len(raw.Name.Parts) == 3 && raw.Name.Parts[1].Text == owner.Name() && raw.Name.Parts[0].Text == b.query.namespace.qualifiers[0].schema
		if qualified {
			if name == "tableoid" {
				ref := b.query.sources[0].Ref
				return plan.TableOIDExpr{Info: b.info(b.typ(sqltype.Oid).Usage(), raw.Pos, expr.Properties{RowDependent: true}), Owner: ref}, nil
			}
			if isSystemColumn(name) {
				return nil, expr.ErrorAt("42P10", fmt.Sprintf(`cannot use system column "%s" in column generation expression`, name), raw.Pos)
			}
		}
		node, err := b.queryColumn(raw)
		if err != nil {
			// A bare relation name is a whole-row reference only after ordinary
			// column lookup has failed; a same-named attribute takes precedence.
			if name == owner.Name() && len(raw.Name.Parts) == 1 {
				diagnostic := expr.ErrorAt("42P17", "cannot use whole-row variable in column generation expression", raw.Pos).(*sqlstate.Error)
				diagnostic.Detail = "This would cause the generated column to depend on its own value."
				return nil, diagnostic
			}
			return nil, err
		}
		if _, whole := node.(plan.RowExpr); whole {
			diagnostic := expr.ErrorAt("42P17", "cannot use whole-row variable in column generation expression", raw.Pos).(*sqlstate.Error)
			diagnostic.Detail = "This would cause the generated column to depend on its own value."
			return nil, diagnostic
		}
		if column, ok := node.(plan.ColumnExpr); ok {
			if _, generated := owner.ColumnMeta.Generations[column.Origin.Address.SubID]; generated {
				diagnostic := expr.ErrorAt("42P17", fmt.Sprintf(`cannot use generated column "%s" in column generation expression`, name), raw.Pos).(*sqlstate.Error)
				diagnostic.Detail = "A generated column cannot reference another generated column."
				return nil, diagnostic
			}
		}
		return node, nil
	}
	if b.consumer.checkConsumer && b.clause.domainValueUsage == nil && len(raw.Name.Parts) > 0 {
		name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
		switch name {
		case "ctid", "xmin", "cmin", "xmax", "cmax":
			return nil, expr.ErrorAt("42P10", `system column "`+name+`" reference in check constraint is invalid`, raw.Pos)
		}
	}
	if b.target.returningAliases != nil {
		return b.returningColumn(raw)
	}
	if b.query.namespace != nil {
		return b.queryColumn(raw)
	}
	if len(raw.Name.Parts) == 2 && !b.target.deleteTarget {
		return nil, expr.ErrorAt("0A000", "qualified column references are unsupported", raw.Pos)
	}
	if len(raw.Name.Parts) == 0 || len(raw.Name.Parts) > 2 {
		return nil, fmt.Errorf("stuntgres: column outside expression scope")
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	if b.target.relation == nil {
		return nil, expr.ErrorAt("42703", fmt.Sprintf(`column "%s" does not exist`, name), raw.Pos)
	}
	if len(raw.Name.Parts) == 2 {
		qualifier := raw.Name.Parts[0].Text
		visible := b.target.name
		if b.target.alias != "" {
			visible = b.target.alias
		}
		if qualifier != visible {
			if qualifier == b.target.name && b.target.alias != "" {
				err := expr.ErrorAt("42P01", fmt.Sprintf(`invalid reference to FROM-clause entry for table "%s"`, qualifier), raw.Pos).(*sqlstate.Error)
				err.Hint = fmt.Sprintf(`Perhaps you meant to reference the table alias "%s".`, b.target.alias)
				return nil, err
			}
			return nil, expr.ErrorAt("42P01", fmt.Sprintf(`missing FROM-clause entry for table "%s"`, qualifier), raw.Pos)
		}
	}
	i, ok := b.target.relation.Index[name]
	if !ok {
		if len(raw.Name.Parts) == 2 {
			return nil, expr.ErrorAt("42703", fmt.Sprintf("column %s.%s does not exist", raw.Name.Parts[0].Text, name), raw.Pos)
		}
		return nil, expr.ErrorAt("42703", fmt.Sprintf(`column "%s" does not exist`, name), raw.Pos)
	}
	return b.columnAt(i, raw.Pos), nil
}
func (b *expressionBinder) useColumn(i int) ids.ObjectRef {
	return b.useColumnOf(b.target.relation, i)
}

func (b *expressionBinder) useColumnOf(relation *catalog.Relation, i int) ids.ObjectRef {
	ref := b.target.ref
	ref.Address.SubID = relation.Attributes[i]
	b.dependency(ref, relation.Revision())
	used := false
	for _, slot := range b.target.uses {
		if slot == i {
			used = true
			break
		}
	}
	if !used {
		b.target.uses = append(b.target.uses, i)
	}
	b.info(relation.Columns[i].Usage, parser.Position{Offset: -1}, expr.Properties{})
	return ref
}
func (b *expressionBinder) columnAt(i int, pos parser.Position) expr.Expr {
	return b.columnAtOf(b.target.relation, i, pos)
}

func (b *expressionBinder) columnAtOf(relation *catalog.Relation, i int, pos parser.Position) expr.Expr {
	ref := b.useColumnOf(relation, i)
	column := plan.ColumnExpr{Info: expr.Info{Usage: relation.Columns[i].Usage, Pos: pos, Property: expr.Properties{RowDependent: true}}, Origin: ref, Scope: 1, Frame: 1, Slot: i}
	if b.target.generatedOwner == nil {
		column = column.WithGeneration(relation, i, 0)
	}
	return column
}
func (b *expressionBinder) bind(raw parser.Expr, policy expressionPolicy) (boundResult expr.Expr, bindError error) {
	if policy.context == filterContext && !policy.operand && policy.booleanConstruct == "" {
		return b.bindBoolean(raw, policy.context, "WHERE")
	}
	if err := b.ctx.Err(); err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, fmt.Errorf("stuntgres: missing scalar expression")
	}
	if slot, ok := raw.(parser.ParameterExpr); ok && slot.Number < 0 && -slot.Number <= len(b.clause.selectedOperatorArgs) {
		node := b.clause.selectedOperatorArgs[-slot.Number-1]
		if b.grouping.argumentOwner.active && !b.grouping.argumentOwner.found {
			// Selection analyzed this operand before the winning argument
			// owner existed. Preserve its lexical occurrence even if the
			// signature does not retain an executable argument.
			if nested := windowOccurrence(node); nested != nil && nested.Frame == b.frameID() {
				b.grouping.argumentOwner.first, b.grouping.argumentOwner.found = nested.Pos, true
			}
		}
		return node, nil
	}
	defer func() {
		if bindError != nil || boundResult == nil {
			return
		}
		if policy.context == assignmentContext && !policy.deferAssignment {
			bindError = rejectRegexpSets(boundResult, "UPDATE", false)
		}
		if policy.context == filterContext {
			clause := b.clause.aggregateClause
			if clause == "" || clause == "SELECT" {
				clause = "WHERE"
			}
			bindError = rejectRegexpSets(boundResult, clause, false)
		}
		if bindError != nil {
			boundResult = nil
		}
	}()
	var node expr.Expr
	if selected, handled, err := b.bindSQLRoutineSyntax(raw, policy); handled {
		if err != nil {
			return nil, err
		}
		node = selected
	}
	if node == nil && b.consumer.partitionConsumer != "" {
		switch r := raw.(type) {
		case parser.ColumnExpr:
			if b.consumer.partitionConsumer == "partition bound" {
				return nil, expr.ErrorAt("0A000", "cannot use column reference in partition bound expression", r.Pos)
			}
		case parser.IndexSubqueryExpr:
			return nil, b.consumer.partitionExpressionError("0A000", "cannot use subquery in "+b.consumer.partitionConsumer, r.Pos)
		case parser.SubqueryExpr:
			return nil, b.consumer.partitionExpressionError("0A000", "cannot use subquery in "+b.consumer.partitionConsumer, r.Pos)
		case parser.AggregateExpr:
			if r.Window == nil && r.Name != "row_number" && r.Name != "ntile" && !functions.WindowValueName(r.Name) && !functions.WindowOffsetName(r.Name) {
				for _, arg := range r.Args {
					if _, err := b.bind(arg, expressionPolicy{context: projectionContext}); err != nil {
						return nil, err
					}
				}
				return nil, b.consumer.partitionExpressionError("42803", "aggregate functions are not allowed in "+b.consumer.partitionContextPlural(), r.Pos)
			}
		}
	}
	if node == nil && b.clause.functionProjection.matches(raw) {
		node = b.clause.functionProjection.operand
	}
	if node == nil {
		switch r := raw.(type) {
		case parser.MergeActionExpr:
			var err error
			node, err = b.bindMergeAction(r)
			if err != nil {
				return nil, err
			}
		case parser.IndexSubqueryExpr:
			if b.consumer.indexPredicateConsumer {
				return nil, expr.ErrorAt("0A000", "cannot use subquery in index predicate", r.Pos)
			}
			return nil, expr.ErrorAt("0A000", "cannot use subquery in index expression", r.Pos)
		case parser.SubqueryExpr:
			if b.params.executeParameters {
				return nil, expr.ErrorAt("0A000", "cannot use subquery in EXECUTE parameter", r.Pos)
			}
			var err error
			node, err = b.bindSubquery(r)
			if err != nil {
				return nil, err
			}
		case parser.RowExpr:
			var err error
			node, err = b.bindRow(r, policy.expected, policy.mode, r.Pos)
			if err != nil {
				return nil, err
			}
		case parser.FieldExpr:
			var err error
			node, err = b.bindField(r)
			if err != nil {
				return nil, err
			}
		case parser.StarExpr:
			var err error
			node, err = b.wholeRow(r.Qualifier, r.Pos, true)
			if err != nil {
				return nil, err
			}
		case parser.IdentityExpr:
			var err error
			node, err = functions.BindIdentity(b, r.Name, r.Pos)
			if err != nil {
				return nil, err
			}
		case parser.CurrentDateTimeExpr:
			var err error
			var warning *sqlstate.Error
			node, warning, err = functions.BindCurrentDateTime(b, r)
			if err != nil {
				return nil, err
			}
			// A held description already analyzed this syntax. Execute must
			// still bind fresh descriptors and evaluate fresh values.
			if warning != nil && !b.expected.Held {
				b.host.EmitWarning(*warning)
			}
		case parser.ForbiddenGenerationExpr:
			return nil, expr.ErrorAt(r.Code, r.Message, r.Pos)
		case parser.GroupingExpr:
			var err error
			node, err = b.bindGrouping(r)
			if err != nil {
				return nil, err
			}
		case parser.AggregateExpr:
			if r.Window == nil && b.cte.recursiveFrames[b.frameID()] {
				return nil, expr.ErrorAt("42P19", "aggregate functions are not allowed in a recursive query's recursive term", r.Pos)
			}
			if b.params.executeParameters {
				if r.Window != nil {
					return nil, expr.ErrorAt("42P20", "window functions are not allowed in EXECUTE parameters", r.Pos)
				}
				return nil, expr.ErrorAt("42803", "aggregate functions are not allowed in EXECUTE parameters", r.Pos)
			}
			name := parser.ObjectName{Parts: []parser.Identifier{{Text: r.Name, Pos: r.Pos}}}
			if r.Schema != nil {
				name.Parts = []parser.Identifier{*r.Schema, {Text: r.Name, Pos: r.Pos}}
			}
			diagnosticName := name
			if len(b.clause.selectedWindowCallee.Parts) != 0 {
				// Consume before Signature binds any children. Nested calls
				// must retain their own lexical diagnostic spelling.
				diagnosticName = b.clause.selectedWindowCallee
				b.clause.selectedWindowCallee = parser.ObjectName{}
			}
			parentAggregates, parentWindows := b.grouping.aggregates, b.grouping.windowCalls
			defer func() {
				if bindError != nil {
					b.grouping.aggregates, b.grouping.windowCalls = parentAggregates, parentWindows
				}
			}()
			if functions.HypotheticalSetName(r.Name) && !r.Within && len(r.Args) == 0 && r.Window != nil {
				var err error
				node, err = b.bindWindow(r, name)
				if err != nil {
					return nil, err
				}
				break
			}
			if r.Within && !functions.OrderedSetName(r.Name) {
				return b.rejectOrdinaryWithin(r)
			}
			if r.Window != nil && !functions.OrderedSetName(r.Name) {
				var err error
				node, err = b.bindWindow(r, name)
				if err != nil {
					return nil, err
				}
				break
			}
			// A window function without OVER resolves its signature first.
			if function, ok := functions.LookupWindowFunction(r.Name); ok && (!functions.HypotheticalSetName(r.Name) || !r.Within && len(r.Args) == 0) {
				if _, err := function.Signature(b, r, name); err != nil {
					return nil, err
				}
				return nil, expr.ErrorAt("42809", "window function "+objectNameText(diagnosticName)+" requires an OVER clause", r.Pos)
			}
			parentOwner := b.grouping.argumentOwner
			b.grouping.argumentOwner = argumentWindowOwner{active: true}
			defer func() { b.grouping.argumentOwner = parentOwner }()
			if b.target.generatedOwner != nil {
				return nil, expr.ErrorAt("42803", "aggregate functions are not allowed in column generation expressions", r.Pos)
			}
			if b.consumer.indexPredicateConsumer {
				return nil, expr.ErrorAt("42803", "aggregate functions are not allowed in index predicates", r.Pos)
			}
			if b.consumer.indexConsumer {
				return nil, expr.ErrorAt("42803", "aggregate functions are not allowed in index expressions", r.Pos)
			}
			if b.clause.aggregateClause == "VALUES" || b.clause.aggregateClause == "INSERT VALUES" && (r.Name == "string_agg" || r.Name == "array_agg") {
				return nil, expr.ErrorAt("42803", "aggregate functions are not allowed in VALUES", r.Pos)
			}
			if b.consumer.checkConsumer {
				return nil, expr.ErrorAt("42803", "aggregate functions are not allowed in check constraints", r.Pos)
			}
			var err error
			var returning sqltype.Usage
			if definition, ok := functions.LookupAggregate(r.Name); ok {
				node, returning, err = b.bindRegisteredAggregate(r, name, definition)
			} else if r.Name == "int_array_aggregate" {
				node, err = b.bindIntaggAggregate(r, name)
			} else {
				// Every other name binds as count, for its 42883 diagnostics.
				node, err = functions.BindCount(b, r, name)
			}
			if err != nil {
				return nil, err
			}
			node, err = b.ownAggregate(node)
			if err != nil {
				return nil, err
			}
			if returning.Type != nil {
				node, err = b.aggregateReturning(r, node, returning)
				if err != nil {
					return nil, err
				}
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
		case parser.SQLJSONExpr:
			var err error
			node, err = b.bindSQLJSON(r)
			if err != nil {
				return nil, err
			}
		case parser.JSONPredicateExpr:
			var err error
			node, err = functions.BindJSONPredicate(b, r)
			if err != nil {
				return nil, err
			}
		case parser.FunctionExpr:
			if b.params.executeParameters && r.Window != nil {
				return nil, expr.ErrorAt("42P20", "window functions are not allowed in EXECUTE parameters", r.Pos)
			}
			var err error
			node, err = b.bindFunction(r)
			if err != nil {
				return nil, err
			}
			switch inquiry := node.(type) {
			case plan.ExtensionPathsExpr:
				b.consumer.catalogSnapshot = true
			case expr.SnapshotDemander:
				demand, _, _ := inquiry.SnapshotDemand(bareInquiryLiteralNull)
				b.consumer.catalogSnapshot = b.consumer.catalogSnapshot || demand
			}
			if r.Window != nil {
				return nil, expr.ErrorAt("42809", "OVER specified, but "+objectNameText(r.Name)+" is not a window function nor an aggregate function", r.Pos)
			}
		case parser.CallExpr:
			var err error
			node, err = functions.BindSequenceCall(b, r)
			if err != nil {
				return nil, err
			}
		case parser.ArrayExpr:
			var err error
			var target sqltype.Usage
			if policy.mode == sqltype.Explicit {
				target = policy.expected
			}
			node, err = b.bindArray(r, target)
			if err != nil {
				return nil, err
			}
		case parser.SubscriptExpr:
			var err error
			node, err = b.bindSubscript(r)
			if err != nil {
				return nil, err
			}
		case parser.RegclassExpr:
			var err error
			node, err = b.bindRegclassCast(r.Child, r.Pos)
			if err != nil {
				return nil, err
			}
		case parser.ColumnExpr:
			if pseudo, matched, err := b.ruleColumn(r); matched {
				if err != nil {
					return nil, err
				}
				node = pseudo
				break
			}
			if b.consumer.defaultConsumer {
				return nil, expr.ErrorAt("0A000", "cannot use column reference in DEFAULT expression", r.Pos)
			}
			if policy.context == assignmentContext {
				return nil, expr.ErrorAt("0A000", "column references outside SELECT are unsupported", r.Pos)
			}
			var err error
			node, err = b.column(r)
			if err != nil {
				return nil, err
			}
		case parser.ParameterExpr:
			if b.stored.checkLiterals != nil {
				if r.Number <= 0 || r.Number > len(b.stored.checkLiterals) {
					return nil, expr.ErrorAt("42P02", fmt.Sprintf("there is no parameter $%d", r.Number), r.Pos)
				}
				node = b.stored.checkLiterals[r.Number-1]
				break
			}
			if b.params.noParameters || b.consumer.defaultConsumer || r.Number <= 0 || b.params.closedRoutineFormals && r.Number > len(b.params.formalParameters) {
				return nil, expr.ErrorAt("42P02", fmt.Sprintf("there is no parameter $%d", r.Number), r.Pos)
			}
			u := policy.expected
			if established := b.params.updateInputParameters[r.Number]; established.Type != nil {
				u = established
			}
			retained := r.Number <= len(b.params.formalParameters) && b.params.formalParameters[r.Number-1].Type != nil
			if retained {
				u = b.params.formalParameters[r.Number-1]
			}
			// Retained parameter contracts keep their logical input type, but their
			// codec must come from the admitted catalog, including private generations.
			if b.expected.Held && r.Number <= len(b.expected.Parameters.Types) {
				u = b.expected.Parameters.Types[r.Number-1]
				retained = u.Type != nil
			}
			if retained {
				catalog := b.bindingCatalog()
				if b.expected.Held && statementCatalogType(u.Type) && b.expected.CatalogOwner != nil &&
					(b.expected.CatalogOwner != catalog.Owner() || b.expected.CatalogEpoch != catalog.Epoch()) {
					return nil, sqlError("0A000", "cached plan must not change parameter type")
				}
				current, _ := catalog.LookupType(u.Type.ID())
				if u.Type.IsDeferredRecord() || u.Type.ID() == 2249 && u.Type.IsComposite() {
					// Anonymous shapes and deferred RECORD are formal occurrence
					// authorities, never catalog's lookup-only RECORD identity.
					current = b.params.parameters[r.Number].Type
				}
				if current == nil && statementCatalogType(u.Type) {
					return nil, sqlError("0A000", "cached plan must not change parameter type")
				}
				u.Type = current
				b.params.parameters[r.Number] = u
			}
			if u.Type == nil {
				u = b.params.parameters[r.Number]
			}
			if u.Type == nil {
				b.params.parameters[r.Number] = sqltype.Usage{}
				node = plan.ParameterExpr{Info: expr.Info{Pos: r.Pos, Property: expr.Properties{ParameterDependent: true}}, Number: r.Number}
			} else {
				if err := b.constrain(r.Number, u, r.Pos); err != nil {
					return nil, err
				}
				node = plan.ParameterExpr{Info: b.info(u.Type.Usage(), r.Pos, expr.Properties{ParameterDependent: true}), Number: r.Number}
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
				if u.Type.IsDomain() {
					// Fresh NULL is an unconstrained physical input. The outer
					// assignment owns inherited typmods and domain constraints.
					u = u.BaseUsage().Type.Usage()
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
			info := b.info(literalUsage, v.Pos, expr.Properties{})
			node = plan.LiteralExpr{Info: info, Value: value}
			if literalUsage.Type.ID() == sqltype.Regclass && !value.IsNull() {
				text, _ := value.Text()
				oid, err := strconv.ParseUint(text, 10, 32)
				if err != nil {
					return nil, err
				}
				auth, err := b.bindingAuthorization()
				if err != nil {
					return nil, err
				}
				address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(oid)}
				if descriptor, exists := auth.Catalog.Descriptor(address); exists {
					b.dependency(b.host.CatalogOwner().Domain.Ref(auth.Catalog.Epoch(), address), descriptor.Revision())
				}
			}
		case parser.TypedLiteralExpr:
			rawType := r.TypeName
			saved, found, err := b.captureAt(r.Pos.Offset)
			if err != nil {
				return nil, err
			}
			if found {
				rawType.Modifiers = nil
			}
			u, err := b.host.ResolveType(rawType)
			if err != nil {
				return nil, err
			}
			if r.RequiresDomain && !u.Type.IsDomain() {
				return nil, expr.ErrorAt("0A000", "typed literals are unsupported", rawType.Pos)
			}
			if found {
				u.Typmod = saved.Typmod
				if saved.Typed {
					u.Typmod = saved.TypedTypmod
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
			if b.collectTemporalConstants() {
				for i := range b.prepared.Temporals {
					if b.prepared.Temporals[i].Offset == r.Pos.Offset && b.prepared.Temporals[i].TypeID == u.Type.ID() {
						b.prepared.Temporals[i].Typed = true
						b.prepared.Temporals[i].TypedTypmod = u.Typmod
					}
				}
			}
			literalInfo := b.info(inputUsage, r.Pos, expr.Properties{})
			node = plan.LiteralExpr{Info: literalInfo, Value: value}
			if inputUsage != u {
				coercionPlan, err := sqltype.PlanCoercion(inputUsage, u, sqltype.Explicit)
				if err != nil {
					return nil, err
				}
				info, err := b.coercionInfo(b.info(u, r.Pos, coercionProperties(node, coercionPlan)), node)
				if err != nil {
					return nil, err
				}
				node = plan.CoercionExpr{Info: info, Child: node, Plan: coercionPlan, Mode: sqltype.Explicit}
			}
		case parser.CollateExpr:
			child, err := b.bind(r.Child, policy)
			if err != nil {
				return nil, err
			}
			name := r.Name
			name.Parts = append([]parser.Identifier(nil), name.Parts...)
			name.Parts[0].Pos = r.Pos
			usage, err := b.host.ResolveColumnCollation(child.ResultUsage(), &name)
			if err != nil {
				return nil, err
			}
			info := b.info(usage, r.Pos, child.Properties())
			info.CollationExplicit = r.Pos
			node = plan.CollationExpr{Info: info, Child: child}
		case parser.CastExpr:
			var err error
			node, err = b.cast(r, policy)
			if err != nil {
				return nil, err
			}
		case parser.TimeZoneExpr:
			var err error
			node, err = functions.BindTimeZone(b, r)
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
		case parser.InListExpr:
			var err error
			node, err = b.bindInList(r)
			if err != nil {
				return nil, err
			}
		case parser.ArrayComparisonExpr:
			var err error
			node, err = b.bindArrayComparison(r)
			if err != nil {
				return nil, err
			}
		case parser.OperatorExpr:
			var err error
			node, err = b.bindCatalogOperator(r, policy)
			if err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("stuntgres: unsupported scalar node")
		}
	}
	if b.clause.functionProjection.matches(raw) && b.clause.functionProjection.operand == nil && node.ResultUsage().Type != nil && node.ResultUsage().Type.Concrete() && node.ResultUsage().Type.BaseType().IsComposite() {
		b.clause.functionProjection.operand = node
	}
	if policy.booleanConstruct != "" && node.ResultUsage().Type.IsDomain() && node.ResultUsage().Type.BaseType().ID() == sqltype.Bool {
		var err error
		node, err = b.coerceConditional(comparisonOperand{node: node}, b.typ(sqltype.Bool).Usage(), policy.booleanConstruct)
		if err != nil {
			return nil, err
		}
	}
	if policy.booleanConstruct != "" && node.ResultUsage().Type.ID() != sqltype.Bool {
		return nil, expr.ErrorAt("42804", fmt.Sprintf("argument of %s must be type boolean, not type %s", policy.booleanConstruct, node.ResultUsage().Type.DiagnosticName()), parser.ExpressionStart(raw))
	}
	// Logical children already passed this policy independently. Do not walk
	// every prefix again when binding a long AND/OR chain.
	if _, logical := node.(plan.LogicalExpr); policy.booleanConstruct != "" && !logical {
		returnsSet := false
		plan.WalkExpression(node, func(child expr.Expr) {
			returnsSet = returnsSet || exec.SourceReturnsSet(child)
		})
		if returnsSet {
			return nil, expr.ErrorAt("42804", fmt.Sprintf("argument of %s must not return a set", policy.booleanConstruct), parser.ExpressionStart(raw))
		}
	}
	if !policy.deferAssignment && (policy.context == assignmentContext || policy.context == functionArgumentContext) && node.ResultUsage() != policy.expected {
		if err := b.captureTypeDiagnosticInput(policy.expected.Type); err != nil {
			return nil, err
		}
		if policy.expected.Type.ContainsCatalogIdentifier() {
			if err := b.captureElementInput(); err != nil {
				return nil, err
			}
		}
		input, annotations := domainCoercionInput(node, policy.expected)
		info := b.info(policy.expected, raw.SourcePosition(), input.Properties())
		var err error
		if policy.context == assignmentContext {
			info, err = storageCoercionInfo(b.bindingCatalog(), info, input)
		} else {
			info, err = b.coercionInfo(info, input)
		}
		if err != nil {
			return nil, err
		}
		coercionPlan, err := sqltype.PlanCoercion(input.ResultUsage(), info.Usage, policy.mode)
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
		if coercionPlan.RequiresCatalogOutput() {
			b.prepared.RegclassConcat = true
		}
		info.Property = coercionProperties(input, coercionPlan)
		if folded, ok, err := b.foldRegtypeConstant(input, coercionPlan, info); ok || err != nil {
			if err != nil {
				return nil, err
			}
			node = restoreDomainCastCollations(annotations, folded)
		} else {
			node = restoreDomainCastCollations(annotations, plan.CoercionExpr{Info: info, Child: input, Plan: coercionPlan, Mode: policy.mode})
		}
		if policy.context == assignmentContext {
			var err error
			node, err = storageDestination(node, policy.expected, raw.SourcePosition())
			if err != nil {
				return nil, err
			}
		}
	}
	return node, nil
}

func functionCastError(raw parser.CastExpr, source string, ambiguous bool) error {
	code, ending, hint := "42883", "does not exist", functions.SignatureHint
	if ambiguous {
		code, ending = "42725", "is not unique"
		hint = "Could not choose a best candidate function. You might need to add explicit type casts."
	}
	name := raw.Callee
	if raw.TypeName.Schema != nil {
		name = raw.TypeName.Schema.Text + "." + name
	}
	d := expr.ErrorAt(code, fmt.Sprintf("function %s(%s) %s", name, source, ending), raw.Pos).(*sqlstate.Error)
	d.Hint = hint
	return d
}

func (b *expressionBinder) cast(raw parser.CastExpr, outerPolicy expressionPolicy) (expr.Expr, error) {
	if raw.Syntax == parser.FunctionCast && legacyFunctionCastPredicate(raw.Child) {
		return nil, expr.ErrorAt("0A000", "functions are unsupported", raw.Pos)
	}
	if !b.consumer.defaultConsumer && b.query.namespace == nil && b.target.returningAliases == nil && b.target.ruleNamespace == nil {
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
		visible, err = b.builtinVisible(name, analyze.FunctionLookup)
		if err != nil {
			return nil, err
		}
	}
	if raw.Syntax == parser.FunctionCast && (!visible || raw.Callee != "oid" && raw.Callee != "xid" && raw.Callee != "cid" && raw.Callee != "xid8" && raw.Callee != "int4" && raw.Callee != "int8" && raw.Callee != "text" && raw.Callee != "bool") {
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
	annotation := raw.TypeName
	if b.clause.jsonbRecordBase {
		annotation, err = b.jsonbRecordCastAnnotation(raw)
		if err != nil {
			return nil, err
		}
	}
	if outerPolicy.thresholdArrayInput {
		target, err = b.host.ResolveAnnotationTypeInput(annotation, unknownNull, false, false, false, true)
	} else if outerPolicy.stringInput {
		target, err = b.host.ResolveStringAnnotationType(annotation, unknownNull)
	} else {
		target, err = b.host.ResolveAnnotationType(annotation, unknownNull)
	}
	if err != nil {
		return nil, err
	}
	if b.clause.jsonbRecordBase && raw.Syntax != parser.FunctionCast && target.Type.IsUserDefined() && target.Type.BaseType().IsComposite() {
		b.prepared.CaptureComposite(preparedCompositeKey{Offset: raw.TypeName.Pos.Offset, Kind: analyze.CompositeRecordArgument}, target, nil)
	}
	if target.Type.ID() == sqltype.Unknown {
		// UNKNOWN is syntax provenance only, never an executable datum type.
		return b.bind(raw.Child, outerPolicy)
	}
	if row, ok := raw.Child.(parser.RowExpr); ok && target.Type.BaseType().IsComposite() {
		return b.bindRow(row, target, sqltype.Explicit, raw.Pos)
	}
	if target.Type.ID() == sqltype.Regclass {
		return b.bindRegclassCast(raw.Child, raw.Pos)
	}
	unsupportedTarget := !target.Type.IsUserDefined() && !target.Type.IsDomain() && !target.Type.IsDeferredRecord() && !sqltype.StoredScalarCastType(target.Type.ID()) && !(outerPolicy.stringInput && target.Type.ID() == sqltype.Char) && target.Type.ID() != sqltype.PgNodeTree && !target.Type.IsArray()
	if unsupportedTarget && !b.consumer.datetimeZoneOperands {
		return nil, expr.ErrorAt("0A000", "explicit casts are supported only for int4, int8, text and bool", raw.Pos)
	}
	if err := b.captureTypeDiagnosticInput(target.Type); err != nil {
		return nil, err
	}
	if target.Type.ContainsCatalogIdentifier() {
		if err := b.captureElementInput(); err != nil {
			return nil, err
		}
	}
	policy := expressionPolicy{context: projectionContext, thresholdArrayInput: outerPolicy.thresholdArrayInput}
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
	case parser.ArrayExpr:
		policy.expected = target
		policy.mode = sqltype.Explicit
	case parser.CastExpr:
		if unknownNullSyntax(child) {
			policy.expected = target
		}
	case parser.ParameterExpr:
		if b.stored.checkLiterals != nil {
			break // bind resolves the captured literal without parameter inference.
		}
		if b.params.noParameters || b.consumer.defaultConsumer {
			return nil, expr.ErrorAt("42P02", fmt.Sprintf("there is no parameter $%d", child.Number), child.Pos)
		}
		if b.params.parameters[child.Number].Type == nil {
			if raw.Syntax == parser.FunctionCast && target.Type.ID() != sqltype.Text {
				if child.Number <= 0 {
					return nil, expr.ErrorAt("42P02", fmt.Sprintf("there is no parameter $%d", child.Number), child.Pos)
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
	// Bind annotations before moving them so their original type checks,
	// locations, captured identities and child effects remain authoritative.
	child, annotations := domainCoercionInput(child, target)
	source := child.ResultUsage()
	if unknownNull && target.Type.IsDeferredRecord() && source == target {
		// This is NULL transport, not a general RECORD cast/coercion edge.
		return child, nil
	}
	if outerPolicy.stringInput && target.Type.ID() == sqltype.Char && source == target {
		// The literal/input path already applied the CHAR codec. Retain that
		// resolved datum without admitting CHAR to general explicit coercion.
		return child, nil
	}
	pos := raw.Pos
	if start := child.SourcePosition(); start.Character > 0 && (pos.Character <= 0 || start.Character < pos.Character) {
		pos = start
	}
	info, err := b.coercionInfo(b.info(target, pos, child.Properties()), child)
	if err != nil {
		return nil, err
	}
	target = info.Usage
	coercionPlan, err := sqltype.PlanCoercion(source, target, sqltype.Explicit)
	if err != nil {
		if sqltype.IsFTS(source.Type.BaseType().ID()) || sqltype.IsFTS(target.Type.BaseType().ID()) {
			return nil, expr.ErrorAt("42846", fmt.Sprintf("cannot cast type %s to %s", b.diagnosticTypeName(source.Type), b.diagnosticTypeName(target.Type)), raw.Pos)
		}
		if source.Type.BaseType().ID() == sqltype.Tid || target.Type.BaseType().ID() == sqltype.Tid {
			if raw.Syntax == parser.FunctionCast {
				return nil, functionCastError(raw, source.Type.DiagnosticName(), false)
			}
			return nil, expr.ErrorAt("42846", fmt.Sprintf("cannot cast type %s to %s", b.diagnosticTypeName(source.Type), b.diagnosticTypeName(target.Type)), raw.Pos)
		}
		if sqltype.IsRoutineAlias(source.Type.BaseType().ID()) || sqltype.IsRoutineAlias(target.Type.BaseType().ID()) {
			return nil, expr.ErrorAt("42846", fmt.Sprintf("cannot cast type %s to %s", b.diagnosticTypeName(source.Type), b.diagnosticTypeName(target.Type)), raw.Pos)
		}
		// A rejected cast from an existing stored scalar to an existing array is
		// a type mismatch, not an unimplemented scalar provider. This only
		// selects the diagnostic below; it adds no coercion capability.
		if sqltype.IsRegObjectAlias(source.Type.BaseType().ID()) || sqltype.IsRegObjectAlias(target.Type.BaseType().ID()) {
			if raw.Syntax == parser.FunctionCast {
				return nil, functionCastError(raw, source.Type.DiagnosticName(), false)
			}
			return nil, expr.ErrorAt("42846", fmt.Sprintf("cannot cast type %s to %s", b.diagnosticTypeName(source.Type), b.diagnosticTypeName(target.Type)), raw.Pos)
		}
		if source.Type.BaseType().IsCube() || target.Type.BaseType().IsCube() {
			return nil, expr.ErrorAt("42846", fmt.Sprintf("cannot cast type %s to %s", b.diagnosticTypeName(source.Type), b.diagnosticTypeName(target.Type)), raw.Pos)
		}
		if !source.Type.BaseType().IsQueryInt() && !target.Type.BaseType().IsQueryInt() && !sqltype.IsTransactionIdentifier(source.Type.BaseType().ID()) && !sqltype.IsTransactionIdentifier(target.Type.BaseType().ID()) && !b.consumer.datetimeZoneOperands && !sqltype.IsNetwork(source.Type.BaseType().ID()) && !sqltype.IsNetwork(target.Type.BaseType().ID()) && source.Type.BaseType().ID() != sqltype.Money && target.Type.BaseType().ID() != sqltype.Money && source.Type.BaseType().ID() != sqltype.Regtype && target.Type.BaseType().ID() != sqltype.Regtype && source.Type.BaseType().ID() != sqltype.Regrole && target.Type.BaseType().ID() != sqltype.Regrole && !source.Type.BaseType().IsEnum() && !target.Type.BaseType().IsEnum() && !source.Type.BaseType().IsComposite() && !target.Type.BaseType().IsComposite() && source.Type.BaseType().ID() != sqltype.JSON && target.Type.BaseType().ID() != sqltype.JSON && source.Type.BaseType().ID() != sqltype.JSONB && target.Type.BaseType().ID() != sqltype.JSONB && source.Type.BaseType().ID() != sqltype.JSONPath && target.Type.BaseType().ID() != sqltype.JSONPath && source.Type.ID() != sqltype.PgNodeTree && target.Type.ID() != sqltype.PgNodeTree && !(target.Type.BaseType().IsArray() && sqltype.StoredScalarCastType(source.Type.BaseType().ID())) && (!sqltype.BaselineCastType(source.Type.ID()) || !sqltype.BaselineCastType(target.Type.ID())) && !sqltype.OIDCastPair(source.Type.ID(), target.Type.ID()) && !sqltype.TemporalCastPair(source.Type.ID(), target.Type.ID()) {
			if !sqltype.IsOperatorAlias(source.Type.BaseType().ID()) && !sqltype.IsOperatorAlias(target.Type.BaseType().ID()) && !sqltype.IsRegTS(source.Type.BaseType().ID()) && !sqltype.IsRegTS(target.Type.BaseType().ID()) {
				return nil, expr.ErrorAt("0A000", "explicit casts are supported only for int4, int8, text and bool", raw.Pos)
			}
		}
		if raw.Syntax == parser.FunctionCast {
			return nil, functionCastError(raw, source.Type.DiagnosticName(), false)
		}
		return nil, expr.ErrorAt("42846", fmt.Sprintf("cannot cast type %s to %s", b.diagnosticTypeName(source.Type), b.diagnosticTypeName(target.Type)), raw.Pos)
	}
	if unsupportedTarget {
		return nil, expr.ErrorAt("0A000", "explicit casts are supported only for int4, int8, text and bool", raw.Pos)
	}
	if coercionPlan.RequiresCatalogOutput() {
		b.prepared.RegclassConcat = true
	}
	if source == target {
		return child, nil
	}
	info.Property = coercionProperties(child, coercionPlan)
	if folded, ok, err := b.foldRegtypeConstant(child, coercionPlan, info); ok || err != nil {
		return restoreDomainCastCollations(annotations, folded), err
	}
	return restoreDomainCastCollations(annotations, plan.CoercionExpr{Info: info, Child: child, Plan: coercionPlan, Mode: sqltype.Explicit}), nil
}

func domainCoercionInput(child expr.Expr, target sqltype.Usage) (expr.Expr, expr.Expr) {
	if !target.Type.IsDomain() || target.Collation.ID == 0 || child.ResultUsage().Type == target.Type {
		return child, nil
	}
	annotations := child
	for {
		annotation, ok := child.(plan.CollationExpr)
		if !ok {
			return child, annotations
		}
		child = annotation.Child
	}
}

func restoreDomainCastCollations(original, coerced expr.Expr) expr.Expr {
	if coerced == nil {
		return nil
	}
	annotation, ok := original.(plan.CollationExpr)
	if !ok {
		return coerced
	}
	ref := annotation.Usage.Collation
	annotation.Child = restoreDomainCastCollations(annotation.Child, coerced)
	annotation.Usage = coerced.ResultUsage()
	annotation.Usage.Collation = ref
	annotation.Property = coerced.Properties()
	return annotation
}

// Storage consumes an expression's value, not its result-collation derivation.
// Keep semantic domain boundaries and annotations inside this final assignment
// while publishing the exact immutable destination Usage.
func storageDestination(node expr.Expr, target sqltype.Usage, pos parser.Position) (expr.Expr, error) {
	if node.ResultUsage() == target {
		return node, nil
	}
	coercionPlan, err := sqltype.PlanCoercion(node.ResultUsage(), target, sqltype.Assignment)
	if err != nil {
		return nil, err
	}
	info := expr.Info{Usage: target, Pos: pos, Property: coercionProperties(node, coercionPlan)}
	return plan.CoercionExpr{Info: info, Child: node, Plan: coercionPlan, Mode: sqltype.Assignment}, nil
}

// Only a new domain conversion adds semantic authority before storage. Other
// assignments already retain their semantic expression as the child and can
// convert directly to the physical destination, without an intermediate relabel.
func storageCoercionInfo(view catalog.Read, info expr.Info, child expr.Expr) (expr.Info, error) {
	if info.Usage.Type.IsDomain() && child.ResultUsage().Type != info.Usage.Type {
		return coercionInfo(view, info, child)
	}
	return info, nil
}

// Strong column/function names survive standard casts; weak literal names do not.
func expressionName(raw parser.Expr, usage sqltype.Usage, bound expr.Expr) (string, bool) {
	for {
		coercion, ok := bound.(plan.CoercionExpr)
		if !ok {
			break
		}
		bound = coercion.Child
	}
	switch r := raw.(type) {
	case parser.RowExpr:
		return "row", false
	case parser.FieldExpr:
		return r.Field.Text, true
	case parser.StarExpr:
		if len(r.Qualifier.Parts) != 0 {
			return r.Qualifier.Parts[len(r.Qualifier.Parts)-1].Text, true
		}
		return "?column?", false
	case parser.SubqueryExpr:
		if r.Kind == parser.SubqueryExists {
			return "exists", true
		}
		if r.Kind == parser.SubqueryScalar {
			if child, ok := bound.(plan.SubqueryExpr); ok && len(child.QueryPlan.Root.Schema()) == 1 {
				name := child.QueryPlan.Root.Schema()[0].Name
				return name, name != "?column?" || len(r.Query.Labels) > 0 && r.Query.Labels[0].Text != ""
			}
		}
		return "?column?", false
	case parser.IdentityExpr:
		return r.Name, true
	case parser.CurrentDateTimeExpr:
		return r.Kind.Name(), true
	case parser.GroupingExpr:
		return "grouping", true
	case parser.AggregateExpr:
		return r.Name, true
	case parser.ConditionalExpr:
		return conditionalName(r), true
	case parser.CaseExpr:
		if r.Else != nil {
			var otherwise expr.Expr
			if expr, ok := bound.(plan.CaseExpr); ok {
				otherwise = expr.Otherwise
			}
			if name, strong := expressionName(r.Else, usage, otherwise); strong {
				return name, true
			}
		}
		return "case", false
	case parser.ColumnExpr:
		return r.Name.Parts[len(r.Name.Parts)-1].Text, true
	case parser.MergeActionExpr:
		return "merge_action", true
	case parser.SQLJSONExpr:
		return r.Kind, true
	case parser.JSONPredicateExpr:
		return "?column?", false
	case parser.FunctionExpr:
		return r.Name.Parts[len(r.Name.Parts)-1].Text, true
	case parser.CallExpr:
		return r.Name.Parts[len(r.Name.Parts)-1].Text, true
	case parser.RegclassExpr:
		if name, strong := expressionName(r.Child, usage, bound); strong {
			return name, true
		}
		return "regclass", false
	case parser.ArrayExpr:
		return "array", false
	case parser.SubscriptExpr:
		return expressionName(r.Base, usage, bound)
	case parser.TimeZoneExpr:
		return "timezone", true
	case parser.CollateExpr:
		if expr, ok := bound.(plan.CollationExpr); ok {
			bound = expr.Child
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
	if d, ok := err.(*sqlstate.Error); ok && (d.Code == "54000" || d.Code == "XX000") {
		copy := *d
		if pos.Character > 0 {
			copy.Position = strconv.Itoa(pos.Character)
		}
		return &copy
	}
	if d, ok := err.(*sqlstate.Error); ok && (d.Code == "42704" || d.Code == "42622" || d.Code == "55P04" || d.Code == "0A000" || d.Code == "22P02" || d.Code == "22P05" || d.Code == "22000" || d.Code == "22001" || d.Code == "2201B" || d.Code == "2202E" || d.Code == "22003" || d.Code == "22007" || d.Code == "22008" || d.Code == "22009" || d.Code == "22015" || d.Code == "22023" || d.Code == "42601") {
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

// Full analysis invalidation recollects temporal scalar/array captures while the
// candidate still supplies fixed signatures and immutable native logical values.
func (b *expressionBinder) collectTemporalConstants() bool {
	if !b.expected.Held || b.expected.RefreshTemporals {
		return true
	}
	owner, generation, ok := b.host.PlanOwner()
	return ok && b.expected.PlanOwner == owner && b.expected.PlanGeneration != generation
}

// Explicit typed literals use this only to discover their frozen modifier
// before resolving the raw declaration again. Scalar reconstruction must use
// captured with the complete contextual usage.
func (b *expressionBinder) captureAt(offset int) (temporalConstant, bool, error) {
	if b.expected.Held {
		captures := b.expected.Temporals
		i := sort.Search(len(captures), func(i int) bool { return captures[i].Offset >= offset })
		if i < len(captures) && captures[i].Offset == offset {
			if b.expected.RefreshTemporals && refreshableTemporalCapture(captures[i]) {
				return temporalConstant{}, false, nil
			}
			saved := captures[i]
			for i++; i < len(captures) && captures[i].Offset == offset; i++ {
				if captures[i].TypeID != saved.TypeID || captures[i].Typmod != saved.Typmod ||
					captures[i].Typed != saved.Typed || captures[i].TypedTypmod != saved.TypedTypmod {
					return temporalConstant{}, false, fmt.Errorf("stuntgres: ambiguous captured temporal metadata")
				}
			}
			return saved, true, nil
		}
	}
	return temporalConstant{}, false, nil
}

func (b *expressionBinder) captured(offset int, usage sqltype.Usage) (temporalConstant, bool, error) {
	if b.expected.Held {
		captures := b.expected.Temporals
		i := sort.Search(len(captures), func(i int) bool { return captures[i].Offset >= offset })
		if i < len(captures) && captures[i].Offset == offset {
			if b.expected.RefreshTemporals && refreshableTemporalCapture(captures[i]) {
				return temporalConstant{}, false, nil
			}
			for ; i < len(captures) && captures[i].Offset == offset; i++ {
				capture := captures[i]
				if capture.TypeID == usage.Type.ID() && capture.Typmod == usage.Typmod {
					return capture, true, nil
				}
			}
			return temporalConstant{}, false, fmt.Errorf("stuntgres: captured temporal usage changed")
		}
	}
	return temporalConstant{}, false, nil
}
func (b *expressionBinder) inputConstant(text string, u sqltype.Usage, pos parser.Position) (sqltype.Value, error) {
	if err := b.captureTypeDiagnosticInput(u.Type); err != nil {
		return sqltype.Value{}, err
	}
	if u.Type.ContainsRegObject() {
		return b.regObjectConstant(text, u, pos)
	}
	if u.Type.ContainsRegproc() {
		return b.routineConstant(text, u, pos)
	}
	if u.Type.ID() == sqltype.Regtype {
		return b.regtypeConstant(text, pos)
	}
	if u.Type.ContainsRegTS() {
		return b.regTSConstant(text, u, pos)
	}
	if sqltype.IsOperatorAlias(u.Type.ID()) {
		return b.operatorConstant(text, u.Type, pos)
	}
	if u.Type.ContainsOperatorAlias() {
		return b.operatorCompoundConstant(text, u, pos)
	}
	if u.Type.ID() == sqltype.Regclass {
		node, _, err := b.bindRegclass(parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: text, Pos: pos}})
		if err != nil {
			return sqltype.Value{}, err
		}
		return node.(plan.LiteralExpr).Value, nil
	}
	if sqltype.IsFTS(u.Type.BaseType().ID()) || u.Type.BaseType().IsCube() || u.Type.BaseType().IsQueryInt() || u.Type.ContainsACLItem() || u.Type.ContainsRegrole() || u.Type.BaseType().IsEnum() || u.Type.BaseType().IsArray() || u.Type.BaseType().IsComposite() || u.Type.BaseType().ID() == sqltype.DateRange || u.Type.BaseType().ID() == sqltype.TimestampRange || u.Type.BaseType().ID() == sqltype.TimestamptzRange {
		if u.Type.BaseType().IsArray() && u.Type.ContainsRegtype() {
			return b.regtypeArrayConstant(text, u, pos)
		}
		c, found, err := b.captured(pos.Offset, u)
		if err != nil {
			return sqltype.Value{}, err
		}
		if found {
			catalog := b.bindingCatalog()
			if hasEnumInput(u.Type) && (b.expected.CatalogOwner != catalog.Owner() || b.expected.CatalogEpoch != catalog.Epoch()) {
				if err := checkExpectedLifetime(b.expected, catalog.Owner(), catalog.Epoch()); err != nil {
					return sqltype.Value{}, err
				}
				return sqltype.Value{}, ids.ErrCatalogReplaced
			}
			return c.Value.Retag(u.Type)
		}
		if u.Type.ContainsCatalogIdentifier() {
			if err := b.captureElementInput(); err != nil {
				return sqltype.Value{}, err
			}
		}
		value, err := b.decodeLiteralInput(text, u.Type, -1, b.input.LiteralInput(pos.Character))
		if err != nil {
			if diagnostic, ok := err.(*sqlstate.Error); ok && pos.Character > 0 {
				copy := *diagnostic
				copy.Position = strconv.Itoa(pos.Character)
				return sqltype.Value{}, &copy
			}
			return sqltype.Value{}, err
		}
		if b.collectTemporalConstants() {
			i := sort.Search(len(b.prepared.Temporals), func(i int) bool { return b.prepared.Temporals[i].Offset >= pos.Offset })
			b.prepared.Temporals = append(b.prepared.Temporals, temporalConstant{})
			copy(b.prepared.Temporals[i+1:], b.prepared.Temporals[i:])
			b.prepared.Temporals[i] = temporalConstant{Offset: pos.Offset, Typmod: u.Typmod, TypeID: u.Type.ID(), Value: value}
		}
		return value, nil
	}
	if u.Type.ID() != sqltype.Interval && u.Type.ID() != sqltype.Timestamp && u.Type.ID() != sqltype.Time && u.Type.ID() != sqltype.Date && u.Type.ID() != sqltype.Timestamptz && u.Type.ID() != sqltype.Timetz {
		return b.decodeLiteralInput(text, u.Type, -1, b.input)
	}
	c, found, err := b.captured(pos.Offset, u)
	if err != nil {
		return sqltype.Value{}, err
	}
	if found {
		if c.TypeID == sqltype.Timetz {
			return sqltype.TimetzValue(u.Type, c.Scalar, c.West)
		}
		if c.TypeID == sqltype.Interval {
			return sqltype.IntervalValue(u.Type, c.Months, c.Days, c.Scalar)
		}
		if c.TypeID == sqltype.Time {
			return sqltype.TimeValue(u.Type, c.Scalar)
		}
		if c.TypeID == sqltype.Date {
			return sqltype.DateValue(u.Type, c.Scalar)
		}
		return sqltype.TimestampValue(u.Type, c.Scalar)
	}
	inputTypmod := int32(-1)
	if u.Type.ID() == sqltype.Interval {
		inputTypmod = u.Typmod
	}
	v, err := b.decodeLiteralInput(text, u.Type, inputTypmod, b.input)
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
	if b.collectTemporalConstants() {
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
		i := sort.Search(len(b.prepared.Temporals), func(i int) bool { return b.prepared.Temporals[i].Offset >= pos.Offset })
		b.prepared.Temporals = append(b.prepared.Temporals, temporalConstant{})
		copy(b.prepared.Temporals[i+1:], b.prepared.Temporals[i:])
		west := int32(0)
		if u.Type.ID() == sqltype.Timetz {
			west, err = v.TimeZoneOffset()
			if err != nil {
				return sqltype.Value{}, err
			}
		}
		b.prepared.Temporals[i] = temporalConstant{Offset: pos.Offset, Scalar: n, Typmod: u.Typmod, TypeID: u.Type.ID(), West: west, Months: months, Days: days}
	}
	return v, nil
}

func (b *expressionBinder) captureElementInput() error {
	if b.input.Regclass != nil && b.input.Regtype != nil && b.input.Regrole != nil && b.input.Operator != nil && b.input.Regproc != nil && b.input.Regcollation != nil && b.input.Regnamespace != nil && b.input.RegTS != nil {
		return nil
	}
	auth, err := b.bindingAuthorization()
	if err != nil {
		return err
	}
	return b.host.CaptureElementInput(auth, &b.input)
}

// Capture recursive catalog input from one coherent authorization snapshot.
// Frozen expressions and newly bound expressions share these same owners.
func (e *Engine) captureElementInput(auth AuthorizationView, op *Operation, input *sqltype.InputEnvironment) error {
	var err error
	if input.Regclass == nil {
		input.Regclass, err = e.captureRegclassInput(auth)
		if err != nil {
			return err
		}
	}
	if input.Regtype == nil {
		input.Regtype, err = e.captureRegtypeInput(auth, op)
		if err != nil {
			return err
		}
	}
	if input.Regproc == nil {
		input.Regproc, err = e.captureRoutineInput(auth, input.Regtype.(capturedRegtypeInput))
	}
	if input.Regrole == nil {
		input.Regrole = capturedRegroleInput{roles: auth.Roles}
	}
	if err == nil && input.Operator == nil {
		input.Operator, err = e.captureOperatorInput(auth, op)
	}
	if err == nil && (input.Regcollation == nil || input.Regnamespace == nil) {
		var resolver capturedRegObjectInput
		resolver, err = e.captureRegObjectInput(auth)
		if err == nil {
			input.Regcollation, input.Regnamespace = resolver, resolver
		}
	}
	if err == nil && input.RegTS == nil {
		input.RegTS, err = e.captureRegTSInput(auth)
	}
	return err
}

// Unknown state exists only during analysis. It never becomes an executable Value.
type comparisonOperand struct {
	node expr.Expr
	raw  parser.Expr
}

func (o comparisonOperand) descriptor() *sqltype.Descriptor {
	if o.node == nil {
		return nil
	}
	return o.node.ResultUsage().Type
}

// operandArgument hands an analyzed comparison operand to functions bind
// code; CoerceArgument rebuilds the operand from Expression and Raw.
func operandArgument(o comparisonOperand) functions.Argument {
	argument := functions.Argument{Expression: o.node, Raw: o.raw}
	if o.node != nil {
		argument.Usage = o.node.ResultUsage()
	}
	return argument
}

func (b *expressionBinder) comparisonOperand(raw parser.Expr) (comparisonOperand, error) {
	return b.comparisonOperandPolicy(raw, expressionPolicy{context: projectionContext})
}

// AnalyzeArgument retains generic binding and occurrence-owned unknown inference.
// The request is closed: children cannot invoke catalog or parameter services.
func (b *expressionBinder) AnalyzeArgument(raw parser.Expr, request functions.AnalysisRequest) (functions.Argument, error) {
	var operand comparisonOperand
	var err error
	switch request.Intent {
	case functions.ComparisonArgument:
		operand, err = b.comparisonOperand(raw)
	case functions.ThresholdArrayArgument:
		operand, err = b.comparisonOperandPolicy(raw, expressionPolicy{context: projectionContext, thresholdArrayInput: true})
	case functions.ZoneArgument:
		// Only explicit casts read the flag; restoring it keeps nested and
		// adjacent calls unaffected.
		previous := b.consumer.datetimeZoneOperands
		b.consumer.datetimeZoneOperands = true
		operand, err = b.comparisonOperand(raw)
		b.consumer.datetimeZoneOperands = previous
	case functions.StringTextArgument, functions.StringOtherArgument:
		operand, err = b.stringOperand(raw, request.Intent == functions.StringTextArgument)
	case functions.RecordBaseArgument:
		previous := b.clause.jsonbRecordBase
		b.clause.jsonbRecordBase = true
		operand, err = b.stringOperand(raw, false)
		b.clause.jsonbRecordBase = previous
	case functions.StringProjectionArgument, functions.ProjectionArgument:
		operand.node, err = b.bind(raw, expressionPolicy{context: projectionContext, stringInput: request.Intent == functions.StringProjectionArgument})
		operand.raw = raw
		if err == nil && unknownNullSyntax(raw) {
			if literal, ok := operand.node.(plan.LiteralExpr); ok {
				literal.UnknownNull = true
				operand.node = literal
			}
		}
	case functions.BareProjectionArgument:
		operand.node, err = b.bind(raw, expressionPolicy{context: projectionContext})
		operand.raw = raw
	case functions.DirectDeferredArgument:
		if literal, ok := raw.(parser.LiteralExpr); ok && (literal.Value.Kind == parser.StringValue || literal.Value.Kind == parser.NullValue) {
			operand.raw = raw
		} else {
			operand.node, err = b.bind(raw, expressionPolicy{context: projectionContext})
			operand.raw = raw
		}
	default:
		return functions.Argument{}, fmt.Errorf("stuntgres: invalid function argument analysis")
	}
	original, _ := b.OriginalArgument(operand.raw)
	argument := functions.Argument{Expression: operand.node, Raw: original}
	if operand.node != nil {
		argument.Usage = operand.node.ResultUsage()
		if argument.Usage.Type == nil {
			argument.Kind = functions.UnresolvedParameterArgument
		}
	} else if literal, ok := operand.raw.(parser.LiteralExpr); ok && literal.Value.Kind == parser.NullValue {
		argument.Kind = functions.DeferredNullArgument
	} else {
		argument.Kind = functions.DeferredStringArgument
	}
	return argument, err
}

func (b *expressionBinder) CoerceArgument(argument functions.Argument, request functions.CoercionRequest) (expr.Expr, error) {
	operand := comparisonOperand{node: argument.Expression, raw: argument.Raw}
	switch request.Intent {
	case functions.FinalizeArgument:
		return b.finalizeOperand(operand, request.Target.Type)
	case functions.LikeArgument:
		return b.likeOperand(operand, request.Target.Type)
	case functions.RecordFormalArgument:
		var record *sqltype.Descriptor
		if parameter, ok := argument.Expression.(plan.ParameterExpr); ok {
			record = b.params.parameters[parameter.Number].Type
		}
		if record == nil {
			var err error
			record, err = sqltype.RecordShape(nil)
			if err != nil {
				return nil, err
			}
		}
		return b.finalizeOperand(operand, record)
	case functions.SizeFormalArgument:
		node, err := b.finalizeOperand(operand, request.Target.Type)
		if err != nil {
			return nil, err
		}
		if node.ResultUsage() == request.Target {
			return node, nil
		}
		info, err := b.coercionInfo(b.info(request.Target, node.SourcePosition(), node.Properties()), node)
		if err != nil {
			return nil, err
		}
		coercionPlan, err := sqltype.PlanCoercion(node.ResultUsage(), info.Usage, sqltype.Implicit)
		if err != nil {
			return nil, err
		}
		info.Property = coercionProperties(node, coercionPlan)
		return plan.CoercionExpr{Info: info, Child: node, Plan: coercionPlan, Mode: sqltype.Implicit}, nil
	case functions.ConditionalArgument:
		return b.coerceConditional(operand, request.Target, request.Label)
	case functions.JSONFormattedArgument:
		node := argument.Expression
		info, err := b.coercionInfo(b.info(request.Target, node.SourcePosition(), coercionProperties(node, request.Plan)), node)
		if err != nil {
			return nil, err
		}
		return plan.CoercionExpr{Info: info, Child: node, Plan: request.Plan, Mode: sqltype.Explicit}, nil
	case functions.BareFormalArgument:
		node := argument.Expression
		if node == nil || !request.Planned {
			return nil, fmt.Errorf("stuntgres: invalid bare function argument coercion")
		}
		properties := coercionProperties(node, request.Plan)
		if request.PreserveProperties {
			properties = node.Properties()
		}
		return plan.CoercionExpr{Info: b.info(request.Target, argument.Raw.SourcePosition(), properties), Child: node, Plan: request.Plan, Mode: sqltype.Implicit}, nil
	case functions.AssignmentFormalArgument:
		if !b.clause.selectedCallArguments || argument.Expression == nil || argument.Usage.Type == nil {
			return b.bind(argument.Raw, expressionPolicy{context: assignmentContext, expected: request.Target, mode: sqltype.Implicit})
		}
	case functions.DirectFormalArgument:
		if argument.Usage.Type == nil {
			return b.bind(argument.Raw, expressionPolicy{context: functionArgumentContext, expected: request.Target, mode: sqltype.Implicit})
		}
	case functions.ExactFormalArgument, functions.TextFormalArgument, functions.CollatedTextFormalArgument:
		node, err := b.finalizeOperand(operand, request.Target.Type)
		if err != nil {
			return nil, err
		}
		argument.Expression, argument.Usage = node, node.ResultUsage()
		if request.Intent == functions.TextFormalArgument && request.Target.Type.ID() == sqltype.Text && argument.Usage.Collation.ID != 0 {
			request.Target.Collation = argument.Usage.Collation
		}
	default:
		return nil, fmt.Errorf("stuntgres: invalid function argument coercion")
	}
	node := argument.Expression
	if argument.Usage == request.Target {
		return node, nil
	}
	coercionPlan := request.Plan
	var err error
	if !request.Planned {
		coercionPlan, err = sqltype.PlanCoercion(argument.Usage, request.Target, sqltype.Implicit)
		if err != nil {
			return nil, err
		}
	}
	properties := node.Properties()
	if !request.PreserveProperties {
		properties = coercionProperties(node, coercionPlan)
	}
	pos := node.SourcePosition()
	if request.AtRaw {
		pos = argument.Raw.SourcePosition()
	}
	info := b.info(request.Target, pos, properties)
	switch {
	case request.Intent == functions.CollatedTextFormalArgument:
		info, err = b.collatedInfo(info, node)
	case request.Intent == functions.TextFormalArgument && request.Target.Type.ID() == sqltype.Text:
		info, err = b.coercionInfo(info, node)
	}
	if err == nil && info.RoutineReference == nil {
		info, err = b.coercionRoutineInfo(info, node)
	}
	if err != nil {
		return nil, err
	}
	return plan.CoercionExpr{Info: info, Child: node, Plan: coercionPlan, Mode: sqltype.Implicit}, nil
}

func (b *expressionBinder) comparisonOperandPolicy(raw parser.Expr, policy expressionPolicy) (comparisonOperand, error) {
	if literal, ok := raw.(parser.LiteralExpr); ok && (literal.Value.Kind == parser.StringValue || literal.Value.Kind == parser.NullValue) {
		return comparisonOperand{raw: raw}, nil
	}
	if annotation, ok := raw.(parser.CastExpr); ok && unknownNullSyntax(raw) {
		var target sqltype.Usage
		var err error
		if policy.thresholdArrayInput {
			target, err = b.host.ResolveAnnotationTypeInput(annotation.TypeName, true, false, false, false, true)
		} else if policy.stringInput {
			target, err = b.host.ResolveStringAnnotationType(annotation.TypeName, true)
		} else {
			target, err = b.host.ResolveAnnotationType(annotation.TypeName, true)
		}
		if err != nil {
			return comparisonOperand{}, err
		}
		if target.Type.ID() == sqltype.Unknown {
			return comparisonOperand{raw: annotation.Child}, nil
		}
	}
	switch raw.(type) {
	case parser.InListExpr, parser.ArrayComparisonExpr:
	case parser.RowExpr, parser.FieldExpr, parser.StarExpr, parser.CurrentDateTimeExpr, parser.IdentityExpr, parser.ColumnExpr, parser.ParameterExpr, parser.LiteralExpr, parser.TypedLiteralExpr, parser.CastExpr, parser.CollateExpr, parser.TimeZoneExpr, parser.OperatorExpr, parser.CallExpr, parser.RegclassExpr, parser.ArrayExpr, parser.SubscriptExpr, parser.FunctionExpr, parser.AggregateExpr, parser.GroupingExpr, parser.CaseExpr, parser.ConditionalExpr, parser.LogicalExpr, parser.PredicateExpr, parser.IndexSubqueryExpr, parser.SubqueryExpr:
	case parser.SQLJSONExpr, parser.JSONPredicateExpr:
	case parser.MergeActionExpr:
	default:
		return comparisonOperand{}, fmt.Errorf("stuntgres: invalid comparison operand")
	}
	node, err := b.bind(raw, policy)
	return comparisonOperand{node: node, raw: raw}, err
}

func (b *expressionBinder) coerceParameter(p plan.ParameterExpr, u sqltype.Usage) (expr.Expr, error) {
	if existing := b.params.parameters[p.Number]; existing.Type != nil && existing.Type != u.Type {
		d := expr.ErrorAt("42P08", fmt.Sprintf("inconsistent types deduced for parameter $%d", p.Number), p.Pos).(*sqlstate.Error)
		d.Detail = fmt.Sprintf("%s versus %s", existing.Type.DiagnosticName(), u.Type.DiagnosticName())
		return nil, d
	}
	b.params.parameters[p.Number] = u.Type.Usage()
	return plan.ParameterExpr{Info: b.info(u.Type.Usage(), p.Pos, p.Property), Number: p.Number}, nil
}

func (b *expressionBinder) finalizeOperand(o comparisonOperand, typ *sqltype.Descriptor) (expr.Expr, error) {
	if o.descriptor() != nil {
		return o.node, nil
	}
	if p, ok := o.node.(plan.ParameterExpr); ok {
		return b.coerceParameter(p, typ.Usage())
	}
	if o.raw == nil {
		return nil, fmt.Errorf("stuntgres: missing unknown comparison operand")
	}
	return b.bind(o.raw, expressionPolicy{context: projectionContext, expected: typ.Usage()})
}

func (b *expressionBinder) bindComparison(raw parser.OperatorExpr) (expr.Expr, error) {
	return b.bindComparisonCandidates(raw, nil)
}

func (b *expressionBinder) bindComparisonCandidates(raw parser.OperatorExpr, candidates *operatorCandidateSet) (expr.Expr, error) {
	if len(raw.Args) != 2 || len(raw.Name.Parts) < 1 || len(raw.Name.Parts) > 2 {
		return nil, fmt.Errorf("stuntgres: invalid comparison expression")
	}
	operator, ok := comparisonOperator(raw.Name.Parts[len(raw.Name.Parts)-1].Text)
	if !ok {
		return nil, fmt.Errorf("stuntgres: invalid comparison operator")
	}
	if left, ok := raw.Args[0].(parser.RowExpr); ok {
		if right, ok := raw.Args[1].(parser.RowExpr); ok {
			return b.bindRowComparison(raw, operator, left, right)
		}
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
	return b.bindComparisonOperandCandidates(raw, operator, left, right, candidates)
}

func (b *expressionBinder) bindComparisonOperands(raw parser.OperatorExpr, operator sqltype.ComparisonOperator, left, right comparisonOperand) (expr.Expr, error) {
	return b.bindComparisonOperandCandidates(raw, operator, left, right, nil)
}

func (b *expressionBinder) bindComparisonOperandCandidates(raw parser.OperatorExpr, operator sqltype.ComparisonOperator, left, right comparisonOperand, candidates *operatorCandidateSet) (expr.Expr, error) {
	lt, rt, err := b.resolveComparisonCandidates(raw, left.descriptor(), right.descriptor(), candidates)
	if err != nil {
		return nil, err
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	declaration, _, err := b.comparisonRoutineDeclaration(name, lt, rt)
	if err != nil {
		return nil, err
	}
	// VARCHAR has binary-compatible TEXT operator formals, but a contextual
	// literal still owns its original concrete usage for stored CHECK replay.
	// Keep that source node and put the conversion on this occurrence.
	contextualize := func(operand comparisonOperand, source, target *sqltype.Descriptor) (comparisonOperand, error) {
		if operand.node == nil && source != nil && source.BaseType().ID() == sqltype.VarChar && target.ID() == sqltype.Text {
			node, err := b.finalizeOperand(operand, source.BaseType())
			operand.node = node
			return operand, err
		}
		return operand, nil
	}
	left, err = contextualize(left, right.descriptor(), lt)
	if err != nil {
		return nil, err
	}
	right, err = contextualize(right, left.descriptor(), rt)
	if err != nil {
		return nil, err
	}
	l, err := b.coerceConditional(left, lt.Usage(), name)
	if err != nil {
		return nil, err
	}
	r, err := b.coerceConditional(right, rt.Usage(), name)
	if err != nil {
		return nil, err
	}
	input, err := b.comparisonInfo(l, r)
	if err != nil {
		return nil, err
	}
	predicate, err := sqltype.BindComparisonPredicate(operator, l.ResultUsage().Type, r.ResultUsage().Type, b.typ(sqltype.Bool), input.Usage.Collation, input.CollationConflict)
	if err != nil {
		return nil, functions.ComparisonError(b, err, left.descriptor(), right.descriptor(), raw)
	}
	p, rp := l.Properties(), r.Properties()
	p.RowDependent = p.RowDependent || rp.RowDependent
	p.ParameterDependent = p.ParameterDependent || rp.ParameterDependent
	p.StageDependent = p.StageDependent || rp.StageDependent
	p.DomainDependent = p.DomainDependent || rp.DomainDependent
	p.RuntimeDomainConversion = p.RuntimeDomainConversion || rp.RuntimeDomainConversion
	if rp.Volatility > p.Volatility {
		p.Volatility = rp.Volatility
	}
	if predicate.SessionDependent() && p.Volatility < expr.Stable {
		p.Volatility = expr.Stable
	}
	namespace := ""
	if len(raw.Name.Parts) == 2 {
		namespace = raw.Name.Parts[0].Text
	}
	info := b.info(b.typ(sqltype.Bool).Usage(), raw.Pos, p)
	if declaration != nil {
		info, err = b.selectedBuiltinRoutineInfo(info, declaration)
		if err != nil {
			return nil, err
		}
	}
	return plan.ComparisonExpr{Info: info, Left: l, Right: r, Predicate: predicate, Operator: name, Namespace: namespace}, nil
}

// Strict arithmetic may erase a target-set subtree when a physical constant
// NULL makes its result independent of producer demand. Ordinary row arithmetic
// and imported parameter NULLs retain their existing execution-owned behavior.
func arithmeticSetConstantNull(original, current plan.ArithmeticExpr) bool {
	return (bitwiseConstantNull(current.Left) || bitwiseConstantNull(current.Right)) &&
		(exec.QuerySetDepth(original) != 0 || exec.QuerySetDepth(current) != 0)
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
func (b *expressionBinder) bindArithmetic(raw parser.OperatorExpr) (expr.Expr, error) {
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
	return b.finishArithmetic(raw, left, right)
}

func (b *expressionBinder) finishArithmetic(raw parser.OperatorExpr, left, right comparisonOperand) (expr.Expr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	operator := arithmeticOperator(name)
	lt, rt := left.descriptor(), right.descriptor()
	if lt != nil {
		lt = lt.BaseType()
	}
	if rt != nil {
		rt = rt.BaseType()
	}
	visible, err := b.builtinVisible(raw.Name, analyze.OperatorLookup)
	if err != nil {
		return nil, err
	}
	if !visible {
		return nil, functions.OperatorError(sqlError("42883", "operator does not exist"), lt, rt, raw)
	}
	if name == "-" && len(raw.Args) == 2 {
		if node, selected, err := functions.SelectJSONBDelete(b, raw, operandArgument(left), operandArgument(right)); selected {
			return node, err
		}
	}
	if symbolicArithmeticName(name) {
		return nil, functions.OperatorError(&sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883"}, lt, rt, raw)
	}
	signature, err := sqltype.ResolveArithmetic(operator, lt, rt, len(raw.Args) == 1)
	if err != nil {
		return nil, functions.OperatorError(err, lt, rt, raw)
	}
	typ := b.typ(signature.Result)
	leftType := b.typ(signature.Left)
	var rightType *sqltype.Descriptor
	if len(raw.Args) == 2 {
		rightType = b.typ(signature.Right)
	}
	provider, err := sqltype.BindArithmetic(operator, leftType, rightType, typ)
	if err != nil {
		return nil, err
	}
	finish := func(o comparisonOperand, target *sqltype.Descriptor) (expr.Expr, error) {
		source := o.descriptor()
		if source == nil {
			source = target
		}
		n, err := b.finalizeOperand(o, source)
		if err != nil {
			return nil, err
		}
		if source != target {
			coercionPlan, err := sqltype.PlanCoercion(n.ResultUsage(), target.Usage(), sqltype.Implicit)
			if err != nil {
				return nil, err
			}
			info, err := b.coercionRoutineInfo(b.info(target.Usage(), n.SourcePosition(), coercionProperties(n, coercionPlan)), n)
			if err != nil {
				return nil, err
			}
			n = plan.CoercionExpr{Info: info, Child: n, Plan: coercionPlan, Mode: sqltype.Implicit}
		}
		return n, nil
	}
	l, err := finish(left, leftType)
	if err != nil {
		return nil, err
	}
	var r expr.Expr
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
		p.DomainDependent = p.DomainDependent || rp.DomainDependent
		p.RuntimeDomainConversion = p.RuntimeDomainConversion || rp.RuntimeDomainConversion
	}
	if provider.SessionDependent() && p.Volatility < expr.Stable {
		p.Volatility = expr.Stable
	}
	// Contextual consumers use the cached expression start; signature errors
	// above retain the raw operator token.
	pos := raw.Pos
	for _, child := range [...]expr.Expr{l, r} {
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
	formalLeft, formalRight := signature.Left, signature.Right
	if len(raw.Args) == 1 {
		formalLeft, formalRight = 0, signature.Left
	}
	info, err := b.builtinOperatorRoutineInfo(b.info(typ.Usage(), pos, p), name, formalLeft, formalRight)
	if err != nil {
		return nil, err
	}
	return plan.ArithmeticExpr{Info: info, Left: l, Right: r, Provider: provider, Operator: operator, Namespace: namespace}, nil
}
