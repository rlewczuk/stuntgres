package engine

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// EvalContext is the per-evaluation state that engine expression nodes recover
// from expr.Context. It is three embedded groups, so every field is still a
// promoted selector (c.Row, c.Context, c.routineChecks). Copying an
// EvalContext (x := *c, queryState.evaluation, indexedStage) copies all three
// groups by value, exactly as before the split.
type EvalContext struct {
	evalFrame
	evalStatement
	evalAdmission
}

// evalFrame is the current row frame: scope/frame numbers, the current and old
// rows, the outer frame chain, query occurrence/provenance and presence slots,
// MERGE/RETURNING row state, the active domain value and the UPDATE output
// tuple. Query openers, row producers and DML executors write it per row or
// per nested frame; frame nodes (column, group, grouping, tableoid, RETURNING,
// MERGE action, cached ORDER BY, window, domain value, UPDATE output) and
// subqueries read it.
type evalFrame struct {
	Scope, Frame                       uint32
	Row                                []sqltype.Value
	OldRow                             []sqltype.Value
	mergeReturning, mergeSourcePresent bool
	mergeAction                        parser.MergeAction
	orderValues                        []sqltype.Value
	// Reused by the current UPDATE execution; nested owners save/restore it.
	updateOutputScratch []sqltype.Value
	updateOutputs       []sqltype.Value
	updateOutputsReady  bool
	Outer               *EvalContext
	query               *queryState
	queryOccurrences    []int
	queryProvenance     []int
	queryRowSource      *querySource
	// Borrowed full logical tuples only while rechecking locked occurrences.
	queryCurrentRows  [][]sqltype.Value
	physicalTable     ids.ObjectRef // Existing generated recipe's containing physical row.
	presenceSlots     []int
	presence          []bool
	domainValue       sqltype.Value
	domainFrameUsage  sqltype.Usage
	domainValueActive bool
}

// evalStatement is the statement runtime: cancellation context, bound
// parameters, input/output environments, statement scope and catalog
// authority, plus per-command prepared CHECK/domain/generation programs and
// preparation flags. Statement executors and preparation set it when they
// build an evaluation; nested frames inherit it by copy. Every node reads
// Context, Input and Output; functions read it through the expr.Context
// capability methods.
type evalStatement struct {
	Context        context.Context
	Parameters     []sqltype.Value
	statementScope *StatementScope
	Input          sqltype.InputEnvironment
	Output         sqltype.OutputEnvironment
	catalog        catalog.Read // Current imported request/view authority for local lowering.
	// Stored CHECK syntax stays immutable; detached executable expressions
	// belong only to this command. A cached nil slice means no nodes changed.
	preparedChecks       map[*catalog.Relation][]expr.Expr
	preparedDomainChecks map[*catalog.Type]domainCheckProgram
	preparedGenerations  map[*catalog.Relation][]expr.Expr // First actual tuple prepares executable recipes, not permission results.
	pgRegex              sqltype.PGRegexExecution
	quoteCaptures        *quoteCaptureCandidate
	quoteOrigin          quoteOrigin
	// Preparation admits closed immutable scalar operators for plain SELECTs
	// and rule programs, without widening parameter or row-dependent demand.
	planStaticOperators bool
	// Separate rule-program permission: actual supplied Param leaves may become
	// typed constants during preparation. Ordinary imported-param barriers stay.
	specializeRuleParameters bool
}

// evalAdmission is routine admission state for local serial DDL/recipe
// preparation (CHECK, generated, index, domain and routine bodies). Admission
// copies set it on a local EvalContext; the statement's live context leaves it
// zero. Recipe-owned manifests are borrowed only by a local admission copy and
// never replace the live scope's authority or another recipe's owner.
type evalAdmission struct {
	routineAuthority   *AuthorizationView // Borrowed local serial DDL admission only; never retained.
	routineRecipe      bool               // Distinguishes an empty owning manifest from the statement manifest.
	routineGraft       bool               // Local ancestry phase only; nil user nodes resume the statement manifest.
	routineChecks      []catalog.ExpressionDependency
	routineReferences  []ids.ObjectRef
	routineConsumer    *catalog.Relation
	routineProgram     *routineAdmissionProgram // Borrowed serial preparation footprint, never row-owned.
	routineBodyContext routineBodyContext
	domainPhase        domainCheckIdentity // Existing transaction/command identity, not a permission result.
}

// A folded value retains conservative type/dependence metadata and the original
// binding retains its lifetime manifest, but the value is not a callable node.
func foldedExpressionInfo(node expr.Expr) expr.Info {
	info := expr.InfoOf(node)
	info.RoutineReference = nil
	return info
}

func (c *EvalContext) FunctionContext() context.Context {
	if c == nil {
		return nil
	}
	return c.Context
}
func (c *EvalContext) FunctionArrayRandomOwner() (expr.RandomOwner, error) {
	if c.liveScope() == nil || c.liveScope().op == nil || c.liveScope().op.session == nil {
		return nil, fmt.Errorf("stuntgres: missing array random session scope")
	}
	return &c.liveScope().op.session.arrayRandom, nil
}

func (c *EvalContext) FunctionInput() sqltype.InputEnvironment   { return c.Input }
func (c *EvalContext) FunctionOutput() sqltype.OutputEnvironment { return c.Output }
func (c *EvalContext) FunctionOverlapsConstant(node expr.Expr) bool {
	return overlapsExecutionConstant(node, c)
}
func (c *EvalContext) FunctionLiteralNull(node expr.Expr) bool { return randomLiteralNull(node) }
func (c *EvalContext) WholeRowNullPresence(node expr.Expr) (handled, absent bool, err error) {
	row, ok := node.(rowExpression)
	if !ok || row.wholeNames == nil {
		return false, false, nil
	}
	if row.Usage.Type == nil || !row.Usage.Type.Concrete() || !row.Usage.Type.BaseType().IsComposite() || len(row.fields) != row.Usage.Type.BaseType().CompositeLen() {
		return true, false, fmt.Errorf("stuntgres: invalid null-count argument type")
	}
	absent, err = row.absent(c)
	return true, absent, err
}
func (c *EvalContext) FunctionJSONOutput() sqltype.OutputEnvironment {
	output := c.Output
	scope := c.liveScope()
	output.Regclass, output.Regtype, output.Regproc = scope, scope, scope
	output.Regrole, output.Regcollation, output.Regnamespace = scope, scope, scope
	output.Operator, output.ACLItem = scope, scope
	output.RegTS = scope
	return output
}
func (c *EvalContext) FunctionStringOutput() sqltype.OutputEnvironment {
	output := c.Output
	scope := c.liveScope()
	output.Regclass, output.Regtype, output.Regproc = scope, scope, scope
	output.Regrole, output.Regcollation, output.Regnamespace = scope, scope, scope
	output.Operator, output.ACLItem = scope, scope
	output.RegTS = scope
	return output
}
func (c *EvalContext) FunctionFormatOutput() sqltype.OutputEnvironment {
	output := c.Output
	scope := c.liveScope()
	output.Regclass, output.Regtype, output.Regproc = scope, scope, scope
	output.Regrole, output.Regcollation, output.Regnamespace = scope, scope, scope
	output.Operator, output.ACLItem = scope, scope
	output.RegTS = scope
	return output
}
func (c *EvalContext) FunctionBinaryConversion(source, destination int) (*sqltype.EncodingConversionHandler, error) {
	procedure, err := c.liveScope().conversionProcedure(source, destination)
	if err != nil || procedure == nil {
		return nil, err
	}
	return &procedure.Conversion, nil
}

