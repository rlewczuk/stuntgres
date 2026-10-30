package enginetests

import (
	"context"
	"database/sql/driver"
	"errors"
	"math"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

type queryUnitProvider struct {
	catalog.ForeignDataWrapper
	path                func(catalog.ForeignQueryRequest) catalog.ForeignQueryPath
	scan                *queryUnitScan
	beginError          error
	discoveries, begins int
	request             catalog.ForeignQueryRequest
}

func (p *queryUnitProvider) GetForeignJoinPaths(_ context.Context, r catalog.ForeignQueryRequest) ([]catalog.ForeignQueryPath, error) {
	p.discoveries++
	p.request = r
	return []catalog.ForeignQueryPath{p.path(r)}, nil
}
func (p *queryUnitProvider) GetForeignUpperPaths(ctx context.Context, r catalog.ForeignQueryRequest) ([]catalog.ForeignQueryPath, error) {
	return p.GetForeignJoinPaths(ctx, r)
}
func (p *queryUnitProvider) BeginForeignQueryScan(_ context.Context, r catalog.ForeignQueryRequest, _ catalog.ForeignQueryPlan) (catalog.ForeignScan, error) {
	p.begins++
	p.request = r
	return p.scan, p.beginError
}

type queryUnitScan struct {
	row             catalog.ForeignRow
	emitted         bool
	ends            int
	endError        error
	cleanupCanceled bool
	rescans         int
	rescan          func(context.Context, catalog.ForeignScanParameters) error
}

func (s *queryUnitScan) IterateForeignScan(context.Context) (catalog.ForeignRow, bool, error) {
	if s.emitted {
		return nil, false, nil
	}
	s.emitted = true
	return s.row, true, nil
}
func (s *queryUnitScan) ReScanForeignScan(ctx context.Context, parameters catalog.ForeignScanParameters) error {
	s.rescans++
	if s.rescan != nil {
		if err := s.rescan(ctx, parameters); err != nil {
			return err
		}
	}
	s.emitted = false
	return nil
}
func (s *queryUnitScan) EndForeignScan(ctx context.Context) error {
	s.ends++
	s.cleanupCanceled = ctx.Err() != nil
	return s.endError
}
func queryUnitPath(r catalog.ForeignQueryRequest) catalog.ForeignQueryPath {
	return catalog.ForeignQueryPath{TotalCost: 1, Compatibility: catalog.ForeignQueryCompatibility{Expressions: true, Collations: true, StatementVisibility: true}, Plan: catalog.ForeignQueryPlan{Slots: append([]catalog.ForeignQuerySlot{}, r.Stage.Slots...)}}
}

func queryUnitTree(t *testing.T, p *queryUnitProvider, zero bool) (*exec.State, *plan.JoinNode, *exec.ForeignScanOwner) {
	t.Helper()
	var integer *sqltype.Descriptor
	for _, typ := range sqltype.Builtins() {
		if typ.ID() == sqltype.Int4 {
			integer = typ
			break
		}
	}
	columns := []catalog.Column{{Name: "k", Usage: integer.Usage(), OriginTable: 20000, OriginAttribute: 1}}
	if zero {
		columns = nil
	}
	owner := &exec.ForeignScanOwner{}
	domain := new(ids.Domain)
	root := &exec.Source{Sources: make([]*exec.Source, 2)}
	nodes := make([]*plan.SourceNode, 2)
	for i := range nodes {
		ref := domain.Ref(1, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(20000 + i)})
		f := &exec.ForeignQuerySource{Wrapper: p, Columns: columns, Request: catalog.ForeignRequest{ServerOID: 1, MappingOID: 2, HandlerOID: 3, RoleOID: 4}, ImportValue: func(ctx context.Context, v driver.Value, u sqltype.Usage, _ sqltype.InputEnvironment) (sqltype.Value, error) {
			if v == nil {
				return sqltype.Null(u.Type), nil
			}
			return sqltype.Integer(u.Type, v.(int64))
		}, ExportValue: func(_ context.Context, v sqltype.Value, _ sqltype.OutputEnvironment) (driver.Value, error) {
			if v.IsNull() {
				return nil, nil
			}
			return v.Integer()
		}}
		if !zero {
			f.Request.Columns = []catalog.ForeignColumn{{Attribute: 1, Name: "k", TypeName: "int4", TypeOID: 23, TypeModifier: -1}}
			f.Request.RequiredAttributes = []int32{1}
		}
		owner.Add(f)
		root.Sources[i] = &exec.Source{Ref: ref, Foreign: f, Anonymous: [][]sqltype.Value{}}
		nodes[i] = &plan.SourceNode{QuerySchema: columns, Ref: ref, Occurrence: i, PresenceSlot: i + 1}
	}
	joined := append(append([]catalog.Column{}, columns...), columns...)
	n := &plan.JoinNode{QuerySchema: joined, Left: nodes[0], Right: nodes[1], LeftWidth: len(columns), RightWidth: len(columns), PresenceSlot: 3, Frame: 1}
	return &exec.State{Source: root, Frame: 1}, n, owner
}

