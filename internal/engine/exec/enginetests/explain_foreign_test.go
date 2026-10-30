package enginetests

import (
	"context"
	"database/sql/driver"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/sqltype"
)

type analyzedQueryProvider struct {
	*queryUnitProvider
	token            *int
	explained        bool
	explanations     int
	explainedRequest catalog.ForeignQueryRequest
}

func (p *analyzedQueryProvider) GetForeignJoinPaths(ctx context.Context, request catalog.ForeignQueryRequest) ([]catalog.ForeignQueryPath, error) {
	paths, err := p.queryUnitProvider.GetForeignJoinPaths(ctx, request)
	p.token = new(int)
	*p.token = p.discoveries
	paths[0].Plan.Private = p.token
	return paths, err
}

func (p *analyzedQueryProvider) ExplainForeignScan(_ context.Context, request catalog.ForeignExplainScanRequest) ([]catalog.ForeignExplainProperty, error) {
	if request.Query == nil || request.Query.Path.Plan.Private != p.token {
		panic("EXPLAIN did not receive the actual selected composite plan")
	}
	p.explained = true
	p.explanations++
	p.explainedRequest = exec.CopyForeignQueryRequest(request.Query.Request)
	// Mutating callback-owned layouts must not affect the executable plan.
	request.Query.Path.Plan.Slots[0].ID = 999
	return []catalog.ForeignExplainProperty{{Name: "Selected", Value: "actual"}}, nil
}

func (p *analyzedQueryProvider) BeginForeignQueryScan(ctx context.Context, request catalog.ForeignQueryRequest, plan catalog.ForeignQueryPlan) (catalog.ForeignScan, error) {
	if !p.explained || plan.Private != p.token || plan.Slots[0].ID == 999 {
		panic("executed plan differs from explained selection")
	}
	return p.queryUnitProvider.BeginForeignQueryScan(ctx, request, plan)
}

func TestExplainAnalyzeForeignPlanIdentity(t *testing.T) {
	base := &queryUnitProvider{path: queryUnitPath, scan: &queryUnitScan{row: catalog.ForeignRow{int64(7), int64(8), true, false, true}}}
	provider := &analyzedQueryProvider{queryUnitProvider: base}
	state, node, owner := queryUnitTree(t, base, false)
	for _, source := range state.Source.Sources {
		source.Foreign.Wrapper = provider
	}
	ctx, observation := exec.Observe(context.Background(), false)
	observation.SetForeignOptions(catalog.ForeignExplainOptions{Costs: true}, true)
	observation.Register(node)
	stage, err := exec.OpenNode(ctx, state, node)
	if err != nil {
		t.Fatal(err)
	}
	if _, present, err := stage.NextOccurrence(ctx); err != nil || !present {
		t.Fatal(present, err)
	}
	if err := stage.Close(); err != nil {
		t.Fatal(err)
	}
	if err := owner.Finish(ctx); err != nil {
		t.Fatal(err)
	}
	if base.discoveries != 1 || base.begins != 1 {
		t.Fatal("independent replan or repeated execution", base.discoveries, base.begins)
	}
	actual := observation.Actual(node)
	if actual.Rows != 1 || actual.Loops != 1 {
		t.Fatal("lost real composite identity", actual)
	}
	foreign, ok := observation.Foreign(node)
	if !ok || !foreign.Composite || len(foreign.Details) == 0 || foreign.Details[len(foreign.Details)-1] != "Selected: actual" {
		t.Fatal("missing owned presentation", foreign)
	}
	before := append([]string(nil), foreign.Details...)
	foreign.Details[0] = "changed"
	again, _ := observation.Foreign(node)
	if !reflect.DeepEqual(again.Details, before) {
		t.Fatal("borrowed presentation backing")
	}
	if foreign.Operation != "join" || len(foreign.Sources) != 2 || len(foreign.Properties) != 1 || foreign.Properties[0].Name != "Selected" || foreign.Estimates["Total Cost"] == 0 {
		t.Fatal("lost construction-time foreign semantics", foreign)
	}
	originalSource, originalProperty, originalCost := foreign.Sources[0], foreign.Properties[0], foreign.Estimates["Total Cost"]
	foreign.Sources[0].Relation = "changed"
	foreign.Properties[0].Value = "changed"
	foreign.Estimates["Total Cost"] = -1
	detached, _ := observation.Foreign(node)
	if detached.Sources[0] != originalSource || detached.Properties[0] != originalProperty || detached.Estimates["Total Cost"] != originalCost {
		t.Fatal("borrowed structured observation backing", detached)
	}
	if actual := observation.Actual(node.Left); actual.Loops != 0 {
		t.Fatal("pushed child fabricated a local execution", actual)
	}
}

