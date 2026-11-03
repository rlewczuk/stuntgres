package enginetests_test

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

const timeTestMillis = 100.0

// Real elapsed work inside a production-demanded expression, not a substituted
// clock or rows-per-millisecond oracle. Start only after nested demand begins.
func timeWorkPastBudget(ctx context.Context) error {
	timer := time.NewTimer(110 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func timeBoundSource(t *testing.T) (*plan.ProjectNode, *exec.Source) {
	t.Helper()
	e := engine.New()
	enginetest.Run(t, e, "CREATE EXTENSION tsm_system_time")
	query, captured := enginetest.QueryFixture(t, e, "SELECT * FROM t TABLESAMPLE system_time(100)")
	rows := make([][]sqltype.Value, 20000)
	for i := range rows {
		rows[i] = captured.Row(i % captured.Count())
	}
	return query.Root.(*plan.ProjectNode), &exec.Source{Ref: captured.Ref, Anonymous: rows}
}

func timeArgument(t *testing.T, leaf *plan.SourceNode, millis float64) *plan.SourceNode {
	t.Helper()
	node, sample := *leaf, *leaf.Sample
	value, err := sqltype.Float(sample.Argument.ResultUsage().Type, millis)
	if err != nil {
		t.Fatal(err)
	}
	sample.Argument = plan.LiteralExpr{Info: expr.Info{Usage: sample.Argument.ResultUsage()}, Value: value}
	node.Sample = &sample
	return &node
}

// The scalar borrows an actual producing capture. After finite private unwind,
// another borrowed reader must resume that same source-owned frontier, not skip
// its pending NULL tuple or replay a field whose successful effect completed.
func TestSystemTimeProducingSourceContinuation(t *testing.T) {
	for _, mode := range []string{"resume", "pending_close", "pending_error", "pending_cancel"} {
		t.Run(mode, func(t *testing.T) {
			base, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			f := timeNewLoopFixture(t, base, "SELECT *, (SELECT bool_and(f) AND bool_or(f) FROM t) FROM t TABLESAMPLE system_time(100)")
			timeAssertNativeQuartets(t, f.rows)
			policy := f.project.Expressions[3].(plan.SubqueryExpr)
			f.install(policy, true)
			defer f.program.Close()
			calls := [3]int{}
			primary := errors.New("genuine pending producing-source expression failure")
			cleanup := errors.New("secondary pending producing-source cleanup failure")
			resuming := false
			var resumeCancel context.CancelFunc
			for field, original := range f.producer.Expressions {
				f.producer.Expressions[field] = enginetest.QueryProbe(original.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) {
					value, err := original.Eval(c)
					if err != nil {
						return sqltype.Value{}, err
					}
					calls[field]++
					if field == 0 && calls[field] == 2 {
						if err := timeWorkPastBudget(base); err != nil {
							return sqltype.Value{}, err
						}
					}
					if field == 1 && resuming {
						if mode == "pending_error" {
							return sqltype.Value{}, primary
						}
						if mode == "pending_cancel" {
							resumeCancel()
						}
					}
					return value, nil
				})
			}
			finish := f.producer.Finish
			f.producer.Finish = func() error {
				err := finish()
				if resuming && (mode == "pending_error" || mode == "pending_cancel") {
					return cleanup
				}
				return err
			}
			stage, err := exec.OpenNode(base, &exec.State{Source: f.raw}, f.leaf)
			defer stage.Close()
			if err != nil {
				t.Fatal("private producing-source unwind escaped as public error", err)
			}
			if calls != [3]int{2, 1, 1} {
				t.Fatal("real second-tuple first-field gate missed or evaluated later fields", calls)
			}
			if f.input.FlatCount != 1 || len(f.input.Flat) != 3 || f.producer.Evaluation.Context != nil {
				t.Error("producing-source unwind published a partial tuple or retained context", f.input.FlatCount, len(f.input.Flat))
			}
			f.assertRetained(t, &stage, true)
			published := len(f.input.Flat)
			abandoned := f.input.Flat[:cap(f.input.Flat)]
			if mode != "pending_close" {
				live, stop := context.WithCancel(base)
				defer stop()
				resuming, resumeCancel = true, stop
				reader, openErr := exec.OpenSourceStage(live, &exec.State{SourceMode: exec.SourceBorrowed}, f.input, f.leaf.Schema())
				if openErr != nil {
					t.Fatal("same capture could not reopen a live borrowed reader", openErr)
				}
				defer reader.Close()
				if reader.Source != f.input || f.input.NestedStage != &f.producer || f.closes != 0 {
					t.Error("borrowed reopen replaced or closed the original producer")
				}
				row := make([]sqltype.Value, 3)
				if mode == "pending_error" || mode == "pending_cancel" {
					present, readErr := reader.Next(live, row)
					if readErr != nil || !present || !reflect.DeepEqual(row, f.rows[0]) {
						t.Error("pending terminal witness lost the published original prefix", present, readErr, row)
					}
					present, readErr = reader.Next(live, row)
					want := error(primary)
					if mode == "pending_cancel" {
						want = context.Canceled
					}
					if present || !errors.Is(readErr, want) || errors.Is(readErr, cleanup) || !f.producer.Closed || f.closes != 1 {
						t.Error("pending source failure/cancel lost primary cause or cleanup", present, readErr, f.producer.Closed, f.closes)
					}
					if calls != [3]int{2, 2, 1} || f.input.FlatCount != 1 || len(f.input.Flat) != 3 {
						t.Error("pending source failure replayed effects or published incomplete tuple", calls, f.input.FlatCount, len(f.input.Flat))
					}
				} else {
					for i, original := range f.rows {
						present, readErr := reader.Next(live, row)
						if readErr != nil || !present {
							t.Errorf("same producing owner lost original occurrence %d: present=%v err=%v", i, present, readErr)
							break
						}
						if !reflect.DeepEqual(row, original) {
							t.Errorf("same producing owner resumed with wrong original tuple %d: got %v want %v", i, row, original)
						}
					}
					present, readErr := reader.Next(live, row)
					if readErr != nil || present || f.input.FlatCount != len(f.rows) || len(f.input.Flat) != 3*len(f.rows) {
						t.Error("same producing owner changed remaining multiplicity or EOF", present, readErr, f.input.FlatCount, len(f.input.Flat))
					}
					if calls != [3]int{len(f.rows), len(f.rows), len(f.rows)} {
						t.Error("same producing owner skipped or replayed completed expression effects", calls, len(f.rows))
					}
					f.assertRetained(t, &stage, true)
				}
				if err := reader.Close(); err != nil || mode == "resume" && (f.producer.Closed || f.closes != 0) {
					t.Error("borrowed reader closed its retained producing owner", err, f.closes)
				}
			}
			timeAssertNativeQuartets(t, f.rows)
			f.close(t, &stage)
			if mode != "resume" {
				timeAssertAbandonedTail(t, abandoned, published)
			}
			if f.producer.Evaluation.Context != nil || f.producer.Expressions != nil || f.producer.Source != nil {
				t.Error("closed pending producing owner retained invocation scratch")
			}
		})
	}
}

func TestSystemTimeElapsedBudget(t *testing.T) {
	project, raw := timeBoundSource(t)
	leaf := project.Child.(*plan.SourceNode)
	accepted, argument := leaf.Sample, leaf.Sample.Argument
	for _, millis := range []float64{timeTestMillis, math.Inf(1), timeTestMillis} {
		calls, closes := 0, 0
		inner := exec.Stage{
			Source: raw, Columns: project.Schema(),
			Evaluation: exec.Context{EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}},
			Finish: func() error { closes++; return nil },
		}
		inner.Expressions = append([]expr.Expr(nil), project.Expressions...)
		inner.Expressions[0] = enginetest.QueryProbe(project.Schema()[0].Usage, func(c *exec.Context) (sqltype.Value, error) {
			calls++
			if calls == 2 && !math.IsInf(millis, 1) {
				if err := timeWorkPastBudget(c.Context); err != nil {
					return sqltype.Value{}, err
				}
			}
			return c.Row[0], nil
		})
		// Child caches and source backing must survive cooperative unwind.
		inner.CacheWidth = 1
		inner.OrderValues = make([]sqltype.Value, raw.Count())
		inner.CacheOccurrences = make([]int, raw.Count())
		for i := range raw.Count() {
			inner.OrderValues[i], inner.CacheOccurrences[i] = raw.Row(i)[0], i
		}
		cache := &inner.OrderValues[0]
		occurrences := &inner.CacheOccurrences[0]
		bridge := &exec.Source{Ref: raw.Ref, NestedRoot: project, NestedInputs: raw, NestedStage: &inner, Materialized: true, FlatWidth: len(project.Schema())}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		stage, err := exec.OpenNode(ctx, &exec.State{Source: bridge}, timeArgument(t, leaf, millis))
		cancel()
		if err != nil {
			_ = stage.Close()
			t.Fatal(err)
		}
		defer stage.Close()
		if math.IsInf(millis, 1) {
			if stage.Count() != raw.Count() || calls != raw.Count() {
				t.Fatal("Infinity did not exhaust genuine finite input", stage.Count(), calls)
			}
		} else if calls >= raw.Count() || stage.Count() < 1 {
			t.Fatal("elapsed scan drained tail or lost accepted prefix", stage.Count(), calls)
		}
		if inner.Closed || inner.Source != raw || closes != 0 || &inner.OrderValues[0] != cache || &inner.CacheOccurrences[0] != occurrences || inner.CacheWidth != 1 {
			t.Fatal("budget EOF terminalized retained producer/cache")
		}
		if len(bridge.Flat) != bridge.FlatCount*bridge.FlatWidth {
			t.Fatal("budget unwind retained an incomplete projected row")
		}
		for i := range stage.Count() {
			if stage.Source.Row(stage.Index(i))[0] != raw.Row(i)[0] {
				t.Fatal("retained prefix lost original values")
			}
		}
		if err := stage.Close(); err != nil || closes != 1 || inner.Source != nil || inner.OrderValues != nil || stage.Finish != nil || stage.Reset != nil {
			t.Fatal("child not released exactly once", err, closes)
		}
		if err := stage.Close(); err != nil || closes != 1 {
			t.Fatal("duplicate child close", err, closes)
		}
		if leaf.Sample != accepted || !reflect.DeepEqual(leaf.Sample.Argument, argument) {
			t.Fatal("clock changed accepted plan")
		}
	}
}

