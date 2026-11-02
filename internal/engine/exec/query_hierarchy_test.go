package exec

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// These units inspect the actual capture/stage/foreign request owners. The
// public hierarchy scenarios separately prove genuine local and remote locks.
type hierarchyHost struct {
	rowMarkHostProbe
	locks    *LockContext
	acquired []QueryPhysicalRow
	latest   storage.RowVersion
}

func (h *hierarchyHost) Context() context.Context { return context.Background() }
func (h *hierarchyHost) QueryLocks() *LockContext { return h.locks }
func (h *hierarchyHost) AcquireQueryRow(leaf Leaf, row ids.RowID, _ plan.RowMark) (bool, error) {
	h.acquired = append(h.acquired, QueryPhysicalRow{Leaf: leaf, Version: storage.RowVersion{Row: row}})
	return true, nil
}
func (h *hierarchyHost) LatestQueryRow(leaf Leaf, candidate storage.RowVersion, hint int, _ lock.Mode) (storage.RowVersion, bool, error) {
	if leaf.Ref != h.acquired[len(h.acquired)-1].Leaf.Ref || leaf.Relation != h.acquired[len(h.acquired)-1].Leaf.Relation || hint != 1 {
		return storage.RowVersion{}, false, errors.New("wrong physical leaf or raw storage hint")
	}
	if h.latest.Version != 0 {
		return h.latest, true, nil
	}
	return candidate, true, nil
}

type hierarchyPolicy struct {
	expr.Info
	calls *int
}

func (p hierarchyPolicy) Eval(context expr.Context) (sqltype.Value, error) {
	row := context.(*Context).Row
	first, err := row[0].Integer()
	if err != nil {
		return sqltype.Value{}, err
	}
	second, err := row[1].Integer()
	if err != nil {
		return sqltype.Value{}, err
	}
	if first != 8 && first != 80 || second != 7 && second != 70 {
		return sqltype.Value{}, errors.New("policy received physical rather than logical layout")
	}
	*p.calls = *p.calls + 1
	return sqltype.Boolean(p.Usage.Type, true), nil
}

func installHierarchyPolicy(f *hierarchyFixture, calls *int) {
	var boolean *sqltype.Descriptor
	for _, typ := range sqltype.Builtins() {
		if typ.ID() == sqltype.Bool {
			boolean = typ
			break
		}
	}
	f.source.RLS = &RLSProgram{Groups: []RLSRuntimeGroup{{Permissive: []RLSRuntimeClause{{Expression: hierarchyPolicy{Info: expr.Info{Usage: boolean.Usage()}, calls: calls}}}}}}
}

type hierarchyScan struct {
	catalog.ForeignScan
	position   int
	identity   []byte
	failure    error
	ends       int
	parameters catalog.ForeignScanParameters
}

func (s *hierarchyScan) IterateForeignScan(ctx context.Context) (catalog.ForeignRow, bool, error) {
	row, _, present, err := s.IterateForeignIdentity(ctx)
	return row, present, err
}
func (s *hierarchyScan) IterateForeignIdentity(ctx context.Context) (catalog.ForeignRow, catalog.ForeignRowIdentity, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, false, err
	}
	if s.failure != nil {
		return nil, nil, false, s.failure
	}
	if s.position == 2 {
		return nil, nil, false, nil
	}
	s.position++
	s.identity[0] = byte('0' + s.position)
	return catalog.ForeignRow{int64(7), int64(8)}, catalog.ForeignRowIdentity{s.identity}, true, nil
}
func (s *hierarchyScan) ReScanForeignScan(_ context.Context, parameters catalog.ForeignScanParameters) error {
	s.position, s.parameters = 0, parameters
	return nil
}
func (s *hierarchyScan) EndForeignScan(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	s.ends++
	return nil
}

type hierarchyProvider struct {
	*rowMarkProbe
	scan *hierarchyScan
}

func (p *hierarchyProvider) BeginForeignScan(_ context.Context, request catalog.ForeignRequest, _ catalog.ForeignPlan) (catalog.ForeignScan, error) {
	p.begin = request
	return p.scan, nil
}

type hierarchyFixture struct {
	source, local, foreign *Source
	state                  *State
	node                   *plan.SourceNode
	provider               *hierarchyProvider
	host                   *hierarchyHost
	owner                  *ForeignScanOwner
	integer, oid           *sqltype.Descriptor
}

