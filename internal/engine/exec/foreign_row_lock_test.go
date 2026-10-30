package exec

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// These contract probes intentionally do not model a remote lock manager. Real
// locks, isolation and rollback are exercised by the public SQL provider suite.
type rowMarkProbe struct {
	catalog.ForeignDataWrapper
	catalog.ForeignTransactionProvider
	selected  catalog.ForeignRowMarkPlan
	result    catalog.ForeignRefetchResult
	failure   error
	seen      catalog.ForeignRowMarkRequest
	identity  catalog.ForeignRowIdentity
	refetches int
	recheck   func(catalog.ForeignRow) (bool, error)
	scan      *rowMarkScanProbe
	planning  []catalog.ForeignRequest
	begin     catalog.ForeignRequest
}

func (p *rowMarkProbe) GetForeignRowMarkType(_ context.Context, r catalog.ForeignRowMarkRequest) (catalog.ForeignRowMarkPlan, error) {
	p.seen = r
	return p.selected, nil
}
func (p *rowMarkProbe) RefetchForeignRow(_ context.Context, r catalog.ForeignRowMarkRequest, identity catalog.ForeignRowIdentity) (catalog.ForeignRefetchResult, error) {
	p.seen, p.identity = r, identity
	p.refetches++
	return p.result, p.failure
}
func (p *rowMarkProbe) RecheckForeignScan(_ context.Context, r catalog.ForeignRowMarkRequest, row catalog.ForeignRow) (bool, error) {
	p.seen = r
	return p.recheck(row)
}
func (p *rowMarkProbe) GetForeignRelSize(_ context.Context, r catalog.ForeignRequest) (catalog.ForeignRelSize, error) {
	p.planning = append(p.planning, r)
	return catalog.ForeignRelSize{Rows: 1}, nil
}
func (p *rowMarkProbe) GetForeignPaths(_ context.Context, r catalog.ForeignRequest, _ catalog.ForeignRelSize) ([]catalog.ForeignPath, error) {
	p.planning = append(p.planning, r)
	return []catalog.ForeignPath{{Attributes: r.RequiredAttributes}}, nil
}
func (p *rowMarkProbe) GetForeignPlan(_ context.Context, r catalog.ForeignRequest, path catalog.ForeignPath) (catalog.ForeignPlan, error) {
	p.planning = append(p.planning, r)
	return catalog.ForeignPlan{Attributes: path.Attributes}, nil
}
func (p *rowMarkProbe) BeginForeignScan(_ context.Context, r catalog.ForeignRequest, _ catalog.ForeignPlan) (catalog.ForeignScan, error) {
	p.begin = r
	return p.scan, nil
}

type rowMarkScanProbe struct {
	catalog.ForeignScan
	identity   []byte
	ends       int
	canceled   bool
	parameters catalog.ForeignScanParameters
}

func (s *rowMarkScanProbe) IterateForeignScan(context.Context) (catalog.ForeignRow, bool, error) {
	return catalog.ForeignRow{int64(7)}, true, nil
}

func (s *rowMarkScanProbe) IterateForeignIdentity(context.Context) (catalog.ForeignRow, catalog.ForeignRowIdentity, bool, error) {
	return catalog.ForeignRow{int64(7)}, catalog.ForeignRowIdentity{s.identity}, true, nil
}
func (s *rowMarkScanProbe) ReScanForeignScan(_ context.Context, parameters catalog.ForeignScanParameters) error {
	s.parameters = parameters
	return nil
}
func (s *rowMarkScanProbe) EndForeignScan(ctx context.Context) error {
	s.ends++
	s.canceled = ctx.Err() != nil
	return nil
}

type rowMarkHostProbe struct {
	Host
	tx catalog.ForeignTransaction
}

