package exec

import (
	"context"
	"database/sql/driver"
	"fmt"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// A protocol-unit source with an immutable frontier and genuine parameter-aware
// Begin/ReScan. It is not SQL compatibility evidence; public tests use real SQL.
type invocationUnitProvider struct {
	rows                                                   []int64
	parameterAware                                         bool
	scans                                                  []*invocationUnitScan
	correlationWidth, correlationSlot                      int
	history                                                []catalog.ForeignScanParameters
	liveScans, peakScans, liveRequests, peakRequests, ends int
}
type invocationUnitScan struct {
	provider   *invocationUnitProvider
	parameters catalog.ForeignScanParameters
	position   int
	request    *invocationUnitRequest
	ended      bool
}
type invocationUnitRequest struct {
	*asyncUnitRequest
	scan   *invocationUnitScan
	closed bool
}

func (p *invocationUnitProvider) GetForeignRelSize(context.Context, catalog.ForeignRequest) (catalog.ForeignRelSize, error) {
	return catalog.ForeignRelSize{Rows: float64(len(p.rows)), Width: 4}, nil
}
func (p *invocationUnitProvider) GetForeignPaths(ctx context.Context, r catalog.ForeignRequest, size catalog.ForeignRelSize) ([]catalog.ForeignPath, error) {
	return []catalog.ForeignPath{{TotalCost: size.Rows, Attributes: r.RequiredAttributes}}, nil
}
func (p *invocationUnitProvider) GetForeignPlan(ctx context.Context, r catalog.ForeignRequest, path catalog.ForeignPath) (catalog.ForeignPlan, error) {
	return catalog.ForeignPlan{Attributes: path.Attributes}, nil
}
func (p *invocationUnitProvider) IsForeignPathAsyncCapable(context.Context, catalog.ForeignRequest, catalog.ForeignPath) (bool, error) {
	return true, nil
}
func (p *invocationUnitProvider) BeginForeignScan(ctx context.Context, r catalog.ForeignRequest, plan catalog.ForeignPlan) (catalog.ForeignScan, error) {
	s := &invocationUnitScan{provider: p}
	s.parameters = r.Parameters
	p.recordParameters(r.Parameters)
	p.scans = append(p.scans, s)
	p.liveScans++
	p.peakScans = max(p.peakScans, p.liveScans)
	return s, ctx.Err()
}
func (s *invocationUnitScan) IterateForeignScan(ctx context.Context) (catalog.ForeignRow, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	for s.position < len(s.provider.rows) {
		id := s.provider.rows[s.position]
		s.position++
		if s.provider.parameterAware {
			width := max(1, s.provider.correlationWidth)
			if len(s.parameters.Values) != 1 || len(s.parameters.Correlation) != width {
				return nil, false, fmt.Errorf("missing actual exported invocation parameters: %+v", s.parameters)
			}
			threshold, ok := s.parameters.Values[0].(int64)
			if !ok {
				return nil, false, fmt.Errorf("bound parameter type %T", s.parameters.Values[0])
			}
			current, ok := s.parameters.Correlation[s.provider.correlationSlot].(int64)
			if !ok {
				return nil, false, fmt.Errorf("correlation type %T", s.parameters.Correlation[s.provider.correlationSlot])
			}
			if id != current || id < threshold {
				continue
			}
		}
		return catalog.ForeignRow{id}, true, nil
	}
	return nil, false, nil
}
func (s *invocationUnitScan) ReScanForeignScan(ctx context.Context, p catalog.ForeignScanParameters) error {
	if s.request != nil {
		return fmt.Errorf("ReScan overlapped an owned request")
	}
	s.parameters = p
	s.provider.recordParameters(p)
	s.position = 0
	return ctx.Err()
}

func (p *invocationUnitProvider) recordParameters(parameters catalog.ForeignScanParameters) {
	parameters.Values = CopyForeignCells(parameters.Values)
	parameters.Correlation = CopyForeignCells(parameters.Correlation)
	p.history = append(p.history, parameters)
}
func (s *invocationUnitScan) RequestForeignScan(ctx context.Context) (catalog.ForeignAsyncRequest, error) {
	if s.request != nil {
		return nil, fmt.Errorf("overlapping optional requests")
	}
	row, present, err := s.IterateForeignScan(ctx)
	request := &invocationUnitRequest{asyncUnitRequest: asyncUnitReady(row, present, err), scan: s}
	s.request = request
	s.provider.liveRequests++
	s.provider.peakRequests = max(s.provider.peakRequests, s.provider.liveRequests)
	return request, nil
}
func (r *invocationUnitRequest) Close(ctx context.Context) error {
	if r.closed {
		return nil
	}
	r.closed = true
	err := r.asyncUnitRequest.Close(ctx)
	r.scan.request = nil
	r.scan.provider.liveRequests--
	return err
}
func (s *invocationUnitScan) EndForeignScan(ctx context.Context) error {
	if s.request != nil {
		return fmt.Errorf("End before request join")
	}
	if s.ended {
		return fmt.Errorf("duplicate End")
	}
	s.ended = true
	s.provider.liveScans--
	s.provider.ends++
	return ctx.Err()
}

type invocationUnitFixture struct {
	state     State
	owner     ForeignScanOwner
	sources   []*Source
	providers []*invocationUnitProvider
	columns   []catalog.Column
	integer   *sqltype.Descriptor
	root      *plan.ProjectNode
}

func newInvocationUnitFixture(t *testing.T, parameterAware bool) *invocationUnitFixture {
	t.Helper()
	integer := asyncUnitType(t, sqltype.Int4)
	f := &invocationUnitFixture{integer: integer, columns: []catalog.Column{{Name: "id", Usage: integer.Usage(), Nullable: true}}}
	for _, rows := range [][]int64{{1, 1, 2}, {3, 4}} {
		provider := &invocationUnitProvider{rows: rows, parameterAware: parameterAware}
		foreign := &ForeignQuerySource{Wrapper: provider, Columns: f.columns,
			Request: catalog.ForeignRequest{RequiredAttributes: []int32{1}, Columns: []catalog.ForeignColumn{{Attribute: 1, Name: "id"}}},
			ExportValue: func(ctx context.Context, v sqltype.Value, output sqltype.OutputEnvironment) (driver.Value, error) {
				return v.Integer()
			},
			ImportValue: func(ctx context.Context, v driver.Value, usage sqltype.Usage, input sqltype.InputEnvironment) (sqltype.Value, error) {
				return sqltype.Integer(usage.Type, v.(int64))
			},
		}
		f.owner.Add(foreign)
		f.sources = append(f.sources, &Source{Foreign: foreign})
		f.providers = append(f.providers, provider)
	}
	f.state = State{Source: &Source{Sources: f.sources}, SourceMode: SourceBorrowed, AsyncAppend: true, asyncCaptured: true}
	f.root = f.union(0, 1)
	t.Cleanup(func() {
		if err := f.state.Source.finishProducers(nil); err != nil {
			t.Error(err)
		}
		if err := f.owner.Finish(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return f
}
func (f *invocationUnitFixture) column(frame uint32) expr.Expr {
	return plan.ColumnExpr{Info: expr.Info{Usage: f.integer.Usage(), Property: expr.Properties{RowDependent: true}}, Scope: 1, Frame: frame, Slot: 0}
}
func (f *invocationUnitFixture) union(left, right int) *plan.ProjectNode {
	l := &plan.ProjectNode{QuerySchema: f.columns, Frame: 2, Child: &plan.SourceNode{QuerySchema: f.columns, Occurrence: left}, Expressions: []expr.Expr{f.column(2)}}
	r := &plan.ProjectNode{QuerySchema: f.columns, Frame: 2, Child: &plan.SourceNode{QuerySchema: f.columns, Occurrence: right}, Expressions: []expr.Expr{f.column(2)}}
	set := &plan.SetNode{QuerySchema: f.columns, Left: l, Right: r, Operation: "union", All: true, LeftExpressions: l.Expressions, RightExpressions: r.Expressions}
	return &plan.ProjectNode{QuerySchema: f.columns, Frame: 3, Child: set, Expressions: []expr.Expr{f.column(3)}}
}
func (f *invocationUnitFixture) checkReusable(t *testing.T) {
	t.Helper()
	for i, source := range f.sources {
		foreign := source.Foreign
		provider := f.providers[i]
		if len(foreign.Executions) > 1 || provider.liveScans > 1 || provider.peakScans > 1 || provider.liveRequests != 0 {
			t.Fatalf("source %d accumulated invocation frontier: executions=%d liveScans=%d peakScans=%d requests=%d", i, len(foreign.Executions), provider.liveScans, provider.peakScans, provider.liveRequests)
		}
		for _, execution := range foreign.Executions {
			if execution.async.request != nil {
				t.Fatal("successful invocation retained request")
			}
		}
	}
}
func (f *invocationUnitFixture) frame(t *testing.T, id, threshold int64) Context {
	t.Helper()
	value, err := sqltype.Integer(f.integer, id)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := sqltype.Integer(f.integer, threshold)
	if err != nil {
		t.Fatal(err)
	}
	f.state.Parameters = []sqltype.Value{bound}
	frame := f.state.Evaluation(context.Background())
	frame.Row = []sqltype.Value{value}
	return frame
}
