package exec

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
	"testing"
	"time"
)

type viewProducerProbe struct {
	expr.Expr
	calls    int
	volatile bool
	failure  error
}

func (p *viewProducerProbe) Properties() expr.Properties {
	properties := p.Expr.Properties()
	if p.volatile {
		properties.Volatility = expr.Volatile
	}
	return properties
}
func (p *viewProducerProbe) Eval(c expr.Context) (sqltype.Value, error) {
	p.calls++
	if p.failure != nil && p.calls == 2 {
		return sqltype.Value{}, p.failure
	}
	return p.Expr.Eval(c)
}
func viewProducerFixture(t *testing.T, volatile bool) (*Source, *State, *viewProducerProbe) {
	t.Helper()
	var integer *sqltype.Descriptor
	for _, typ := range sqltype.Builtins() {
		if typ.ID() == sqltype.Int4 {
			integer = typ
			break
		}
	}
	columns := []catalog.Column{{Name: "id", Usage: integer.Usage(), Nullable: true}}
	rows := make([][]sqltype.Value, 2)
	for i, n := range []int64{1, 3} {
		value, err := sqltype.Integer(integer, n)
		if err != nil {
			t.Fatal(err)
		}
		rows[i] = []sqltype.Value{value}
	}
	probe := &viewProducerProbe{Expr: plan.ColumnExpr{Info: expr.Info{Usage: integer.Usage(), Property: expr.Properties{RowDependent: true}}, Scope: 1, Frame: 1, Slot: 0}, volatile: volatile}
	root := &plan.ProjectNode{QuerySchema: columns, Child: &plan.SourceNode{QuerySchema: columns}, Expressions: []expr.Expr{probe}, Frame: 1}
	// Match production lineage ownership: Inputs is the same owned capture as
	// NestedInputs, and Stage starts as a sentinel until producer publication.
	inputs := &Source{Anonymous: rows}
	source := &Source{Materialized: true, FlatWidth: 1, NestedRoot: root, NestedInputs: inputs, Lineage: &QueryLockLineage{Root: root, Inputs: inputs, Stage: &Source{Materialized: true}}}
	return source, &State{Source: source, SourceMode: SourceBorrowed}, probe
}
func viewProducerPull(t *testing.T, stage *Stage, want int64) {
	t.Helper()
	i, present, err := stage.NextOccurrence(context.Background())
	if err != nil || !present {
		t.Fatal("missing lazy row", present, err)
	}
	value, err := stage.Source.Row(stage.Index(i))[0].Integer()
	if err != nil || value != want {
		t.Fatal("physical/cached row identity", value, err, want)
	}
}