func TestExplainAnalyzeForeignPlanReScan(t *testing.T) {
	scan := &queryUnitScan{row: catalog.ForeignRow{int64(7), int64(8), true, false, true}}
	base := &queryUnitProvider{path: queryUnitPath, scan: scan}
	provider := &analyzedQueryProvider{queryUnitProvider: base}
	state, node, owner := queryUnitTree(t, base, false)
	t.Cleanup(func() { _ = owner.Finish(context.Background()) })
	for _, source := range state.Source.Sources {
		source.Foreign.Wrapper = provider
	}
	value, err := sqltype.Integer(node.Schema()[0].Usage.Type, 70)
	if err != nil {
		t.Fatal(err)
	}
	state.Parameters = []sqltype.Value{value}
	var exportContexts []context.Context
	source := state.Source.Sources[0].Foreign
	export := source.ExportValue
	source.ExportValue = func(ctx context.Context, value sqltype.Value, output sqltype.OutputEnvironment) (driver.Value, error) {
		exportContexts = append(exportContexts, ctx)
		return export(ctx, value, output)
	}
	first, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx, observation := exec.Observe(first, false)
	observation.Register(node)
	stage, err := exec.OpenNode(ctx, state, node)
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	assertFrame := func(request catalog.ForeignQueryRequest, expected int64) {
		t.Helper()
		want := []driver.Value{expected}
		if !reflect.DeepEqual(request.Parameters.Values, want) || len(request.Parameters.Correlation) != 0 {
			t.Fatal("wrong selected frame", request.Parameters)
		}
		for _, source := range request.Sources {
			if !reflect.DeepEqual(source.Request.Parameters.Values, want) || len(source.Request.Parameters.Correlation) != 0 {
				t.Fatal("wrong per-source frame", source.Request.Parameters)
			}
		}
	}
	assertFrame(base.request, 70)
	assertFrame(provider.explainedRequest, 70)
	if len(exportContexts) != 1 || exportContexts[0] != ctx {
		t.Fatal("discovery/explanation exported more than once", exportContexts)
	}
	if _, present, err := stage.NextOccurrence(ctx); err != nil || !present {
		t.Fatal("initial demand", present, err)
	}
	assertFrame(base.request, 70)
	if len(exportContexts) != 1 {
		t.Fatal("Begin re-exported selected frame", exportContexts)
	}
	token := provider.token
	cancel()
	for _, number := range []int64{170, 270} {
		value, err := sqltype.Integer(value.Type(), number)
		if err != nil {
			t.Fatal(err)
		}
		// Exercise the existing stage owner with a fresh typed frame, without
		// rediscovery or a new import/evaluation of the original statement.
		state.Parameters[0] = value
		current := context.WithValue(context.Background(), struct{}{}, number)
		scan.rescan = func(demand context.Context, parameters catalog.ForeignScanParameters) error {
			if demand != current || demand.Err() != nil || !reflect.DeepEqual(parameters.Values, []driver.Value{number}) || len(parameters.Correlation) != 0 {
				t.Fatal("ReScan retained selected frame/context", demand, parameters)
			}
			return nil
		}
		changed, err := stage.Rewind(current)
		if err != nil || !changed {
			t.Fatal("rewind", changed, err)
		}
		if len(exportContexts) != scan.rescans+1 || exportContexts[len(exportContexts)-1] != current {
			t.Fatal("ReScan did not export exactly once under current demand", exportContexts)
		}
		if _, present, err := stage.NextOccurrence(current); err != nil || !present {
			t.Fatal("rescanned demand", present, err)
		}
		assertFrame(provider.explainedRequest, 70)
		if provider.token != token || base.discoveries != 1 || base.begins != 1 || provider.explanations != 1 {
			t.Fatal("ReScan replaced explained selection", base.discoveries, base.begins, provider.explanations)
		}
	}
	actual := observation.Actual(node)
	if actual.Rows != 3 || actual.Loops != 3 {
		t.Fatal("lost composite rescan identity", actual)
	}
	if actual := observation.Actual(node.Left); actual.Loops != 0 {
		t.Fatal("pushed child acquired local work", actual)
	}
	if err := stage.Close(); err != nil {
		t.Fatal(err)
	}
	if err := owner.Finish(ctx); err != nil {
		t.Fatal(err)
	}
	if scan.rescans != 2 || scan.ends != 1 || scan.cleanupCanceled {
		t.Fatal("rescan ownership", scan.rescans, scan.ends, scan.cleanupCanceled)
	}
}
