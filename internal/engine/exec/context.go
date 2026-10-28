package exec

import (
	"context"
	"fmt"
	"time"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"stuntgres/internal/workadmission"
)

// Context is the per-evaluation state that engine expression nodes recover
// from expr.Context. It is three embedded groups, so every field is still a
// promoted selector (c.Row, c.Context, c.routineChecks). Copying an
// Context (x := *c, State.evaluation, Stage) copies all three
// groups by value, exactly as before the split.
type Context struct {
	EvalFrame
	EvalStatement
	EvalAdmission
	castElement sqltype.Value
	caseTest    sqltype.Value
}

// EvalFrame is the current row frame: scope/frame numbers, the current and old
// rows, the outer frame chain, query occurrence/provenance and presence slots,
// MERGE/RETURNING row state, the active domain value and the UPDATE output
// tuple. Query openers, row producers and DML executors write it per row or
// per nested frame; frame nodes (column, group, grouping, tableoid, RETURNING,
// MERGE action, cached ORDER BY, window, domain value, UPDATE output) and
// subqueries read it.
type EvalFrame struct {
	Scope, Frame                       uint32
	Row                                []sqltype.Value
	OldRow                             []sqltype.Value
	MergeReturning, MergeSourcePresent bool
	MergeAction                        parser.MergeAction
	OrderValues                        []sqltype.Value
	// Reused by the current UPDATE execution; nested owners save/restore it.
	UpdateOutputScratch []sqltype.Value
	UpdateOutputs       []sqltype.Value
	UpdateOutputsReady  bool
	Outer               *Context
	Query               *State
	QueryOccurrences    []int
	QueryProvenance     []int
	queryRowSource      *Source
	// Borrowed full logical tuples only while rechecking locked occurrences.
	QueryCurrentRows  [][]sqltype.Value
	PhysicalTable     ids.ObjectRef // Existing generated recipe's containing physical row.
	PresenceSlots     []int
	Presence          []bool
	DomainValue       sqltype.Value
	DomainFrameUsage  sqltype.Usage
	DomainValueActive bool
}

// EvalStatement is the statement runtime: cancellation context, bound
// parameters, input/output environments, statement host and catalog
// authority, plus per-command prepared CHECK/domain/generation programs and
// preparation flags. Statement executors and preparation set it when they
// build an evaluation; nested frames inherit it by copy. Every node reads
// Context, Input and Output; functions read it through the expr.Context
// capability methods.
type EvalStatement struct {
	Context    context.Context
	Parameters []sqltype.Value
	Host       Host
	Input      sqltype.InputEnvironment
	Output     sqltype.OutputEnvironment
	Catalog    catalog.Read // Current imported request/view authority for local lowering.
	// Stored CHECK syntax stays immutable; command-private expressions and
	// consumed actor phases belong to the live statement, never catalog nodes.
	PreparedChecks       map[*catalog.Relation]CheckProgram
	PreparedDomainChecks map[*catalog.Type]DomainCheckProgram
	PreparedGenerations  map[*catalog.Relation][]expr.Expr // First actual tuple prepares executable recipes, not permission results.
	PGRegex              sqltype.PGRegexExecution
	FoldCaptures         *FoldCaptureCandidate
	FoldOrigin           FoldOrigin
	// Preparation admits closed immutable scalar operators for plain SELECTs
	// and rule programs, without widening parameter or row-dependent demand.
	PlanStaticOperators bool
	// Separate rule-program permission: actual supplied Param leaves may become
	// typed constants during preparation. Ordinary imported-param barriers stay.
	SpecializeRuleParameters bool
	// Borrowed only while a PL assignment projection evaluates its child.
	// Domain VALUE frames suppress propagation to their SQL CHECK routines.
	PLAssignmentPlanning bool
	// Requested only by a private PL query-preflight copy for its diagnostic
	// owner. Ordinary preflight preserves the provider's original error API.
	PLPreflightOrigin bool
}

// EvalAdmission is routine admission state for local serial DDL/recipe
// preparation (CHECK, generated, index, domain and routine bodies). Admission
// copies set it on a local Context; the statement's live context leaves it
// zero. Recipe-owned manifests are borrowed only by a local admission copy and
// never replace the live scope's authority or another recipe's owner.
type EvalAdmission struct {
	RoutineAuthority   AdmissionAuthority          // Borrowed local serial DDL admission only; never retained.
	RoutineRecipe      bool                        // Distinguishes an empty owning manifest from the statement manifest.
	RoutineGraft       bool                        // Local ancestry phase only; nil user nodes resume the statement manifest.
	RoutineGraftSource expr.RoutineRecipeReference // Exact view or rule-action ancestry; never a permission cache.
	RoutineChecks      []catalog.ExpressionDependency
	RoutineReferences  []ids.ObjectRef
	RoutineConsumer    *catalog.Relation
	RoutineProgram     *RoutineProgram // Borrowed serial preparation footprint, never row-owned.
	RoutineBodyContext RoutineBodyContext
	DomainPhase        ids.CommandIdentity // Existing transaction/command identity, not a permission result.
}

func (c *Context) FunctionContext() context.Context {
	if c == nil {
		return nil
	}
	return c.Context
}

func (c *Context) FunctionArrayRandomOwner() (expr.RandomOwner, error) {
	host := c.LiveHost()
	if host == nil {
		return nil, fmt.Errorf("stuntgres: missing array random session scope")
	}
	return host.FunctionArrayRandomOwner()
}

func (c *Context) FunctionInput() sqltype.InputEnvironment {
	input := c.Input
	if host := c.LiveHost(); host != nil {
		current, _ := host.Environments()
		// Retained stages keep their other captured presentation. Runtime XML
		// character conversion alone follows the current FETCH/Execute admission.
		input.XMLMode = current.XMLMode
	}
	return input
}

