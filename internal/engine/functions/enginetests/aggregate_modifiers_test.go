package enginetests_test

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestAggregateModifiersTupleDistinct(t *testing.T) {
	b := enginetest.ExpressionBinder(t, context.Background(), engine.New(), sqltype.InputEnvironment{}, nil)
	typ := b.Type(sqltype.Text)
	family, err := sqltype.BindGrouping(typ.Usage())
	if err != nil {
		t.Fatal(err)
	}
	grouping := []plan.Grouping{{Grouping: family}, {Grouping: family}}
	a, comma, slash := sqltype.String(typ, "a"), sqltype.String(typ, ","), sqltype.String(typ, "/")
	null := sqltype.Null(typ)
	var state exec.AggregateDistinctState
	for i, tc := range []struct {
		tuple []sqltype.Value
		admit bool
	}{
		{[]sqltype.Value{a, comma}, true}, {[]sqltype.Value{a, comma}, false},
		{[]sqltype.Value{a, slash}, true}, {[]sqltype.Value{a, null}, true}, {[]sqltype.Value{a, null}, false},
		{[]sqltype.Value{null, comma}, true}, {[]sqltype.Value{null, comma}, false},
	} {
		admit, err := state.Admit(context.Background(), grouping, tc.tuple)
		if err != nil || admit != tc.admit {
			t.Fatal(i, admit, err)
		}
	}
	// Force unequal tuples into the same hash candidate list. Exact family
	// equality, rather than the bucket hit, decides admission.
	tuple := []sqltype.Value{sqltype.String(typ, "b"), comma}
	hash := uint64(14695981039346656037)
	for i, f := range grouping {
		h, err := f.Hash(context.Background(), tuple[i])
		if err != nil {
			t.Fatal(err)
		}
		hash = (hash ^ h) * 1099511628211
	}
	state.Buckets[hash] = []int{0}
	admit, err := state.Admit(context.Background(), grouping, tuple)
	if err != nil || !admit || len(state.Tuples) != 10 {
		t.Fatal(admit, err, state.Tuples)
	}
	admit, err = state.Admit(context.Background(), grouping, tuple)
	if err != nil || admit {
		t.Fatal(admit, err)
	}
	var other exec.AggregateDistinctState
	if admit, err := other.Admit(context.Background(), grouping, []sqltype.Value{a, comma}); err != nil || !admit {
		t.Fatal("cross-call/group/execution state", admit, err)
	}
	if len(state.Tuples) != 10 {
		t.Fatal("independent state mutated retained tuples")
	}
}

func TestAggregateModifiersBindingIdentity(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE SEQUENCE am_unit_sequence")
	b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	b.Clause.AggregateClause = "SELECT"
	for i, tc := range []struct {
		call string
		slot int
	}{
		{"count(1)", 0}, {"count(ALL 1)", 0}, {"count(DISTINCT 1)", 1},
		{"count(1) FILTER(WHERE TRUE)", 2}, {"count(1) FILTER(WHERE FALSE)", 3},
		{"count(1 ORDER BY 1)", 4}, {"count(1 ORDER BY 1 DESC)", 5},
		{"count(1 ORDER BY 1 NULLS FIRST)", 6}, {"count(1 ORDER BY 1)", 4},
		{"count(1) FILTER(WHERE nextval('am_unit_sequence')>0)", 7},
		{"count(1) FILTER(WHERE nextval('am_unit_sequence')>0)", 8},
		{"count(1 ORDER BY nextval('am_unit_sequence'))", 9},
		{"count(1 ORDER BY nextval('am_unit_sequence'))", 10},
	} {
		raw := enginetest.Statement(t, "SELECT "+tc.call).(*parser.Select).Targets[0]
		expr, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err != nil || expr.(plan.AggregateExpr).Slot != tc.slot {
			t.Fatal(i, expr, err)
		}
	}
	raw := enginetest.Statement(t, "SELECT count(DISTINCT nextval('am_unit_sequence') ORDER BY nextval('am_unit_sequence'),nextval('am_unit_sequence'))").(*parser.Select).Targets[0]
	bound, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
	if err != nil {
		t.Fatal(err)
	}
	call := b.Grouping.Aggregates[bound.(plan.AggregateExpr).Slot]
	if call.Order[0].Slot != 0 || call.Order[1].Slot != 0 || bound.Properties().Volatility != expr.Volatile {
		t.Fatal(call, bound.Properties())
	}
	altered := call
	altered.Provider = functions.NewCountProvider(b.Type(sqltype.Int4).Usage())
	if b.SameAggregateCall(call, altered) {
		t.Fatal("result Usage omitted from identity")
	}
}