func TestFDWQueryLayoutsAndOwnership(t *testing.T) {
	for _, zero := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "zero_width"}[zero], func(t *testing.T) {
			scan := &queryUnitScan{row: catalog.ForeignRow{int64(7), int64(8), true, false, true}}
			if zero {
				scan.row = catalog.ForeignRow{true, false, true}
			}
			p := &queryUnitProvider{path: queryUnitPath, scan: scan}
			state, node, owner := queryUnitTree(t, p, zero)
			// Preserve the named slice type and nil/empty distinction: a
			// []catalog.Column snapshot is not DeepEqual to plan.QuerySchema.
			before := slices.Clone(node.QuerySchema)
			backing := node.QuerySchema
			left := node.Left
			right := node.Right
			stage, err := exec.OpenNode(context.Background(), state, node)
			if err != nil {
				t.Fatal(err)
			}
			if p.begins != 0 || p.discoveries != 1 {
				t.Fatal("discovery acquired a resource", p.begins, p.discoveries)
			}
			index, present, err := stage.NextOccurrence(context.Background())
			if err != nil || !present {
				t.Fatal(index, present, err)
			}
			if got := stage.Source.PresenceRow(index); !reflect.DeepEqual(got, []bool{true, false, true}) {
				t.Fatal("presence lost", got)
			}
			if got := stage.Source.Row(index); len(got) != len(before) {
				t.Fatal("hidden cells leaked into ordinary frame", len(got))
			}
			if !reflect.DeepEqual(before, node.QuerySchema) || node.Left != left || node.Right != right {
				t.Fatal("execution mutated retained graph")
			}
			if len(backing) != 0 && &backing[0] != &node.QuerySchema[0] {
				t.Fatal("execution replaced retained schema backing")
			}
			if err := stage.Close(); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if err := owner.Finish(ctx); err != nil {
				t.Fatal(err)
			}
			if err := owner.Finish(ctx); err != nil {
				t.Fatal(err)
			}
			if p.begins != 1 || scan.ends != 1 || scan.cleanupCanceled {
				t.Fatal("resource ownership", p.begins, scan.ends, scan.cleanupCanceled)
			}
		})
	}
}

