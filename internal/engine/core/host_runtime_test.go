package core_test

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/host"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func hostOwnerDefinition(check func() error) host.LanguageDefinition {
	enter := func() error {
		if check != nil {
			return check()
		}
		return nil
	}
	from := func(_ context.Context, v host.SQLValue) (host.Object, error) {
		if err := enter(); err != nil {
			return host.Object{}, err
		}
		if v.Null {
			return host.Object{Null: true}, nil
		}
		m := make(host.HstoreMap, len(v.Hstore))
		for _, p := range v.Hstore {
			m[p.Key] = host.HstoreValue{Text: p.Value, Null: p.Null}
		}
		return host.Object{Value: m}, nil
	}
	to := func(_ context.Context, v host.Object) (host.SQLValue, error) {
		if err := enter(); err != nil {
			return host.SQLValue{}, err
		}
		if v.Null {
			return host.SQLValue{Type: host.Hstore, Null: true}, nil
		}
		m, ok := v.Value.(host.HstoreMap)
		if !ok || m == nil {
			return host.SQLValue{}, errors.New("wrong map")
		}
		entries := make([]host.HstoreEntry, 0, len(m))
		for k, v := range m {
			entries = append(entries, host.HstoreEntry{Key: k, Value: v.Text, Null: v.Null})
		}
		return host.SQLValue{Type: host.Hstore, Hstore: entries}, nil
	}
	program := func(_ context.Context, args []host.Object) (host.Object, error) {
		if err := enter(); err != nil {
			return host.Object{}, err
		}
		m, ok := args[0].Value.(host.HstoreMap)
		if !ok {
			return host.Object{}, errors.New("wrong input")
		}
		n, err := strconv.Atoi(m["a"].Text)
		if err != nil {
			return host.Object{}, err
		}
		m["a"] = host.HstoreValue{Text: strconv.Itoa(n + 1)}
		m["c"] = host.HstoreValue{Text: "3"}
		return host.Object{Value: m}, nil
	}
	return host.LanguageDefinition{Provider: "owner-host", ABIVersion: "map-v1", Name: "owner_host", HandlerName: host.Name{Schema: "public", Name: "owner_host_handler"}, Handler: func(ctx context.Context, p host.ProgramFunc, args []host.Object) (host.Object, error) {
		if err := enter(); err != nil {
			return host.Object{}, err
		}
		return p(ctx, args)
	}, Conversions: []host.ConversionDefinition{{ID: "map", Type: host.Hstore, Bootstrap: true, FromName: host.Name{Schema: "public", Name: "owner_host_from"}, ToName: host.Name{Schema: "public", Name: "owner_host_to"}, FromSQL: from, ToSQL: to}}, Programs: []host.ProgramDefinition{{ID: "increment", Version: "1", Inputs: []host.Type{host.Hstore}, Result: host.Hstore, Execute: program}}}
}
func hostOwnerEngine(t *testing.T, check func() error) (*core.Engine, *catalog.Procedure) {
	t.Helper()
	r, err := host.New(hostOwnerDefinition(check))
	if err != nil {
		t.Fatal(err)
	}
	e := engine.New()
	if err = e.InstallHostDefinitions([]*host.Runtime{r}); err != nil {
		t.Fatal(err)
	}
	enginetest.Run(t, e, "CREATE EXTENSION hstore")
	enginetest.Run(t, e, "CREATE FUNCTION owner_host_fn(v hstore) RETURNS hstore LANGUAGE owner_host TRANSFORM FOR TYPE hstore AS 'increment'")
	for _, descriptor := range e.CatalogRead().Descriptors() {
		if p, ok := descriptor.(*catalog.Procedure); ok && p.Name() == "owner_host_fn" {
			return e, p
		}
	}
	t.Fatal("host declaration absent")
	return nil, nil
}
func TestHostCatalogAuthority(t *testing.T) {
	e, p := hostOwnerEngine(t, nil)
	read := e.CatalogRead()
	if p.SQL != nil || p.Host == nil || p.Host.Layout.Kind != proc.HostBody || p.RoutineDefinition() != p.Host.Layout {
		t.Fatal("host acquired fake SQL body")
	}
	if err := (stored.ProcedureProvider{}).Validate(read, p); err != nil {
		t.Fatal(err)
	}
	_, foreign := hostOwnerEngine(t, nil)
	for _, modify := range []func(*catalog.Procedure){func(d *catalog.Procedure) { d.Host = foreign.Host }, func(d *catalog.Procedure) { d.SQL = p.Host.Layout }, func(d *catalog.Procedure) { d.PLHandler = catalog.PLCallHandler }, func(d *catalog.Procedure) { d.HostHandler = p.Host.Program.Runtime() }, func(d *catalog.Procedure) {
		h := *d.Host
		layout := *h.Layout
		typ, _ := sqltype.Hstore(layout.Result.Type.ID(), "hstore", "public.hstore")
		layout.Result = typ.Usage()
		h.Layout = &layout
		d.Host = &h
	}} {
		state := catalog.CloneState(read.State())
		copy := *p
		modify(&copy)
		state.SetDescriptor(copy.Address(), &copy)
		if err := (stored.ProcedureProvider{}).Validate(e.Catalog.Read(state), &copy); err == nil {
			t.Fatal("forged host authority accepted")
		}
	}
	for _, descriptor := range read.Descriptors() {
		pair, ok := descriptor.(*catalog.Transform)
		if !ok {
			continue
		}
		if err := (catalog.TransformProvider{}).Validate(read, pair); err != nil {
			t.Fatal(err)
		}
		if pair.Runtime != p.Host.Program.Runtime() || pair.Type.ObjectID != p.Result {
			t.Fatal("pair borrowed foreign provider/type")
		}
		dependencies := map[ids.ObjectAddress]bool{}
		pair.Dependencies(func(a ids.ObjectAddress) bool { dependencies[a] = true; return true })
		if !dependencies[p.Language] || !dependencies[pair.Type] || !dependencies[pair.From] || !dependencies[pair.To] {
			t.Fatal("real pair edges missing")
		}
	}
}
func TestHostProgramResolution(t *testing.T) {
	e, p := hostOwnerEngine(t, nil)
	before := e.CatalogRead().State()
	for _, query := range []string{"CREATE FUNCTION owner_bad(v hstore) RETURNS hstore LANGUAGE owner_host AS 'missing'", "CREATE OR REPLACE FUNCTION owner_host_fn(v hstore) RETURNS hstore LANGUAGE owner_host AS 'missing'", "CREATE FUNCTION owner_bad(v text) RETURNS hstore LANGUAGE owner_host AS 'increment'"} {
		_, err := enginetest.Execute(e, context.Background(), enginetest.Statement(t, query), nil)
		if err == nil || e.CatalogRead().State() != before {
			t.Fatal("failed program resolution published a descriptor", err)
		}
	}
	if current, _ := e.CatalogRead().Procedure(p.Address()); current != p || current.Host.Program != p.Host.Program {
		t.Fatal("failed replacement changed immutable executable token")
	}
	hostOwnerPublicationCancellation(t)
}
func TestHostInvocationFrameOwnership(t *testing.T) {
	e, p := hostOwnerEngine(t, nil)
	definition := p.RoutineDefinition()
	frame := proc.NewFrame(definition.Inputs)
	input, err := definition.Inputs[0].Type.HstoreFromEntries(context.Background(), []sqltype.HstoreEntry{{Key: "a", Value: "7"}, {Key: "b", Null: true}})
	if err != nil {
		t.Fatal(err)
	}
	if err = frame.Bind(0, input, definition.Inputs[0]); err != nil {
		t.Fatal(err)
	}
	op, done, err := enginetest.Operation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	value, err := core.ScopeInvokeSQLRoutine(scope, p, frame, definition.Result, nil, sqltype.CollationRef{}, false)
	if err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	if value.Type() != definition.Result.Type || definition != p.Host.Layout || core.ScopeActiveSQLRoutineDepth(scope) != 0 {
		t.Fatal("invocation replaced layout/result owner or leaked depth")
	}
	frame.Close()
	_, closedResultErr := frame.Result(value, definition.Result)
	if frame.Parameters != nil || frame.Bind(0, input, definition.Inputs[0]) == nil || closedResultErr == nil {
		t.Fatal("closed frame retained executable slots")
	}
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	if core.ActiveScopeOfLease(op.Lease) != nil || scope.ReplacementRead {
		t.Fatal("finished invocation retained live scope/reservation")
	}
}
func TestHostCallbackReadmission(t *testing.T) {
	var e *core.Engine
	var op *session.Operation
	calls := 0
	check := func() error {
		calls++
		if !e.Mu.TryLock() {
			return errors.New("callback under Engine.Mu")
		}
		e.Mu.Unlock()
		if !e.ReplacementMu.TryLock() {
			return errors.New("callback under replacement reservation")
		}
		e.ReplacementMu.Unlock()
		if op == nil {
			return errors.New("DDL invoked application code")
		}
		store := op.EffectiveSettings()
		if !store.Mu.TryLock() {
			return errors.New("callback under settings lock")
		}
		store.Mu.Unlock()
		if !op.Session.Mu.TryLock() {
			return errors.New("callback under session lock")
		}
		op.Session.Mu.Unlock()
		return nil
	}
	e, _ = hostOwnerEngine(t, check)
	var err error
	var done func()
	op, done, err = enginetest.Operation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	result, err := enginetest.ExecuteFrame(e, op, enginetest.Statement(t, "SELECT owner_host_fn('a=>7,b=>NULL'::hstore)"), nil)
	if err = hostOwnerResultError(result, err); err != nil {
		t.Fatal(err)
	}
	if calls != 4 {
		t.Fatal("did not exercise all four real application callbacks", calls)
	}
	// Actual replacement from the application callback must retire the old epoch,
	// not retry a supplied executable or publish its old result.
	var replacing *core.Engine
	replaced := false
	replacementCallbacks := 0
	replace := func() error {
		replacementCallbacks++
		if !replaced {
			replaced = true
			return engine.Reset(context.Background(), replacing)
		}
		return nil
	}
	replacing, _ = hostOwnerEngine(t, replace)
	before := replacing.CatalogRead().Epoch()
	result, err = enginetest.Execute(replacing, context.Background(), enginetest.Statement(t, "SELECT owner_host_fn('a=>7'::hstore)"), nil)
	err = hostOwnerResultError(result, err)
	if err == nil || replacing.CatalogRead().Epoch() == before || replacementCallbacks != 1 {
		t.Fatal("callback replacement dispatched a later callback or published stale continuation", replacementCallbacks, err)
	}
}
func TestHostInvocationEnvironment(t *testing.T) {
	e, p := hostOwnerEngine(t, nil)
	enginetest.Run(t, e, "ALTER FUNCTION owner_host_fn(hstore) SECURITY DEFINER SET application_name='host-owned'")
	op, done, err := enginetest.Operation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	before := op.RoleState()
	store := op.EffectiveSettings()
	level := store.NestLevel
	result, err := enginetest.ExecuteFrame(e, op, enginetest.Statement(t, "SELECT owner_host_fn('a=>7'::hstore)"), nil)
	if err = hostOwnerResultError(result, err); err != nil {
		t.Fatal(err)
	}
	if op.RoleState() != before || op.EffectiveSettings() != store || store.NestLevel != level || core.ActiveScopeOfLease(op.Lease) != nil {
		t.Fatal("host environment retained actor/settings/live scope")
	}
	current, _ := e.CatalogRead().Procedure(p.Address())
	if current.Host != p.Host {
		t.Fatal("attribute revision replaced executable identity")
	}
}
func TestHostRestoreRemap(t *testing.T) {
	sourceCalls, targetCalls := 0, 0
	source, p := hostOwnerEngine(t, func() error { sourceCalls++; return nil })
	var script bytes.Buffer
	if err := engine.Dump(context.Background(), source, &script); err != nil {
		t.Fatal(err)
	}
	target, previous := hostOwnerEngine(t, func() error { targetCalls++; return nil })
	registered := previous.Host.Program
	for round := range 2 {
		retained := target.CatalogRead()
		old := previous
		oldType := old.Host.Layout.Result.Type
		oldPair := retained.Get(old.Host.Pairs[0])
		oldConverter := retained.Get(old.Host.Converters[0])
		if err := engine.Restore(context.Background(), target, bytes.NewReader(script.Bytes())); err != nil {
			t.Fatal(err)
		}
		read := target.CatalogRead()
		var current *catalog.Procedure
		for _, d := range read.Descriptors() {
			if candidate, ok := d.(*catalog.Procedure); ok && candidate.Name() == p.Name() {
				current = candidate
				break
			}
		}
		if current == nil || read.Epoch() == retained.Epoch() || current.Host == p.Host || current.Host == old.Host || current.Host.Layout == old.Host.Layout || current.Host.Layout.Result.Type == oldType || &current.Host.Layout.Formals[0] == &old.Host.Layout.Formals[0] || read.Get(current.Host.Pairs[0]) == oldPair || read.Get(current.Host.Converters[0]) == oldConverter {
			t.Fatal("restore reused previous catalog/layout/type/pair/direction backing")
		}
		if current.Host.Program != registered || current.Host.Program == p.Host.Program || current.Host.Program.Runtime() == p.Host.Program.Runtime() || current.Host.Tokens[0] != old.Host.Tokens[0] {
			t.Fatal("restore did not bind immutable destination registry ownership")
		}
		if retained.Get(old.Address()) != old || old.Host.Layout.Result.Type != oldType || retained.Get(old.Host.Pairs[0]) != oldPair || retained.Get(old.Host.Converters[0]) != oldConverter {
			t.Fatal("restore mutated retained old catalog backing")
		}
		if current.Host.Program.ID() != p.Host.Program.ID() || current.Host.Program.Runtime().Fingerprint() != p.Host.Program.Runtime().Fingerprint() {
			t.Fatal("logical executable identity changed")
		}
		if _, err := exec.ValidateStoredRoutineDescriptor(read, current); err != nil {
			t.Fatal(err)
		}
		result, err := enginetest.Execute(target, context.Background(), enginetest.Statement(t, "SELECT owner_host_fn('a=>7'::hstore)"), nil)
		if err = hostOwnerResultError(result, err); err != nil {
			t.Fatal(err)
		}
		if sourceCalls != 0 || targetCalls != (round+1)*4 {
			t.Fatal("restore adopted source callbacks or omitted genuine destination execution", sourceCalls, targetCalls)
		}
		previous = current
	}
	absent := engine.New()
	before := absent.CatalogRead().State()
	if err := engine.Restore(context.Background(), absent, bytes.NewReader(script.Bytes())); err == nil || absent.CatalogRead().State() != before {
		t.Fatal("missing host provider partially published")
	}
}