func newHierarchyFixture(t *testing.T, locks *LockContext) *hierarchyFixture {
	t.Helper()
	f := &hierarchyFixture{}
	for _, typ := range sqltype.Builtins() {
		if typ.ID() == sqltype.Int4 {
			f.integer = typ
		}
		if typ.ID() == sqltype.Oid {
			f.oid = typ
		}
	}
	value := func(n int64) sqltype.Value {
		v, err := sqltype.Integer(f.integer, n)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	domain := &ids.Domain{}
	parent := domain.Ref(1, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 501})
	local := domain.Ref(1, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 502})
	foreign := domain.Ref(1, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 503})
	columns := []catalog.Column{{Name: "v", Usage: f.integer.Usage()}, {Name: "w", Usage: f.integer.Usage()}}
	physical := []catalog.Column{columns[1], columns[0]}
	f.provider = &hierarchyProvider{rowMarkProbe: &rowMarkProbe{selected: catalog.ForeignRowMarkPlan{Type: catalog.ForeignRowMarkUpdate, Identity: catalog.ForeignUpdateTargets{Names: []string{"rid"}}}, recheck: func(catalog.ForeignRow) (bool, error) { return true, nil }}, scan: &hierarchyScan{identity: []byte("0-row")}}
	f.host = &hierarchyHost{rowMarkHostProbe: rowMarkHostProbe{tx: &rowMarkTransactionProbe{}}, locks: locks}
	rows := []storage.RowVersion{
		{Row: 11, Version: 21, Born: 1, Values: []sqltype.Value{value(0), value(0)}},
		{Row: 12, Version: 22, Born: 2, Values: []sqltype.Value{value(7), value(8)}},
	}
	owner := &catalog.Owner{Domain: domain}
	state := catalog.NewState(1)
	logical := &catalog.Relation{ObjectAddress: parent.Address, ObjectName: "parent", RelationKind: catalog.TableRelation, Columns: columns, Attributes: []int32{1, 2}}
	logical.ColumnMeta.Generations = map[int32]catalog.ColumnGeneration{1: {
		Kind:    catalog.GenerationVirtual,
		Planned: plan.ColumnExpr{Info: expr.Info{Usage: f.integer.Usage()}, Scope: 1, Frame: 1, Slot: 1},
	}}
	state.SetDescriptor(parent.Address, logical)
	view := visibility.NewView(&visibility.Data{Catalog: catalog.NewRead(owner, state), Frontier: 3, Committed: map[ids.ObjectAddress][]storage.RowVersion{local.Address: rows}})
	var err error
	f.local, err = CapturePrefix(view, local)
	if err != nil {
		t.Fatal(err)
	}
	f.local.Visible = []int{1}
	remote := &ForeignQuerySource{Wrapper: f.provider, Columns: physical, Request: catalog.ForeignRequest{Columns: []catalog.ForeignColumn{{Attribute: 1}, {Attribute: 2}}, RequiredAttributes: []int32{1, 2}}, ImportValue: func(_ context.Context, cell driver.Value, usage sqltype.Usage, _ sqltype.InputEnvironment) (sqltype.Value, error) {
		return sqltype.Integer(usage.Type, cell.(int64))
	}, ExportValue: func(_ context.Context, v sqltype.Value, _ sqltype.OutputEnvironment) (driver.Value, error) {
		return v.Integer()
	}}
	f.foreign = &Source{Ref: foreign, Foreign: remote, Anonymous: [][]sqltype.Value{}}
	f.source = &Source{Ref: parent, View: view, Host: f.host, Locks: locks, Inheritance: []InheritanceQuerySegment{
		{Leaf: Leaf{Ref: local, Relation: &catalog.Relation{ObjectAddress: local.Address, ObjectName: "local", RelationKind: catalog.TableRelation, Columns: physical}, Storage: &storage.Relation{Versions: rows}}, Prefix: f.local, Layout: []int{1, 0}, Scratch: make([]sqltype.Value, 2)},
		{Leaf: Leaf{Ref: foreign, Relation: &catalog.Relation{ObjectAddress: foreign.Address, ObjectName: "remote", RelationKind: catalog.ForeignRelation, Columns: physical}}, Prefix: f.foreign, Layout: []int{1, 0}, Scratch: make([]sqltype.Value, 2)},
	}}
	if locks != nil {
		CaptureQueryLockLeaves(locks, Leaf{Ref: parent, Relation: &catalog.Relation{RelationKind: catalog.TableRelation}}, f.source, plan.RowMark{Mode: lock.ForUpdate, Wait: parser.RowLockNowait})
	} else {
		f.local.Host, f.foreign.Host = f.host, f.host
	}
	f.owner = &ForeignScanOwner{}
	f.owner.Add(remote)
	f.state = &State{Source: f.source, IdentityReads: locks != nil, methodsCaptured: true, asyncCaptured: true, Retain: true}
	f.node = &plan.SourceNode{Ref: parent, QuerySchema: columns}
	t.Cleanup(func() {
		if err := f.owner.Finish(context.Background()); err != nil {
			t.Error(err)
		}
		f.source.Close()
	})
	return f
}