func (c *Context) FunctionOutput() sqltype.OutputEnvironment { return c.Output }

func (c *Context) FunctionOverlapsConstant(node expr.Expr) bool {
	return OverlapsExecutionConstant(node, c)
}

func (c *Context) FunctionLiteralNull(node expr.Expr) bool { return RandomLiteralNull(node) }

func (c *Context) WholeRowNullPresence(node expr.Expr) (handled, absent bool, err error) {
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

func (c *Context) FunctionJSONOutput() sqltype.OutputEnvironment { return c.presentedOutput() }

func (c *Context) FunctionStringOutput() sqltype.OutputEnvironment { return c.presentedOutput() }

func (c *Context) FunctionFormatOutput() sqltype.OutputEnvironment { return c.presentedOutput() }

func (c *Context) FunctionBinaryConversion(source, destination int) (*sqltype.EncodingConversionHandler, error) {
	host := c.LiveHost()
	if host == nil {
		return nil, fmt.Errorf("stuntgres: missing conversion execution snapshot")
	}
	return host.FunctionBinaryConversion(source, destination)
}

func (c *Context) FunctionPropertiesMatch(properties expr.Properties, rule expr.IntrinsicPropertyRule) bool {
	return expr.PropertiesMatch(properties, rule)
}

func EvaluationContext(c *Context) error {
	if c == nil || c.Context == nil {
		return fmt.Errorf("stuntgres: missing expression context")
	}
	if c.DomainValueActive {
		c.Input.DomainChecks = c
	}
	return c.Context.Err()
}

// frameRow runs the node prologue and returns the evaluation frame that owns
// (scope, frame), after checking that slot holds a value valid for usage.
func (c *Context) frameRow(scope, frame uint32, slot int, usage sqltype.Usage) (*Context, error) {
	if err := EvaluationContext(c); err != nil {
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

func (c *Context) FrameValue(scope, frame uint32, slot int, usage sqltype.Usage) (sqltype.Value, error) {
	row, err := c.frameRow(scope, frame, slot, usage)
	if err != nil {
		return sqltype.Value{}, err
	}
	return row.Row[slot], nil
}

func (c *Context) EvalColumn(e plan.ColumnExpr) (sqltype.Value, error) {
	row, err := c.frameRow(e.Scope, e.Frame, e.Slot, e.Usage)
	if err != nil {
		return sqltype.Value{}, err
	}
	if e.MergeSource && !row.MergeSourcePresent {
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
		SelectVirtualGenerationRecipe(&local, e.VirtualOwner, e.VirtualOwner.ColumnMeta.Generations[e.VirtualAttribute])
	} else if e.Origin != (ids.ObjectRef{}) {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing virtual generation owner")
	}
	if e.QueryOccurrence != 0 {
		source, index, err := row.queryOperandSource(e.QueryOccurrence - 1)
		if err != nil {
			return sqltype.Value{}, err
		}
		local.PhysicalTable = source.PhysicalRef(index)
		if source.View != nil && e.Origin.Address.SubID != 0 {
			var current []sqltype.Value
			if row.QueryCurrentRows != nil {
				if source.Occurrence < 0 || source.Occurrence >= len(row.QueryCurrentRows) || row.QueryCurrentRows[source.Occurrence] == nil {
					return sqltype.Value{}, fmt.Errorf("stuntgres: missing virtual recheck occurrence")
				}
				current = row.QueryCurrentRows[source.Occurrence]
			}
			recipe, local.Row, err = local.ColumnCapturedGeneration(e, source, index, current)
			if err != nil {
				return sqltype.Value{}, err
			}
		}
	}
	return recipe.Eval(&local)
}

func (c *Context) ParameterValue(number int, usage sqltype.Usage) (sqltype.Value, error) {
	if err := EvaluationContext(c); err != nil {
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

// Authority is a borrowed, statement-admission-only slot. Captured query
// trees share it; clearing it never changes their immutable data or frames.
// Session transaction admission serializes internal access. This slot must not
// implement context.Context: callbacks may retain a context after admission ends.
type Authority struct {
	Host       Host
	Expression *CursorExpressionData
}

type CursorExpressionData struct {
	Identity       IdentitySnapshot
	StatementStart time.Time
}

// LiveHost is the runtime's only route to statement, session and engine
// state. It returns an untyped nil when no statement scope is attached.
func (c *Context) LiveHost() Host {
	if c == nil {
		return nil
	}
	if c.Query != nil {
		return c.Query.Source.LiveHost()
	}
	return c.Host
}

func (s *Source) LiveHost() Host {
	if s == nil {
		return nil
	}
	if s.Authority != nil {
		return s.Authority.Host
	}
	return s.Host // Standalone internal query fixtures have no admission slot.
}

// Presentation formats reg* and aclitem output from the statement snapshot,
// or reports the missing snapshot when the record is detached.
func (c *Context) Presentation() PresentationFormatters {
	if host := c.LiveHost(); host != nil {
		return host
	}
	return DetachedPresentation{}
}

func (c *Context) setContext(ctx context.Context) {
	runtime := ctx
	if c.Query != nil && c.Query.ExecutionContext != nil {
		c.Context = c.Query.ExecutionContext
	} else if ctx == nil && c.Query != nil && c.Query.Source != nil && c.Query.Source.Authority != nil && c.Query.Source.Authority.Host != nil {
		// Initial evaluators may be constructed without a pull context. Capture
		// the admitted request itself, never the mutable authority that owns it.
		c.Context = c.Query.Source.Authority.Host.Context()
	} else {
		c.Context = ctx
	}
	if c.Context != nil {
		c.Context = workadmission.Rebase(c.Context, runtime)
	}
}
