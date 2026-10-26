package core_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// Instrument the real string algorithm rather than returning canned partials.
// The coordinator still owns partition scans, grouping, codec transport, merge,
// finalization and all cancellation/resource exits.
type aggregateCombineProbe struct {
	provider functions.AggregateProvider
	advance  func(context.Context, any, []sqltype.Value, expr.Context) error
	phase    func(string)
	finals   atomic.Int64
	encodes  atomic.Int64
	decodes  atomic.Int64
	combines atomic.Int64
}

func (p *aggregateCombineProbe) ResultUsage() sqltype.Usage            { return p.provider.ResultUsage() }
func (p *aggregateCombineProbe) Init(ctx context.Context) (any, error) { return p.provider.Init(ctx) }
func (p *aggregateCombineProbe) PartialCapable() bool {
	return p.provider.(functions.PartialAggregateProvider).PartialCapable()
}
func (p *aggregateCombineProbe) Advance(ctx context.Context, state any, values []sqltype.Value, evaluation expr.Context) error {
	if p.advance != nil {
		if err := p.advance(ctx, state, values, evaluation); err != nil {
			return err
		}
	}
	return p.provider.Advance(ctx, state, values, evaluation)
}
func (p *aggregateCombineProbe) Finalize(ctx context.Context, state any) (sqltype.Value, error) {
	p.finals.Add(1)
	if p.phase != nil {
		p.phase("finalize")
	}
	return p.provider.Finalize(ctx, state)
}
func (p *aggregateCombineProbe) Combine(ctx context.Context, left, right any, evaluation expr.Context) error {
	p.combines.Add(1)
	if p.phase != nil {
		p.phase("combine")
	}
	return p.provider.(functions.PartialAggregateProvider).Combine(ctx, left, right, evaluation)
}
func (p *aggregateCombineProbe) Serialize(ctx context.Context, state any) ([]byte, bool, error) {
	p.encodes.Add(1)
	if p.phase != nil {
		p.phase("serialize")
	}
	return p.provider.(functions.AggregateStateTransport).Serialize(ctx, state)
}
func (p *aggregateCombineProbe) Deserialize(ctx context.Context, data []byte, present bool) (any, error) {
	p.decodes.Add(1)
	if p.phase != nil {
		p.phase("deserialize")
	}
	return p.provider.(functions.AggregateStateTransport).Deserialize(ctx, data, present)
}

func aggregateCombineParallelFixture(t *testing.T) (*core.Engine, *plan.Query, *exec.Source, *plan.AggregateNode, *aggregateCombineProbe) {
	t.Helper()
	e, query, source := queryCoreFixture(t, "SELECT string_agg(id::text,','::text) AS v FROM t")
	n, eligible := exec.ParallelAggregatePlan(query.Root, source)
	if !eligible {
		t.Fatal("supported string aggregate not eligible")
	}
	probe := &aggregateCombineProbe{provider: n.Calls[0].Provider}
	n.Calls[0].Provider = probe
	return e, query, source, n, probe
}

func aggregateCombineString(t *testing.T, rows exec.RowResult) string {
	t.Helper()
	defer rows.Close()
	values := make([]sqltype.Value, len(rows.Schema()))
	present, err := rows.Next(context.Background(), values)
	if err != nil || !present {
		t.Fatal(present, err)
	}
	text, err := values[0].Text()
	if err != nil {
		t.Fatal(err)
	}
	present, err = rows.Next(context.Background(), values)
	if err != nil || present {
		t.Fatal(present, err)
	}
	return text
}