func TestSystemTimeNestedDeadline(t *testing.T) {
	for _, prefix := range []bool{false, true} {
		t.Run(map[bool]string{false: "all_rejected", true: "accepted_prefix"}[prefix], func(t *testing.T) {
			project, raw := timeBoundSource(t)
			leaf := project.Child.(*plan.SourceNode)
			boolean := raw.Row(0)[2].Type()
			calls := 0
			policy := enginetest.QueryProbe(boolean.Usage(), func(c *exec.Context) (sqltype.Value, error) {
				calls++
				if prefix && calls == 1 {
					return sqltype.Boolean(boolean, true), nil
				}
				if err := timeWorkPastBudget(c.Context); err != nil {
					return sqltype.Value{}, err
				}
				return sqltype.Boolean(boolean, false), nil
			})
			raw.RLS = &exec.RLSProgram{Volatile: true, Groups: []exec.RLSRuntimeGroup{{Permissive: []exec.RLSRuntimeClause{{Expression: policy}}}}}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			stage, err := exec.OpenNode(ctx, &exec.State{Source: raw}, leaf)
			if err != nil {
				t.Fatal("nested budget escaped as statement failure", err)
			}
			defer stage.Close()
			if calls == 0 || calls >= raw.Count() {
				t.Fatal("rejected tail was not cooperatively bounded", calls)
			}
			if !prefix && stage.Count() != 0 || prefix && stage.Count() < 1 {
				t.Fatal("wrong accepted prefix", stage.Count())
			}
			if stage.Source != raw || stage.Closed || stage.Terminal != nil {
				t.Fatal("successful budget EOF lost source backing")
			}
			if prefix && stage.Source.Row(stage.Index(0))[0] != raw.Row(0)[0] {
				t.Fatal("accepted prefix identity lost")
			}
			if err := stage.Close(); err != nil || stage.Source != nil || stage.Selection != nil {
				t.Fatal("budget EOF cleanup", err)
			}
		})
	}
	t.Run("physical_source_budgets", timePhysicalDeadline)
	t.Run("nested_sample_owners", timeNestedSampleOwners)
}

