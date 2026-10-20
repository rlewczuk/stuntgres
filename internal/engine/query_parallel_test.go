package engine

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

type parallelTestOutcome struct {
	rows  RowResult
	count int
	err   error
}

// Every exit, including a failed assertion at a gate, cancels and joins the
// real executor before fixture cleanup. No timing-based concurrency assertion.
func parallelTestStart(t *testing.T, call func(context.Context) parallelTestOutcome) (context.Context, context.CancelFunc, func() parallelTestOutcome) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	done := make(chan parallelTestOutcome, 1)
	go func() { done <- call(ctx) }()
	var once sync.Once
	var out parallelTestOutcome
	join := func() parallelTestOutcome {
		once.Do(func() {
			select {
			case out = <-done:
			case <-time.After(6 * time.Second):
				t.Error("parallel executor did not join")
				out.err = context.DeadlineExceeded
			}
		})
		return out
	}
	t.Cleanup(func() {
		cancel()
		out := join()
		if out.rows != nil {
			out.rows.Close()
		}
	})
	return ctx, cancel, join
}

func parallelTestIDs(t *testing.T, rows RowResult) []int64 {
	t.Helper()
	defer rows.Close()
	values := make([]sqltype.Value, len(rows.Schema()))
	var ids []int64
	for {
		present, err := rows.Next(context.Background(), values)
		if err != nil {
			t.Fatal(err)
		}
		if !present {
			return ids
		}
		id, err := values[0].Integer()
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
}

func TestQueryParallelPartition(t *testing.T) {
	parallelTransactionOccurrences(t)
	for _, visible := range [][]int{nil, {}, {0}, {0, 1}, {3, 1, 0}, {0, 1, 2, 3}} {
		_, query, source := queryCoreFixture(t, "SELECT id FROM t")
		source.visible = visible
		want := []int64{1, 2, 3, 4}
		if visible != nil {
			want = make([]int64, len(visible))
			for i, index := range visible {
				want[i] = int64(index + 1)
			}
		}
		for _, ceiling := range []int{0, 1, 2, 7, 1024} {
			pin, err := parallelQuerySource(source)
			if err != nil {
				t.Fatal(err)
			}
			rows, count, err := finalizeSelectQuery(context.Background(), query.Root, pin, nil, ceiling)
			if err != nil || count != len(want) {
				t.Fatal(count, err)
			}
			ids := parallelTestIDs(t, rows)
			if len(ids) != len(want) {
				t.Fatal(ids, want)
			}
			seen := map[int64]int{}
			for _, id := range ids {
				seen[id]++
			}
			for _, id := range want {
				if seen[id] != 1 {
					t.Fatal(ids, want)
				}
				delete(seen, id)
			}
			if len(seen) != 0 {
				t.Fatal(seen)
			}
			if queryParallelWorkers(ceiling, len(want)) > min(ceiling, len(want)) {
				t.Fatal("ceiling exceeded")
			}
		}
	}
	// Logical visibility can select an own version while omitting an older head.
	_, query, source := queryCoreFixture(t, "SELECT id FROM t")
	source.own = []storage.RowVersion{source.committed[0]}
	source.own[0].Values = append([]sqltype.Value(nil), source.committed[0].Values...)
	source.own[0].Values[0], _ = sqltype.Integer(source.own[0].Values[0].Type(), 99)
	source.visible = []int{4, 2, 3}
	rows, count, err := finalizeSelectQuery(context.Background(), query.Root, source, nil, 2)
	if err != nil || count != 3 {
		t.Fatal(count, err)
	}
	seen := map[int64]int{}
	for _, id := range parallelTestIDs(t, rows) {
		seen[id]++
	}
	if !reflect.DeepEqual(seen, map[int64]int{99: 1, 3: 1, 4: 1}) {
		t.Fatal(seen)
	}
}

func TestQueryParallelConcurrentExecution(t *testing.T) {
	for _, phase := range []string{"target", "filter"} {
		t.Run(phase, func(t *testing.T) {
			_, query, source := queryCoreFixture(t, "SELECT id FROM t WHERE f")
			project, ordered, eligible := parallelQueryPlan(query.Root, source)
			if !eligible || ordered != nil {
				t.Fatal("ordinary scan not eligible")
			}
			entered := make(chan *queryState, 2)
			release := make(chan struct{})
			var mu sync.Mutex
			seen := map[int64]int{}
			workers := map[*queryState]bool{}
			gate := func(c *EvalContext) error {
				mu.Lock()
				first := !workers[c.query]
				workers[c.query] = true
				id, _ := c.Row[0].Integer()
				seen[id]++
				mu.Unlock()
				if first {
					entered <- c.query
					select {
					case <-release:
					case <-c.Context.Done():
						return c.Context.Err()
					}
				}
				return nil
			}
			if phase == "target" {
				project.expressions[0] = queryProbe(project.Schema()[0].Usage, func(c *EvalContext) (sqltype.Value, error) {
					if err := gate(c); err != nil {
						return sqltype.Value{}, err
					}
					return c.Row[0], nil
				})
			} else {
				filter := project.child.(*filterNode)
				filter.predicate = queryProbe(filter.predicate.ResultUsage(), func(c *EvalContext) (sqltype.Value, error) {
					if err := gate(c); err != nil {
						return sqltype.Value{}, err
					}
					return c.Row[2], nil
				})
			}
			ctx, _, join := parallelTestStart(t, func(ctx context.Context) parallelTestOutcome {
				r, n, e := finalizeParallelQuery(ctx, project, nil, source, nil, 2)
				return parallelTestOutcome{r, n, e}
			})
			states := make([]*queryState, 2)
			for i := range states {
				select {
				case states[i] = <-entered:
				case <-ctx.Done():
					t.Fatal("two actual evaluators did not enter", ctx.Err())
				}
			}
			if states[0] == states[1] || states[0].source == states[1].source || states[0].source.view == states[1].source.view || states[0].source.view == source.view {
				t.Fatal("worker state or pin shared")
			}
			if states[0].scanStart == states[1].scanStart || states[0].source.view.Data() != states[1].source.view.Data() {
				t.Fatal("ranges/frontiers")
			}
			close(release)
			out := join()
			if out.err != nil || out.count != 2 {
				t.Fatal(out.count, out.err)
			}
			for _, state := range states {
				if state.source.view != nil {
					t.Fatal("joined worker retained pin")
				}
			}
			ids := parallelTestIDs(t, out.rows)
			if !reflect.DeepEqual(ids, []int64{1, 4}) {
				t.Fatal(ids)
			}
			if phase == "target" && !reflect.DeepEqual(seen, map[int64]int{1: 1, 4: 1}) {
				t.Fatal(seen)
			}
			if phase == "filter" && !reflect.DeepEqual(seen, map[int64]int{1: 1, 2: 1, 3: 1, 4: 1}) {
				t.Fatal(seen)
			}
		})
	}
}

func TestQueryParallelSnapshot(t *testing.T) {
	e, query, source := queryCoreFixture(t, "SELECT id FROM t")
	project := query.Root.(*projectNode)
	entered := make(chan *queryState, 2)
	releases := []chan struct{}{make(chan struct{}), make(chan struct{})}
	project.expressions[0] = queryProbe(project.Schema()[0].Usage, func(c *EvalContext) (sqltype.Value, error) {
		id, _ := c.Row[0].Integer()
		if int(id) == c.query.scanStart+1 {
			entered <- c.query
			select {
			case <-releases[c.query.scanStart/2]:
			case <-c.Context.Done():
				return sqltype.Value{}, c.Context.Err()
			}
		}
		return c.Row[0], nil
	})
	// Exercise the same real worker executor separately so completion of worker0
	// deterministically establishes its release while worker1 remains in Eval.
	type workerOutcome struct {
		run queryParallelRun
		err error
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := []chan workerOutcome{make(chan workerOutcome, 1), make(chan workerOutcome, 1)}
	joined := []bool{false, false}
	defer func() {
		cancel()
		for i := range done {
			if !joined[i] {
				select {
				case <-done[i]:
				case <-time.After(6 * time.Second):
					t.Error("snapshot worker did not join")
				}
			}
		}
	}()
	for i := range 2 {
		go func(i int) {
			r, err := runParallelQuery(ctx, project, source, nil, i*2, (i+1)*2, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}, false)
			done[i] <- workerOutcome{r, err}
		}(i)
	}
	states := make([]*queryState, 2)
	for range 2 {
		select {
		case s := <-entered:
			states[s.scanStart/2] = s
		case <-ctx.Done():
			t.Fatal("workers did not enter")
		}
	}
	pins := []*visibility.View{states[0].source.view, states[1].source.view}
	if pins[0] == pins[1] || pins[0].Data() != source.view.Data() || pins[1].Data() != source.view.Data() {
		t.Fatal("pin/frontier isolation")
	}
	run(t, e, "UPDATE t SET id=9 WHERE id=1")
	run(t, e, "INSERT INTO t VALUES(5,5,TRUE)")
	close(releases[0])
	var first workerOutcome
	select {
	case first = <-done[0]:
		joined[0] = true
	case <-ctx.Done():
		t.Fatal("first worker did not complete")
	}
	if first.err != nil || pins[0].Load() != nil || pins[1].Load() == nil {
		t.Fatal("independent worker release", first.err)
	}
	close(releases[1])
	var second workerOutcome
	select {
	case second = <-done[1]:
		joined[1] = true
	case <-ctx.Done():
		t.Fatal("second worker did not complete")
	}
	if second.err != nil {
		t.Fatal(second.err)
	}
	rows, err := gatherQueryRuns(ctx, project, nil, source, []queryParallelRun{first.run, second.run}, nil, false, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	if got := parallelTestIDs(t, rows); !reflect.DeepEqual(got, []int64{1, 2, 3, 4}) {
		t.Fatal(got)
	}
	fresh := run(t, e, "SELECT id FROM t ORDER BY id")
	if got := parallelTestIDs(t, fresh.Rows); !reflect.DeepEqual(got, []int64{2, 3, 4, 5, 9}) {
		t.Fatal(got)
	}
}

func TestQueryParallelEligibility(t *testing.T) {
	parallelAdmissionAndTemporary(t)
	parallelSerialEntryProof(t)
	_, query, source := queryCoreFixture(t, "SELECT id FROM t WHERE v>=1 ORDER BY v+1 DESC NULLS LAST,id")
	project, ordered, ok := parallelQueryPlan(query.Root, source)
	if !ok || ordered == nil {
		t.Fatal("positive scalar scan rejected")
	}
	for _, mode := range []lock.Mode{lock.ForKeyShare, lock.ForShare, lock.ForNoKeyUpdate, lock.ForUpdate} {
		copy := *source
		copy.inherited = queryRowMark{mode: mode}
		if _, _, ok := parallelQueryPlan(project, &copy); ok {
			t.Fatal("inherited rowmark admitted", mode)
		}
		locked := *project
		locked.child = &rowLockNode{querySchema: project.child.Schema(), child: project.child, marks: []queryRowMark{{mode: mode}}, firstMode: mode}
		if _, _, ok := parallelQueryPlan(&locked, source); ok {
			t.Fatal("nested rowmark admitted", mode)
		}
	}
	for _, node := range []LogicalNode{&singletonNode{}, &valuesNode{}, &rowLockNode{}, &limitNode{}, &evaluationNode{}, &joinNode{}, &aggregateNode{}, &distinctNode{}, &distinctOnNode{}, &setNode{}, &windowNode{}, &derivedNode{}, &functionFromNode{}, &setProjectNode{}} {
		copy := *project
		copy.child = node
		if _, _, ok := parallelQueryPlan(&copy, source); ok {
			t.Fatalf("unsupported %T admitted", node)
		}
	}
	for _, mutate := range []func(*querySource){
		func(s *querySource) { s.locks = &queryLockContext{} }, func(s *querySource) { s.lineage = &queryLockLineage{} }, func(s *querySource) { s.inherited.mode = 1 },
		func(s *querySource) { s.sources = []*querySource{s} }, func(s *querySource) { s.partialMask = &storage.IndexRoot{} }, func(s *querySource) { s.hashIndexes = []capturedHashIndex{} },
		func(s *querySource) { s.inheritance = []inheritanceQuerySegment{} }, func(s *querySource) { s.anonymous = [][]sqltype.Value{} }, func(s *querySource) { s.singleton = true },
		func(s *querySource) { s.materialized = true }, func(s *querySource) { s.final = &finalRows{} }, func(s *querySource) { s.input = &querySource{} }, func(s *querySource) { s.nestedRoot = &singletonNode{} },
		func(s *querySource) { s.window = true }, func(s *querySource) { s.scratch = []sqltype.Value{} }, func(s *querySource) { s.rlsDenied = true }, func(s *querySource) { s.storedRef = s.ref },
	} {
		copy := *source
		mutate(&copy)
		if _, _, ok := parallelQueryPlan(project, &copy); ok {
			t.Fatal("unsupported source admitted")
		}
	}
	column := project.expressions[0].(columnExpression)
	for _, expr := range []expr.Expr{sequenceCallExpression{}, functions.Random{}, functions.Advisory{}, subqueryExpression{}, queryProbe(column.ResultUsage(), nil)} {
		if parallelQueryExpression(expr, project.frame, len(project.child.Schema())) {
			t.Fatalf("unsafe/unknown %T admitted", expr)
		}
		copy := *ordered
		copy.keys = append([]sortKey(nil), ordered.keys...)
		copy.keys[0].expression = expr
		p := *project
		p.child = &copy
		if _, _, ok := parallelQueryPlan(&p, source); ok {
			t.Fatal("hidden unsafe key admitted")
		}
	}
	for _, mutate := range []func(*columnExpression){func(c *columnExpression) { c.virtual = column }, func(c *columnExpression) { c.frame++ }, func(c *columnExpression) { c.scope++ }} {
		copy := column
		mutate(&copy)
		if parallelQueryExpression(copy, project.frame, len(project.child.Schema())) {
			t.Fatal("virtual/outer column admitted")
		}
	}
}

func TestQueryParallelErrorIdentity(t *testing.T) {
	parallelOpenFailureProof(t)
	_, query, source := queryCoreFixture(t, "SELECT id FROM t")
	project := query.Root.(*projectNode)
	blocked := make(chan struct{})
	var joined atomic.Bool
	sentinel := errors.New("primary worker failure")
	project.expressions[0] = queryProbe(project.Schema()[0].Usage, func(c *EvalContext) (sqltype.Value, error) {
		if c.query.scanStart == 0 {
			select {
			case <-blocked:
				return sqltype.Value{}, sentinel
			case <-c.Context.Done():
				return sqltype.Value{}, c.Context.Err()
			}
		}
		close(blocked)
		<-c.Context.Done()
		joined.Store(true)
		return sqltype.Value{}, c.Context.Err()
	})
	_, _, join := parallelTestStart(t, func(ctx context.Context) parallelTestOutcome {
		r, n, e := finalizeParallelQuery(ctx, project, nil, source, nil, 2)
		return parallelTestOutcome{r, n, e}
	})
	out := join()
	if out.rows != nil || out.count != 0 || out.err != sentinel || !joined.Load() || source.view != nil {
		t.Fatal(out, joined.Load(), source.view)
	}
}

func TestQueryParallelCancellation(t *testing.T) {
	parallelMergeCancellationProof(t)
	for _, phase := range []string{"before", "filter", "target", "sort"} {
		t.Run(phase, func(t *testing.T) {
			_, query, source := queryCoreFixture(t, "SELECT id FROM t WHERE f ORDER BY id+1")
			project := query.Root.(*projectNode)
			ordered := project.child.(*sortNode)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var calls atomic.Int64
			probe := func(usage sqltype.Usage) expr.Expr {
				return queryProbe(usage, func(c *EvalContext) (sqltype.Value, error) {
					calls.Add(1)
					cancel()
					return sqltype.Value{}, c.Context.Err()
				})
			}
			switch phase {
			case "before":
				cancel()
			case "filter":
				f := ordered.child.(*filterNode)
				f.predicate = probe(f.predicate.ResultUsage())
			case "target":
				project.expressions[0] = probe(project.Schema()[0].Usage)
			case "sort":
				ordered.keys[0].expression = probe(ordered.keys[0].expression.ResultUsage())
			}
			rows, n, err := finalizeParallelQuery(ctx, project, ordered, source, nil, 2)
			if rows != nil || n != 0 || !errors.Is(err, context.Canceled) || source.view != nil {
				t.Fatal(rows, n, err)
			}
			if phase != "before" && calls.Load() == 0 {
				t.Fatal("actual evaluator not reached")
			}
		})
	}
	// The live admission authority must not overwrite sibling cancellation.
	_, _, source := queryCoreFixture(t, "SELECT id FROM t")
	scope := &StatementScope{}
	source.authority = &queryAuthority{scope: scope}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	state := &queryState{source: source, executionContext: ctx}
	evaluation := state.evaluation(context.Background())
	if evaluation.Context != ctx || evaluation.Context.Err() != context.Canceled {
		t.Fatal("worker cancellation replaced by authority")
	}
	if evaluation.liveScope() != scope {
		t.Fatal("worker context lost borrowed authority")
	}
}

func TestQueryParallelMerge(t *testing.T) {
	parallelCachedOrderProof(t)
	for _, q := range []string{"SELECT id FROM t ORDER BY v DESC NULLS LAST,id", "SELECT id FROM t ORDER BY v+1 ASC NULLS FIRST,id", "SELECT id+1 AS id FROM t ORDER BY v DESC NULLS LAST,id"} {
		_, query, source := queryCoreFixture(t, q)
		project, ordered, ok := parallelQueryPlan(query.Root, source)
		if !ok {
			t.Fatal(q, "not eligible")
		}
		rows, n, err := finalizeParallelQuery(context.Background(), project, ordered, source, nil, 2)
		if err != nil || n != 4 {
			t.Fatal(n, err)
		}
		want := []int64{3, 1, 4, 2}
		if q == "SELECT id FROM t ORDER BY v+1 ASC NULLS FIRST,id" {
			want = []int64{2, 1, 4, 3}
		}
		if q == "SELECT id+1 AS id FROM t ORDER BY v DESC NULLS LAST,id" {
			want = []int64{4, 2, 5, 3}
		}
		if ids := parallelTestIDs(t, rows); !reflect.DeepEqual(ids, want) {
			t.Fatal(q, ids, want)
		}
	}
	for _, workers := range []int{2, 4} {
		_, query, source := queryCoreFixture(t, "SELECT id FROM t ORDER BY id")
		project := query.Root.(*projectNode)
		ordered := project.child.(*sortNode)
		ordered.keys[0].comparison = sqltype.Comparison{}
		// At four singleton runs no local comparison is possible: failure proves
		// the leader actually compares run heads, not a discarded sorted plan.
		rows, n, err := finalizeParallelQuery(context.Background(), project, ordered, source, nil, workers)
		if rows != nil || n != 0 || err == nil || err.Error() != "invalid comparison values" || source.view != nil {
			t.Fatal(rows, n, err)
		}
	}
}

func TestQueryParallelOwnership(t *testing.T) {
	for _, q := range []string{"SELECT id FROM t", "SELECT id+1 AS id FROM t", "SELECT FROM t"} {
		_, query, source := queryCoreFixture(t, q)
		rows, n, err := finalizeSelectQuery(context.Background(), query.Root, source, nil, 2)
		if err != nil || n != 4 {
			t.Fatal(n, err)
		}
		result := rows.(*finalRows)
		computed := q == "SELECT id+1 AS id FROM t"
		if computed && source.view != nil {
			t.Fatal("computed input retained")
		}
		if !computed && (result.source != source || source.view == nil || len(result.selection) != 4) {
			t.Fatal("direct pin/selection lost")
		}
		schema := rows.Schema()
		values := make([]sqltype.Value, len(schema))
		for range n {
			present, err := rows.Next(context.Background(), values)
			if !present || err != nil {
				t.Fatal(present, err)
			}
		}
		if len(values) != 0 {
			id, _ := values[0].Integer()
			want := int64(4)
			if computed {
				want = 5
			}
			if id != want {
				t.Fatal(id)
			}
		}
		if present, err := rows.Next(context.Background(), values); present || err != nil {
			t.Fatal(present, err)
		}
		if source.view != nil {
			t.Fatal("EOF retained pin")
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(rows.Schema(), schema) {
			t.Fatal("close erased schema")
		}
	}
}