func TestViewProducerReentry(t *testing.T) {
	for _, volatile := range []bool{false, true} {
		name := "stable"
		if volatile {
			name = "volatile"
		}
		t.Run(name, func(t *testing.T) {
			source, state, probe := viewProducerFixture(t, volatile)
			defer source.Close()
			open := func() Stage {
				stage, err := OpenSourceStage(context.Background(), state, source, source.NestedRoot.Schema())
				if err != nil {
					t.Fatal(err)
				}
				return stage
			}
			first := open()
			if probe.calls != 0 || source.FlatCount != 0 {
				t.Fatal("opening eagerly evaluated view")
			}
			viewProducerPull(t, &first, 1)
			producer := source.NestedStage
			if producer == nil || !producer.Retain || probe.calls != 1 || source.FlatCount != 1 {
				t.Fatal("first pull drained or failed producer ownership")
			}
			closed := 0
			finish := producer.Finish
			producer.Finish = func() error {
				closed++
				if finish != nil {
					return finish()
				}
				return nil
			}
			if err := first.Close(); err != nil {
				t.Fatal(err)
			}
			if producer.Closed || closed != 0 {
				t.Fatal("short-circuiting reader closed shared producer")
			}
			second := open()
			if second.Selection != nil || !second.scanRange {
				t.Fatal("rebuilt identity index vector for cached prefix")
			}
			if volatile && (source.FlatCount != 0 || len(source.Lineage.Selection) != 0) {
				t.Fatal("volatile reset retained stale output/lineage")
			}
			viewProducerPull(t, &second, 1)
			expected := 1
			if volatile {
				expected = 2
			}
			if probe.calls != expected {
				t.Fatal("cached replay or volatile demand changed", probe.calls, expected)
			}
			viewProducerPull(t, &second, 3)
			if probe.calls != expected+1 || source.FlatCount != 2 || len(source.Lineage.Selection) != 2 {
				t.Fatal("lost/duplicated uncaptured frontier or lineage")
			}
			if err := second.Close(); err != nil {
				t.Fatal(err)
			}
			third := open()
			if err := third.Drain(context.Background()); err != nil {
				t.Fatal(err)
			}
			expected = 2
			if volatile {
				expected = 5
			}
			if probe.calls != expected || third.Count() != 2 || len(source.Lineage.Selection) != 2 {
				t.Fatal("range drain lost replay/reset", probe.calls, third.Count())
			}
			if err := third.Close(); err != nil {
				t.Fatal(err)
			}
			if producer.Closed || closed != 0 {
				t.Fatal("reader/EOF closed source-owned producer")
			}
			source.Close()
			source.Close()
			if !producer.Closed || closed != 1 || source.NestedStage != nil {
				t.Fatal("producer not disposed exactly once", closed)
			}
		})
	}
}

func TestViewProducerFailure(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		name := "expression"
		if cancelled {
			name = "cancel"
		}
		t.Run(name, func(t *testing.T) {
			source, state, probe := viewProducerFixture(t, false)
			defer source.Close()
			failure := errors.New("view projection failure")
			if !cancelled {
				probe.failure = failure
			}
			first, err := OpenSourceStage(context.Background(), state, source, source.NestedRoot.Schema())
			if err != nil {
				t.Fatal(err)
			}
			viewProducerPull(t, &first, 1)
			if err := first.Close(); err != nil {
				t.Fatal(err)
			}
			producer := source.NestedStage
			closed := 0
			finish := producer.Finish
			producer.Finish = func() error {
				closed++
				if finish != nil {
					return finish()
				}
				return nil
			}
			second, err := OpenSourceStage(context.Background(), state, source, source.NestedRoot.Schema())
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			if cancelled {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				failure = context.Canceled
			} else {
				viewProducerPull(t, &second, 1)
			}
			if _, _, err := second.NextOccurrence(ctx); !errors.Is(err, failure) {
				t.Fatal("lost producer failure", err)
			}
			if !producer.Closed || closed != 1 {
				t.Fatal("failure did not close producer once", closed)
			}
			if _, err := OpenSourceStage(context.Background(), state, source, source.NestedRoot.Schema()); !errors.Is(err, failure) {
				t.Fatal("reopened failed producer or replayed stale prefix", err)
			}
			source.Close()
			if closed != 1 {
				t.Fatal("owner repeated failed producer disposal", closed)
			}
		})
	}
}