// The aggregate is demanded inside RLS, not a final aggregate after sampling.
// Its argument probe gates real bound aggregate work without replacing the
// executor, provider, monotonic clock, or private budget checkpoints.
func TestSystemTimeNestedAggregateDeadline(t *testing.T) {
	for _, mode := range []string{"all_rejected", "accepted_prefix", "expired_cancel", "argument_failure", "infinity", "borrowed_producer"} {
		t.Run(mode, func(t *testing.T) {
			prefix := mode == "accepted_prefix" || mode == "borrowed_producer"
			e := engine.New()
			enginetest.Run(t, e, "CREATE EXTENSION tsm_system_time")
			query, captured := enginetest.QueryFixture(t, e, "SELECT *, (SELECT bool_and(f) AND bool_or(f) FROM t) FROM t TABLESAMPLE system_time(100)")
			project := query.Root.(*plan.ProjectNode)
			leaf := project.Child.(*plan.SourceNode)
			subquery, ok := project.Expressions[3].(plan.SubqueryExpr)
			if !ok {
				t.Fatal("policy fixture did not bind a scalar subquery", project.Expressions[3])
			}
			aggregate := subquery.QueryPlan.Root.(*plan.ProjectNode).Child.(*plan.AggregateNode)
			if len(aggregate.Calls) != 2 || len(aggregate.Calls[0].Arguments) != 1 || len(aggregate.Calls[1].Arguments) != 1 {
				t.Fatal("policy fixture did not retain both bound aggregate arguments")
			}
			rows := make([][]sqltype.Value, 20000)
			for i := range rows {
				rows[i] = captured.Row(i % captured.Count())
			}
			raw := &exec.Source{Ref: captured.Ref, Anonymous: rows}
			// The policy's captured relation is distinct from the RLS occurrence,
			// so demanding the genuine subquery cannot recurse into its own policy.
			inputRows := rows
			if mode == "borrowed_producer" {
				inputRows = rows[:64]
			}
			input := &exec.Source{Ref: captured.Ref, Anonymous: inputRows}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			primary := errors.New("genuine nested aggregate argument failure")
			cleanup := errors.New("secondary nested producer cleanup failure")
			argumentCalls, secondArgumentCalls, policyCalls, closes := 0, 0, 0, 0
			argument := aggregate.Calls[0].Arguments[0]
			aggregate.Calls[0].Arguments[0] = enginetest.QueryProbe(argument.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) {
				value, err := argument.Eval(c)
				if err != nil {
					return sqltype.Value{}, err
				}
				argumentCalls++
				if argumentCalls == 1 && mode != "infinity" {
					// The real clock started before this first argument was
					// demanded. No worker or synthetic clock advances it.
					if err := timeWorkPastBudget(c.Context); err != nil {
						return sqltype.Value{}, err
					}
					if mode == "expired_cancel" {
						cancel()
					}
					if mode == "argument_failure" {
						return sqltype.Value{}, primary
					}
				}
				// Return a successful typed value after the gate: the aggregate
				// must checkpoint before invoking the next call in this row.
				return value, nil
			})
			secondArgument := aggregate.Calls[1].Arguments[0]
			aggregate.Calls[1].Arguments[0] = enginetest.QueryProbe(secondArgument.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) {
				secondArgumentCalls++
				return secondArgument.Eval(c)
			})
			boolean := subquery.ResultUsage().Type
			policy := enginetest.QueryProbe(subquery.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) {
				policyCalls++
				if prefix && policyCalls == 1 {
					return sqltype.Boolean(boolean, true), nil
				}
				firstAggregateRow := 1
				if prefix {
					firstAggregateRow = 2
				}
				if policyCalls == firstAggregateRow {
					return subquery.Eval(c)
				}
				// Only one scalar aggregate invocation is needed for this
				// witness, including the exhaustive Infinity control.
				return sqltype.Boolean(boolean, false), nil
			})
			program := &exec.RLSProgram{Volatile: true, Groups: []exec.RLSRuntimeGroup{{Permissive: []exec.RLSRuntimeClause{{
				Expression: policy, State: exec.State{Source: input, SourceMode: exec.SourceBorrowed},
			}}}}}
			defer program.Close()
			raw.RLS = program
			plainLeaf := *leaf
			plainLeaf.Sample = nil
			innerProject := *project
			innerProject.QuerySchema = leaf.Schema()
			innerProject.Child = &plainLeaf
			innerProject.Expressions = project.Expressions[:3]
			// A policy capture can itself own a producing source. The borrowed
			// aggregate must leave that reached producer alive across budget EOF.
			inputCloses := 0
			var capturedStage exec.Stage
			if mode == "borrowed_producer" {
				capturedInput := *input
				var err error
				capturedStage, err = exec.OpenNode(ctx, &exec.State{Source: &capturedInput}, &innerProject)
				if err != nil {
					t.Fatal(err)
				}
				finish := capturedStage.Finish
				capturedStage.Finish = func() error {
					inputCloses++
					if finish != nil {
						return finish()
					}
					return nil
				}
				*input = exec.Source{Ref: captured.Ref, NestedRoot: &innerProject, NestedInputs: &capturedInput,
					NestedStage: &capturedStage, Materialized: true, FlatWidth: len(leaf.Schema())}
			}
			inner, err := exec.OpenNode(ctx, &exec.State{Source: raw}, &innerProject)
			if err != nil {
				t.Fatal(err)
			}
			finish := inner.Finish
			inner.Finish = func() error {
				closes++
				if finish != nil {
					if err := finish(); err != nil {
						return err
					}
				}
				if mode == "expired_cancel" || mode == "argument_failure" {
					return cleanup
				}
				return nil
			}
			bridge := &exec.Source{
				Ref: raw.Ref, NestedRoot: &innerProject, NestedInputs: raw,
				NestedStage: &inner, Materialized: true, FlatWidth: len(leaf.Schema()),
			}
			defer bridge.Close()
			sample := leaf
			if mode == "infinity" {
				sample = timeArgument(t, leaf, math.Inf(1))
			}
			stage, err := exec.OpenNode(ctx, &exec.State{Source: bridge}, sample)
			defer stage.Close()
			if mode == "expired_cancel" || mode == "argument_failure" {
				want := error(context.Canceled)
				if mode == "argument_failure" {
					want = primary
				}
				if !errors.Is(err, want) || errors.Is(err, cleanup) || argumentCalls != 1 || secondArgumentCalls != 0 {
					t.Fatal("nested aggregate failure/cancellation became budget EOF or cleanup error", err, argumentCalls, secondArgumentCalls)
				}
				if stage.Source != nil || stage.Selection != nil || !inner.Closed || inner.Source != nil || closes != 1 {
					t.Fatal("failed aggregate scan retained its producer", closes)
				}
			} else {
				if err != nil {
					t.Fatal("nested aggregate budget escaped as statement failure", err)
				}
				if argumentCalls == 0 || mode != "infinity" && argumentCalls >= len(inputRows) {
					t.Errorf("nested aggregate consumed its remaining input after budget expiry: evaluated %d of %d arguments", argumentCalls, len(inputRows))
				}
				// A row-boundary-only checkpoint is insufficient: the next
				// aggregate call in this same row is also cooperative SQL work.
				if mode != "infinity" && secondArgumentCalls != 0 {
					t.Error("nested aggregate continued to a second call after budget expiry", secondArgumentCalls)
				}
				if mode == "infinity" && (argumentCalls != len(rows) || secondArgumentCalls != len(rows) || policyCalls != len(rows)) {
					t.Error("Infinity did not exhaust genuine aggregate and RLS input", argumentCalls, secondArgumentCalls, policyCalls)
				}
				if prefix {
					if stage.Count() < 1 {
						t.Error("nested aggregate budget lost an already accepted prefix")
					}
					for i := range stage.Count() {
						if !reflect.DeepEqual(stage.Source.Row(stage.Index(i)), raw.Row(i)) {
							t.Error("retained prefix lost original visible values", i)
						}
					}
				} else if stage.Count() != 0 {
					t.Error("all-rejected aggregate policy exposed a row", stage.Count())
				}
				if stage.Source != bridge || stage.Closed || stage.Terminal != nil || inner.Closed || inner.Source != raw || closes != 0 {
					t.Error("normal aggregate budget EOF terminalized retained backing", closes)
				}
				if len(bridge.Flat) != bridge.FlatCount*bridge.FlatWidth {
					t.Error("aggregate unwind retained an incomplete projected row")
				}
			}
			clause := &program.Groups[0].Permissive[0]
			if clause.Evaluation.Context != nil || clause.Evaluation.Query != nil || clause.State.Source != input {
				t.Error("policy retained an invocation context or lost its captured source owner")
			}
			if mode == "borrowed_producer" {
				if inputCloses != 0 || capturedStage.Closed || capturedStage.Terminal != nil || capturedStage.Source == nil || input.NestedStage != &capturedStage || input.FlatCount != len(inputRows) {
					t.Fatal("borrowed aggregate unwinding released its captured producer", inputCloses)
				}
				for i := range inputRows {
					if !reflect.DeepEqual(input.Row(i), inputRows[i]) {
						t.Fatal("borrowed producer lost its retained values", i)
					}
				}
			}
			if err := stage.Close(); err != nil || closes != 1 || !inner.Closed || inner.Source != nil || stage.Source != nil || stage.Finish != nil || stage.Reset != nil {
				t.Fatal("aggregate sample producer was not released exactly once", err, closes)
			}
			if err := stage.Close(); err != nil || closes != 1 {
				t.Fatal("aggregate sample closed its producer twice", err, closes)
			}
			if mode == "borrowed_producer" {
				program.Close()
				program.Close()
				if inputCloses != 1 || !capturedStage.Closed || capturedStage.Source != nil || input.NestedStage != nil {
					t.Fatal("capture owner did not release its producer exactly once", inputCloses)
				}
			}
		})
	}
}