func hierarchyInteger(t *testing.T, value sqltype.Value) int64 {
	t.Helper()
	n, err := value.Integer()
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func hierarchyNext(t *testing.T, stage *Stage) int {
	t.Helper()
	ordinal, present, err := stage.NextOccurrence(context.Background())
	if err != nil || !present {
		t.Fatalf("hierarchy demand: %v %v", present, err)
	}
	return stage.Index(ordinal)
}

func TestForeignHierarchyLineage(t *testing.T) {
	for _, mode := range []string{"marked", "identity", "unlocked"} {
		t.Run(mode, func(t *testing.T) {
			var locks *LockContext
			if mode != "unlocked" {
				locks = &LockContext{IdentityOnly: mode == "identity"}
			}
			f := newHierarchyFixture(t, locks)
			policyCalls := 0
			installHierarchyPolicy(f, &policyCalls)
			if locks != nil {
				if len(locks.Leaves) != 2 || !reflect.DeepEqual(AppendQueryOccurrences(nil, f.source), []int{0, 1}) {
					t.Fatal("capture did not expand physical manifest before opening", locks, f.source.Occurrences)
				}
				for i, segment := range f.source.Inheritance {
					entry := locks.Leaves[i]
					if entry.Source != segment.Prefix || !reflect.DeepEqual(entry.Leaf, segment.Leaf) || entry.Source == f.source || entry.Mark.Mode != lock.ForUpdate || entry.Mark.Wait != parser.RowLockNowait {
						t.Fatal("aggregate or reconstructed lock target", entry)
					}
				}
				pending := AppendPendingRowMarks(nil, []plan.RowMark{{Mode: lock.ForUpdate}}, f.source)
				if len(pending) != 1 || !reflect.DeepEqual(pending[0].Occurrences, []int{0, 1}) {
					t.Fatal(pending)
				}
			}
			stage, err := OpenNode(context.Background(), f.state, f.node)
			if err != nil {
				t.Fatal(err)
			}
			defer stage.Close()
			for i := range 3 {
				index := hierarchyNext(t, &stage)
				if row := f.source.Row(index); hierarchyInteger(t, row[0]) != 8 || hierarchyInteger(t, row[1]) != 7 {
					t.Fatal("parent layout", row)
				}
				if locks == nil {
					continue
				}
				want := []int{0, AbsentQueryIndex}
				if i > 0 {
					want = []int{AbsentQueryIndex, i - 1}
				}
				if got := AppendQueryProvenance(nil, f.source, index); !reflect.DeepEqual(got, want) {
					t.Fatal(got, want)
				}
				versions := AppendQueryVersions(nil, f.source, index, locks)
				active := 0
				if i > 0 {
					active = 1
				}
				if len(versions) != 2 || versions[1-active].Values != nil || hierarchyInteger(t, versions[active].Values[0]) != 7 {
					t.Fatal("physical versions", versions)
				}
				if i == 0 {
					locator := CursorOccurrencePhysicalRow(f.source, index, 0, locks)
					if versions[0].Row != 12 || versions[0].Version != 22 || versions[0].Born != 2 || !reflect.DeepEqual(locator.Leaf, locks.Leaves[0].Leaf) || locator.Hint != 1 {
						t.Fatal("lost true visible/raw version", versions, locator)
					}
				} else if locks.RowMarked() {
					if len(f.foreign.Foreign.captured) != i || f.foreign.Foreign.captured[i-1].request != f.foreign.Foreign.Executions[0].markRequest {
						t.Fatal("foreign request/index authority")
					}
				}
			}
			if policyCalls != 3 {
				t.Fatal("logical RLS not applied once per emitted row", policyCalls)
			}
			if mode != "marked" && (len(f.source.Versions) != 0 || f.source.VersionValid != nil) {
				t.Fatal("unneeded version cache")
			}
			if mode == "marked" {
				a, b := f.foreign.Foreign.captured[0], f.foreign.Foreign.captured[1]
				if string(a.identity[0].([]byte)) != "1-row" || string(b.identity[0].([]byte)) != "2-row" || f.provider.seen.Strength != catalog.ForeignLockUpdate || f.provider.seen.WaitPolicy != catalog.ForeignLockNoWait {
					t.Fatal("duplicate values lost distinct identity or inherited mark")
				}
			}
		})
	}
	// Demand identity through the real projection opener and Stage.Next, rather
	// than preinstalling an evaluator's occurrence/provenance buffers by hand.
	// The identity-only case is the unlocked tableoid path used by public SQL.
	for _, identityOnly := range []bool{false, true} {
		t.Run(map[bool]string{false: "marked-projection", true: "identity-projection"}[identityOnly], func(t *testing.T) {
			f := newHierarchyFixture(t, &LockContext{IdentityOnly: identityOnly})
			f.state.IdentityReads = false
			logical, _ := f.source.View.Catalog().Relation(f.source.Ref.Address)
			origin := f.source.Ref
			origin.Address.SubID = 1
			project := &plan.ProjectNode{
				QuerySchema: plan.QuerySchema{{Name: "v", Usage: f.integer.Usage()}, {Name: "generated", Usage: f.integer.Usage()}, {Name: "physical", Usage: f.oid.Usage()}},
				Child:       f.node,
				Expressions: []expr.Expr{
					plan.ColumnExpr{Info: expr.Info{Usage: f.integer.Usage()}, Scope: 1, Frame: 1, Slot: 0},
					plan.ColumnExpr{Info: expr.Info{Usage: f.integer.Usage()}, Origin: origin, Scope: 1, Frame: 1, Slot: 0, Virtual: logical.ColumnMeta.Generations[1].Planned, VirtualOwner: logical, VirtualAttribute: 1, RowWidth: 2, QueryOccurrence: 1},
					plan.TableOIDExpr{Info: expr.Info{Usage: f.oid.Usage()}, Owner: f.source.Ref, Scope: 1, Frame: 1, QueryOccurrence: 1},
				},
			}
			stage, err := OpenQueryProjectStage(context.Background(), f.state, project)
			if err != nil {
				t.Fatal(err)
			}
			defer stage.Close()
			row := make([]sqltype.Value, 3)
			for _, physical := range []ids.ObjectRef{f.local.Ref, f.foreign.Ref, f.foreign.Ref} {
				present, err := stage.Next(context.Background(), row)
				if err != nil || !present {
					t.Fatal("projected hierarchy identity", present, err)
				}
				oid, err := row[2].OID()
				if err != nil || oid != uint32(physical.Address.ObjectID) || hierarchyInteger(t, row[0]) != 8 || hierarchyInteger(t, row[1]) != 7 {
					t.Fatal("projection lost logical layout or actual physical occurrence", row, err)
				}
			}
			if present, err := stage.Next(context.Background(), row); err != nil || present {
				t.Fatal("projected hierarchy multiplicity", present, err)
			}
			if identityOnly && (len(f.host.acquired) != 0 || len(f.source.Versions) != 0 || len(f.foreign.Foreign.captured) != 0) {
				t.Fatal("identity-only projection requested row locking")
			}
		})
	}
}

func TestForeignHierarchyRewindOwnership(t *testing.T) {
	f := newHierarchyFixture(t, &LockContext{})
	parameter := []byte("old")
	f.state.Parameters = []sqltype.Value{{}}
	f.foreign.Foreign.ExportValue = func(context.Context, sqltype.Value, sqltype.OutputEnvironment) (driver.Value, error) {
		return parameter, nil
	}
	stage, err := OpenNode(context.Background(), f.state, f.node)
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	hierarchyNext(t, &stage)
	index := hierarchyNext(t, &stage)
	old := f.foreign.Foreign.captured[0].request
	identity := f.foreign.Foreign.captured[0].identity
	version := f.source.Versions[index*2+1]
	manifest := &f.source.Occurrences[0]
	pin := f.local.View.Data()
	parameter[0], f.provider.scan.identity[0] = 'X', 'X'
	f.provider.begin.Parameters.Values[0].([]byte)[1] = 'X'
	parameter = []byte("new")
	if changed, err := stage.Rewind(context.Background()); err != nil || !changed {
		t.Fatal(changed, err)
	}
	if f.source.FlatCount != 0 || len(f.source.Flat) != 0 || len(f.source.Provenance) != 0 || len(f.source.Versions) != 0 || f.source.VersionValid != nil || len(f.source.PhysicalRefs) != 0 || len(stage.Selection) != 0 || stage.Done {
		t.Fatal("output generations reset out of step")
	}
	if manifest != &f.source.Occurrences[0] || pin != f.local.View.Data() || f.foreign.Foreign.Executions[0].active {
		t.Fatal("rewind destroyed manifest/pin or kept borrowed stage active")
	}
	hierarchyNext(t, &stage)
	index = hierarchyNext(t, &stage)
	current := f.foreign.Foreign.captured[1].request
	if got := AppendQueryProvenance(nil, f.source, index); !reflect.DeepEqual(got, []int{-1, 1}) {
		t.Fatal("rescan assumed child index zero", got)
	}
	if old == current || current != f.foreign.Foreign.Executions[0].markRequest || old.Transaction != f.host.tx || current.Transaction != old.Transaction || string(old.Parameters.Values[0].([]byte)) != "old" || string(current.Parameters.Values[0].([]byte)) != "new" || string(identity[0].([]byte)) != "1-row" || hierarchyInteger(t, version.Values[0]) != 7 {
		t.Fatal("retained foreign authority mutated across generation")
	}
	if &version.Values[0] != &f.foreign.Anonymous[0][0] || old != f.foreign.Foreign.captured[0].request {
		t.Fatal("old version/request backing replaced")
	}
	if err := stage.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.owner.Finish(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.provider.scan.ends != 1 || f.foreign.Foreign.Executions[0].active {
		t.Fatal("owner failed to retire real execution once")
	}
	for _, canceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "provider-error", true: "cancel"}[canceled], func(t *testing.T) {
			g := newHierarchyFixture(t, &LockContext{})
			s, err := OpenNode(context.Background(), g.state, g.node)
			if err != nil {
				t.Fatal(err)
			}
			hierarchyNext(t, &s)
			hierarchyNext(t, &s)
			primary := errors.New("hierarchy provider failure")
			g.provider.scan.failure = primary
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if canceled {
				cancel()
				primary = context.Canceled
			}
			if _, _, err := s.NextOccurrence(ctx); !errors.Is(err, primary) || !s.Closed || g.foreign.Foreign.Executions[0].active {
				t.Fatal("primary failure/borrowed stage ownership", err)
			}
			if err := g.owner.Finish(ctx); err != nil {
				t.Fatal(err)
			}
			if g.provider.scan.ends != 1 {
				t.Fatal("scan cleanup was not once-only")
			}
		})
	}
}

