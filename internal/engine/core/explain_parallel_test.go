package core_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

type explainPartialProbe struct {
	functions.AggregateProvider
	functions.PartialAggregateProvider
	functions.AggregateStateTransport
	advances, combines, finals atomic.Int64
	encodes, decodes atomic.Int64
	badOwner atomic.Bool
	cancel context.CancelFunc
}
func (p *explainPartialProbe) Advance(ctx context.Context, state any, values []sqltype.Value, evaluation expr.Context) error {
	if !exec.InParallelWorker(ctx) { p.badOwner.Store(true) }
	p.advances.Add(1)
	return p.AggregateProvider.Advance(ctx, state, values, evaluation)
}
func (p *explainPartialProbe) Combine(ctx context.Context, left, right any, evaluation expr.Context) error {
	if exec.InParallelWorker(ctx) || !exec.InParallelOperation(ctx) { p.badOwner.Store(true) }
	p.combines.Add(1)
	if p.cancel != nil { p.cancel(); return context.Canceled }
	return p.PartialAggregateProvider.Combine(ctx, left, right, evaluation)
}
func (p *explainPartialProbe) Finalize(ctx context.Context, state any) (sqltype.Value, error) {
	if exec.InParallelWorker(ctx) || !exec.InParallelOperation(ctx) { p.badOwner.Store(true) }
	p.finals.Add(1)
	return p.AggregateProvider.Finalize(ctx, state)
}
func (p *explainPartialProbe) Serialize(ctx context.Context, state any) ([]byte, bool, error) {
	if !exec.InParallelWorker(ctx) { p.badOwner.Store(true) }
	p.encodes.Add(1)
	return p.AggregateStateTransport.Serialize(ctx, state)
}
func (p *explainPartialProbe) Deserialize(ctx context.Context, data []byte, present bool) (any, error) {
	if exec.InParallelWorker(ctx) || !exec.InParallelOperation(ctx) { p.badOwner.Store(true) }
	p.decodes.Add(1)
	return p.AggregateStateTransport.Deserialize(ctx, data, present)
}

func TestExplainAnalyzeParallelAggregateOwnership(t *testing.T) {
	for _, tc := range []struct{name, query string; output, groups, input int64; cancel bool}{
		{"groups", "SELECT id%2 AS k,string_agg('x'::text,''::text) AS v FROM t GROUP BY id%2 HAVING string_agg('x'::text,''::text) IS NOT NULL ORDER BY k", 2, 2, 4, false},
		{"having-empty", "SELECT id%2 AS k,string_agg('x'::text,''::text) AS v FROM t GROUP BY id%2 HAVING string_agg('x'::text,''::text) IS NULL ORDER BY k", 0, 2, 4, false},
		{"empty-global", "SELECT string_agg('x'::text,''::text) FROM t WHERE id<0", 1, 1, 0, false},
		{"empty-global-having-rejected", "SELECT string_agg('x'::text,''::text) FROM t WHERE id<0 HAVING string_agg('x'::text,''::text) IS NOT NULL", 0, 1, 0, false},
		{"empty-global-having-accepted", "SELECT string_agg('x'::text,''::text) FROM t WHERE id<0 HAVING string_agg('x'::text,''::text) IS NULL", 1, 1, 0, false},
		{"empty-grouped", "SELECT id%2,string_agg('x'::text,''::text) FROM t WHERE id<0 GROUP BY id%2", 0, 0, 0, false},
		{"cancel-combine", "SELECT id%2,string_agg('x'::text,''::text) FROM t GROUP BY id%2", 0, 0, 4, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, query, source := queryCoreFixture(t, tc.query)
			pin, err := exec.ParallelQuerySource(source)
			if err != nil { t.Fatal(err) }
			aggregate, ok := exec.ParallelAggregatePlan(query.Root, pin)
			if !ok { pin.Close(); t.Fatal("fixture must admit the real parallel aggregate") }
			provider := aggregate.Calls[0].Provider
			probe := &explainPartialProbe{AggregateProvider: provider, PartialAggregateProvider: provider.(functions.PartialAggregateProvider), AggregateStateTransport: provider.(functions.AggregateStateTransport)}
			aggregate.Calls[0].Provider = probe
			base, cancel := context.WithCancel(context.Background()); defer cancel()
			if tc.cancel { probe.cancel = cancel }
			ctx, observed := exec.Observe(base, true)
			observed.Register(query.Root)
			var originals []plan.Node
			for node := query.Root; node != nil; { originals = append(originals, node); node, _ = plan.QueryInputs(node) }
			rows, count, err := exec.FinalizeSelect(ctx, query.Root, pin, nil, 2)
			if tc.cancel {
				if !errors.Is(err, context.Canceled) || rows != nil || count != 0 || probe.combines.Load() == 0 || probe.encodes.Load()!=4 || probe.decodes.Load()==0 || probe.badOwner.Load() || pin.View != nil { t.Fatal("cancellation did not settle parallel owner", rows, count, err) }
				return
			}
			if err != nil { t.Fatal(err) }
			defer rows.Close()
			if int64(count) != tc.output || probe.badOwner.Load() { t.Fatal(count, probe.badOwner.Load()) }
			if tc.input != 0 && (probe.advances.Load() != tc.input || probe.combines.Load() == 0) { t.Fatal("partial/combine not executed", probe.advances.Load(), probe.combines.Load()) }
			if probe.finals.Load() != tc.groups { t.Fatal("leader finalize ownership", probe.finals.Load()) }
			wantTransport:=tc.groups*2
			if probe.encodes.Load()!=wantTransport || probe.decodes.Load()!=wantTransport || probe.combines.Load()!=wantTransport {t.Fatal("real partial codec/combine path not exercised",probe.encodes.Load(),probe.decodes.Load(),probe.combines.Load(),wantTransport)}
			tuple:=make([]sqltype.Value,len(rows.Schema()))
			for ordinal:=int64(0);;ordinal++ {
				present,err:=rows.Next(ctx,tuple)
				if err!=nil {t.Fatal(err)}
				if !present {if ordinal!=tc.output {t.Fatal("delivered output count",ordinal)};break}
				if tc.name=="groups" {
					key,keyErr:=tuple[0].Integer();text,textErr:=tuple[1].Text()
					if keyErr!=nil || textErr!=nil || key!=ordinal || text!="xx" {t.Fatal("incorrect merged groups",key,text,keyErr,textErr)}
				}
				if (tc.name=="empty-global" || tc.name=="empty-global-having-accepted") && !tuple[0].IsNull() {t.Fatal("empty global string aggregate must be NULL",tuple[0])}
			}
			for _, node := range originals {
				actual := observed.Actual(node)
				wantRows, wantLoops := tc.output, int64(1)
				switch node.(type) {
				case *plan.AggregateNode: wantRows = tc.groups
				case *plan.SourceNode: wantRows, wantLoops = 4, 2
				case *plan.FilterNode:
					if node != aggregate.Child { wantRows = tc.output } else { wantRows, wantLoops = tc.input, 2 }
				}
				if !observed.Known(node) || actual.Rows != wantRows || actual.Loops != wantLoops || actual.Elapsed < 0 { t.Fatalf("original %T: %+v want rows%d loops%d", node, actual, wantRows, wantLoops) }
			}
		})
	}
}
