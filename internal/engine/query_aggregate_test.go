package engine

import (
	"context"
	"errors"
	"math"
	"runtime"
	"strings"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

type aggregateProbeProvider struct {
	functions.AggregateProvider
	advance  func(context.Context, any, sqltype.Value, sqltype.Value) error
	finalize func(context.Context, any) (sqltype.Value, error)
}

func (p aggregateProbeProvider) Advance(ctx context.Context, s any, value, separator sqltype.Value) error {
	if p.advance != nil {
		return p.advance(ctx, s, value, separator)
	}
	return p.AggregateProvider.Advance(ctx, s, value, separator)
}
func (p aggregateProbeProvider) Finalize(ctx context.Context, s any) (sqltype.Value, error) {
	if p.finalize != nil {
		return p.finalize(ctx, s)
	}
	return p.AggregateProvider.Finalize(ctx, s)
}

func TestQueryAggregateCountProvider(t *testing.T) {
	e, query, source := queryCoreFixture(t, "SELECT count(*) FROM t")
	_ = e
	defer source.close()
	node := query.Root.(*projectNode).child.(*aggregateNode)
	p := node.calls[0].provider
	if p.ResultUsage() != query.Root.Schema()[0].Usage {
		t.Fatal("count provider/result usage", p.ResultUsage(), query.Root.Schema())
	}
}
func TestQueryAggregateEagerOwnershipAndMetadata(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT v,count(*) FROM t GROUP BY v ORDER BY v")
	schema := query.Root.Schema()
	if schema[0].OriginTable == 0 || schema[0].OriginAttribute != 2 || !schema[0].NullabilityKnown || !schema[0].Nullable || schema[1].Usage.Type.ID() != sqltype.Int8 || schema[1].OriginTable != 0 || !schema[1].NullabilityKnown || schema[1].Nullable {
		t.Fatal(schema)
	}
	rows, count, err := finalizeQuery(context.Background(), query.Root, source, nil)
	if err != nil || count != 3 || source.view != nil {
		t.Fatal(rows, count, err, source.view)
	}
	defer rows.Close()
	scratch := make([]sqltype.Value, 2)
	for _, want := range [][2]int64{{1, 2}, {2, 1}, {0, 1}} {
		present, err := rows.Next(context.Background(), scratch)
		if !present || err != nil {
			t.Fatal(present, err)
		}
		n, _ := scratch[1].Integer()
		if n != want[1] {
			t.Fatal(n, want)
		}
		if want[0] == 0 {
			if !scratch[0].IsNull() {
				t.Fatal(scratch)
			}
		} else {
			v, _ := scratch[0].Integer()
			if v != want[0] {
				t.Fatal(v, want)
			}
		}
	}
}
func TestQueryAggregateCancellationAndLateErrors(t *testing.T) {
	for _, phase := range []string{"key", "advance", "finalize", "having", "HAVING-to-sort boundary", "projection", "prepublication", "late_finalize"} {
		t.Run(phase, func(t *testing.T) {
			_, query, source := queryCoreFixture(t, "SELECT v,count(*) FROM t GROUP BY v HAVING count(*)>0 ORDER BY v")
			project := query.Root.(*projectNode)
			sort := project.child.(*sortNode)
			having := sort.child.(*filterNode)
			node := having.child.(*aggregateNode)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			sentinel := errors.New("late aggregate finalization")
			switch phase {
			case "key":
				original := node.keys[0]
				node.keys[0] = queryProbe(original.ResultUsage(), func(c *EvalContext) (sqltype.Value, error) {
					value, err := original.Eval(c)
					cancel()
					return value, err
				})
			case "advance":
				original := node.calls[0].provider
				node.calls[0].provider = aggregateProbeProvider{AggregateProvider: original, advance: func(ctx context.Context, s any, value, separator sqltype.Value) error {
					err := original.Advance(ctx, s, value, separator)
					cancel()
					return err
				}}
			case "finalize":
				original := node.calls[0].provider
				node.calls[0].provider = aggregateProbeProvider{AggregateProvider: original, finalize: func(ctx context.Context, s any) (sqltype.Value, error) {
					v, err := original.Finalize(ctx, s)
					cancel()
					return v, err
				}}
			case "having":
				original := having.predicate
				having.predicate = queryProbe(original.ResultUsage(), func(c *EvalContext) (sqltype.Value, error) {
					cancel()
					return sqltype.Boolean(original.ResultUsage().Type, false), nil
				})
			case "HAVING-to-sort boundary":
				original := having.predicate
				calls := 0
				having.predicate = queryProbe(original.ResultUsage(), func(c *EvalContext) (sqltype.Value, error) {
					calls++
					value, err := original.Eval(c)
					if calls == 3 {
						cancel()
					}
					return value, err
				})
			case "projection", "prepublication":
				original := project.expressions[0]
				calls := 0
				project.expressions[0] = queryProbe(original.ResultUsage(), func(c *EvalContext) (sqltype.Value, error) {
					calls++
					value, err := original.Eval(c)
					if phase == "projection" || calls == 3 {
						cancel()
					}
					return value, err
				})
			case "late_finalize":
				original := node.calls[0].provider
				calls := 0
				node.calls[0].provider = aggregateProbeProvider{AggregateProvider: original, finalize: func(ctx context.Context, s any) (sqltype.Value, error) {
					calls++
					if calls == 2 {
						return sqltype.Value{}, sentinel
					}
					return original.Finalize(ctx, s)
				}}
			}
			rows, count, err := finalizeQuery(ctx, query.Root, source, nil)
			want := error(context.Canceled)
			if phase == "late_finalize" {
				want = sentinel
			}
			if rows != nil || count != 0 || !errors.Is(err, want) || source.view != nil || source.anonymous != nil {
				t.Fatal(rows, count, err, want, source)
			}
		})
	}
}
func TestQueryAggregateHashCollisionEquality(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT v,count(*) FROM t GROUP BY v")
	defer source.close()
	node := query.Root.(*projectNode).child.(*aggregateNode)
	typ := node.keys[0].ResultUsage().Type
	one, _ := sqltype.Integer(typ, 1)
	two, _ := sqltype.Integer(typ, 2)
	groups := []queryGroup{{keys: []sqltype.Value{one}}, {keys: []sqltype.Value{two}}}
	// A forced collision bucket is not evidence that its first key is equal.
	index, err := findQueryGroup(context.Background(), node.grouping, []sqltype.Value{two}, []int{0}, groups, []int{0, 1})
	if err != nil || index != 1 {
		t.Fatal(index, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := findQueryGroup(ctx, node.grouping, []sqltype.Value{two}, []int{0}, groups, []int{0, 1}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestQueryAggregateInsertLateProviderFailure(t *testing.T) {
	b, source := insertSelectEvaluationFixture(t, "SELECT count(*) FROM t GROUP BY v")
	node := b.query.Root.(*projectNode).child.(*aggregateNode)
	original := node.calls[0].provider
	sentinel := errors.New("late INSERT aggregate provider")
	calls := 0
	node.calls[0].provider = aggregateProbeProvider{AggregateProvider: original, finalize: func(ctx context.Context, s any) (sqltype.Value, error) {
		calls++
		if calls == 2 {
			return sqltype.Value{}, sentinel
		}
		return original.Finalize(ctx, s)
	}}
	rows, err := evaluateInsertQuery(context.Background(), b, source, nil, "public", sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}, nil)
	if rows.values != nil || err != sentinel || calls != 2 || source.view != nil || source.anonymous != nil {
		t.Fatal(rows, err, calls, source)
	}
}

func TestQueryAggregateLateKeyFailure(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT v,count(*) FROM t GROUP BY v")
	node := query.Root.(*projectNode).child.(*aggregateNode)
	original := node.keys[0]
	sentinel := errors.New("late group key")
	calls := 0
	node.keys[0] = queryProbe(original.ResultUsage(), func(c *EvalContext) (sqltype.Value, error) {
		calls++
		if calls == 3 {
			return sqltype.Value{}, sentinel
		}
		return original.Eval(c)
	})
	rows, count, err := finalizeQuery(context.Background(), query.Root, source, nil)
	if rows != nil || count != 0 || err != sentinel || calls != 3 || source.view != nil {
		t.Fatal(rows, count, err, calls)
	}
}

type aggregateCancelContext struct {
	context.Context
	armed     bool
	calls, at int
}

func (c *aggregateCancelContext) Err() error {
	if c.armed {
		c.calls++
		if c.calls >= c.at {
			return context.Canceled
		}
	}
	return nil
}
func TestQueryAggregateMidHashAndCollisionCancellation(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT v,count(*) FROM t GROUP BY v")
	node := query.Root.(*projectNode).child.(*aggregateNode)
	usage := source.row(0)[1].Type().Usage()
	// The collision comparator cancels after entering typed equality, not merely
	// before opening the stage.
	one, _ := sqltype.Integer(usage.Type, 1)
	two, _ := sqltype.Integer(usage.Type, 2)
	collision := &aggregateCancelContext{Context: context.Background(), armed: true, at: 4}
	if _, err := findQueryGroup(collision, node.grouping, []sqltype.Value{two}, []int{0}, []queryGroup{{keys: []sqltype.Value{one}}, {keys: []sqltype.Value{two}}}, []int{0, 1}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	// A real admitted TEXT key is scanned and canceled inside the shared hash.
	types := sqltype.Builtins()
	var descriptor *sqltype.Descriptor
	for _, d := range types {
		if d.ID() == sqltype.Text {
			descriptor = d
		}
	}
	hashContext := &aggregateCancelContext{Context: context.Background(), at: 4}
	node.grouping[0].Grouping, _ = sqltype.BindGrouping(descriptor.Usage())
	node.keys[0] = queryProbe(descriptor.Usage(), func(*EvalContext) (sqltype.Value, error) {
		hashContext.armed = true
		return sqltype.String(descriptor, strings.Repeat("x", 2048)), nil
	})
	rows, count, err := finalizeQuery(hashContext, query.Root, source, nil)
	if rows != nil || count != 0 || !errors.Is(err, context.Canceled) || source.view != nil {
		t.Fatal(rows, count, err, source)
	}
}

func TestQueryAggregateSemanticIdentityAndStagePreflight(t *testing.T) {
	e, query, source := queryCoreFixture(t, "SELECT (v+1)*2,count(*) FROM t GROUP BY v+1")
	defer source.close()
	node := query.Root.(*projectNode).child.(*aggregateNode)
	key := node.keys[0].(arithmeticExpression)
	binder := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	equivalent := key
	equivalent.Pos.Character = 999
	column := key.left.(columnExpression)
	column.Pos.Character = 888
	equivalent.left = column
	if !binder.sameGroupExpression(key, equivalent) {
		t.Fatal("positions changed semantic identity")
	}
	reversed := key
	reversed.left, reversed.right = key.right, key.left
	if binder.sameGroupExpression(key, reversed) {
		t.Fatal("algebraic identity admitted")
	}
	first := parameterExpression{expressionInfo: expressionInfo{Usage: key.ResultUsage()}, number: 1}
	second := first
	second.number = 2
	if binder.sameGroupExpression(first, second) {
		t.Fatal("different parameters equated")
	}
	differentUsage := first
	differentUsage.Usage.Typmod = 4
	if binder.sameGroupExpression(first, differentUsage) {
		t.Fatal("different typmods equated")
	}
	projected := query.Root.(*projectNode).expressions[0]
	if !projected.Properties().StageDependent {
		t.Fatal("group dependence lost")
	}
	folded, _, err := preflightCoercions(&EvalContext{Context: context.Background()}, projected)
	if err != nil || !folded.Properties().StageDependent {
		t.Fatal("group result evaluated during preflight", err)
	}
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
			merge = merge || strings.Contains(frame.Function, ".mergeQueryIndices")
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

func TestQueryAggregateActualSortComparisonCancellation(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT v,count(*) FROM t GROUP BY v ORDER BY v")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	probe := &aggregateSortCancelContext{Context: ctx, cancel: cancel}
	rows, count, err := finalizeQuery(probe, query.Root, source, nil)
	if !probe.comparisonEntered || ctx.Err() != context.Canceled ||
		!errors.Is(err, context.Canceled) || rows != nil || count != 0 {
		t.Fatal(probe.comparisonEntered, rows, count, err)
	}
}

// A real bounded provider proves reached Advance failure stops later arguments
// and publishes no rows. Count's unreachable practical maximum remains covered
// in the child's private state unit; NULL filtering is tested with real COUNT.
func TestQueryAggregateRealAdvanceFailure(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT sum(1::real) FROM t")
	node := query.Root.(*projectNode).child.(*aggregateNode)
	usage := node.calls[0].argument.ResultUsage()
	argumentEffects := 0
	node.calls[0].argument = queryProbe(usage, func(*EvalContext) (sqltype.Value, error) {
		argumentEffects++
		return sqltype.Float(usage.Type, math.MaxFloat32)
	})
	rows, count, err := finalizeQuery(context.Background(), query.Root, source, nil)
	var diagnostic *sqlstate.Error
	if rows != nil || count != 0 || !errors.As(err, &diagnostic) || *diagnostic != (sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22003", Message: "value out of range: overflow"}) || argumentEffects != 2 || source.view != nil || source.anonymous != nil {
		t.Fatal(rows, count, err, argumentEffects, source)
	}
}