func TestViewProducerSourceOwnership(t *testing.T) {
	for _, item := range []struct {
		name string
		mode SourceMode
	}{
		{"one_shot", SourceOwned}, {"rowstream", SourceReusable}, {"borrowed", SourceBorrowed},
	} {
		t.Run(item.name, func(t *testing.T) {
			source, state, _ := viewProducerFixture(t, false)
			defer source.Close()
			state.SourceMode = item.mode
			reader, err := OpenSourceStage(context.Background(), state, source, source.NestedRoot.Schema())
			if err != nil {
				t.Fatal(err)
			}
			viewProducerPull(t, &reader, 1)
			producer := source.NestedStage
			cleanup := errors.New("required producer close")
			closes := 0
			finish := producer.Finish
			producer.Finish = func() error {
				closes++
				if finish != nil {
					if err := finish(); err != nil {
						return err
					}
				}
				return cleanup
			}
			err = reader.Close()
			if item.mode == SourceBorrowed {
				if err != nil || producer.Closed || closes != 0 {
					t.Fatal("borrower completed producer", err, closes)
				}
				if err = state.FinishSources(nil); err != nil || closes != 0 {
					t.Fatal("borrower finish", err, closes)
				}
				owner := State{Source: source, SourceMode: SourceReusable}
				err = owner.FinishSources(nil)
			}
			if !errors.Is(err, cleanup) || !producer.Closed || closes != 1 {
				t.Fatal("owner lost close veto", err, closes)
			}
			if err := reader.Close(); err != nil {
				t.Fatal("repeated reader close", err)
			}
			source.Close()
			if closes != 1 {
				t.Fatal("duplicate owner disposal", closes)
			}
		})
	}
}

func TestViewProducerInvocationAbort(t *testing.T) {
	for _, primary := range []error{errors.New("expression outside Next"), context.Canceled} {
		t.Run(primary.Error(), func(t *testing.T) {
			source, state, _ := viewProducerFixture(t, false)
			defer source.Close()
			reader, err := OpenSourceStage(context.Background(), state, source, source.NestedRoot.Schema())
			if err != nil {
				t.Fatal(err)
			}
			viewProducerPull(t, &reader, 1)
			producer := source.NestedStage
			closes := 0
			finish := producer.Finish
			producer.Finish = func() error {
				closes++
				if finish != nil {
					_ = finish()
				}
				return errors.New("secondary cleanup")
			}
			if err := state.finishInvocation(&reader, primary); !errors.Is(err, primary) {
				t.Fatal("primary replaced", err)
			}
			if !producer.Closed || !errors.Is(producer.Terminal, primary) || closes != 1 {
				t.Fatal("borrowed producer not aborted", producer.Terminal, closes)
			}
			if _, err := OpenSourceStage(context.Background(), state, source, source.NestedRoot.Schema()); !errors.Is(err, primary) {
				t.Fatal("aborted producer reopened", err)
			}
			source.Close()
			if closes != 1 {
				t.Fatal("duplicate failed disposal", closes)
			}
		})
	}
}

func TestViewProducerScalarOnlyOwnerClose(t *testing.T) {
	source, borrower, _ := viewProducerFixture(t, false)
	defer source.Close()
	reader, err := OpenSourceStage(context.Background(), borrower, source, source.NestedRoot.Schema())
	if err != nil {
		t.Fatal(err)
	}
	viewProducerPull(t, &reader, 1)
	producer := source.NestedStage
	cleanup := errors.New("scalar-only helper close")
	closes := 0
	finish := producer.Finish
	producer.Finish = func() error {
		closes++
		if finish != nil {
			_ = finish()
		}
		return cleanup
	}
	if err := reader.Close(); err != nil || closes != 0 {
		t.Fatal("borrower close", err, closes)
	}
	// The owner reads a different ordinary occurrence. Its scalar invocation
	// alone reached the helper; child.Close cannot discover that producer.
	parent := &Source{Anonymous: source.NestedInputs.Anonymous}
	parent.Sources = []*Source{parent, source}
	defer parent.Close()
	owner := State{Source: parent, SourceMode: SourceReusable}
	stage, err := OpenQueryProjectStage(context.Background(), &owner, source.NestedRoot.(*plan.ProjectNode))
	if err != nil {
		t.Fatal(err)
	}
	if err := stage.Close(); !errors.Is(err, cleanup) || !producer.Closed || closes != 1 {
		t.Fatal("scalar helper veto lost", err, closes)
	}
	if err := stage.Close(); err != nil {
		t.Fatal("repeated owner finish", err)
	}
	parent.Close()
	if closes != 1 {
		t.Fatal("duplicate scalar helper disposal", closes)
	}
}