func aggregateCombineConcurrent(t *testing.T, mutate bool) {
	t.Helper()
	e, query, source, _, probe := aggregateCombineParallelFixture(t)
	entered := make(chan *exec.State, 2)
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	var mu sync.Mutex
	states := map[*exec.State]any{}
	seen := map[string]int{}
	probe.advance = func(ctx context.Context, state any, values []sqltype.Value, evaluation expr.Context) error {
		if !exec.InParallelWorker(ctx) {
			return errors.New("partial transition lacks worker marker")
		}
		c := evaluation.(*exec.Context)
		v, err := values[0].Text()
		if err != nil {
			return err
		}
		mu.Lock()
		_, exists := states[c.Query]
		states[c.Query] = state
		seen[v]++
		mu.Unlock()
		if !exists {
			select {
			case entered <- c.Query:
			case <-ctx.Done():
				return ctx.Err()
			}
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}
	ctx, _, join := parallelTestStart(t, func(ctx context.Context) parallelTestOutcome {
		rows, count, err := exec.FinalizeSelect(ctx, query.Root, source, nil, 2)
		return parallelTestOutcome{rows: rows, count: count, err: err}
	})
	var workers []*exec.State
	for range 2 {
		select {
		case worker := <-entered:
			workers = append(workers, worker)
		case <-ctx.Done():
			t.Fatal("two actual workers not admitted", ctx.Err())
		}
	}
	if workers[0] == workers[1] || workers[0].Source == workers[1].Source || workers[0].Source.View == workers[1].Source.View {
		t.Fatal("shared mutable worker ownership")
	}
	if mutate {
		enginetest.Run(t, e, "INSERT INTO t VALUES(5,5,TRUE)")
	}
	releaseOnce.Do(func() { close(release) })
	out := join()
	if out.err != nil || out.count != 1 {
		t.Fatal(out.count, out.err)
	}
	if got := aggregateCombineString(t, out.rows); got != "1,2,3,4" {
		t.Fatal(got)
	}
	mu.Lock()
	if !reflect.DeepEqual(seen, map[string]int{"1": 1, "2": 1, "3": 1, "4": 1}) || len(states) != 2 {
		t.Error(seen, len(states))
	}
	mu.Unlock()
	if probe.finals.Load() != 1 || probe.encodes.Load() != 2 || probe.decodes.Load() != 2 || probe.combines.Load() != 2 {
		t.Fatal("not actual partial/codec/leader finalization", probe.finals.Load(), probe.encodes.Load(), probe.decodes.Load(), probe.combines.Load())
	}
	if mutate {
		captured := enginetest.Run(t, e, "SELECT * FROM t").Rows.(*exec.FinalRows)
		fresh, err := exec.CaptureSource(context.Background(), captured.Source.View, captured.Source.Ref)
		captured.Close()
		if err != nil {
			t.Fatal(err)
		}
		probe.advance = nil
		rows, _, err := exec.FinalizeSelect(context.Background(), query.Root, fresh, nil, 2)
		if err != nil {
			t.Fatal(err)
		}
		if got := aggregateCombineString(t, rows); got != "1,2,3,4,5" {
			t.Fatal("fresh frontier", got)
		}
	}
}

func TestAggregateCombineConcurrentExecution(t *testing.T) { aggregateCombineConcurrent(t, false) }
func TestAggregateCombineOwnership(t *testing.T) {
	aggregateCombineConcurrent(t, true)
	t.Run("builtin_string_support", aggregateCombineStringOwnerAuthority)
}

func TestAggregateCombineEligibility(t *testing.T) {
	_, query, source, n, probe := aggregateCombineParallelFixture(t)
	if _, ok := exec.ParallelAggregatePlan(query.Root, source); !ok {
		t.Fatal("eligible real codec lost")
	}
	original := n.Calls[0]
	n.Calls[0].Order = []plan.SortKey{{}}
	if _, ok := exec.ParallelAggregatePlan(query.Root, source); ok {
		t.Fatal("ordered partial admitted")
	}
	n.Calls[0] = original
	n.Sets = [][]int{{}, {}}
	if _, ok := exec.ParallelAggregatePlan(query.Root, source); ok {
		t.Fatal("grouping sets admitted")
	}
	n.Sets = nil
	n.Calls[0].Provider = functions.NewStringAggregateProvider(probe.ResultUsage())
	project := query.Root.(*plan.ProjectNode)
	old := project.Expressions[0]
	project.Expressions[0] = queryProbe(old.ResultUsage(), func(*exec.Context) (sqltype.Value, error) { return sqltype.Value{}, nil })
	if _, ok := exec.ParallelAggregatePlan(query.Root, source); ok {
		t.Fatal("unknown surrounding evaluator admitted")
	}
	e := engine.New()
	enginetest.Run(t, e, "CREATE DOMAIN ac_unit_domain AS bigint CHECK(VALUE>=0)")
	enginetest.Run(t, e, "CREATE FUNCTION ac_domain_step(ac_unit_domain,integer) RETURNS ac_unit_domain LANGUAGE SQL AS $$SELECT (coalesce($1,0)+$2)::ac_unit_domain$$")
	enginetest.Run(t, e, "CREATE FUNCTION ac_domain_merge(ac_unit_domain,ac_unit_domain) RETURNS ac_unit_domain LANGUAGE SQL AS $$SELECT ($1+$2)::ac_unit_domain$$")
	enginetest.Run(t, e, "CREATE AGGREGATE ac_domain(integer) (SFUNC=ac_domain_step,STYPE=ac_unit_domain,COMBINEFUNC=ac_domain_merge,INITCOND='10',PARALLEL=SAFE)")
	_, domain, _, _ := aggregateCombineBindUnit(t, e, "SELECT ac_domain(x) AS v FROM(VALUES(1),(2)) s(x)")
	if !domain.StateUsage.Type.IsDomain() || domain.CombinePlan == nil || domain.PartialCapable() {
		t.Fatal("domain initializer escaped serial fallback")
	}
}

func aggregateCombineFailure(t *testing.T, primary bool) {
	t.Helper()
	_, query, source, _, probe := aggregateCombineParallelFixture(t)
	entered := make(chan struct{}, 2)
	failure := errors.New("actual partial transition sentinel")
	probe.advance = func(ctx context.Context, _ any, values []sqltype.Value, _ expr.Context) error {
		entered <- struct{}{}
		if primary {
			v, _ := values[0].Text()
			if v == "1" {
				select {
				case <-entered:
				case <-ctx.Done():
					return ctx.Err()
				}
				select {
				case <-entered:
				case <-ctx.Done():
					return ctx.Err()
				}
				return failure
			}
		}
		<-ctx.Done()
		return ctx.Err()
	}
	ctx, cancel, join := parallelTestStart(t, func(ctx context.Context) parallelTestOutcome {
		rows, count, err := exec.FinalizeSelect(ctx, query.Root, source, nil, 2)
		return parallelTestOutcome{rows: rows, count: count, err: err}
	})
	if !primary {
		for range 2 {
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		}
		cancel()
	}
	out := join()
	want := error(context.Canceled)
	if primary {
		want = failure
	}
	if out.rows != nil || out.count != 0 || !errors.Is(out.err, want) {
		t.Fatal(out.rows, out.count, out.err)
	}
	if probe.finals.Load() != 0 || probe.combines.Load() != 0 {
		t.Fatal("failed partial published final state")
	}
}

func TestAggregateCombineCancellation(t *testing.T) {
	aggregateCombineFailure(t, false)
	for _, phase := range []string{"serialize", "deserialize", "combine", "finalize"} {
		t.Run(phase, func(t *testing.T) {
			_, query, source, _, probe := aggregateCombineParallelFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var reached atomic.Bool
			probe.phase = func(current string) {
				if current == phase {
					reached.Store(true)
					cancel()
				}
			}
			rows, count, err := exec.FinalizeSelect(ctx, query.Root, source, nil, 2)
			if rows != nil || count != 0 || !errors.Is(err, context.Canceled) || !reached.Load() {
				t.Fatal(rows, count, err, reached.Load())
			}
		})
	}
}
func TestAggregateCombineErrorIdentity(t *testing.T) { aggregateCombineFailure(t, true) }
