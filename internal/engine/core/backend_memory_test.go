package core

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unsafe"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
)

func backendMemoryOwner() *sessionAllocation {
	a := new(sessionAllocation)
	a.payload.allocation = a
	a.session.Payload = &a.payload
	a.payload.PreparedSQL = NewPreparedRegistry(a.session.SessionToken)
	return a
}

func TestBackendMemoryOwnership(t *testing.T) {
	a := backendMemoryOwner()
	capture := func() pgcatalog.BackendMemorySnapshot {
		t.Helper()
		got, err := captureBackendMemory(context.Background(), &a.session)
		if err != nil { t.Fatal(err) }
		return got
	}
	root, registry, definition := int64(unsafe.Sizeof(*a)), int64(unsafe.Sizeof(*a.payload.PreparedSQL)), int64(unsafe.Sizeof(PreparedDefinition{}))
	want := pgcatalog.BackendMemorySnapshot{Count: 3, Own: [3]int64{root, registry, 0}}
	if got := capture(); got != want { t.Fatal("actual empty owners", got, want) }
	// Embedded session, payload and owner extension are charged once via a,
	// rather than each counted again as a separately allocated owner.
	if root < int64(unsafe.Sizeof(a.session))+int64(unsafe.Sizeof(a.payload))+int64(unsafe.Sizeof(a.owner)) { t.Fatal("combined layout omitted embedded ownership") }
	first := &PreparedDefinition{Source: "short"}
	a.payload.PreparedSQL.Install("one", first)
	want.Own[2] = definition
	retained := capture()
	if retained != want { t.Fatal("one actual definition", retained, want) }
	first.Source = strings.Repeat("not selected referenced storage", 1024)
	first.ParameterOIDs = make([]uint32, 1000)
	if got := capture(); got != retained { t.Fatal("referenced strings/slice backing charged", got, retained) }
	second := &PreparedDefinition{Source: first.Source, ParameterOIDs: first.ParameterOIDs}
	a.payload.PreparedSQL.Install("two", second)
	want.Own[2] = 2*definition
	if got := capture(); got != want { t.Fatal("distinct layouts with shared referenced backing", got, want) }
	a.payload.PreparedSQL.Install("alias", first)
	if got := capture(); got != want { t.Fatal("alias double charged", got, want) }
	a.payload.PreparedSQL.Remove("one")
	if got := capture(); got != want { t.Fatal("remaining alias lost owner", got, want) }
	a.payload.PreparedSQL.Remove("alias")
	want.Own[2] = definition
	if got := capture(); got != want { t.Fatal("actual removal", got, want) }
	a.payload.PreparedSQL.Install("two", &PreparedDefinition{Source: "replacement"})
	if got := capture(); got != want { t.Fatal("same-name replacement accumulated owners", got, want) }
	a.payload.PreparedSQL.ClearDefinitions()
	want.Own[2] = 0
	if got := capture(); got != want || retained.Own[2] != definition { t.Fatal("clear mutated detached capture", got, want, retained) }
	a.payload.PreparedSQL.Bind("retired", &PreparedBinding{Entry: *first, Retired: true})
	if got := capture(); got != want { t.Fatal("retired binding or its referenced graph charged as installed definition", got, want) }
	peer := backendMemoryOwner()
	peerSnapshot, err := captureBackendMemory(context.Background(), &peer.session)
	if err != nil || peerSnapshot != want { t.Fatal("independent owner", peerSnapshot, err) }
	a.payload.PreparedSQL.Install("local", first)
	if got, err := captureBackendMemory(context.Background(), &peer.session); err != nil || got != peerSnapshot { t.Fatal("peer registry affected measurement", got, err) }
	withoutRegistry := backendMemoryOwner()
	withoutRegistry.payload.PreparedSQL = nil
	got, err := captureBackendMemory(context.Background(), &withoutRegistry.session)
	if err != nil || got != (pgcatalog.BackendMemorySnapshot{Count: 1, Own: [3]int64{root}}) { t.Fatal("unattached registry invented", got, err) }
	missing := new(sessionAllocation)
	missing.session.Payload = &missing.payload
	wrongAllocation := backendMemoryOwner()
	wrongAllocation.payload.allocation = backendMemoryOwner()
	wrongSession := &session.Session{Payload: &a.payload}
	wrongPayload := backendMemoryOwner()
	wrongPayload.session.Payload = &sessionPayload{allocation: wrongPayload}
	for _, invalid := range []*session.Session{nil, {}, {Payload: &sessionPayload{}}, &missing.session, &wrongAllocation.session, wrongSession, &wrongPayload.session} {
		if got, err := captureBackendMemory(context.Background(), invalid); err == nil || got != (pgcatalog.BackendMemorySnapshot{}) { t.Fatal("literal owner received invented bytes", got, err) }
	}
	peer.session.SessionToken = new(ids.Domain).Session(1)
	if got, err := captureBackendMemory(context.Background(), &peer.session); err == nil || got != (pgcatalog.BackendMemorySnapshot{}) { t.Fatal("foreign registry token accepted", got, err) }
}