func TestAggregateModifiersParameterTargets(t *testing.T) {
	for _, query := range []string{
		"SELECT count($1 ORDER BY $1)",
		"SELECT count(DISTINCT $1 ORDER BY $1)",
	} {
		b := enginetest.ExpressionBinder(t, context.Background(), engine.New(), sqltype.InputEnvironment{}, nil)
		b.Clause.AggregateClause = "SELECT"
		raw := enginetest.Statement(t, query).(*parser.Select).Targets[0]
		expression, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err != nil {
			t.Fatal(query, err)
		}
		parameters, err := b.Finish()
		if err != nil {
			t.Fatal(query, err)
		}
		call := b.Grouping.Aggregates[expression.(plan.AggregateExpr).Slot]
		if parameters.Count != 1 || parameters.Types[0].Type.ID() != sqltype.Text ||
			call.Arguments[0].ResultUsage().Type.ID() != sqltype.Text ||
			call.Order[0].Slot != 0 || call.Order[0].Cache != 0 || len(b.Params.AnyParameters) != 0 {
			t.Fatal(query, parameters, call, b.Params.AnyParameters)
		}
	}
}

// Observe real family entry, not pipeline Err-call counts or fake providers.
type aggregateModifierFamilyCancelContext struct {
	context.Context
	cancel  context.CancelFunc
	target  string
	entered bool
}

func (c *aggregateModifierFamilyCancelContext) Err() error {
	if !c.entered {
		var pcs [32]uintptr
		n := runtime.Callers(2, pcs[:])
		frames := runtime.CallersFrames(pcs[:n])
		for {
			frame, more := frames.Next()
			if strings.HasSuffix(frame.Function, c.target) {
				c.entered = true
				c.cancel()
				break
			}
			if !more {
				break
			}
		}
	}
	return c.Context.Err()
}

func TestAggregateModifiersFamilyCancellation(t *testing.T) {
	b := enginetest.ExpressionBinder(t, context.Background(), engine.New(), sqltype.InputEnvironment{}, nil)
	typ := b.Type(sqltype.Text)
	family, err := sqltype.BindGrouping(typ.Usage())
	if err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{".Grouping.PrepareKey", ".Grouping.Hash", ".Grouping.Equal"} {
		t.Run(phase, func(t *testing.T) {
			var state exec.AggregateDistinctState
			grouping := []plan.Grouping{{Grouping: family}}
			value := sqltype.String(typ, strings.Repeat("x", 8192))
			if _, err := state.Admit(context.Background(), grouping, []sqltype.Value{value}); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			probe := &aggregateModifierFamilyCancelContext{Context: ctx, cancel: cancel, target: phase}
			admit, err := state.Admit(probe, grouping, []sqltype.Value{value})
			if !probe.entered || !errors.Is(err, context.Canceled) || admit || len(state.Tuples) != 1 {
				t.Fatal(phase, probe.entered, admit, err, state.Tuples)
			}
		})
	}
}