func timeNestedSampleOwners(t *testing.T) {
	for _, outerFinite := range []bool{false, true} {
		project, raw := timeBoundSource(t)
		leaf := project.Child.(*plan.SourceNode)
		outerMillis, innerMillis := math.Inf(1), timeTestMillis
		if outerFinite {
			outerMillis, innerMillis = timeTestMillis, math.Inf(1)
		}
		innerProject := *project
		innerProject.Child = timeArgument(t, leaf, innerMillis)
		boolean := raw.Row(0)[2].Type()
		calls := 0
		policy := enginetest.QueryProbe(boolean.Usage(), func(c *exec.Context) (sqltype.Value, error) {
			calls++
			if err := timeWorkPastBudget(c.Context); err != nil {
				return sqltype.Value{}, err
			}
			return sqltype.Boolean(boolean, false), nil
		})
		program := &exec.RLSProgram{Groups: []exec.RLSRuntimeGroup{{Permissive: []exec.RLSRuntimeClause{{Expression: policy}}}}}
		raw.RLS = program
		bridge := &exec.Source{Ref: raw.Ref, NestedRoot: &innerProject, NestedInputs: raw, Materialized: true, FlatWidth: len(project.Schema())}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		stage, err := exec.OpenNode(ctx, &exec.State{Source: bridge}, timeArgument(t, leaf, outerMillis))
		cancel()
		if err != nil {
			t.Fatal("nested budget escaped its owning sampler", outerFinite, err)
		}
		if calls == 0 || calls >= raw.Count() || stage.Count() != 0 || stage.Source != bridge {
			_ = stage.Close()
			t.Fatal("nested sampler swallowed enclosing budget or drained tail", outerFinite, calls)
		}
		if program.Groups[0].Permissive[0].Evaluation.Context != nil {
			_ = stage.Close()
			t.Fatal("policy retained nested invocation context")
		}
		if err := stage.Close(); err != nil || stage.Source != nil || stage.Finish != nil || stage.Reset != nil {
			t.Fatal("nested owner cleanup", err)
		}
		if bridge.NestedStage != nil && !bridge.NestedStage.Closed {
			t.Fatal("nested sample retained a live child")
		}
		bridge.Close()
	}
}