func backendMemoryUnitTypes() [10]*sqltype.Descriptor {
	all := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, d := range sqltype.Builtins() { all[d.ID()] = d }
	var types [10]*sqltype.Descriptor
	for i, id := range [...]sqltype.ID{25, 25, 25, 23, 1007, 20, 20, 20, 20, 20} { types[i] = all[id] }
	return types
}

func TestBackendMemoryTypedCapture(t *testing.T) {
	a := backendMemoryOwner()
	a.payload.PreparedSQL.Install("one", &PreparedDefinition{})
	snapshot, err := captureBackendMemory(context.Background(), &a.session)
	if err != nil { t.Fatal(err) }
	types := backendMemoryUnitTypes()
	rows, err := pgcatalog.BackendMemoryRows(context.Background(), types, snapshot)
	if err != nil || len(rows) != 3 { t.Fatal(rows, err) }
	var total int64
	for i, row := range rows {
		if len(row) != 10 { t.Fatal(row) }
		for j, value := range row { if value.Type() != types[j] || !value.ValidResult(types[j].Usage()) { t.Fatal("typed result", i, j, value) } }
		for _, j := range []int{1, 6, 7, 8} { if !row[j].IsNull() { t.Fatal("unsupported field fabricated", i, j, row[j]) } }
		own, err := row[5].Integer(); used, usedErr := row[9].Integer()
		if err != nil || usedErr != nil || own != snapshot.Own[i] || used != own { t.Fatal("own layout", own, used, err, usedErr) }
		total += own
		path := row[4]
		if path.ArrayLen() != i+1 || path.ArrayRank() != 1 || path.ArrayDimension(0) != (sqltype.ArrayDimension{Lower: 1, Length: int32(i+1)}) { t.Fatal("path ownership", path) }
		for j := range i+1 { element := path.ArrayElement(j); number, err := element.Integer(); if err != nil || element.Type() != types[3] || number != int64(j+1) { t.Fatal("path element", element, err) } }
	}
	if total != int64(unsafe.Sizeof(*a))+int64(unsafe.Sizeof(*a.payload.PreparedSQL))+int64(unsafe.Sizeof(PreparedDefinition{})) { t.Fatal("unique selected object sum", total) }
	root, _ := rows[0][5].Integer()
	if root == total { t.Fatal("root own bytes became inclusive subtree") }
	old := rows[2][5]
	a.payload.PreparedSQL.ClearDefinitions()
	if value, _ := old.Integer(); value != int64(unsafe.Sizeof(PreparedDefinition{})) { t.Fatal("returned scalar retained live registry", value) }
	foreign := backendMemoryUnitTypes()
	bad := types
	bad[3] = foreign[3]
	if foreign[3] == types[3] { t.Fatal("fixture must use independent descriptor generations") }
	if rows, err := pgcatalog.BackendMemoryRows(context.Background(), bad, snapshot); err == nil || rows != nil { t.Fatal("foreign path generation accepted", rows, err) }
}

// Cancellation fires at a deterministic real context checkpoint, not a hook
// inside the capture implementation or a timing race with a tiny traversal.
type backendMemoryCancelContext struct {
	context.Context
	cancel context.CancelFunc
	at, calls int
}
func (c *backendMemoryCancelContext) Err() error {
	c.calls++
	if c.calls == c.at { c.cancel() }
	return c.Context.Err()
}

func TestBackendMemoryCancellation(t *testing.T) {
	a := backendMemoryOwner()
	for _, name := range []string{"one", "two", "three"} { a.payload.PreparedSQL.Install(name, &PreparedDefinition{}) }
	baseline, err := captureBackendMemory(context.Background(), &a.session)
	if err != nil { t.Fatal(err) }
	ctx, cancel := context.WithCancel(context.Background()); cancel()
	if got, err := captureBackendMemory(ctx, &a.session); !errors.Is(err, context.Canceled) || got != (pgcatalog.BackendMemorySnapshot{}) { t.Fatal("pre-cancel capture published", got, err) }
	for _, at := range []int{3, 5} {
		ctx, cancel := context.WithCancel(context.Background())
		trigger := &backendMemoryCancelContext{Context: ctx, cancel: cancel, at: at}
		got, err := captureBackendMemory(trigger, &a.session)
		cancel()
		if !errors.Is(err, context.Canceled) || got != (pgcatalog.BackendMemorySnapshot{}) || trigger.calls != at { t.Fatal("traversal/finalization published prefix", at, got, err, trigger.calls) }
	}
	ctx, cancel = context.WithCancel(context.Background())
	trigger := &backendMemoryCancelContext{Context: ctx, cancel: cancel, at: 3}
	rows, err := pgcatalog.BackendMemoryRows(trigger, backendMemoryUnitTypes(), baseline)
	cancel()
	if !errors.Is(err, context.Canceled) || rows != nil { t.Fatal("typed materialization published prefix", rows, err) }
	if got, err := captureBackendMemory(context.Background(), &a.session); err != nil || got != baseline { t.Fatal("cancellation changed actual owners", got, err) }
}