func (*rowMarkHostProbe) Environments() (sqltype.InputEnvironment, sqltype.OutputEnvironment) {
	return sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}
}
func (*rowMarkHostProbe) CTEInvocation() *CTEInvocation                               { return nil }
func (*rowMarkHostProbe) ReleaseReplacementRead()                                     {}
func (*rowMarkHostProbe) TouchForeignScan(context.Context, *ForeignQuerySource) error { return nil }
func (h *rowMarkHostProbe) EnlistForeign(context.Context, catalog.ForeignDataWrapper, catalog.ForeignRequest, bool) (catalog.ForeignTransaction, error) {
	return h.tx, nil
}
func (*rowMarkHostProbe) ForeignCharacteristics() (*catalog.ForeignTransactionCharacteristics, error) {
	return &catalog.ForeignTransactionCharacteristics{Isolation: "serializable", ReadOnly: true, Deferrable: true}, nil
}

type rowMarkTransactionProbe struct{ catalog.ForeignTransaction }

func TestForeignRowMarkRequestOwnership(t *testing.T) {
	original := catalog.ForeignRequest{
		Identity:        &catalog.ForeignUpdateTargets{Names: []string{"rid"}},
		RowMark:         &catalog.ForeignRowMarkPlan{Type: catalog.ForeignRowMarkUpdate, Identity: catalog.ForeignUpdateTargets{Names: []string{"rid"}}},
		Characteristics: &catalog.ForeignTransactionCharacteristics{Isolation: "serializable", ReadOnly: true},
		Parameters:      catalog.ForeignScanParameters{Values: []driver.Value{[]byte("old")}},
	}
	copy := CopyForeignRequest(original)
	copy.Identity.Names[0] = "changed"
	copy.RowMark.Identity.Names[0] = "changed"
	copy.Characteristics.Isolation = "read committed"
	copy.Parameters.Values[0].([]byte)[0] = 'X'
	if original.Identity.Names[0] != "rid" || original.RowMark.Identity.Names[0] != "rid" || original.Characteristics.Isolation != "serializable" || string(original.Parameters.Values[0].([]byte)) != "old" {
		t.Fatal("callback request aliases execution-owned metadata")
	}
	for _, pair := range []struct {
		mode     lock.Mode
		strength catalog.ForeignLockStrength
	}{
		{0, catalog.ForeignLockNone}, {lock.ForKeyShare, catalog.ForeignLockKeyShare}, {lock.ForShare, catalog.ForeignLockShare}, {lock.ForNoKeyUpdate, catalog.ForeignLockNoKeyUpdate}, {lock.ForUpdate, catalog.ForeignLockUpdate},
	} {
		for _, wait := range []struct {
			local   parser.RowLockWait
			foreign catalog.ForeignLockWaitPolicy
		}{
			{parser.RowLockBlock, catalog.ForeignLockBlock}, {parser.RowLockNowait, catalog.ForeignLockNoWait}, {parser.RowLockSkip, catalog.ForeignLockSkipLocked},
		} {
			r := foreignMarkRequest(original, plan.RowMark{Mode: pair.mode, Wait: wait.local})
			if r.Strength != pair.strength || r.WaitPolicy != wait.foreign {
				t.Fatal(r)
			}
		}
	}
}