func TestAggregateModifiersEvaluationPublication(t *testing.T) {
	for _, phase := range []string{"success", "filter", "key", "hash", "equality", "sort", "replay", "finalize"} {
		t.Run(phase, func(t *testing.T) {
			_, query, source := queryCoreFixture(t, "SELECT count(DISTINCT v ORDER BY v) FILTER(WHERE TRUE) FROM t")
			node := query.Root.(*plan.ProjectNode).Child.(*plan.AggregateNode)
			call := &node.Calls[0]
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			filters, values, keys, advances, finalizes := 0, 0, 0, 0, 0
			original := call.Arguments[0]
			call.Arguments[0] = queryProbe(original.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) { values++; return original.Eval(c) })
			boolType := call.Filter.ResultUsage().Type
			call.Filter = queryProbe(boolType.Usage(), func(c *exec.Context) (sqltype.Value, error) {
				filters++
				if phase == "filter" {
					cancel()
				}
				return sqltype.Boolean(boolType, filters != 2), nil
			})
			// A matched order expression must never be evaluated separately.
			call.Order[0].Expression = queryProbe(original.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) {
				keys++
				if phase == "key" {
					cancel()
				}
				return original.Eval(c)
			})
			if phase == "key" {
				call.Order[0].Slot = -1
			}
			provider := call.Provider
			call.Provider = aggregateProbeProvider{AggregateProvider: provider, advance: func(ctx context.Context, s any, v, d sqltype.Value) error {
				advances++
				err := provider.Advance(ctx, s, []sqltype.Value{v, d}, nil)
				if phase == "replay" {
					cancel()
				}
				return err
			}, finalize: func(ctx context.Context, s any) (sqltype.Value, error) {
				finalizes++
				v, err := provider.Finalize(ctx, s)
				if phase == "finalize" {
					cancel()
				}
				return v, err
			}}
			var execution context.Context = ctx
			var familyProbe *aggregateModifierFamilyCancelContext
			if phase == "hash" || phase == "equality" {
				target := ".Grouping.Hash"
				if phase == "equality" {
					target = ".Grouping.Equal"
				}
				familyProbe = &aggregateModifierFamilyCancelContext{Context: ctx, cancel: cancel, target: target}
				execution = familyProbe
			}
			var sortProbe *aggregateSortCancelContext
			if phase == "sort" {
				sortProbe = &aggregateSortCancelContext{Context: ctx, cancel: cancel, aggregateOrdering: true}
				execution = sortProbe
			}
			rows, count, err := exec.Finalize(execution, query.Root, source, nil)
			if phase == "success" {
				if err != nil || count != 1 || filters != 4 || values != 3 || keys != 0 || finalizes != 1 {
					t.Fatal(rows, count, err, filters, values, keys, advances, finalizes)
				}
				out := make([]sqltype.Value, 1)
				present, err := rows.Next(ctx, out)
				// The retained fixture values are (1,NULL,2,1); row2 is rejected.
				value, _ := out[0].Integer()
				if err != nil || !present || value != 2 {
					t.Fatal(out, present, err)
				}
				if filters != 4 || values != 3 || keys != 0 {
					t.Fatal("Next evaluated aggregate inputs")
				}
				rows.Close()
			} else {
				if rows != nil || count != 0 || !errors.Is(err, context.Canceled) {
					t.Fatal("partial publication", rows, count, err)
				}
				if familyProbe != nil && !familyProbe.entered {
					t.Fatal("typed family not entered")
				}
				if sortProbe != nil && !sortProbe.comparisonEntered {
					t.Fatal("sort comparator not entered")
				}
			}
			if source.View != nil || source.Anonymous != nil {
				t.Fatal("source retained", source)
			}
		})
	}
}