func timePhysicalDeadline(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE EXTENSION tsm_system_time")
	query, _ := enginetest.QueryFixture(t, e, "SELECT * FROM t TABLESAMPLE system_time(100)")
	for _, sql := range []string{
		"CREATE TABLE time_deadline_child() INHERITS(t)",
		"CREATE TABLE time_deadline_other() INHERITS(t)",
		"INSERT INTO time_deadline_child VALUES(11,1,true),(12,2,true),(13,3,true),(14,4,true)",
		"INSERT INTO time_deadline_other VALUES(21,1,true),(22,2,true),(23,3,true),(24,4,true)",
	} {
		enginetest.Run(t, e, sql)
	}
	rows := enginetest.Run(t, e, "SELECT * FROM t").Rows.(*exec.FinalRows)
	defer rows.Close()
	source := rows.Source
	boolean := source.Row(0)[2].Type()
	for _, nested := range []bool{false, true} {
		seen := make(map[ids.ObjectRef]int)
		policy := enginetest.QueryProbe(boolean.Usage(), func(c *exec.Context) (sqltype.Value, error) {
			seen[c.PhysicalTable]++
			if err := timeWorkPastBudget(c.Context); err != nil {
				return sqltype.Value{}, err
			}
			return sqltype.Boolean(boolean, false), nil
		})
		source.RLS = &exec.RLSProgram{Groups: []exec.RLSRuntimeGroup{{Permissive: []exec.RLSRuntimeClause{{Expression: policy}}}}}
		input := source
		if nested {
			input = &exec.Source{Ref: source.Ref, Input: source, Layout: []int{0, 1, 2}, Scratch: make([]sqltype.Value, 3)}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		leaf := query.Root.(*plan.ProjectNode).Child.(*plan.SourceNode)
		stage, err := exec.OpenNode(ctx, &exec.State{Source: input}, leaf)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		if stage.Count() != 0 || len(seen) != len(source.Inheritance) {
			_ = stage.Close()
			t.Fatal("physical sources shared one expired budget", nested, seen, stage.Count())
		}
		for _, segment := range source.Inheritance {
			if seen[segment.Leaf.Ref] == 0 || seen[segment.Leaf.Ref] >= segment.Prefix.Count() {
				_ = stage.Close()
				t.Fatal("physical rejected tail fully consumed", nested, segment.Leaf.Ref, seen)
			}
		}
		if err := stage.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSystemTimeScanCancellation(t *testing.T) {
	for _, mode := range []string{"finite_cancel", "expired_cancel", "infinity_cancel", "policy_failure"} {
		t.Run(mode, func(t *testing.T) {
			project, raw := timeBoundSource(t)
			leaf := project.Child.(*plan.SourceNode)
			if mode == "infinity_cancel" {
				leaf = timeArgument(t, leaf, math.Inf(1))
			}
			boolean := raw.Row(0)[2].Type()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			admitted, release, joined := make(chan struct{}), make(chan struct{}), make(chan struct{})
			primary := errors.New("genuine policy failure")
			go func() {
				defer close(joined)
				select {
				case <-admitted:
				case <-ctx.Done():
					close(release)
					return
				}
				if mode == "expired_cancel" || mode == "policy_failure" {
					_ = timeWorkPastBudget(ctx)
				}
				if mode != "policy_failure" {
					cancel()
				}
				close(release)
			}()
			defer func() { cancel(); <-joined }()
			calls := 0
			policy := enginetest.QueryProbe(boolean.Usage(), func(c *exec.Context) (sqltype.Value, error) {
				calls++
				if calls == 1 {
					close(admitted)
				}
				guard := time.NewTimer(5 * time.Second)
				defer guard.Stop()
				select {
				case <-release:
				case <-guard.C:
					return sqltype.Value{}, errors.New("scan cancellation gate did not release")
				}
				if mode == "policy_failure" {
					return sqltype.Value{}, primary
				}
				return sqltype.Boolean(boolean, false), nil
			})
			raw.RLS = &exec.RLSProgram{Groups: []exec.RLSRuntimeGroup{{Permissive: []exec.RLSRuntimeClause{{Expression: policy}}}}}
			stage, err := exec.OpenNode(ctx, &exec.State{Source: raw}, leaf)
			want := error(context.Canceled)
			if mode == "policy_failure" {
				want = primary
			}
			if !errors.Is(err, want) || stage.Source != nil || stage.Selection != nil || calls == 0 || calls >= raw.Count() {
				t.Fatal("cancellation/failure became sample EOF", err, calls)
			}
			// A fresh invocation receives no expired/canceled state from its peer.
			raw.RLS = nil
			recovered, err := exec.OpenNode(context.Background(), &exec.State{Source: raw}, timeArgument(t, leaf, 1))
			if err != nil {
				t.Fatal("failed scan poisoned independent invocation", err)
			}
			_ = recovered.Close()
		})
	}
	t.Run("visibility_preparation", func(t *testing.T) {
		_, raw := timeBoundSource(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		// Err is the existing active visibility-loop cooperative seam, not a
		// production hook. The parent is genuinely canceled on that checkpoint.
		active := &timeVisibilityContext{Context: ctx, cancel: cancel}
		source := &exec.Source{Committed: []storage.RowVersion{{Row: 1, Version: 10, Born: 1}, {Row: 2, Version: 20, Born: 1}}, NeedsBirthFilter: true, Ref: raw.Ref}
		heap := accessMethodHeapOperationsUnit(t, engine.New().CatalogRead())
		selected, err := exec.SelectQuerySource(active, source, 1, nil, nil, heap)
		if !errors.Is(err, context.Canceled) || selected != nil || active.checks < 2 {
			t.Fatal("active visibility preparation ignored cancellation", selected, err, active.checks)
		}
	})
}

type timeVisibilityContext struct {
	context.Context
	cancel context.CancelFunc
	checks int
}

func (c *timeVisibilityContext) Err() error {
	c.checks++
	if c.checks == 2 {
		c.cancel()
	}
	return c.Context.Err()
}