func TestForeignRowMarkCapabilities(t *testing.T) {
	ctx := context.Background()
	f := &ForeignQuerySource{}
	request := catalog.ForeignRequest{Columns: []catalog.ForeignColumn{{Attribute: 1}, {Attribute: 2}}}
	if err := f.selectRowMark(ctx, &request, plan.RowMark{Mode: lock.ForUpdate}); err != nil || request.RowMark.Type != catalog.ForeignRowMarkCopy || request.Identity != nil || !reflect.DeepEqual(request.RequiredAttributes, []int32{1, 2}) {
		t.Fatal("missing optional provider must select COPY/full tuple", request, err)
	}
	p := &rowMarkProbe{}
	r := foreignMarkRequest(catalog.ForeignRequest{}, plan.RowMark{Mode: lock.ForUpdate})
	for _, selected := range []catalog.ForeignRowMarkPlan{
		{Type: 255}, {Type: catalog.ForeignRowMarkReference},
		{Type: catalog.ForeignRowMarkKeyShare, Identity: catalog.ForeignUpdateTargets{Names: []string{"rid"}}},
		{Type: catalog.ForeignRowMarkUpdate, Identity: catalog.ForeignUpdateTargets{Names: []string{"rid", "rid"}}},
	} {
		if err := validateForeignMark(p, r, selected); err == nil {
			t.Fatal("accepted invalid descriptor", selected)
		}
	}
	selected := catalog.ForeignRowMarkPlan{Type: catalog.ForeignRowMarkUpdate, Identity: catalog.ForeignUpdateTargets{Names: []string{"rid"}}}
	if err := validateForeignMark(p, r, selected); err != nil {
		t.Fatal(err)
	}
	if err := validateForeignMark(p, foreignMarkRequest(catalog.ForeignRequest{}, plan.RowMark{}), selected); err == nil {
		t.Fatal("locking unmarked sibling")
	}
	for _, identity := range []catalog.ForeignRowIdentity{nil, {int64(1), int64(2)}, {map[string]int{"x": 1}}, {[]int{1}}} {
		if err := validateForeignIdentity(identity, &selected.Identity); err == nil {
			t.Fatal("accepted invalid identity", identity)
		}
	}
	if err := validateForeignIdentity(catalog.ForeignRowIdentity{[]byte("opaque")}, &selected.Identity); err != nil {
		t.Fatal(err)
	}
}

func TestForeignRowMarkScanOwnership(t *testing.T) {
	for _, selected := range []catalog.ForeignRowMarkType{catalog.ForeignRowMarkCopy, catalog.ForeignRowMarkUpdate} {
		t.Run(map[catalog.ForeignRowMarkType]string{catalog.ForeignRowMarkCopy: "COPY", catalog.ForeignRowMarkUpdate: "UPDATE"}[selected], func(t *testing.T) {
			testForeignRowMarkScanOwnership(t, selected)
		})
	}
}