func TestAggregateModifiersImmutableConsumers(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT count(DISTINCT v ORDER BY v) FILTER(WHERE f) FROM t")
	defer source.Close()
	node := query.Root.(*plan.ProjectNode).Child.(*plan.AggregateNode)
	original := node.Calls[0]
	rewritten, err := plan.RewriteQueryExpressions(node, func(bound expr.Expr) (expr.Expr, bool, error) {
		column, ok := bound.(plan.ColumnExpr)
		if !ok || column.Slot != 2 {
			return bound, false, nil
		}
		return plan.LiteralExpr{Info: expr.Info{Usage: column.ResultUsage()}, Value: sqltype.Boolean(column.ResultUsage().Type, false)}, true, nil
	})
	if err != nil || rewritten == node {
		t.Fatal("FILTER rewrite not copied", rewritten, err)
	}
	changed := rewritten.(*plan.AggregateNode).Calls[0]
	if _, ok := changed.Filter.(plan.LiteralExpr); !ok {
		t.Fatal("FILTER rewrite lost")
	}
	if _, ok := node.Calls[0].Filter.(plan.ColumnExpr); !ok {
		t.Fatal("original FILTER mutated")
	}
	if changed.Order[0].Slot != original.Order[0].Slot || changed.Order[0].Cache != original.Order[0].Cache || changed.StageWidth != original.StageWidth {
		t.Fatal("target reuse lost across rewrite")
	}
	folded, err := view.PreflightQuery(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}, node)
	if err != nil || folded != node {
		t.Fatal("unchanged preflight mutated immutable plan", folded, err)
	}
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, typ := range sqltype.Builtins() {
		types[typ.ID()] = typ
	}
	remapped, err := stored.RemapViewQuery(engine.New().CatalogRead(), node, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, func(old *sqltype.Descriptor) *sqltype.Descriptor { return types[old.ID()] })
	if err != nil || remapped == node {
		t.Fatal(remapped, err)
	}
	call := remapped.(*plan.AggregateNode).Calls[0]
	if call.Filter.ResultUsage().Type == original.Filter.ResultUsage().Type || call.Arguments[0].ResultUsage().Type == original.Arguments[0].ResultUsage().Type {
		t.Fatal("modifier descriptor ownership not remapped")
	}
	value, _ := sqltype.Integer(call.Arguments[0].ResultUsage().Type, 1)
	if _, err := call.Distinct[0].Hash(context.Background(), value); err != nil {
		t.Fatal("remapped grouping retains old descriptor", err)
	}
	if _, err := original.Distinct[0].Hash(context.Background(), value); err == nil {
		t.Fatal("original grouping mutated")
	}
	if sign, err := call.Order[0].Comparison.Compare(context.Background(), value, value, sqltype.InputEnvironment{}); err != nil || sign != 0 {
		t.Fatal("remapped comparison retains old descriptor", sign, err)
	}
	if original.Arguments[0].ResultUsage().Type != node.Calls[0].Arguments[0].ResultUsage().Type || original.Filter.ResultUsage().Type != node.Calls[0].Filter.ResultUsage().Type {
		t.Fatal("remap mutated original inputs")
	}
}

func TestAggregateModifiersOuterOwnershipSharing(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT (SELECT count(*) FILTER(WHERE o.v=1)),(SELECT count(*) FILTER(WHERE o.v=1)),(SELECT count(*) FILTER(WHERE o.v=2)) FROM t o")
	node := query.Root.(*plan.ProjectNode).Child.(*plan.AggregateNode)
	if len(node.Calls) != 2 || node.Calls[0].Filter == nil || node.Calls[1].Filter == nil {
		t.Fatal("incomplete outer-owner modifier identity", node.Calls)
	}
	rows, count, err := exec.Finalize(context.Background(), query.Root, source, nil)
	if err != nil || count != 1 {
		t.Fatal(rows, count, err)
	}
	defer rows.Close()
	out := make([]sqltype.Value, 3)
	present, err := rows.Next(context.Background(), out)
	if err != nil || !present {
		t.Fatal(present, err)
	}
	for i, want := range []int64{2, 2, 1} {
		got, err := out[i].Integer()
		if err != nil || got != want {
			t.Fatal(i, got, want, err)
		}
	}
}

func TestAggregateModifiersExecutionStateIsolation(t *testing.T) {
	e, query, source := queryCoreFixture(t, "SELECT f,count(DISTINCT v ORDER BY v) FROM t GROUP BY f ORDER BY f NULLS LAST")
	for execution := range 2 {
		if execution > 0 {
			snapshot := enginetest.Run(t, e, "SELECT * FROM t").Rows.(*exec.FinalRows)
			var err error
			source, err = exec.CaptureSource(context.Background(), snapshot.Source.View, snapshot.Source.Ref)
			snapshot.Close()
			if err != nil {
				t.Fatal(err)
			}
		}
		rows, count, err := exec.Finalize(context.Background(), query.Root, source, nil)
		if err != nil || count != 3 {
			t.Fatal(execution, rows, count, err)
		}
		out := make([]sqltype.Value, 2)
		for i, want := range []int64{1, 1, 0} {
			present, err := rows.Next(context.Background(), out)
			got, accessErr := out[1].Integer()
			if err != nil || accessErr != nil || !present || got != want {
				t.Fatal(execution, i, got, want, err, accessErr)
			}
			if i == 2 {
				if !out[0].IsNull() {
					t.Fatal(out)
				}
			} else {
				flag, err := out[0].Boolean()
				if err != nil || flag != (i == 1) {
					t.Fatal(out, err)
				}
			}
		}
		if present, err := rows.Next(context.Background(), out); present || err != nil {
			t.Fatal(present, err)
		}
		rows.Close()
	}
}