func (c *EvalContext) FunctionPropertiesMatch(properties expr.Properties, rule expr.IntrinsicPropertyRule) bool {
	return expr.PropertiesMatch(properties, rule)
}
func evaluationContext(c *EvalContext) error {
	if c == nil || c.Context == nil {
		return fmt.Errorf("stuntgres: missing expression context")
	}
	if c.domainValueActive {
		c.Input.DomainChecks = c
	}
	return c.Context.Err()
}

type literalExpression struct {
	expr.Info
	value sqltype.Value
}

func (e literalExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	c := evalContext(ctx)
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	if e.Usage.Type == nil || !e.Usage.Type.Concrete() || e.value.Type() != e.Usage.Type ||
		e.UnknownNull && (!e.value.IsNull() || e.Usage.Type.ID() != sqltype.Text) {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid literal expression")
	}
	return e.value, nil
}

type columnExpression struct {
	expr.Info
	origin             ids.ObjectRef
	scope, frame       uint32
	slot               int
	virtual            expr.Expr
	virtualOwner       *catalog.Relation
	virtualAttribute   int32
	rowStart, rowWidth int
	// Zero denotes an owner-row read; SQL occurrence IDs are encoded plus one.
	queryOccurrence int
	mergeSource     bool // Missing MERGE source tuples short-circuit virtual recipes.
}

func (e columnExpression) rowContext(c *EvalContext) (*EvalContext, error) {
	if err := evaluationContext(c); err != nil {
		return nil, err
	}
	for c != nil && (c.Scope != e.scope || c.Frame != e.frame) {
		c = c.Outer
	}
	if c == nil || e.slot < 0 || e.slot >= len(c.Row) {
		return nil, fmt.Errorf("stuntgres: invalid expression row frame")
	}
	v := c.Row[e.slot]
	if !v.ValidResult(e.Usage) {
		return nil, fmt.Errorf("stuntgres: invalid expression column type")
	}
	return c, nil
}

func (e columnExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	c := evalContext(ctx)
	row, err := e.rowContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if e.mergeSource && !row.mergeSourcePresent {
		return sqltype.ResultNull(e.Usage), nil
	}
	if e.virtual == nil {
		return row.Row[e.slot], nil
	}
	if e.queryOccurrence != 0 {
		absent, err := row.queryOperandAbsent(e.queryOccurrence - 1)
		if err != nil {
			return sqltype.Value{}, err
		}
		if absent {
			return sqltype.ResultNull(e.Usage), nil
		}
	}
	if e.rowStart < 0 || e.rowWidth <= 0 || e.rowStart > len(row.Row)-e.rowWidth {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid virtual expression row window")
	}
	local := *row
	local.Context = c.Context
	local.Scope, local.Frame = 1, 1
	local.Row = row.Row[e.rowStart : e.rowStart+e.rowWidth]
	recipe := e.virtual
	if e.virtualOwner != nil {
		selectVirtualGenerationRecipe(&local, e.virtualOwner, e.virtualOwner.ColumnMeta.Generations[e.virtualAttribute])
	} else if e.origin != (ids.ObjectRef{}) {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing virtual generation owner")
	}
	if e.queryOccurrence != 0 {
		source, index, err := row.queryOperandSource(e.queryOccurrence - 1)
		if err != nil {
			return sqltype.Value{}, err
		}
		local.physicalTable = source.physicalRef(index)
		if source.view != nil && e.origin.Address.SubID != 0 {
			var current []sqltype.Value
			if row.queryCurrentRows != nil {
				if source.occurrence < 0 || source.occurrence >= len(row.queryCurrentRows) || row.queryCurrentRows[source.occurrence] == nil {
					return sqltype.Value{}, fmt.Errorf("stuntgres: missing virtual recheck occurrence")
				}
				current = row.queryCurrentRows[source.occurrence]
			}
			recipe, local.Row, err = e.capturedGeneration(source, index, current, &local)
			if err != nil {
				return sqltype.Value{}, err
			}
		}
	}
	return recipe.Eval(&local)
}

type parameterExpression struct {
	expr.Info
	number int
}

func (e parameterExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	c := evalContext(ctx)
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	if e.number <= 0 || e.number > len(c.Parameters) {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid expression parameter frame")
	}
	v := c.Parameters[e.number-1]
	if e.Usage.Type == nil || !e.Usage.Type.Concrete() || v.Type() != e.Usage.Type {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid expression parameter type")
	}
	return v, nil
}

type coercionExpression struct {
	expr.Info
	child expr.Expr
	plan  sqltype.CoercionPlan
	mode  sqltype.CoercionContext
	// The explicit conversion belongs to the enclosing ARRAY or ROW cast, not
	// to a written cast at this element's raw syntax occurrence.
	syntheticConstructorElement bool
}

// COLLATE changes analyzed usage only; values retain the same type and datum.
type collationExpression struct {
	expr.Info
	child expr.Expr
}

func (e collationExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	return e.child.Eval(ctx)
}

func (e coercionExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	c := evalContext(ctx)
	v, err := e.child.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	output := c.Output
	output.Regclass = c.liveScope()
	output.Regtype = c.liveScope()
	output.Regrole = c.liveScope()
	output.Regcollation = c.liveScope()
	output.Regnamespace = c.liveScope()
	output.Regproc = c.liveScope()
	output.Operator = c.liveScope()
	output.RegTS = c.liveScope()
	output.ACLItem = c.liveScope()
	input := c.Input
	if input.Regrole == nil {
		if scope := c.liveScope(); scope != nil {
			input.Regrole = scope.input.Regrole
		}
	}
	if input.Regcollation == nil || input.Regnamespace == nil {
		if scope := c.liveScope(); scope != nil {
			input.Regcollation, input.Regnamespace = scope.input.Regcollation, scope.input.Regnamespace
		}
	}
	if sqltype.ContainsDomainConstraints(e.Usage.Type) {
		input.DomainChecks = c
	}
	return e.plan.Apply(c.Context, v, input, output)
}
func coercionProperties(child expr.Expr, plan sqltype.CoercionPlan) expr.Properties {
	properties := child.Properties()
	properties.RuntimeDomainConversion = properties.RuntimeDomainConversion || plan.ChecksDomain()
	if plan.SessionDependent() && properties.Volatility < expr.Stable {
		properties.Volatility = expr.Stable
	}
	return properties
}

type comparisonExpression struct {
	expr.Info
	left, right expr.Expr
	predicate   sqltype.ComparisonPredicate
	operator    string
	namespace   string
}