// The helper can finish all fields before the final publication checkpoint
// expires. Reenter through the real CTE owner to publish without another Eval.
func TestViewProducerPendingPublication(t *testing.T) {
	for _, width := range []int{0, 1} {
		name := "zero_width"
		if width != 0 {
			name = "typed_null"
		}
		t.Run(name, func(t *testing.T) {
			fixture, _, probe := viewProducerFixture(t, false)
			defer fixture.Close()
			schema := fixture.NestedRoot.Schema()[:width]
			input := fixture.NestedInputs
			var expressions []expr.Expr
			if width == 0 {
				input = &Source{Anonymous: [][]sqltype.Value{{}, {}}}
			} else {
				input.Anonymous[0][0] = sqltype.Null(schema[0].Usage.Type)
				expressions = []expr.Expr{probe}
			}
			output := &Source{Materialized: true, FlatWidth: width}
			producer := CTEProducer{
				Opened: true, Source: output, project: &plan.ProjectNode{QuerySchema: schema},
				Inner: Stage{Source: input, Columns: schema, Expressions: expressions,
					Evaluation: Context{EvalFrame: EvalFrame{Scope: 1, Frame: 1}}, projectionOutput: output},
			}
			defer producer.close()
			live := context.Background()
			i, present, err := producer.Inner.NextOccurrence(live)
			if err != nil || !present {
				t.Fatal("missing original occurrence", present, err)
			}
			output.beginProjection(i)
			if err := output.projectPending(live, &producer.Inner, schema); err != nil {
				t.Fatal(err)
			}
			if probe.calls != width || output.projectionFields != width {
				t.Fatal("whole fields did not complete once", probe.calls, output.projectionFields)
			}
			// Only the final checkpoint remains. Use a real already-expired
			// budget, without a production clock or inspection hook.
			budget := &sampleBudget{start: time.Now(), millis: 0}
			expired := context.WithValue(live, sampleBudgetKey{}, budget)
			if err := output.projectPending(expired, &producer.Inner, schema); err != budget {
				t.Fatal("final publication checkpoint lost its owner", err)
			}
			if !output.projectionPending || output.FlatCount != 0 || len(output.Flat) != 0 || probe.calls != width {
				t.Fatal("final expiry published or replayed the completed row")
			}
			if present, err := producer.Ensure(live, 0); err != nil || !present {
				t.Fatal("live owner did not publish its saved row", present, err)
			}
			if output.projectionPending || output.FlatCount != 1 || len(output.Flat) != width || probe.calls != width {
				t.Fatal("saved row publication replayed fields or lost zero-width count")
			}
			if width != 0 && !reflect.DeepEqual(output.Row(0), input.Row(0)) {
				t.Fatal("saved typed NULL or zero-width tuple changed")
			}
			if present, err := producer.Ensure(live, 1); err != nil || !present {
				t.Fatal("owner did not advance after publication", present, err)
			}
			if output.FlatCount != 2 || probe.calls != 2*width || width != 0 && !reflect.DeepEqual(output.Row(1), input.Row(1)) {
				t.Fatal("next original occurrence skipped or replayed", output.FlatCount, probe.calls)
			}
		})
	}
}