func queryCoreFixture(t *testing.T, sql string) (*core.Engine, *plan.Query, *exec.Source) {
	t.Helper()
	// Parsing may terminate the test; never do it while holding e.mu, since
	// deferred session teardown needs that mutex before WaitEnd can return.
	ast := enginetest.Statement(t, sql)
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE t(id INT NOT NULL,v INT,f BOOL)")
	enginetest.Run(t, e, "INSERT INTO t VALUES(1,1,TRUE),(2,NULL,NULL),(3,2,FALSE),(4,1,TRUE)")
	captured := enginetest.Run(t, e, "SELECT * FROM t").Rows.(*exec.FinalRows)
	source, captureErr := exec.CaptureSource(context.Background(), captured.Source.View, captured.Source.Ref)
	if captureErr != nil {
		t.Fatal(captureErr)
	}
	captured.Close()
	t.Cleanup(source.Close)
	op, done, err := enginetest.Operation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	from := ast.(*parser.Select).From
	if alias, ok := from.(*parser.AliasReference); ok {
		from = alias.Input
	}
	ref, err := e.ResolveReference(auth, from.(*parser.TableReference).Name)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	b, err := core.Describe(op.Ctx, e.AnalysisHost(op), ast, auth, ref, sqltype.InputEnvironment{}, nil, nil)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return e, b.Query, source
}

func queryProbe(usage sqltype.Usage, eval func(*exec.Context) (sqltype.Value, error)) expr.Expr {
	return queryProbeExpression{Info: expr.Info{Usage: usage, Pos: parser.Position{Offset: -1}}, eval: eval}
}

type aggregateProbeProvider struct {
	functions.AggregateProvider
	advance  func(context.Context, any, sqltype.Value, sqltype.Value) error
	finalize func(context.Context, any) (sqltype.Value, error)
}

func (p aggregateProbeProvider) Advance(ctx context.Context, s any, values []sqltype.Value, evaluation expr.Context) error {
	if p.advance != nil {
		var value, separator sqltype.Value
		if len(values) > 0 {
			value = values[0]
		}
		if len(values) > 1 {
			separator = values[1]
		}
		return p.advance(ctx, s, value, separator)
	}
	return p.AggregateProvider.Advance(ctx, s, values, evaluation)
}

func (p aggregateProbeProvider) Finalize(ctx context.Context, s any) (sqltype.Value, error) {
	if p.finalize != nil {
		return p.finalize(ctx, s)
	}
	return p.AggregateProvider.Finalize(ctx, s)
}

// This execution-local test context observes real comparator/merge entry. It
// invokes a real CancelFunc there, without altering production sort callbacks,
// relying on whole-pipeline Err-call counts, or introducing timing races.
type aggregateSortCancelContext struct {
	context.Context
	cancel            context.CancelFunc
	comparisonEntered bool
	aggregateOrdering bool
}

func (c *aggregateSortCancelContext) Err() error {
	if !c.comparisonEntered {
		var pcs [32]uintptr
		n := runtime.Callers(2, pcs[:])
		frames := runtime.CallersFrames(pcs[:n])
		comparison, merge, replay := false, false, false
		for {
			frame, more := frames.Next()
			comparison = comparison || strings.HasSuffix(frame.Function, ".Comparison.Compare")
			merge = merge || strings.Contains(frame.Function, ".MergeQueryIndices")
			replay = replay || strings.HasSuffix(frame.Function, ".replayOrderedAggregate")
			if !more {
				break
			}
		}
		if comparison && merge && (c.aggregateOrdering && replay || !c.aggregateOrdering && !replay) {
			c.comparisonEntered = true
			c.cancel()
		}
	}
	return c.Context.Err()
}

type queryProbeExpression struct {
	expr.Info
	eval func(*exec.Context) (sqltype.Value, error)
}

func (e queryProbeExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	return e.eval(evalContext(ctx))
}

// evalContext recovers the engine evaluation context in test probes. A nil or
// foreign context yields nil.
func evalContext(ctx expr.Context) *exec.Context {
	c, _ := ctx.(*exec.Context)
	return c
}
