package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"time"
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
// parameters, input/output environments, statement host and catalog
// authority, plus per-command prepared CHECK/domain/generation programs and
// preparation flags. Statement executors and preparation set it when they
// build an evaluation; nested frames inherit it by copy. Every node reads
// Context, Input and Output; functions read it through the expr.Context
// capability methods.
type evalStatement struct {
	Context    context.Context
	Parameters []sqltype.Value
	host       execHost
	Input      sqltype.InputEnvironment
	Output     sqltype.OutputEnvironment
	catalog    catalog.Read // Current imported request/view authority for local lowering.
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
	routineAuthority   admissionAuthority // Borrowed local serial DDL admission only; never retained.
	routineRecipe      bool               // Distinguishes an empty owning manifest from the statement manifest.
	routineGraft       bool               // Local ancestry phase only; nil user nodes resume the statement manifest.
	routineChecks      []catalog.ExpressionDependency
	routineReferences  []ids.ObjectRef
	routineConsumer    *catalog.Relation
	routineProgram     admissionProgram // Borrowed serial preparation footprint, never row-owned.
	routineBodyContext routineBodyContext
	domainPhase        ids.CommandIdentity // Existing transaction/command identity, not a permission result.
}

func (c *EvalContext) FunctionContext() context.Context {
	if c == nil {
		return nil
	}
	return c.Context
}

func (c *EvalContext) FunctionArrayRandomOwner() (expr.RandomOwner, error) {
	host := c.liveHost()
	if host == nil {
		return nil, fmt.Errorf("stuntgres: missing array random session scope")
	}
	return host.FunctionArrayRandomOwner()
}

func (c *EvalContext) FunctionInput() sqltype.InputEnvironment { return c.Input }

func (c *EvalContext) FunctionOutput() sqltype.OutputEnvironment { return c.Output }

func (c *EvalContext) FunctionOverlapsConstant(node expr.Expr) bool {
	return overlapsExecutionConstant(node, c)
}

func (c *EvalContext) FunctionLiteralNull(node expr.Expr) bool { return randomLiteralNull(node) }

func (c *EvalContext) WholeRowNullPresence(node expr.Expr) (handled, absent bool, err error) {
	row, ok := node.(plan.RowExpr)
	if !ok || row.WholeNames == nil {
		return false, false, nil
	}
	if row.Usage.Type == nil || !row.Usage.Type.Concrete() || !row.Usage.Type.BaseType().IsComposite() || len(row.Fields) != row.Usage.Type.BaseType().CompositeLen() {
		return true, false, fmt.Errorf("stuntgres: invalid null-count argument type")
	}
	absent, err = c.RowAbsent(row)
	return true, absent, err
}

func (c *EvalContext) FunctionJSONOutput() sqltype.OutputEnvironment { return c.presentedOutput() }

func (c *EvalContext) FunctionStringOutput() sqltype.OutputEnvironment { return c.presentedOutput() }

func (c *EvalContext) FunctionFormatOutput() sqltype.OutputEnvironment { return c.presentedOutput() }