func TestForeignHierarchyRecheckIdentity(t *testing.T) {
	f := newHierarchyFixture(t, &LockContext{})
	value := func(n int64) sqltype.Value {
		v, err := sqltype.Integer(f.integer, n)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	f.host.latest = storage.RowVersion{Row: 12, Version: 23, Born: 3, Values: []sqltype.Value{value(70), value(80)}}
	f.provider.result = catalog.ForeignRefetchResult{Present: true, Updated: true, Row: catalog.ForeignRow{int64(70), int64(80)}}
	policyCalls := 0
	installHierarchyPolicy(f, &policyCalls)
	child, err := OpenNode(context.Background(), f.state, f.node)
	if err != nil {
		t.Fatal(err)
	}
	child.Pending = AppendPendingRowMarks(nil, []plan.RowMark{{Mode: lock.ForUpdate}}, f.source)
	stage, err := SettlePendingQueryMarks(context.Background(), f.state, f.node, child)
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	for i := range 3 {
		index := hierarchyNext(t, &stage)
		if row := f.source.Row(index); hierarchyInteger(t, row[0]) != 80 || hierarchyInteger(t, row[1]) != 70 {
			t.Fatal("stale or physical-layout replacement", row)
		}
		evaluation := f.state.Evaluation(context.Background())
		evaluation.BindQueryRow(f.source, index)
		logical, _ := f.source.View.Catalog().Relation(f.source.Ref.Address)
		origin := f.source.Ref
		origin.Address.SubID = 1
		virtual := plan.ColumnExpr{Info: expr.Info{Usage: f.integer.Usage()}, Origin: origin, Scope: 1, Frame: 1, Slot: 0, Virtual: logical.ColumnMeta.Generations[1].Planned, VirtualOwner: logical, VirtualAttribute: 1, RowWidth: 2, QueryOccurrence: 1}
		generated, err := evaluation.EvalColumn(virtual)
		if err != nil || hierarchyInteger(t, generated) != 70 {
			t.Fatal("virtual recipe lost effective logical tuple", generated, err)
		}
		physical, visible, err := evaluation.queryOperandSource(0)
		if err != nil || visible < 0 || physical == f.source {
			t.Fatal("logical member did not select actual capture", physical, visible, err)
		}
		// EPQ supplies physical tuples while the expression still belongs to
		// the logical parent. Use the actual effective candidate version.
		evaluation.QueryCurrentRows = make([][]sqltype.Value, 2)
		evaluation.QueryCurrentRows[physical.Occurrence] = CursorOccurrenceVersion(f.source, index, physical.Occurrence, f.source.Locks).Values
		generated, err = evaluation.EvalColumn(virtual)
		if err != nil || hierarchyInteger(t, generated) != 70 {
			t.Fatal("virtual EPQ used child layout or absent sibling", generated, err)
		}
		evaluation.QueryCurrentRows = nil
		want := f.local.Ref
		if i > 0 {
			want = f.foreign.Ref
		}
		oid, err := evaluation.EvalTableOID(plan.TableOIDExpr{Info: expr.Info{Usage: f.oid.Usage()}, Owner: f.source.Ref, Scope: 1, Frame: 1, QueryOccurrence: 1})
		number, oidErr := oid.OID()
		if err != nil || oidErr != nil || number != uint32(want.Address.ObjectID) {
			t.Fatal("tableoid selected absent sibling", oid, err, oidErr)
		}
		if absent, err := evaluation.queryOperandAbsent(0); err != nil || absent {
			t.Fatal(absent, err)
		}
		if i == 0 && CursorOccurrenceVersion(f.source, index, 0, f.source.Locks).Version != 23 {
			t.Fatal("effective local version lost")
		}
		if i > 0 && f.provider.refetches != i {
			t.Fatal("absent sibling refetched or active row skipped", f.provider.refetches)
		}
		if i > 0 {
			captured := &f.foreign.Foreign.captured[visible]
			if f.provider.seen.Transaction != captured.request.Transaction || f.provider.seen.Transaction != f.host.tx || !reflect.DeepEqual(f.provider.identity, captured.identity) || &captured.latest[0] != &f.source.Versions[index*2+1].Values[0] {
				t.Fatal("recheck reconstructed foreign authority or lost effective backing")
			}
		}
	}
	if policyCalls != 6 {
		t.Fatal("RLS must run once on capture and once per replacement", policyCalls)
	}
	// A later frontier must see cached effective versions, not resurrect the
	// original predecessor or refetch the already settled foreign identities.
	later := Stage{Source: f.source, Selection: []int{0, 1, 2}, Pending: AppendPendingRowMarks(nil, []plan.RowMark{{Mode: lock.ForUpdate}}, f.source)}
	second, err := SettlePendingQueryMarks(context.Background(), f.state, f.node, later)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	hierarchyNext(t, &second)
	hierarchyNext(t, &second)
	if f.provider.refetches != 2 || CursorOccurrenceVersion(f.source, 0, 0, f.source.Locks).Version != 23 {
		t.Fatal("later frontier lost effective version/foreign settlement")
	}
	before := append([]sqltype.Value(nil), f.source.Row(0)...)
	f.source.Inheritance[0].MapValues([]sqltype.Value{value(1), value(2)})
	if !reflect.DeepEqual(before, f.source.Row(0)) {
		t.Fatal("retained candidate aliases mapping scratch")
	}
	evaluation := f.state.Evaluation(context.Background())
	evaluation.QueryOccurrences, evaluation.QueryProvenance = f.source.Occurrences, []int{-1, -1}
	if absent, err := evaluation.queryOperandAbsent(0); err != nil || !absent {
		t.Fatal("NULL extension inferred from visible cells", absent, err)
	}
	oid, err := evaluation.EvalTableOID(plan.TableOIDExpr{Info: expr.Info{Usage: f.oid.Usage()}, Owner: f.source.Ref, Scope: 1, Frame: 1, QueryOccurrence: 1})
	if err != nil || !oid.IsNull() {
		t.Fatal("absent tableoid is not typed NULL", oid, err)
	}
	f.source.Locks.Leaves[0].Absent, f.source.Locks.Leaves[1].Absent = true, true
	if !absentLockedSource(f.source, f.source.Locks) {
		t.Fatal("absent alternative hierarchy treated as present")
	}
}