func TestViewProducerPendingFrame(t *testing.T) {
	fixture, _, probe := viewProducerFixture(t, false)
	defer fixture.Close()
	schema := fixture.NestedRoot.Schema()
	input := fixture.NestedInputs
	output := &Source{Materialized: true, FlatWidth: 1}
	inner := Stage{
		Source: input, Columns: schema, Expressions: []expr.Expr{probe},
		Selection: []int{1, 0}, CacheWidth: 1, CacheOccurrences: []int{0, 1},
		OrderValues: []sqltype.Value{input.Row(1)[0], input.Row(0)[0]},
		Evaluation: Context{EvalFrame: EvalFrame{Scope: 1, Frame: 1}},
	}
	output.beginProjection(1) // Ordinal 1 is physical row 0, cache occurrence 1.
	expired, stop := context.WithCancel(context.Background())
	stop()
	inner.Evaluation.setContext(expired)
	inner.Evaluation.Row, inner.Evaluation.OrderValues = input.Row(1), inner.OrderRow(0)
	live, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := output.projectPending(live, &inner, schema); err != nil {
		t.Fatal("pending projection reused the expired context", err)
	}
	if inner.Evaluation.Context != live || !reflect.DeepEqual(inner.Evaluation.Row, input.Row(0)) ||
		!reflect.DeepEqual(inner.Evaluation.OrderValues, inner.OrderRow(1)) || probe.calls != 1 {
		t.Fatal("resume failed to rebind the live row/context/order frame")
	}
	output.publishProjection()
	if !reflect.DeepEqual(output.Row(0), input.Row(0)) {
		t.Fatal("resume confused occurrence ordinal with physical row")
	}
}

func TestViewProducerPendingRewind(t *testing.T) {
	for _, volatile := range []bool{false, true} {
		name := "unchanged"
		if volatile {
			name = "changed_volatile"
		}
		t.Run(name, func(t *testing.T) {
			source, state, probe := viewProducerFixture(t, volatile)
			defer source.Close()
			schema := source.NestedRoot.Schema()
			reader, err := OpenSourceStage(context.Background(), state, source, schema)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			viewProducerPull(t, &reader, 1)
			inner := source.NestedStage
			i, present, err := inner.NextOccurrence(context.Background())
			if err != nil || !present {
				t.Fatal("missing pending original occurrence", present, err)
			}
			source.beginProjection(i)
			if err := source.projectPending(context.Background(), inner, schema); err != nil {
				t.Fatal(err)
			}
			backing := source.Flat[:cap(source.Flat)]
			saved := backing[1]
			prefix := source.Flat[0]
			if err := reader.Close(); err != nil || !source.projectionPending || !reflect.DeepEqual(backing[1], saved) {
				t.Fatal("borrowed close discarded pending output", err)
			}
			reopened, err := OpenSourceStage(context.Background(), state, source, schema)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			if volatile {
				if source.projectionPending || source.projectionFields != 0 || source.FlatCount != 0 || len(source.Flat) != 0 ||
					!reflect.DeepEqual(backing[0], sqltype.Value{}) || !reflect.DeepEqual(backing[1], sqltype.Value{}) ||
					len(source.Lineage.Selection) != 0 {
					t.Fatal("changed rewind retained abandoned published/pending backing")
				}
				viewProducerPull(t, &reopened, 1)
				viewProducerPull(t, &reopened, 3)
				if probe.calls != 4 {
					t.Fatal("new volatile invocation did not reevaluate demanded fields", probe.calls)
				}
			} else {
				if !source.projectionPending || source.projectionOrdinal != i || source.projectionFields != 1 ||
					inner.Position != 2 || !reflect.DeepEqual(backing[1], saved) || !reflect.DeepEqual(backing[0], prefix) {
					t.Fatal("unchanged rewind lost the unpublished frontier or backing")
				}
				viewProducerPull(t, &reopened, 1)
				if !source.projectionPending || probe.calls != 2 {
					t.Fatal("prefix replay eagerly completed pending work")
				}
				viewProducerPull(t, &reopened, 3)
				if source.projectionPending || probe.calls != 2 || len(source.Lineage.Selection) != 2 {
					t.Fatal("unchanged continuation replayed fields or duplicated lineage")
				}
			}
		})
	}
}

