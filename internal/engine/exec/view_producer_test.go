package exec

import (
	"context"
	"errors"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
	"testing"
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
