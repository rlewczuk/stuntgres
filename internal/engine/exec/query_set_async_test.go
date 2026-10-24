package exec

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

type asyncScriptScan struct {
	requests            []catalog.ForeignAsyncRequest
	next, rescans, ends int
}

func (s *asyncScriptScan) RequestForeignScan(context.Context) (catalog.ForeignAsyncRequest, error) {
	if s.next >= len(s.requests) {
		return nil, errors.New("unexpected request beyond bounded script")
	}
	r := s.requests[s.next]
	s.next++
	return r, nil
}
func (s *asyncScriptScan) IterateForeignScan(context.Context) (catalog.ForeignRow, bool, error) {
	return nil, false, errors.New("synchronous callback on async scan")
}
func (s *asyncScriptScan) ReScanForeignScan(context.Context, catalog.ForeignScanParameters) error {
	s.rescans++
	s.next = 0
	return nil
}
func (s *asyncScriptScan) EndForeignScan(context.Context) error { s.ends++; return nil }

type asyncScriptProvider struct {
	scan       *asyncScriptScan
	beginError error
}

func (p *asyncScriptProvider) GetForeignRelSize(context.Context, catalog.ForeignRequest) (catalog.ForeignRelSize, error) {
	return catalog.ForeignRelSize{Rows: 2}, nil
}
func (p *asyncScriptProvider) GetForeignPaths(context.Context, catalog.ForeignRequest, catalog.ForeignRelSize) ([]catalog.ForeignPath, error) {
	return []catalog.ForeignPath{{TotalCost: 1}}, nil
}
func (p *asyncScriptProvider) GetForeignPlan(context.Context, catalog.ForeignRequest, catalog.ForeignPath) (catalog.ForeignPlan, error) {
	return catalog.ForeignPlan{}, nil
}
func (p *asyncScriptProvider) IsForeignPathAsyncCapable(context.Context, catalog.ForeignRequest, catalog.ForeignPath) (bool, error) {
	return true, nil
}
func (p *asyncScriptProvider) BeginForeignScan(context.Context, catalog.ForeignRequest, catalog.ForeignPlan) (catalog.ForeignScan, error) {
	return p.scan, p.beginError
}

type asyncRejectProbe struct {
	plan.LiteralExpr
	calls   int
	failure error
}

func (p *asyncRejectProbe) Eval(expr.Context) (sqltype.Value, error) {
	p.calls++
	return p.Value, p.failure
}

func TestFDWAsyncFilteredFrontier(t *testing.T) {
	for _, fails := range []bool{false, true} {
		name := "reject_then_pending"
		if fails {
			name = "real_filter_error"
		}
		t.Run(name, func(t *testing.T) {
			boolean := asyncUnitType(t, sqltype.Bool)
			primary := errors.New("local qualification failed")
			predicate := &asyncRejectProbe{LiteralExpr: plan.LiteralExpr{Info: expr.Info{Usage: boolean.Usage(), Property: expr.Properties{Volatility: expr.Volatile, RowDependent: true}}, Value: sqltype.Boolean(boolean, false)}}
			if fails {
				predicate.failure = primary
			}
			pending := &asyncUnitRequest{ready: make(chan struct{}), present: true}
			slow := &asyncScriptScan{requests: []catalog.ForeignAsyncRequest{asyncUnitReady(nil, true, nil), pending}}
			ready := &asyncScriptScan{requests: []catalog.ForeignAsyncRequest{asyncUnitReady(nil, true, nil), asyncUnitReady(nil, false, nil)}}
			leftSource, rightSource := &Source{Foreign: &ForeignQuerySource{Wrapper: &asyncScriptProvider{scan: slow}}}, &Source{Foreign: &ForeignQuerySource{Wrapper: &asyncScriptProvider{scan: ready}}}
			owner := &ForeignScanOwner{}
			owner.Add(leftSource.Foreign)
			owner.Add(rightSource.Foreign)
			state := &State{Source: &Source{Sources: []*Source{leftSource, rightSource}}, SourceMode: SourceBorrowed, AsyncAppend: true, asyncCaptured: true}
			left := &plan.ProjectNode{Child: &plan.FilterNode{Child: &plan.SourceNode{Occurrence: 0}, Predicate: predicate}}
			right := &plan.ProjectNode{Child: &plan.SourceNode{Occurrence: 1}}
			stage, err := OpenSetNode(context.Background(), state, &plan.SetNode{Left: left, Right: right, Operation: "union", All: true})
			if err != nil {
				t.Fatal(err)
			}
			_, present, err := stage.NextOccurrence(context.Background())
			if fails {
				if err != primary || present {
					t.Fatal("filter error changed", err)
				}
			} else {
				if err != nil || !present || predicate.calls != 1 || slow.next != 2 || pending.completes != 0 {
					t.Fatal("rejected row blocked ready sibling", err, predicate.calls, slow.next)
				}
			}
			_ = stage.Close()
			if err := owner.Finish(context.Background()); err != nil {
				t.Fatal(err)
			}
			if slow.ends != 1 || ready.ends != 1 {
				t.Fatal("scan ownership lost")
			}
			if !fails && (pending.cancels != 1 || pending.closes != 1) {
				t.Fatal("pending filtered request leaked")
			}
		})
	}
}

func TestFDWAsyncPartialBeginAndReset(t *testing.T) {
	t.Run("partial_begin", func(t *testing.T) {
		primary := errors.New("begin failed after acquiring scan")
		scan := &asyncScriptScan{}
		source := &Source{Foreign: &ForeignQuerySource{Wrapper: &asyncScriptProvider{scan: scan, beginError: primary}}}
		owner := &ForeignScanOwner{}
		owner.Add(source.Foreign)
		stage, err := OpenForeignStage(context.Background(), &State{Source: source}, source, &plan.SourceNode{})
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := stage.NextOccurrence(context.Background()); err != primary {
			t.Fatal("Begin primary lost", err)
		}
		if err := owner.Finish(context.Background()); err != nil || scan.ends != 1 {
			t.Fatal("partial Begin leaked", err)
		}
	})
	t.Run("reset_discards_quiesced_generation", func(t *testing.T) {
		scan := &asyncScriptScan{requests: []catalog.ForeignAsyncRequest{asyncUnitReady(nil, true, nil), asyncUnitReady(nil, false, nil)}}
		source := &Source{Foreign: &ForeignQuerySource{Wrapper: &asyncScriptProvider{scan: scan}}}
		owner := &ForeignScanOwner{}
		owner.Add(source.Foreign)
		node := &plan.SourceNode{}
		e := &ForeignExecution{async: foreignAsyncState{capable: true}}
		state := &State{Source: source, AsyncAppend: true, asyncCaptured: true, asyncFrontier: &foreignAppendFrontier{admitted: map[*plan.SourceNode]*ForeignExecution{node: e}}}
		stage, err := OpenForeignStage(context.Background(), state, source, node)
		if err != nil {
			t.Fatal(err)
		}
		if err := e.async.start(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := e.requestAsync(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := owner.Quiesce(context.Background()); err != nil || !e.async.buffered {
			t.Fatal(err)
		}
		if _, err := stage.Rewind(context.Background()); err != nil || e.async.buffered || scan.rescans != 1 {
			t.Fatal("reset retained old generation", err)
		}
		_ = stage.Close()
		if err := owner.Finish(context.Background()); err != nil || scan.ends != 1 {
			t.Fatal(err)
		}
	})
}