func (e comparisonExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	c := evalContext(ctx)
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
func preflightCoercions(c *EvalContext, expression expr.Expr) (expr.Expr, bool, error) {
	return preflightCoercionsWithPolicy(c, expression, false)
}

// Consumed stored defaults and standalone CHECK preparation use decisive
// constant planning. Ordinary source, DELETE and supplied-value preflight
// retain their existing traversal demand.
func preflightCoercionsWithPolicy(c *EvalContext, expression expr.Expr, decisiveDefaults bool) (expr.Expr, bool, error) {
	node, changed, err := preflightCoercionNode(c, expression, decisiveDefaults)
	if err != nil {
		return nil, false, err
	}
	node, folded, err := foldStrictParameterNull(c, node)
	return node, changed || folded, err
}

func preflightCoercionNode(c *EvalContext, expression expr.Expr, decisiveDefaults bool) (expr.Expr, bool, error) {
	if err := evaluationContext(c); err != nil {
		return nil, false, err
	}
	recipe, err := expressionRecipeDescriptor(expression)
	if err != nil {
		return nil, false, err
	}
	if recipe != nil && (!c.routineGraft || c.routineConsumer != recipe) || recipe == nil && c.routineGraft {
		parent := c
		local := *c
		if err := local.selectExpressionRecipe(recipe); err != nil {
			return nil, false, err
		}
		c = &local
		defer func() { parent.preparedDomainChecks = c.preparedDomainChecks }()
	}
	if c.specializeRuleParameters && expression != nil && expression.Properties().ParameterDependent {
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
	case subqueryExpression:
		if !c.planStaticOperators || e.cteDefinition || e.queryPlan == nil {
			return e, false, nil
		}
		input := e.queryPlan.Root
		var presence *projectNode
		if e.presenceOnly {
			// Eligible EXISTS uses nextOccurrence, never its outer targets.
			// Keep actual child predicate and presence/sort preparation.
			presence = input.(*projectNode)
			input = presence.child
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
			copy.child = root
			root = &copy
		}
		query := *e.queryPlan
		query.Root = root
		e.queryPlan = &query
		return e, true, nil
	case domainValueExpression:
		return e, false, nil
	case installedSoundexFunctionExpression:
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
	case installedMetaphoneFunctionExpression:
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
	case installedLevenshteinFunctionExpression:
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
	case installedUUIDOsspFunctionExpression:
		return e, false, nil
	case installedCryptoFunctionExpression:
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
	case assignmentValueExpression:
		base, baseChanged, err := preflightCoercionsWithPolicy(c, e.base, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		value, valueChanged, err := preflightCoercionsWithPolicy(c, e.value, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		targets, err := preflightArrayTargetsWithPlanning(c, []*arrayAssignmentTarget{e.target}, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		changed := baseChanged || valueChanged || targets[0] != e.target
		if changed {
			e.base, e.value, e.target = base, value, targets[0]
		}
		return e, changed, nil
	case installedFunctionExpression:
		arg, changed, err := preflightCoercionsWithPolicy(c, e.Arg, decisiveDefaults)
		if changed {
			e.Arg = arg
		}
		return e, changed, err
	case viewQualificationExpression:
		return mapViewQualification(e, func(child expr.Expr) (expr.Expr, bool, error) {
			return preflightCoercionsWithPolicy(c, child, decisiveDefaults)
		})
	case groupingExpression:
		var args []expr.Expr
		for i, arg := range e.args {
			next, changed, err := preflightCoercionsWithPolicy(c, arg, decisiveDefaults)
			if err != nil {
				return nil, false, err
			}
			if changed {
				if args == nil {
					args = append([]expr.Expr(nil), e.args...)
				}
				args[i] = next
			}
		}
		if args != nil {
			e.args = args
		}
		return e, args != nil, nil
	case extensionPathsExpression:
		arg, changed, err := preflightCoercionsWithPolicy(c, e.arg, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		e.arg = arg
		return e, changed, nil
	case caseExpression:
		return simplifyConditional(c, expression, decisiveDefaults)
	case conditionalExpression:
		if err := e.extremaError(); err != nil {
			return nil, false, err
		}
		if e.kind == parser.Coalesce {
			return simplifyConditional(c, expression, decisiveDefaults)
		}
		var args []expr.Expr
		for i, arg := range e.args {
			next, changed, err := preflightCoercionsWithPolicy(c, arg, decisiveDefaults)
			if err != nil {
				return nil, false, err
			}
			if changed {
				if args == nil {
					args = append([]expr.Expr(nil), e.args...)
				}
				args[i] = next
			}
		}
		if args != nil {
			e.args = args
		}
		next, changed, err := simplifyConditional(c, e, decisiveDefaults)
		return next, changed || args != nil, err
	case rowExpression:
		next, changed, err := mapRow(e, func(old expr.Expr) (expr.Expr, bool, error) {
			next, changed, err := simplifyConditional(c, old, decisiveDefaults)
			if err != nil {
				return nil, false, err
			}
			folded, did, err := preflightCoercionsWithPolicy(c, next, decisiveDefaults)
			return folded, changed || did, err
		})
		return next, changed, err
	case fieldExpression:
		base, changed, err := preflightCoercionsWithPolicy(c, e.base, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		e.base = base
		return e, changed, nil
	case arrayExpression:
		var elements []expr.Expr
		for i, old := range e.elements {
			next, changed, err := preflightCoercionsWithPolicy(c, old, decisiveDefaults)
			if err != nil {
				return nil, false, err
			}
			if changed {
				if elements == nil {
					elements = append([]expr.Expr(nil), e.elements...)
				}
				elements[i] = next
			}
		}
		if elements != nil {
			e.elements = elements
		}
		p := e.Properties()
		if p.Volatility == expr.Immutable && !p.RowDependent && !p.ParameterDependent && !p.StageDependent && !p.DomainDependent && !p.RuntimeDomainConversion {
			value, err := evaluatePlanningExpression(c, e)
			return preflightValue(c, e.Info, value, err)
		}
		return e, elements != nil, nil
	case subscriptExpression:
		next, changed, err := mapSubscript(e, func(old expr.Expr) (expr.Expr, bool, error) {
			return preflightCoercionsWithPolicy(c, old, decisiveDefaults)
		})
		return next, changed, err
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
		if e.plan.ChecksDomain() {
			if err := c.prepareDomainConversion(e.child.ResultUsage().Type, e.Usage.Type); err != nil {
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
			return preflightValue(c, e.Info, sqltype.Null(e.Usage.Type), nil)
		}
		p := e.Properties()
		if p.Volatility == expr.Immutable && !p.RowDependent && !p.StageDependent && !p.DomainDependent && !p.RuntimeDomainConversion {
			value, err := evaluatePlanningExpression(c, e)
			return preflightValue(c, e.Info, value, err)
		}
		return e, lc || rc, nil
	case rowComparisonExpression:
		var pairs []comparisonExpression
		for i, pair := range e.comparisons {
			left, leftChanged, err := preflightCoercionsWithPolicy(c, pair.left, false)
			if err != nil {
				return nil, false, err
			}
			right, rightChanged, err := preflightCoercionsWithPolicy(c, pair.right, false)
			if err != nil {
				return nil, false, err
			}
			// The pair belongs to the row's NULL-aware/lexicographic frontier,
			// not an independently foldable scalar comparison.
			if leftChanged || rightChanged {
				if pairs == nil {
					pairs = append([]comparisonExpression(nil), e.comparisons...)
				}
				pair.left, pair.right = left, right
				pairs[i] = pair
			}
		}
		if pairs != nil {
			e.comparisons = pairs
			return e, true, nil
		}
		return e, false, nil
	case scalarArrayComparisonExpression:
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
				value, err := evaluatePlanningExpression(c, e)
				return preflightValue(c, e.Info, value, err)
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
		var right expr.Expr
		var rc bool
		if e.right != nil {
			right, rc, err = preflightCoercionsWithPolicy(c, e.right, decisiveDefaults)
			if err != nil {
				return nil, false, err
			}
		}
		e.left, e.right = left, right
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
			_, l := left.(literalExpression)
			_, r := right.(literalExpression)
			p := e.Properties()
			if l && (right == nil || r) && p == (expr.Properties{}) {
				v, err := evaluatePlanningExpression(c, e)
				return preflightValue(c, e.Info, v, err)
			}
		}
		if lc || rc {
			return e, true, nil
		}
	case selectedOperatorExpression:
		provider, changed, err := preflightCoercionsWithPolicy(c, e.provider, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		if changed {
			e.provider = provider
			return e, true, nil
		}
	case arithmeticExpression:
		left, leftChanged, err := preflightCoercionsWithPolicy(c, e.left, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		var right expr.Expr
		var rightChanged bool
		if e.right != nil {
			right, rightChanged, err = preflightCoercionsWithPolicy(c, e.right, decisiveDefaults)
			if err != nil {
				return nil, false, err
			}
		}
		e.left, e.right = left, right
		if arithmeticSetConstantNull(expression.(arithmeticExpression), e) {
			return preflightValue(c, e.Info, sqltype.Null(e.Usage.Type), nil)
		}
		if (e.left.ResultUsage().Type.ID() == sqltype.Inet || e.right != nil && e.right.ResultUsage().Type.ID() == sqltype.Inet) && networkOperatorExecutionConstant(e, c) {
			if folded, changed, err := foldStrictParameterNull(c, e); err != nil || changed {
				return folded, changed, err
			}
			value, err := evaluatePlanningExpression(c, e)
			return preflightValue(c, e.Info, value, err)
		}
		if decisiveDefaults || c.planStaticOperators {
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
	case nullTestExpression:
		child, changed, err := preflightCoercionsWithPolicy(c, e.child, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		if decisiveDefaults {
			if _, constant := child.(literalExpression); constant {
				e.child = child
				value, err := evaluatePlanningExpression(c, e)
				return preflightValue(c, e.Info, value, err)
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
				value, err := evaluatePlanningExpression(c, e)
				return preflightValue(c, e.Info, value, err)
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
				value, err := evaluatePlanningExpression(c, e)
				return preflightValue(c, e.Info, value, err)
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
				return preflightValue(c, e.Info, value, nil)
			}
		}
		var right expr.Expr
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
				value, err := evaluatePlanningExpression(c, e)
				return preflightValue(c, e.Info, value, err)
			}
		}
		if leftChanged || rightChanged {
			e.left, e.right = left, right
			return e, true, nil
		}
	case expr.Composite:
		// Function nodes name their fold; installed-routine wrappers keep
		// their cases above.
		return preflightComposite(c, e, decisiveDefaults)
	}
	return expression, false, nil
}

func preflightValue(c *EvalContext, info expr.Info, value sqltype.Value, err error) (expr.Expr, bool, error) {
	if err != nil {
		return nil, false, err
	}
	if err := evaluationContext(c); err != nil {
		return nil, false, err
	}
	info.RoutineReference = nil
	return literalExpression{Info: info, value: value}, true, nil
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

type expressionBinder struct {
	engine                 *Engine
	ctx                    context.Context
	relation               *catalog.Relation
	insertTarget           *catalog.Relation // Diagnostic candidate only, never a source.
	ref                    ids.ObjectRef
	ruleNamespace          *ruleNamespace
	parameters             map[int]sqltype.Usage
	formalParameters       []sqltype.Usage
	closedRoutineFormals   bool // SQL body slots are declarations, not inferred public parameters.
	executeParameters      bool
	updateParameters       bool
	updateInputParameters  map[int]sqltype.Usage
	uses                   []int
	returningReadRequired  bool
	dependencies           []catalog.ExpressionDependency
	input                  sqltype.InputEnvironment
	op                     *Operation
	expected               *StatementDescription
	temporals              []temporalConstant
	composites             map[preparedCompositeKey]compositeOccurrence
	jsonbRecordBase        bool // Scope named cast identity to selected base trees.
	domainValueUsage       *sqltype.Usage
	functionProjection     functionProjectionScope
	selectedOperatorArgs   []expr.Expr
	selectedOperatorSyntax []parser.Expr
	selectedCallArguments  bool              // Known arguments remain reusable throughout provider binding.
	selectedCallReentry    bool              // Only the immediate selected occurrence skips selection once.
	selectedWindowCallee   parser.ObjectName // Diagnostic spelling, never lookup authority.
	anyParameters          []parameterExpression
	deleteTarget           bool
	deleteUsingSources     bool
	mergeWhen              bool
	mergeInput             bool
	targetName             string
	targetAlias            string
	returningAliases       *returningAliases
	defaultConsumer        bool
	defaultTemporary       ids.SessionToken
	// Selected timezone/calendar operand analysis owns its released rejection
	// diagnostic without admitting additional explicit cast providers.
	datetimeZoneOperands       bool
	checkConsumer              bool
	policyConsumer             bool
	indexConsumer              bool
	indexPredicateConsumer     bool
	partitionConsumer          string
	checkDefinitionEquality    bool
	generatedOwner             *catalog.Relation
	visibilitySnapshot         bool
	catalogSnapshot            bool // Demanded inquiry nodes after STRICT folding.
	sequenceConstants          []sequenceConstant
	regtypeConstants           []regtypeConstant
	regclassConcat             bool
	aggregateClause            string
	stringSetGuard             string
	stringFromForbidden        bool
	stringSingleInsert         bool
	queryHasTargetSet          bool
	fromSetObservation         fromSetObservation
	regexpClause               string
	aggregates                 []aggregateCall
	groupingActive             bool
	groupSets                  [][]int
	windowRaw                  []parser.WindowDefinition
	windowDefinitions          map[string]boundWindowSpec
	windowCalls                []windowCall
	argumentOwner              argumentWindowOwner
	queryAuth                  *AuthorizationView
	querySources               []queryLeaf
	queryLockAdmission         *queryLockAdmission
	queryNamespace             *queryNamespace
	queryHidden                []queryQualifier
	queryNonLateral            bool
	queryFrame, nextQueryFrame uint32
	queryScopes                []queryBindingScope
	cteBindings                []cteBinding
	recursiveDefinition        *cteDefinition
	recursiveFrames            map[uint32]bool
	cteScanOrdinal             int
	cteAnalysisDepth           int
	cteExecutables             []*BoundQuery
	statementCTEs              []*cteDefinition
	cteSelectBodies            bool
	resolvedQueryFrame         uint32
	presenceFrames             map[uint32]int
	suppressWholePresence      bool
	queryRowDiagnosticMode     lock.Mode // Clause diagnostics only; tuple authority is queryRowMark.
	subqueryDemand             bool
	groupSubquery              bool
	viewValuesCount            int
	noParameters               bool
	// Materialized definitions need frozen renderer metadata while retaining
	// their distinct, post-analysis bound-parameter diagnostic.
	materializedDefinition bool
	// Only internal TYPE CHECK reparsing supplies these immutable literal nodes.
	// Generated markers are not prepared parameters and never enter inference.
	checkLiterals []literalExpression
	// Frozen syntax retains analyzed lowering without changing prepared source.
	// Analysis-only templates never enter executable nodes or prepared frames.
	storedExpressionSyntax map[storedExpressionSyntaxKey]parser.Expr
	viewLiterals           map[viewLiteralPath]literalExpression
	viewCastUsages         map[viewLiteralPath]sqltype.Usage
	viewRoutineRefs        map[viewLiteralPath]ids.ObjectRef
	viewOperatorRefs       map[viewLiteralPath]ids.ObjectRef
	viewComposites         map[viewLiteralPath]compositeOccurrence
	viewSimilarSubstrings  map[viewLiteralPath]bool
	viewPosixSubstrings    map[viewLiteralPath]bool
	viewColumns            map[viewLiteralPath]viewColumn
	viewStars              map[viewLiteralPath][]viewColumn
	viewJoinCount          int
	viewFunctionCount      int
	viewNamespaces         []viewNamespace
	viewCollations         map[viewLiteralPath]uint32
}

func newExpressionBinder(ctx context.Context, e *Engine, input sqltype.InputEnvironment, op *Operation, expected *StatementDescription) *expressionBinder {
	captureEnumOperationSafety(op, &input)
	b := &expressionBinder{engine: e, ctx: ctx, parameters: make(map[int]sqltype.Usage), input: input, op: op, expected: expected}
	if expected != nil {
		b.seedParameters(expected.Parameters.Types)
		if expected.refreshTemporals {
			for _, capture := range expected.temporals {
				if !refreshableTemporalCapture(capture) {
					b.temporals = append(b.temporals, capture)
				}
			}
		}
	}
	return b
}

func (b *expressionBinder) seedParameters(types []sqltype.Usage) {
	b.formalParameters = types
	currentType := recordTypeMapper(func(t *sqltype.Descriptor) *sqltype.Descriptor { return b.typ(t.ID()) })
	for i, usage := range types {
		if usage.Type != nil {
			usage.Type = currentType(usage.Type)
		}
		b.parameters[i+1] = usage
	}
}

// Binding must retain the caller's catalog/path authority, including a private
// CREATE candidate. Execution still opens providers through live authorization.
func (b *expressionBinder) bindingAuthorization() (AuthorizationView, error) {
	if b.queryAuth != nil {
		auth := *b.queryAuth
		if b.op != nil && b.op.definition != nil {
			auth.definition = b.op.definition
		}
		return auth, nil
	}
	return b.engine.authorizationLocked(b.op)
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
	if b.queryAuth != nil {
		return b.queryAuth.Catalog
	}
	return b.engine.catalog.read(b.engine.catalog.state)
}

func (b *expressionBinder) info(u sqltype.Usage, pos parser.Position, p expr.Properties) expr.Info {
	view := b.bindingCatalog()
	address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(u.Type.ID())}
	revision := ids.DefinitionRevision(1)
	if descriptor, ok := b.definitionDescriptor(address); ok {
		revision = descriptor.Revision()
	}
	b.dependency(b.engine.catalog.Domain.Ref(view.Epoch(), address), revision)
	if u.Collation.ID != 0 {
		address := ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: ids.OID(u.Collation.ID)}
		if d, ok := b.definitionDescriptor(address); ok {
			b.dependency(b.engine.catalog.Domain.Ref(view.Epoch(), address), d.Revision())
		}
	}
	return expr.Info{Usage: u, Pos: pos, Property: p}
}
func (b *expressionBinder) constrain(n int, u sqltype.Usage, pos parser.Position) error {
	if n <= 0 || b.closedRoutineFormals && n > len(b.formalParameters) {
		return expr.ErrorAt("42P02", fmt.Sprintf("there is no parameter $%d", n), pos)
	}
	if old, ok := b.parameters[n]; ok && old.Type != nil && old.Type != u.Type {
		if b.updateParameters {
			// UPDATE uses occurrence-sensitive inference diagnostics; other
			// assignment consumers retain their selected 0A000 boundary.
			_, err := b.coerceParameter(parameterExpression{Info: expr.Info{Pos: pos}, number: n}, u)
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
			return ParameterInfo{}, expr.ErrorAt("42P08", fmt.Sprintf("could not determine data type of parameter $%d", p.number), p.Pos)
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
		if err := b.captureTypeDiagnosticInput(info.Types[i].Type); err != nil {
			return ParameterInfo{}, err
		}
	}
	return info, nil
}

// Output fallback belongs to an unresolved occurrence, not its subsequently
// constrained shared slot. These private provisional nodes never escape describe.
func (b *expressionBinder) finalizeProjection(node expr.Expr) (expr.Expr, error) {
	p, ok := node.(parameterExpression)
	if !ok || p.Usage.Type != nil {
		return node, nil
	}
	return b.coerceParameter(p, b.typ(sqltype.Text).Usage())
}
func (b *expressionBinder) column(raw parser.ColumnExpr) (expr.Expr, error) {
	if b.domainValueUsage != nil && len(raw.Name.Parts) == 1 && raw.Name.Parts[0].Text == "value" {
		return domainValueExpression{b.info(*b.domainValueUsage, raw.Pos, expr.Properties{DomainDependent: true})}, nil
	}
	if b.generatedOwner != nil {
		name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
		owner := b.generatedOwner
		qualified := len(raw.Name.Parts) == 1 || len(raw.Name.Parts) == 2 && raw.Name.Parts[0].Text == owner.Name() || len(raw.Name.Parts) == 3 && raw.Name.Parts[1].Text == owner.Name() && raw.Name.Parts[0].Text == b.queryNamespace.qualifiers[0].schema
		if qualified {
			if name == "tableoid" {
				ref := b.querySources[0].ref
				return tableOIDExpression{b.info(b.typ(sqltype.Oid).Usage(), raw.Pos, expr.Properties{RowDependent: true}), ref}, nil
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
		if _, whole := node.(rowExpression); whole {
			diagnostic := expr.ErrorAt("42P17", "cannot use whole-row variable in column generation expression", raw.Pos).(*sqlstate.Error)
			diagnostic.Detail = "This would cause the generated column to depend on its own value."
			return nil, diagnostic
		}
		if column, ok := node.(columnExpression); ok {
			if _, generated := owner.ColumnMeta.Generations[column.origin.Address.SubID]; generated {
				diagnostic := expr.ErrorAt("42P17", fmt.Sprintf(`cannot use generated column "%s" in column generation expression`, name), raw.Pos).(*sqlstate.Error)
				diagnostic.Detail = "A generated column cannot reference another generated column."
				return nil, diagnostic
			}
		}
		return node, nil
	}
	if b.checkConsumer && b.domainValueUsage == nil && len(raw.Name.Parts) > 0 {
		name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
		switch name {
		case "ctid", "xmin", "cmin", "xmax", "cmax":
			return nil, expr.ErrorAt("42P10", `system column "`+name+`" reference in check constraint is invalid`, raw.Pos)
		}
	}
	if b.returningAliases != nil {
		return b.returningColumn(raw)
	}
	if b.queryNamespace != nil {
		return b.queryColumn(raw)
	}
	if len(raw.Name.Parts) == 2 && !b.deleteTarget {
		return nil, expr.ErrorAt("0A000", "qualified column references are unsupported", raw.Pos)
	}
	if len(raw.Name.Parts) == 0 || len(raw.Name.Parts) > 2 {
		return nil, fmt.Errorf("stuntgres: column outside expression scope")
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	if b.relation == nil {
		return nil, expr.ErrorAt("42703", fmt.Sprintf(`column "%s" does not exist`, name), raw.Pos)
	}
	if len(raw.Name.Parts) == 2 {
		qualifier := raw.Name.Parts[0].Text
		visible := b.targetName
		if b.targetAlias != "" {
			visible = b.targetAlias
		}
		if qualifier != visible {
			if qualifier == b.targetName && b.targetAlias != "" {
				err := expr.ErrorAt("42P01", fmt.Sprintf(`invalid reference to FROM-clause entry for table "%s"`, qualifier), raw.Pos).(*sqlstate.Error)
				err.Hint = fmt.Sprintf(`Perhaps you meant to reference the table alias "%s".`, b.targetAlias)
				return nil, err
			}
			return nil, expr.ErrorAt("42P01", fmt.Sprintf(`missing FROM-clause entry for table "%s"`, qualifier), raw.Pos)
		}
	}
	i, ok := b.relation.Index[name]
	if !ok {
		if len(raw.Name.Parts) == 2 {
			return nil, expr.ErrorAt("42703", fmt.Sprintf("column %s.%s does not exist", raw.Name.Parts[0].Text, name), raw.Pos)
		}
		return nil, expr.ErrorAt("42703", fmt.Sprintf(`column "%s" does not exist`, name), raw.Pos)
	}
	return b.columnAt(i, raw.Pos), nil
}
func (b *expressionBinder) useColumn(i int) ids.ObjectRef {
	return b.useColumnOf(b.relation, i)
}

func (b *expressionBinder) useColumnOf(relation *catalog.Relation, i int) ids.ObjectRef {
	ref := b.ref
	ref.Address.SubID = relation.Attributes[i]
	b.dependency(ref, relation.Revision())
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
	b.info(relation.Columns[i].Usage, parser.Position{Offset: -1}, expr.Properties{})
	return ref
}
func (b *expressionBinder) columnAt(i int, pos parser.Position) expr.Expr {
	return b.columnAtOf(b.relation, i, pos)
}

func (b *expressionBinder) columnAtOf(relation *catalog.Relation, i int, pos parser.Position) expr.Expr {
	ref := b.useColumnOf(relation, i)
	column := columnExpression{Info: expr.Info{Usage: relation.Columns[i].Usage, Pos: pos, Property: expr.Properties{RowDependent: true}}, origin: ref, scope: 1, frame: 1, slot: i}
	if b.generatedOwner == nil {
		column = column.withGeneration(relation, i, 0)
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
	if slot, ok := raw.(parser.ParameterExpr); ok && slot.Number < 0 && -slot.Number <= len(b.selectedOperatorArgs) {
		node := b.selectedOperatorArgs[-slot.Number-1]
		if b.argumentOwner.active && !b.argumentOwner.found {
			// Selection analyzed this operand before the winning argument
			// owner existed. Preserve its lexical occurrence even if the
			// signature does not retain an executable argument.
			if nested := windowOccurrence(node); nested != nil && nested.frame == b.frameID() {
				b.argumentOwner.first, b.argumentOwner.found = nested.Pos, true
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
			clause := b.aggregateClause
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
	if node == nil && b.partitionConsumer != "" {
		switch r := raw.(type) {
		case parser.ColumnExpr:
			if b.partitionConsumer == "partition bound" {
				return nil, expr.ErrorAt("0A000", "cannot use column reference in partition bound expression", r.Pos)
			}
		case parser.IndexSubqueryExpr:
			return nil, b.partitionExpressionError("0A000", "cannot use subquery in "+b.partitionConsumer, r.Pos)
		case parser.SubqueryExpr:
			return nil, b.partitionExpressionError("0A000", "cannot use subquery in "+b.partitionConsumer, r.Pos)
		case parser.AggregateExpr:
			if r.Window == nil && r.Name != "row_number" && r.Name != "ntile" && !functions.WindowValueName(r.Name) && !functions.WindowOffsetName(r.Name) {
				for _, arg := range r.Args {
					if _, err := b.bind(arg, expressionPolicy{context: projectionContext}); err != nil {
						return nil, err
					}
				}
				return nil, b.partitionExpressionError("42803", "aggregate functions are not allowed in "+b.partitionContextPlural(), r.Pos)
			}
		}
	}
	if node == nil && b.functionProjection.matches(raw) {
		node = b.functionProjection.operand
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
			if b.indexPredicateConsumer {
				return nil, expr.ErrorAt("0A000", "cannot use subquery in index predicate", r.Pos)
			}
			return nil, expr.ErrorAt("0A000", "cannot use subquery in index expression", r.Pos)
		case parser.SubqueryExpr:
			if b.executeParameters {
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
			if warning != nil && b.op != nil && b.expected == nil {
				b.op.emitNotice(levelWarning, *warning)
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
			if r.Window == nil && b.recursiveFrames[b.frameID()] {
				return nil, expr.ErrorAt("42P19", "aggregate functions are not allowed in a recursive query's recursive term", r.Pos)
			}
			if b.executeParameters {
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
			if len(b.selectedWindowCallee.Parts) != 0 {
				// Consume before Signature binds any children. Nested calls
				// must retain their own lexical diagnostic spelling.
				diagnosticName = b.selectedWindowCallee
				b.selectedWindowCallee = parser.ObjectName{}
			}
			parentAggregates, parentWindows := b.aggregates, b.windowCalls
			defer func() {
				if bindError != nil {
					b.aggregates, b.windowCalls = parentAggregates, parentWindows
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
			parentOwner := b.argumentOwner
			b.argumentOwner = argumentWindowOwner{active: true}
			defer func() { b.argumentOwner = parentOwner }()
			if b.generatedOwner != nil {
				return nil, expr.ErrorAt("42803", "aggregate functions are not allowed in column generation expressions", r.Pos)
			}
			if b.indexPredicateConsumer {
				return nil, expr.ErrorAt("42803", "aggregate functions are not allowed in index predicates", r.Pos)
			}
			if b.indexConsumer {
				return nil, expr.ErrorAt("42803", "aggregate functions are not allowed in index expressions", r.Pos)
			}
			if b.aggregateClause == "VALUES" || b.aggregateClause == "INSERT VALUES" && (r.Name == "string_agg" || r.Name == "array_agg") {
				return nil, expr.ErrorAt("42803", "aggregate functions are not allowed in VALUES", r.Pos)
			}
			if b.checkConsumer {
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
			if b.executeParameters && r.Window != nil {
				return nil, expr.ErrorAt("42P20", "window functions are not allowed in EXECUTE parameters", r.Pos)
			}
			var err error
			node, err = b.bindFunction(r)
			if err != nil {
				return nil, err
			}
			switch inquiry := node.(type) {
			case extensionPathsExpression:
				b.catalogSnapshot = true
			case expr.SnapshotDemander:
				demand, _, _ := inquiry.SnapshotDemand(bareInquiryLiteralNull)
				b.catalogSnapshot = b.catalogSnapshot || demand
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
			if b.defaultConsumer {
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
			if b.checkLiterals != nil {
				if r.Number <= 0 || r.Number > len(b.checkLiterals) {
					return nil, expr.ErrorAt("42P02", fmt.Sprintf("there is no parameter $%d", r.Number), r.Pos)
				}
				node = b.checkLiterals[r.Number-1]
				break
			}
			if b.noParameters || b.defaultConsumer || r.Number <= 0 || b.closedRoutineFormals && r.Number > len(b.formalParameters) {
				return nil, expr.ErrorAt("42P02", fmt.Sprintf("there is no parameter $%d", r.Number), r.Pos)
			}
			u := policy.expected
			if established := b.updateInputParameters[r.Number]; established.Type != nil {
				u = established
			}
			retained := r.Number <= len(b.formalParameters) && b.formalParameters[r.Number-1].Type != nil
			if retained {
				u = b.formalParameters[r.Number-1]
			}
			// Retained parameter contracts keep their logical input type, but their
			// codec must come from the admitted catalog, including private generations.
			if b.expected != nil && r.Number <= len(b.expected.Parameters.Types) {
				u = b.expected.Parameters.Types[r.Number-1]
				retained = u.Type != nil
			}
			if retained {
				catalog := b.bindingCatalog()
				if b.expected != nil && statementCatalogType(u.Type) && b.expected.catalogOwner != nil &&
					(b.expected.catalogOwner != catalog.Owner() || b.expected.catalogEpoch != catalog.Epoch()) {
					return nil, sqlError("0A000", "cached plan must not change parameter type")
				}
				current, _ := catalog.LookupType(u.Type.ID())
				if u.Type.IsDeferredRecord() || u.Type.ID() == 2249 && u.Type.IsComposite() {
					// Anonymous shapes and deferred RECORD are formal occurrence
					// authorities, never catalog's lookup-only RECORD identity.
					current = b.parameters[r.Number].Type
				}
				if current == nil && statementCatalogType(u.Type) {
					return nil, sqlError("0A000", "cached plan must not change parameter type")
				}
				u.Type = current
				b.parameters[r.Number] = u
			}
			if u.Type == nil {
				u = b.parameters[r.Number]
			}
			if u.Type == nil {
				b.parameters[r.Number] = sqltype.Usage{}
				node = parameterExpression{expr.Info{Pos: r.Pos, Property: expr.Properties{ParameterDependent: true}}, r.Number}
			} else {
				if err := b.constrain(r.Number, u, r.Pos); err != nil {
					return nil, err
				}
				node = parameterExpression{b.info(u.Type.Usage(), r.Pos, expr.Properties{ParameterDependent: true}), r.Number}
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
			node = literalExpression{info, value}
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
					b.dependency(b.engine.catalog.Domain.Ref(auth.Catalog.Epoch(), address), descriptor.Revision())
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
			u, err := b.engine.resolveType(rawType, b.op)
			if err != nil {
				return nil, err
			}
			if r.RequiresDomain && !u.Type.IsDomain() {
				return nil, expr.ErrorAt("0A000", "typed literals are unsupported", rawType.Pos)
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
			if b.collectTemporalConstants() {
				for i := range b.temporals {
					if b.temporals[i].offset == r.Pos.Offset && b.temporals[i].typeID == u.Type.ID() {
						b.temporals[i].typed = true
						b.temporals[i].typedTypmod = u.Typmod
					}
				}
			}
			literalInfo := b.info(inputUsage, r.Pos, expr.Properties{})
			node = literalExpression{literalInfo, value}
			if inputUsage != u {
				plan, err := sqltype.PlanCoercion(inputUsage, u, sqltype.Explicit)
				if err != nil {
					return nil, err
				}
				info, err := b.coercionInfo(b.info(u, r.Pos, coercionProperties(node, plan)), node)
				if err != nil {
					return nil, err
				}
				node = coercionExpression{info, node, plan, sqltype.Explicit, false}
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
			info.CollationExplicit = r.Pos
			node = collationExpression{info, child}
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
	if b.functionProjection.matches(raw) && b.functionProjection.operand == nil && node.ResultUsage().Type != nil && node.ResultUsage().Type.Concrete() && node.ResultUsage().Type.BaseType().IsComposite() {
		b.functionProjection.operand = node
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
	if _, logical := node.(logicalExpression); policy.booleanConstruct != "" && !logical {
		returnsSet := false
		walkBoundExpression(node, func(child expr.Expr) {
			returnsSet = returnsSet || sourceReturnsSet(child)
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
		plan, err := sqltype.PlanCoercion(input.ResultUsage(), info.Usage, policy.mode)
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
		if plan.RequiresCatalogOutput() {
			b.regclassConcat = true
		}
		info.Property = coercionProperties(input, plan)
		if folded, ok, err := b.foldRegtypeConstant(input, plan, info); ok || err != nil {
			if err != nil {
				return nil, err
			}
			node = restoreDomainCastCollations(annotations, folded)
		} else {
			node = restoreDomainCastCollations(annotations, coercionExpression{info, input, plan, policy.mode, false})
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
	if !b.defaultConsumer && b.queryNamespace == nil && b.returningAliases == nil && b.ruleNamespace == nil {
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
	if b.jsonbRecordBase {
		annotation, err = b.jsonbRecordCastAnnotation(raw)
		if err != nil {
			return nil, err
		}
	}
	if outerPolicy.thresholdArrayInput {
		target, err = b.engine.resolveAnnotationTypeInput(annotation, b.op, unknownNull, false, false, false, true)
	} else if outerPolicy.stringInput {
		target, err = b.engine.resolveStringAnnotationType(annotation, b.op, unknownNull)
	} else {
		target, err = b.engine.resolveAnnotationType(annotation, b.op, unknownNull)
	}
	if err != nil {
		return nil, err
	}
	if b.jsonbRecordBase && raw.Syntax != parser.FunctionCast && target.Type.IsUserDefined() && target.Type.BaseType().IsComposite() {
		b.captureComposite(preparedCompositeKey{offset: raw.TypeName.Pos.Offset, kind: compositeRecordArgument}, target, nil)
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
	if unsupportedTarget && !b.datetimeZoneOperands {
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
		if b.checkLiterals != nil {
			break // bind resolves the captured literal without parameter inference.
		}
		if b.noParameters || b.defaultConsumer {
			return nil, expr.ErrorAt("42P02", fmt.Sprintf("there is no parameter $%d", child.Number), child.Pos)
		}
		if b.parameters[child.Number].Type == nil {
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
	plan, err := sqltype.PlanCoercion(source, target, sqltype.Explicit)
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
		if !source.Type.BaseType().IsQueryInt() && !target.Type.BaseType().IsQueryInt() && !sqltype.IsTransactionIdentifier(source.Type.BaseType().ID()) && !sqltype.IsTransactionIdentifier(target.Type.BaseType().ID()) && !b.datetimeZoneOperands && !sqltype.IsNetwork(source.Type.BaseType().ID()) && !sqltype.IsNetwork(target.Type.BaseType().ID()) && source.Type.BaseType().ID() != sqltype.Money && target.Type.BaseType().ID() != sqltype.Money && source.Type.BaseType().ID() != sqltype.Regtype && target.Type.BaseType().ID() != sqltype.Regtype && source.Type.BaseType().ID() != sqltype.Regrole && target.Type.BaseType().ID() != sqltype.Regrole && !source.Type.BaseType().IsEnum() && !target.Type.BaseType().IsEnum() && !source.Type.BaseType().IsComposite() && !target.Type.BaseType().IsComposite() && source.Type.BaseType().ID() != sqltype.JSON && target.Type.BaseType().ID() != sqltype.JSON && source.Type.BaseType().ID() != sqltype.JSONB && target.Type.BaseType().ID() != sqltype.JSONB && source.Type.BaseType().ID() != sqltype.JSONPath && target.Type.BaseType().ID() != sqltype.JSONPath && source.Type.ID() != sqltype.PgNodeTree && target.Type.ID() != sqltype.PgNodeTree && !(target.Type.BaseType().IsArray() && sqltype.StoredScalarCastType(source.Type.BaseType().ID())) && (!sqltype.BaselineCastType(source.Type.ID()) || !sqltype.BaselineCastType(target.Type.ID())) && !sqltype.OIDCastPair(source.Type.ID(), target.Type.ID()) && !sqltype.TemporalCastPair(source.Type.ID(), target.Type.ID()) {
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
	if plan.RequiresCatalogOutput() {
		b.regclassConcat = true
	}
	if source == target {
		return child, nil
	}
	info.Property = coercionProperties(child, plan)
	if folded, ok, err := b.foldRegtypeConstant(child, plan, info); ok || err != nil {
		return restoreDomainCastCollations(annotations, folded), err
	}
	return restoreDomainCastCollations(annotations, coercionExpression{info, child, plan, sqltype.Explicit, false}), nil
}

func domainCoercionInput(child expr.Expr, target sqltype.Usage) (expr.Expr, expr.Expr) {
	if !target.Type.IsDomain() || target.Collation.ID == 0 || child.ResultUsage().Type == target.Type {
		return child, nil
	}
	annotations := child
	for {
		annotation, ok := child.(collationExpression)
		if !ok {
			return child, annotations
		}
		child = annotation.child
	}
}

func restoreDomainCastCollations(original, coerced expr.Expr) expr.Expr {
	if coerced == nil {
		return nil
	}
	annotation, ok := original.(collationExpression)
	if !ok {
		return coerced
	}
	ref := annotation.Usage.Collation
	annotation.child = restoreDomainCastCollations(annotation.child, coerced)
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
	plan, err := sqltype.PlanCoercion(node.ResultUsage(), target, sqltype.Assignment)
	if err != nil {
		return nil, err
	}
	info := expr.Info{Usage: target, Pos: pos, Property: coercionProperties(node, plan)}
	return coercionExpression{info, node, plan, sqltype.Assignment, false}, nil
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
		coercion, ok := bound.(coercionExpression)
		if !ok {
			break
		}
		bound = coercion.child
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
	case parser.GroupingExpr:
		return "grouping", true
	case parser.AggregateExpr:
		return r.Name, true
	case parser.ConditionalExpr:
		return conditionalName(r), true
	case parser.CaseExpr:
		if r.Else != nil {
			var otherwise expr.Expr
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
	return b.expected == nil || b.expected.refreshTemporals || b.op != nil && b.expected.planOwner == b.op.session.token && b.expected.planGeneration != b.op.session.discardPlansGeneration
}

// Explicit typed literals use this only to discover their frozen modifier
// before resolving the raw declaration again. Scalar reconstruction must use
// captured with the complete contextual usage.
func (b *expressionBinder) captureAt(offset int) (temporalConstant, bool, error) {
	if b.expected != nil {
		captures := b.expected.temporals
		i := sort.Search(len(captures), func(i int) bool { return captures[i].offset >= offset })
		if i < len(captures) && captures[i].offset == offset {
			if b.expected.refreshTemporals && refreshableTemporalCapture(captures[i]) {
				return temporalConstant{}, false, nil
			}
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
			if b.expected.refreshTemporals && refreshableTemporalCapture(captures[i]) {
				return temporalConstant{}, false, nil
			}
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
		return node.(literalExpression).value, nil
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
			if hasEnumInput(u.Type) && (b.expected.catalogOwner != catalog.Owner() || b.expected.catalogEpoch != catalog.Epoch()) {
				actual := *b.expected
				actual.catalogOwner = catalog.Owner()
				actual.catalogEpoch = catalog.Epoch()
				if err := checkStatementSignature(*b.expected, actual); err != nil {
					return sqltype.Value{}, err
				}
				return sqltype.Value{}, ids.ErrCatalogReplaced
			}
			return c.value.Retag(u.Type)
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
			i := sort.Search(len(b.temporals), func(i int) bool { return b.temporals[i].offset >= pos.Offset })
			b.temporals = append(b.temporals, temporalConstant{})
			copy(b.temporals[i+1:], b.temporals[i:])
			b.temporals[i] = temporalConstant{offset: pos.Offset, typmod: u.Typmod, typeID: u.Type.ID(), value: value}
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
	if b.input.Regclass != nil && b.input.Regtype != nil && b.input.Regrole != nil && b.input.Operator != nil && b.input.Regproc != nil && b.input.Regcollation != nil && b.input.Regnamespace != nil && b.input.RegTS != nil {
		return nil
	}
	auth, err := b.bindingAuthorization()
	if err != nil {
		return err
	}
	return b.engine.captureElementInput(auth, b.op, &b.input)
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
		previous := b.datetimeZoneOperands
		b.datetimeZoneOperands = true
		operand, err = b.comparisonOperand(raw)
		b.datetimeZoneOperands = previous
	case functions.StringTextArgument, functions.StringOtherArgument:
		operand, err = b.stringOperand(raw, request.Intent == functions.StringTextArgument)
	case functions.RecordBaseArgument:
		previous := b.jsonbRecordBase
		b.jsonbRecordBase = true
		operand, err = b.stringOperand(raw, false)
		b.jsonbRecordBase = previous
	case functions.StringProjectionArgument, functions.ProjectionArgument:
		operand.node, err = b.bind(raw, expressionPolicy{context: projectionContext, stringInput: request.Intent == functions.StringProjectionArgument})
		operand.raw = raw
		if err == nil && unknownNullSyntax(raw) {
			if literal, ok := operand.node.(literalExpression); ok {
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
		if parameter, ok := argument.Expression.(parameterExpression); ok {
			record = b.parameters[parameter.number].Type
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
		plan, err := sqltype.PlanCoercion(node.ResultUsage(), info.Usage, sqltype.Implicit)
		if err != nil {
			return nil, err
		}
		info.Property = coercionProperties(node, plan)
		return coercionExpression{info, node, plan, sqltype.Implicit, false}, nil
	case functions.ConditionalArgument:
		return b.coerceConditional(operand, request.Target, request.Label)
	case functions.JSONFormattedArgument:
		node := argument.Expression
		info, err := b.coercionInfo(b.info(request.Target, node.SourcePosition(), coercionProperties(node, request.Plan)), node)
		if err != nil {
			return nil, err
		}
		return coercionExpression{info, node, request.Plan, sqltype.Explicit, false}, nil
	case functions.BareFormalArgument:
		node := argument.Expression
		if node == nil || !request.Planned {
			return nil, fmt.Errorf("stuntgres: invalid bare function argument coercion")
		}
		properties := coercionProperties(node, request.Plan)
		if request.PreserveProperties {
			properties = node.Properties()
		}
		return coercionExpression{b.info(request.Target, argument.Raw.SourcePosition(), properties), node, request.Plan, sqltype.Implicit, false}, nil
	case functions.AssignmentFormalArgument:
		if !b.selectedCallArguments || argument.Expression == nil || argument.Usage.Type == nil {
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
	plan := request.Plan
	var err error
	if !request.Planned {
		plan, err = sqltype.PlanCoercion(argument.Usage, request.Target, sqltype.Implicit)
		if err != nil {
			return nil, err
		}
	}
	properties := node.Properties()
	if !request.PreserveProperties {
		properties = coercionProperties(node, plan)
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
	return coercionExpression{info, node, plan, sqltype.Implicit, false}, nil
}

func (b *expressionBinder) comparisonOperandPolicy(raw parser.Expr, policy expressionPolicy) (comparisonOperand, error) {
	if literal, ok := raw.(parser.LiteralExpr); ok && (literal.Value.Kind == parser.StringValue || literal.Value.Kind == parser.NullValue) {
		return comparisonOperand{raw: raw}, nil
	}
	if annotation, ok := raw.(parser.CastExpr); ok && unknownNullSyntax(raw) {
		var target sqltype.Usage
		var err error
		if policy.thresholdArrayInput {
			target, err = b.engine.resolveAnnotationTypeInput(annotation.TypeName, b.op, true, false, false, false, true)
		} else if policy.stringInput {
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

func (b *expressionBinder) coerceParameter(p parameterExpression, u sqltype.Usage) (expr.Expr, error) {
	if existing := b.parameters[p.number]; existing.Type != nil && existing.Type != u.Type {
		d := expr.ErrorAt("42P08", fmt.Sprintf("inconsistent types deduced for parameter $%d", p.number), p.Pos).(*sqlstate.Error)
		d.Detail = fmt.Sprintf("%s versus %s", existing.Type.DiagnosticName(), u.Type.DiagnosticName())
		return nil, d
	}
	b.parameters[p.number] = u.Type.Usage()
	return parameterExpression{b.info(u.Type.Usage(), p.Pos, p.Property), p.number}, nil
}

func (b *expressionBinder) finalizeOperand(o comparisonOperand, typ *sqltype.Descriptor) (expr.Expr, error) {
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
	return comparisonExpression{info, l, r, predicate, name, namespace}, nil
}

type arithmeticExpression struct {
	expr.Info
	left, right expr.Expr
	provider    sqltype.Arithmetic
	operator    sqltype.ArithmeticOperator
	namespace   string
}

// Strict arithmetic may erase a target-set subtree when a physical constant
// NULL makes its result independent of producer demand. Ordinary row arithmetic
// and imported parameter NULLs retain their existing execution-owned behavior.
func arithmeticSetConstantNull(original, current arithmeticExpression) bool {
	return (bitwiseConstantNull(current.left) || bitwiseConstantNull(current.right)) &&
		(querySetDepth(original) != 0 || querySetDepth(current) != 0)
}

func (e arithmeticExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	c := evalContext(ctx)
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
	visible, err := b.builtinVisible(raw.Name, operatorLookup)
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
			plan, err := sqltype.PlanCoercion(n.ResultUsage(), target.Usage(), sqltype.Implicit)
			if err != nil {
				return nil, err
			}
			info, err := b.coercionRoutineInfo(b.info(target.Usage(), n.SourcePosition(), coercionProperties(n, plan)), n)
			if err != nil {
				return nil, err
			}
			n = coercionExpression{info, n, plan, sqltype.Implicit, false}
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
	return arithmeticExpression{info, l, r, provider, operator, namespace}, nil
}