func testForeignRowMarkScanOwnership(t *testing.T, selected catalog.ForeignRowMarkType) {
	ctx := context.Background()
	var integer *sqltype.Descriptor
	for _, descriptor := range sqltype.Builtins() {
		if descriptor.ID() == sqltype.Int4 {
			integer = descriptor
			break
		}
	}
	p := &rowMarkProbe{selected: catalog.ForeignRowMarkPlan{Type: selected, Identity: catalog.ForeignUpdateTargets{Names: []string{"rid"}}}, scan: &rowMarkScanProbe{identity: []byte("original")}}
	f := &ForeignQuerySource{Wrapper: p, Columns: []catalog.Column{{Name: "v", Usage: integer.Usage()}}, Request: catalog.ForeignRequest{Columns: []catalog.ForeignColumn{{Attribute: 1}}, RequiredAttributes: []int32{1}}, ImportValue: func(_ context.Context, v driver.Value, usage sqltype.Usage, _ sqltype.InputEnvironment) (sqltype.Value, error) {
		return sqltype.Integer(usage.Type, v.(int64))
	}}
	tx := &rowMarkTransactionProbe{}
	source := &Source{Foreign: f, Anonymous: [][]sqltype.Value{}, Host: &rowMarkHostProbe{tx: tx}}
	source.Locks = &LockContext{Leaves: []QueryLockLeaf{{Source: source, Mark: plan.RowMark{Mode: lock.ForUpdate}}}}
	owner := &ForeignScanOwner{}
	owner.Add(f)
	state := &State{Source: source}
	// Exported parameter buffers belong to the adapter/provider, not the snapshot.
	parameter := []byte("old")
	f.ExportValue = func(_ context.Context, _ sqltype.Value, _ sqltype.OutputEnvironment) (driver.Value, error) {
		return parameter, nil
	}
	state.Parameters = []sqltype.Value{{}}
	stage, err := OpenForeignStage(ctx, state, source, &plan.SourceNode{QuerySchema: f.Columns})
	if err != nil {
		t.Fatal(err)
	}
	if present, err := stage.Produce(ctx, &stage); err != nil || !present {
		t.Fatal(present, err)
	}
	p.scan.identity[0] = 'X'
	if selected != catalog.ForeignRowMarkCopy && string(f.captured[0].identity[0].([]byte)) != "original" {
		t.Fatal("capture aliases scan identity buffer")
	}
	if f.captured[0].request.Transaction != tx || f.captured[0].request.Characteristics.Isolation != "serializable" {
		t.Fatal("lost owner execution facts")
	}
	if present, err := stage.Produce(ctx, &stage); err != nil || !present {
		t.Fatal(present, err)
	}
	old := f.captured[0].request
	if old != f.captured[1].request || old != f.Executions[0].markRequest || &old.Parameters.Values[0] != &f.Executions[0].request.Parameters.Values[0] {
		t.Fatal("rows must share the actual execution snapshot, not equal copies")
	}
	// Providers can retain and mutate requests after Begin and rescan.
	parameter[0] = 'X'
	p.begin.Parameters.Values[0].([]byte)[1] = 'X'
	p.begin.RowMark.Type = catalog.ForeignRowMarkReference
	p.begin.Characteristics.Isolation = "read committed"
	p.selected.Identity.Names[0] = "mutated"
	if string(old.Parameters.Values[0].([]byte)) != "old" || old.RowMark.Type != selected || old.RowMark.Identity.Names[0] != "rid" || old.Characteristics.Isolation != "serializable" {
		t.Fatal("provider buffers alias immutable snapshot")
	}
	parameter = []byte("new")
	for _, planning := range p.planning {
		if planning.Transaction != nil || planning.Characteristics != nil {
			t.Fatal("planning leaked execution owner", planning)
		}
	}
	if _, err := stage.Reset(ctx, &stage); err != nil {
		t.Fatal(err)
	}
	if present, err := stage.Produce(ctx, &stage); err != nil || !present {
		t.Fatal(present, err)
	}
	if present, err := stage.Produce(ctx, &stage); err != nil || !present {
		t.Fatal(present, err)
	}
	current := f.captured[2].request
	if len(f.captured) != 4 || old == current || current != f.captured[3].request || current != f.Executions[0].markRequest {
		t.Fatal("rescan must share a new generation without replacing old captures")
	}
	p.scan.parameters.Values[0].([]byte)[0] = 'X'
	if string(old.Parameters.Values[0].([]byte)) != "old" || string(current.Parameters.Values[0].([]byte)) != "new" {
		t.Fatal("rescan changed old parameters or exposed new snapshot buffers")
	}
	if selected != catalog.ForeignRowMarkCopy && (string(f.captured[0].identity[0].([]byte)) != "original" || string(f.captured[2].identity[0].([]byte)) != "Xriginal") {
		t.Fatal("rescan identity/source index misalignment")
	}
	p.recheck = func(catalog.ForeignRow) (bool, error) { return true, nil }
	p.result = catalog.ForeignRefetchResult{Row: catalog.ForeignRow{int64(9)}, Present: true, Updated: true}
	for i, expected := range []string{"old", "old", "new", "new"} {
		if selected != catalog.ForeignRowMarkCopy {
			if _, present, _, err := f.refetchRow(ctx, source, i, plan.RowMark{Mode: lock.ForUpdate}, false); err != nil || !present {
				t.Fatal("refetch failed", present, err)
			}
			if string(p.seen.Parameters.Values[0].([]byte)) != expected {
				t.Fatal("refetch lost captured generation parameters", i, p.seen.Parameters)
			}
			p.seen.Parameters.Values[0].([]byte)[0] = 'X'
			p.seen.RowMark.Identity.Names[0] = "refetch-mutated"
			p.identity[0].([]byte)[0] = 'Y'
			if i == 0 && (f.captured[1].latest != nil || f.captured[1].settled.Mode != 0) {
				t.Fatal("shared request must not share per-row tuple or settled state")
			}
		}
		if pass, err := f.recheckRow(ctx, source, i, source.Row(i)); err != nil || !pass {
			t.Fatal("recheck failed", pass, err)
		}
		if string(p.seen.Parameters.Values[0].([]byte)) != expected {
			t.Fatal("recheck lost captured generation parameters", i, p.seen.Parameters)
		}
		p.seen.Parameters.Values[0].([]byte)[0] = 'X'
		p.seen.RowMark.Identity.Names[0] = "recheck-mutated"
		p.seen.Characteristics.Isolation = "read committed"
		if string(f.captured[i].request.Parameters.Values[0].([]byte)) != expected || f.captured[i].request.RowMark.Identity.Names[0] != "rid" || f.captured[i].request.Characteristics.Isolation != "serializable" {
			t.Fatal("callback mutation reached shared generation", i)
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := owner.Finish(cancelled); err != nil {
		t.Fatal(err)
	}
	if err := owner.Finish(cancelled); err != nil {
		t.Fatal(err)
	}
	if p.scan.ends != 1 || p.scan.canceled {
		t.Fatal("cleanup must run once detached from cancellation", p.scan)
	}
}

func TestForeignRowMarkRefetchResults(t *testing.T) {
	ctx := context.Background()
	primary := errors.New("provider primary")
	p := &rowMarkProbe{}
	f := &ForeignQuerySource{Wrapper: p, captured: []foreignCapturedRow{{identity: catalog.ForeignRowIdentity{[]byte("original")}, request: &catalog.ForeignRowMarkRequest{ForeignRequest: catalog.ForeignRequest{RowMark: &catalog.ForeignRowMarkPlan{Type: catalog.ForeignRowMarkUpdate}}}}}}
	source := &Source{Foreign: f, Anonymous: [][]sqltype.Value{{}}, Host: &rowMarkHostProbe{}}
	mark := plan.RowMark{Mode: lock.ForUpdate}
	p.result, p.failure = catalog.ForeignRefetchResult{Present: true, Updated: true}, primary
	if row, present, _, err := f.refetchRow(ctx, source, 0, mark, false); !errors.Is(err, primary) || present || row != nil {
		t.Fatal("error must veto all fields", row, present, err)
	}
	p.failure = nil
	p.result = catalog.ForeignRefetchResult{}
	if _, present, _, err := f.refetchRow(ctx, source, 0, mark, false); err != nil || present {
		t.Fatal("missing is not a NULL/empty tuple", present, err)
	}
	p.result = catalog.ForeignRefetchResult{Row: catalog.ForeignRow{}, Present: true, Updated: true}
	if row, present, updated, err := f.refetchRow(ctx, source, 0, mark, false); err != nil || !present || !updated || len(row) != 0 {
		t.Fatal("present zero-width tuple rejected", row, present, updated, err)
	}
	before := p.refetches
	if _, present, updated, err := f.refetchRow(ctx, source, 0, mark, false); err != nil || !present || updated || p.refetches != before {
		t.Fatal("settled nested mark refetched", present, updated, err)
	}
	p.identity[0].([]byte)[0] = 'X'
	if string(f.captured[0].identity[0].([]byte)) != "original" {
		t.Fatal("provider mutated owned capture identity")
	}
}

func TestForeignRowMarkCopyReferenceRecheck(t *testing.T) {
	ctx := context.Background()
	p := &rowMarkProbe{result: catalog.ForeignRefetchResult{Row: catalog.ForeignRow{}, Present: true, Updated: true}}
	f := &ForeignQuerySource{Wrapper: p, captured: []foreignCapturedRow{{request: &catalog.ForeignRowMarkRequest{ForeignRequest: catalog.ForeignRequest{RowMark: &catalog.ForeignRowMarkPlan{Type: catalog.ForeignRowMarkCopy}}}}}}
	source := &Source{Foreign: f, Anonymous: [][]sqltype.Value{{}}, Host: &rowMarkHostProbe{}}
	if _, present, changed, err := f.refetchRow(ctx, source, 0, plan.RowMark{Mode: lock.ForUpdate}, false); err != nil || !present || changed || p.refetches != 0 {
		t.Fatal("COPY claimed a lock/refetch", err)
	}
	f.captured[0].request = &catalog.ForeignRowMarkRequest{ForeignRequest: catalog.ForeignRequest{RowMark: &catalog.ForeignRowMarkPlan{Type: catalog.ForeignRowMarkReference}}}
	if _, _, _, err := f.refetchRow(ctx, source, 0, plan.RowMark{}, false); err != nil || p.refetches != 0 {
		t.Fatal("REFERENCE refreshed unchanged candidate", err)
	}
	if _, _, _, err := f.refetchRow(ctx, source, 0, plan.RowMark{}, true); err != nil || p.refetches != 1 || p.seen.Strength != catalog.ForeignLockNone {
		t.Fatal("REFERENCE must refetch unlocked on EPQ", err)
	}
	p.recheck = func(row catalog.ForeignRow) (bool, error) { return len(row) != 0, nil }
	if pass, err := f.recheckRow(ctx, source, 0, nil); err != nil || pass {
		t.Fatal("provider false ignored", pass, err)
	}
	var boolean *sqltype.Descriptor
	for _, descriptor := range sqltype.Builtins() {
		if descriptor.ID() == sqltype.Bool {
			boolean = descriptor
			break
		}
	}
	f.ExportValue = func(_ context.Context, value sqltype.Value, _ sqltype.OutputEnvironment) (driver.Value, error) {
		return value.Boolean()
	}
	p.recheck = func(row catalog.ForeignRow) (bool, error) { return row[0].(bool), nil }
	for _, flag := range []bool{true, false} {
		if pass, err := f.recheckRow(ctx, source, 0, []sqltype.Value{sqltype.Boolean(boolean, flag)}); err != nil || pass != flag {
			t.Fatal("actual tuple predicate not rechecked", flag, pass, err)
		}
	}
	primary := errors.New("recheck primary")
	p.recheck = func(catalog.ForeignRow) (bool, error) { return true, primary }
	if _, err := f.recheckRow(ctx, source, 0, nil); !errors.Is(err, primary) {
		t.Fatal("provider error lost", err)
	}
}

func TestForeignRowMarkNeverUsesLocalAuthority(t *testing.T) {
	ctx := context.Background()
	p := &rowMarkProbe{result: catalog.ForeignRefetchResult{Row: catalog.ForeignRow{}, Present: true}}
	tx := &rowMarkTransactionProbe{}
	f := &ForeignQuerySource{Wrapper: p, captured: []foreignCapturedRow{{identity: catalog.ForeignRowIdentity{int64(1)}, request: &catalog.ForeignRowMarkRequest{ForeignRequest: catalog.ForeignRequest{Transaction: tx, RowMark: &catalog.ForeignRowMarkPlan{Type: catalog.ForeignRowMarkUpdate}}}}}}
	// The host's local AcquireQueryRow and LatestQueryRow methods deliberately
	// have no implementation. Foreign arbitration must never invoke either.
	source := &Source{Foreign: f, Anonymous: [][]sqltype.Value{{}}, Host: &rowMarkHostProbe{tx: tx}}
	mark := plan.RowMark{Mode: lock.ForUpdate}
	source.Locks = &LockContext{Leaves: []QueryLockLeaf{{Source: source, Mark: mark}}}
	child := Stage{Source: source, Selection: []int{0}, Pending: []queryPendingMark{{Occurrences: []int{0}, Mark: mark}}}
	state := &State{Source: source, methodsCaptured: true}
	out, err := ArbitrateViewQuery(ctx, state, &plan.SourceNode{}, child)
	if err != nil {
		t.Fatal(err)
	}
	if present, err := out.Produce(ctx, &out); err != nil || !present || p.refetches != 1 || p.seen.Transaction != tx {
		t.Fatal("foreign arbitration did not retain provider authority", present, p.refetches, err)
	}
	absent := &Source{Anonymous: [][]sqltype.Value{{}}, Materialized: true, Occurrences: []int{0}, Provenance: []int{AbsentQueryIndex}, Host: source.Host}
	child = Stage{Source: absent, Selection: []int{0}, Pending: []queryPendingMark{{Occurrences: []int{0}, Mark: mark}}}
	out, err = ArbitrateViewQuery(ctx, state, &plan.SourceNode{}, child)
	if err != nil {
		t.Fatal(err)
	}
	if present, err := out.Produce(ctx, &out); err != nil || !present || p.refetches != 1 {
		t.Fatal("absent outer occurrence was refetched", present, p.refetches, err)
	}
}

func TestForeignRowMarkStoredFrontierSelection(t *testing.T) {
	locks := &LockContext{Leaves: []QueryLockLeaf{
		{Source: &Source{}},
		{Source: &Source{Foreign: &ForeignQuerySource{}}},
		{Source: &Source{}},
		{Source: &Source{Foreign: &ForeignQuerySource{}}},
	}}
	update := plan.RowMark{Mode: lock.ForUpdate, Wait: parser.RowLockNowait}
	share := plan.RowMark{Mode: lock.ForShare, Wait: parser.RowLockSkip}
	pending := []queryPendingMark{
		{Occurrences: []int{0, 1, 2, 3}, Mark: update},
		{Occurrences: []int{1}, Mark: share},
	}
	want := []queryPendingMark{
		{Occurrences: []int{1}, Mark: update},
		{Occurrences: []int{3}, Mark: update},
		{Occurrences: []int{1}, Mark: share},
	}
	selected := pendingForeignQueryMarks(pending, locks)
	if !reflect.DeepEqual(selected, want) {
		t.Fatal("stored frontier changed occurrence order, multiplicity or marks", selected)
	}
	if !reflect.DeepEqual(pending[0].Occurrences, []int{0, 1, 2, 3}) ||
		&selected[0].Occurrences[0] != &pending[0].Occurrences[1] {
		t.Fatal("stored frontier mutated or copied immutable occurrence identities")
	}
	if local := pendingForeignQueryMarks([]queryPendingMark{{Occurrences: []int{0, 2}, Mark: update}}, locks); local != nil {
		t.Fatal("stored frontier consumed local marks", local)
	}
	if empty := pendingForeignQueryMarks(nil, nil); empty != nil {
		t.Fatal("unmarked producer allocated a manifest", empty)
	}
}

func TestForeignRowMarkPresentNullTuple(t *testing.T) {
	var integer *sqltype.Descriptor
	for _, descriptor := range sqltype.Builtins() {
		if descriptor.ID() == sqltype.Int4 {
			integer = descriptor
			break
		}
	}
	p := &rowMarkProbe{result: catalog.ForeignRefetchResult{Row: catalog.ForeignRow{nil}, Present: true, Updated: true}}
	f := &ForeignQuerySource{Wrapper: p, Columns: []catalog.Column{{Usage: integer.Usage()}}, ImportValue: func(_ context.Context, value driver.Value, usage sqltype.Usage, _ sqltype.InputEnvironment) (sqltype.Value, error) {
		if value != nil {
			t.Fatal("expected actual NULL cell", value)
		}
		return sqltype.Null(usage.Type), nil
	}, captured: []foreignCapturedRow{{request: &catalog.ForeignRowMarkRequest{ForeignRequest: catalog.ForeignRequest{RowMark: &catalog.ForeignRowMarkPlan{Type: catalog.ForeignRowMarkUpdate}}}}}}
	source := &Source{Foreign: f, Anonymous: [][]sqltype.Value{{sqltype.Null(integer)}}, Host: &rowMarkHostProbe{}}
	row, present, changed, err := f.refetchRow(context.Background(), source, 0, plan.RowMark{Mode: lock.ForUpdate}, false)
	if err != nil || !present || !changed || len(row) != 1 || !row[0].IsNull() {
		t.Fatal("all-NULL row confused with missing row", row, present, changed, err)
	}
}