func TestFDWQueryInvalidPaths(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*catalog.ForeignQueryPath)
	}{
		{"nan", func(p *catalog.ForeignQueryPath) { p.TotalCost = math.NaN() }}, {"infinity", func(p *catalog.ForeignQueryPath) { p.TotalCost = math.Inf(1) }}, {"negative", func(p *catalog.ForeignQueryPath) { p.StartupCost = -1 }}, {"inverted", func(p *catalog.ForeignQueryPath) { p.StartupCost = 2 }},
		{"omitted", func(p *catalog.ForeignQueryPath) { p.Plan.Slots = p.Plan.Slots[1:] }}, {"duplicate", func(p *catalog.ForeignQueryPath) { p.Plan.Slots[1] = p.Plan.Slots[0] }}, {"type", func(p *catalog.ForeignQueryPath) { p.Plan.Slots[0].TypeOID = 25 }}, {"range", func(p *catalog.ForeignQueryPath) { p.Plan.Slots[0].ID = 100 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &queryUnitProvider{path: func(r catalog.ForeignQueryRequest) catalog.ForeignQueryPath {
				path := queryUnitPath(r)
				tc.change(&path)
				return path
			}}
			state, node, owner := queryUnitTree(t, p, false)
			_, err := exec.OpenNode(context.Background(), state, node)
			if err == nil || p.begins != 0 {
				t.Fatal("invalid path reached Begin", err, p.begins)
			}
			if err := owner.Finish(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestFDWQueryBeginErrorOwnership(t *testing.T) {
	primary, cleanup := errors.New("begin"), errors.New("end")
	scan := &queryUnitScan{endError: cleanup}
	p := &queryUnitProvider{path: queryUnitPath, scan: scan, beginError: primary}
	state, node, owner := queryUnitTree(t, p, false)
	stage, err := exec.OpenNode(context.Background(), state, node)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = stage.NextOccurrence(context.Background())
	if !errors.Is(err, primary) {
		t.Fatal(err)
	}
	if err := owner.Finish(context.Background()); !errors.Is(err, cleanup) {
		t.Fatal(err)
	}
	if scan.ends != 1 {
		t.Fatal("Begin+error leaked or double-ended", scan.ends)
	}
}

func TestFDWQueryRequestCopies(t *testing.T) {
	cell := []byte("owned")
	count := int64(2)
	r := catalog.ForeignQueryRequest{Parameters: catalog.ForeignScanParameters{Values: []driver.Value{cell}}, Sources: []catalog.ForeignQuerySource{{Request: catalog.ForeignRequest{TableOptions: []catalog.ForeignOption{{Name: "name", Value: "original"}}}}}, Stage: catalog.ForeignQueryStage{Kind: "limit", Count: &count, Outputs: []catalog.ForeignQueryExpression{{Kind: "literal", Literal: cell, Arguments: []catalog.ForeignQueryExpression{{Kind: "literal", Literal: cell}}}}}}
	copy := exec.CopyForeignQueryRequest(r)
	copy.Parameters.Values[0].([]byte)[0] = 'X'
	copy.Sources[0].Request.TableOptions[0].Value = "changed"
	*copy.Stage.Count = 9
	copy.Stage.Outputs[0].Arguments[0].Literal.([]byte)[0] = 'Y'
	if string(cell) != "owned" || count != 2 || r.Sources[0].Request.TableOptions[0].Value != "original" {
		t.Fatal("request metadata aliases retained inputs")
	}
}

func TestFDWQueryAdmissionBarriers(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*exec.State, *plan.JoinNode)
	}{
		{"server", func(s *exec.State, _ *plan.JoinNode) { s.Source.Sources[1].Foreign.Request.ServerOID++ }},
		{"mapping", func(s *exec.State, _ *plan.JoinNode) { s.Source.Sources[1].Foreign.Request.MappingOID++ }},
		{"handler", func(s *exec.State, _ *plan.JoinNode) { s.Source.Sources[1].Foreign.Request.HandlerOID++ }},
		{"role", func(s *exec.State, _ *plan.JoinNode) { s.Source.Sources[1].Foreign.Request.RoleOID++ }},
		{"rls_denied", func(s *exec.State, _ *plan.JoinNode) { s.Source.Sources[0].RlsDenied = true }},
		{"stored_boundary", func(s *exec.State, _ *plan.JoinNode) { s.Source.Sources[0].Input = &exec.Source{} }},
		{"identity", func(s *exec.State, _ *plan.JoinNode) { s.IdentityReads = true }},
		{"correlated", func(_ *exec.State, n *plan.JoinNode) { n.CorrelatedRight = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &queryUnitProvider{path: queryUnitPath}
			state, node, owner := queryUnitTree(t, p, false)
			tc.change(state, node)
			stage, err := exec.OpenNode(context.Background(), state, node)
			if err == nil {
				_ = stage.Close()
			}
			if p.discoveries != 0 || p.begins != 0 {
				t.Fatal("unsafe boundary offered to provider", p.discoveries, p.begins)
			}
			if err := owner.Finish(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestFDWQueryCompatibilityDecline(t *testing.T) {
	for _, missing := range []string{"expressions", "collations", "visibility"} {
		t.Run(missing, func(t *testing.T) {
			p := &queryUnitProvider{path: func(r catalog.ForeignQueryRequest) catalog.ForeignQueryPath {
				path := queryUnitPath(r)
				switch missing {
				case "expressions":
					path.Compatibility.Expressions = false
				case "collations":
					path.Compatibility.Collations = false
				case "visibility":
					path.Compatibility.StatementVisibility = false
				}
				return path
			}}
			state, node, owner := queryUnitTree(t, p, false)
			stage, err := exec.OpenNode(context.Background(), state, node)
			if err != nil {
				t.Fatal(err)
			}
			if stage.Source.Foreign != nil || p.begins != 0 || p.discoveries != 1 {
				t.Fatal("absent compatibility selected remote execution")
			}
			_ = stage.Close()
			if err := owner.Finish(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestFDWQueryUnusedAttributePadding(t *testing.T) {
	p := &queryUnitProvider{path: queryUnitPath, scan: &queryUnitScan{row: catalog.ForeignRow{nil, int64(8), true, true, true}}}
	state, node, owner := queryUnitTree(t, p, false)
	state.Source.Sources[0].Foreign.Request.RequiredAttributes = nil
	stage, err := exec.OpenNode(context.Background(), state, node)
	if err != nil {
		t.Fatal(err)
	}
	if !p.request.Stage.Children[0].Slots[0].Unused || p.request.Stage.Children[0].Outputs[0].Kind != "literal" || p.request.Stage.Children[0].Outputs[0].Literal != nil || !p.request.Stage.Slots[0].Unused {
		t.Fatal("unrequested source attribute was read or lost its typed padding")
	}
	index, present, err := stage.NextOccurrence(context.Background())
	if err != nil || !present || !stage.Source.Row(index)[0].IsNull() {
		t.Fatal("padding frame import", present, err)
	}
	_ = stage.Close()
	if err := owner.Finish(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestFDWQueryMalformedPresence(t *testing.T) {
	p := &queryUnitProvider{path: queryUnitPath, scan: &queryUnitScan{row: catalog.ForeignRow{int64(7), int64(8), true, nil, true}}}
	state, node, owner := queryUnitTree(t, p, false)
	stage, err := exec.OpenNode(context.Background(), state, node)
	if err != nil {
		t.Fatal(err)
	}
	if _, present, err := stage.NextOccurrence(context.Background()); err == nil || present {
		t.Fatal("NULL presence accepted", present, err)
	}
	_ = stage.Close()
	if err := owner.Finish(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.scan.ends != 1 {
		t.Fatal("failed presence import leaked scan")
	}
}