func TestViewProducerPendingAbandonment(t *testing.T) {
	for _, mode := range []string{"entry_cancel", "inner_failure", "finish_closed_inner", "detach"} {
		t.Run(mode, func(t *testing.T) {
			source, state, _ := viewProducerFixture(t, false)
			defer source.Close()
			reader, err := OpenSourceStage(context.Background(), state, source, source.NestedRoot.Schema())
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			viewProducerPull(t, &reader, 1)
			inner := source.NestedStage
			i, present, err := inner.NextOccurrence(context.Background())
			if err != nil || !present {
				t.Fatal("missing pending occurrence", present, err)
			}
			source.beginProjection(i)
			if err := source.projectPending(context.Background(), inner, source.NestedRoot.Schema()); err != nil {
				t.Fatal(err)
			}
			backing := source.Flat[:cap(source.Flat)]
			prefix := source.Flat[0]
			closes := 0
			cleanup := errors.New("secondary pending cleanup")
			finish := inner.Finish
			inner.Finish = func() error {
				closes++
				if finish != nil {
					_ = finish()
				}
				return cleanup
			}
			switch mode {
			case "entry_cancel":
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				if _, _, err := reader.NextOccurrence(ctx); err != context.Canceled {
					t.Fatal("entry cancel lost its primary cause", err)
				}
			case "inner_failure":
				primary := errors.New("direct inner failure")
				if err := inner.fail(primary); err != primary {
					t.Fatal("direct inner failure lost its primary cause", err)
				}
			case "finish_closed_inner":
				if err := inner.Close(); err != cleanup {
					t.Fatal("actual producer close lost its veto", err)
				}
				owner := State{Source: source, SourceMode: SourceOwned}
				if err := owner.FinishSources(nil); err != nil {
					t.Fatal("already closed owner cleanup repeated", err)
				}
			case "detach":
				detachQuerySource(source)
			}
			if source.projectionPending || source.projectionFields != 0 || !reflect.DeepEqual(backing[1], sqltype.Value{}) ||
				!reflect.DeepEqual(backing[0], prefix) || len(source.Flat) != 1 || source.FlatCount != 1 ||
				closes != 1 || inner.Evaluation.Context != nil || inner.Source != nil {
				t.Fatal("actual owner abandonment retained values/scratch, lost prefix or repeated cleanup", closes)
			}
		})
	}
}

func TestViewProducerPendingChildExpiry(t *testing.T) {
	for _, cte := range []bool{false, true} {
		name := "source"
		if cte {
			name = "cte"
		}
		t.Run(name, func(t *testing.T) {
			source, state, probe := viewProducerFixture(t, false)
			defer source.Close()
			schema := source.NestedRoot.Schema()
			budget := &sampleBudget{start: time.Now(), millis: 0}
			inner := Stage{
				Source: &Source{Materialized: true}, Columns: schema, Expressions: []expr.Expr{probe},
				Produce: func(context.Context, *Stage) (bool, error) { return false, budget },
			}
			if cte {
				producer := CTEProducer{Opened: true, Source: source,
					project: source.NestedRoot.(*plan.ProjectNode), Inner: inner}
				defer producer.close()
				if present, err := producer.Ensure(context.Background(), 0); present || err != budget {
					t.Fatal("CTE child private owner changed", present, err)
				}
				if producer.Closed || producer.Inner.Position != 0 {
					t.Fatal("CTE child expiry consumed a nonexistent occurrence")
				}
			} else {
				source.NestedStage = &inner
				reader, err := OpenSourceStage(context.Background(), state, source, schema)
				if err != nil {
					t.Fatal(err)
				}
				defer reader.Close()
				if _, present, err := reader.NextOccurrence(context.Background()); present || err != budget {
					t.Fatal("source child private owner changed", present, err)
				}
				if inner.Closed || inner.Position != 0 {
					t.Fatal("source child expiry consumed a nonexistent occurrence")
				}
			}
			if source.projectionPending || source.projectionFields != 0 || source.FlatCount != 0 || len(source.Flat) != 0 || probe.calls != 0 {
				t.Fatal("child expiry invented a pending projected occurrence")
			}
		})
	}
}