func (c *EvalContext) FunctionBinaryConversion(source, destination int) (*sqltype.EncodingConversionHandler, error) {
	host := c.liveHost()
	if host == nil {
		return nil, fmt.Errorf("stuntgres: missing conversion execution snapshot")
	}
	return host.FunctionBinaryConversion(source, destination)
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

// frameRow runs the node prologue and returns the evaluation frame that owns
// (scope, frame), after checking that slot holds a value valid for usage.
func (c *EvalContext) frameRow(scope, frame uint32, slot int, usage sqltype.Usage) (*EvalContext, error) {
	if err := evaluationContext(c); err != nil {
		return nil, err
	}
	for c != nil && (c.Scope != scope || c.Frame != frame) {
		c = c.Outer
	}
	if c == nil || slot < 0 || slot >= len(c.Row) {
		return nil, fmt.Errorf("stuntgres: invalid expression row frame")
	}
	v := c.Row[slot]
	if !v.ValidResult(usage) {
		return nil, fmt.Errorf("stuntgres: invalid expression column type")
	}
	return c, nil
}

func (c *EvalContext) FrameValue(scope, frame uint32, slot int, usage sqltype.Usage) (sqltype.Value, error) {
	row, err := c.frameRow(scope, frame, slot, usage)
	if err != nil {
		return sqltype.Value{}, err
	}
	return row.Row[slot], nil
}

func (c *EvalContext) EvalColumn(e plan.ColumnExpr) (sqltype.Value, error) {
	row, err := c.frameRow(e.Scope, e.Frame, e.Slot, e.Usage)
	if err != nil {
		return sqltype.Value{}, err
	}
	if e.MergeSource && !row.mergeSourcePresent {
		return sqltype.ResultNull(e.Usage), nil
	}
	if e.Virtual == nil {
		return row.Row[e.Slot], nil
	}
	if e.QueryOccurrence != 0 {
		absent, err := row.queryOperandAbsent(e.QueryOccurrence - 1)
		if err != nil {
			return sqltype.Value{}, err
		}
		if absent {
			return sqltype.ResultNull(e.Usage), nil
		}
	}
	if e.RowStart < 0 || e.RowWidth <= 0 || e.RowStart > len(row.Row)-e.RowWidth {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid virtual expression row window")
	}
	local := *row
	local.Context = c.Context
	local.Scope, local.Frame = 1, 1
	local.Row = row.Row[e.RowStart : e.RowStart+e.RowWidth]
	recipe := e.Virtual
	if e.VirtualOwner != nil {
		selectVirtualGenerationRecipe(&local, e.VirtualOwner, e.VirtualOwner.ColumnMeta.Generations[e.VirtualAttribute])
	} else if e.Origin != (ids.ObjectRef{}) {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing virtual generation owner")
	}
	if e.QueryOccurrence != 0 {
		source, index, err := row.queryOperandSource(e.QueryOccurrence - 1)
		if err != nil {
			return sqltype.Value{}, err
		}
		local.physicalTable = source.physicalRef(index)
		if source.view != nil && e.Origin.Address.SubID != 0 {
			var current []sqltype.Value
			if row.queryCurrentRows != nil {
				if source.occurrence < 0 || source.occurrence >= len(row.queryCurrentRows) || row.queryCurrentRows[source.occurrence] == nil {
					return sqltype.Value{}, fmt.Errorf("stuntgres: missing virtual recheck occurrence")
				}
				current = row.queryCurrentRows[source.occurrence]
			}
			recipe, local.Row, err = local.columnCapturedGeneration(e, source, index, current)
			if err != nil {
				return sqltype.Value{}, err
			}
		}
	}
	return recipe.Eval(&local)
}

func (c *EvalContext) ParameterValue(number int, usage sqltype.Usage) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	if number <= 0 || number > len(c.Parameters) {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid expression parameter frame")
	}
	v := c.Parameters[number-1]
	if usage.Type == nil || !usage.Type.Concrete() || v.Type() != usage.Type {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid expression parameter type")
	}
	return v, nil
}

// queryAuthority is a borrowed, statement-admission-only slot. Captured query
// trees share it; clearing it never changes their immutable data or frames.
// Session transaction admission serializes internal access. This slot must not
// implement context.Context: callbacks may retain a context after admission ends.
type queryAuthority struct {
	host       execHost
	expression *cursorExpressionData
}

type cursorExpressionData struct {
	identity       identitySnapshot
	statementStart time.Time
}

// liveHost is the runtime's only route to statement, session and engine
// state. It returns an untyped nil when no statement scope is attached.
func (c *EvalContext) liveHost() execHost {
	if c == nil {
		return nil
	}
	if c.query != nil {
		return c.query.source.liveHost()
	}
	return c.host
}

func (s *querySource) liveHost() execHost {
	if s == nil {
		return nil
	}
	if s.authority != nil {
		return s.authority.host
	}
	return s.host // Standalone internal query fixtures have no admission slot.
}

// presentation formats reg* and aclitem output from the statement snapshot,
// or reports the missing snapshot when the record is detached.
func (c *EvalContext) presentation() presentationFormatters {
	if host := c.liveHost(); host != nil {
		return host
	}
	return detachedPresentation{}
}

func (c *EvalContext) setContext(ctx context.Context) {
	if c.query != nil && c.query.executionContext != nil {
		c.Context = c.query.executionContext
	} else if ctx == nil && c.query != nil && c.query.source != nil && c.query.source.authority != nil && c.query.source.authority.host != nil {
		// Initial evaluators may be constructed without a pull context. Capture
		// the admitted request itself, never the mutable authority that owns it.
		c.Context = c.query.source.authority.host.Context()
	} else {
		c.Context = ctx
	}
}
