package core_test

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/copyfmt"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type clientEncodingDefaults struct {
	e      *core.Engine
	calls  int
	cancel context.CancelFunc
}

func (p *clientEncodingDefaults) SessionDefault(ctx context.Context, d settings.Definition) (string, bool, error) {
	// A real reentrant catalog read proves startup callbacks hold no Engine.Mu.
	p.e.CatalogRead()
	if d.Name != "client_encoding" {
		return "", false, nil
	}
	p.calls++
	if p.cancel != nil {
		p.cancel()
	}
	return "LATIN1", true, nil
}

func TestClientEncodingAdmissionJournal(t *testing.T) {
	e := engine.New()
	admin := enginetest.StartSession(t, e, nil)
	run := func(s *session.Session, q string) { enginetest.SessionSQL(t, s, q) }
	run(admin, "CREATE SCHEMA ce_hidden")
	for _, name := range []string{"iso_8859_1_to_utf8", "utf8_to_iso_8859_1"} {
		run(admin, "ALTER CONVERSION pg_catalog."+name+" SET SCHEMA ce_hidden")
	}
	createPair := func() {
		run(admin, "CREATE DEFAULT CONVERSION public.ce_in FOR 'LATIN1' TO 'UTF8' FROM pg_catalog.iso8859_1_to_utf8")
		run(admin, "CREATE DEFAULT CONVERSION public.ce_out FOR 'UTF8' TO 'LATIN1' FROM pg_catalog.utf8_to_iso8859_1")
	}
	dropPair := func() { run(admin, "DROP CONVERSION public.ce_in"); run(admin, "DROP CONVERSION public.ce_out") }
	createPair()
	provider := &clientEncodingDefaults{e: e}
	s := enginetest.StartSession(t, e, provider)
	old := s.Settings.ClientEncoding()
	oldIdentity := old.DefaultIdentity()
	if provider.calls != 1 || old.ID() != 8 || oldIdentity[0] == 0 || oldIdentity[1] == 0 || s.Settings.DefaultTyped["client_encoding"].ClientEncoding != 8 {
		t.Fatalf("initial capability %+v calls%d", oldIdentity, provider.calls)
	}
	invocation, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer invocation.Done()
	invocation.Restricted = &session.RestrictedInvocation{}
	detached := invocation.EffectiveSettings()
	mark := invocation.CaptureInvocation()
	if detached == s.Settings || detached.ClientEncoding().DefaultIdentity() != oldIdentity {
		t.Fatal("detached entry did not capture effective ID")
	}
	encodingDefinition, _ := e.Settings.Lookup("client_encoding")
	utf8Value, err := encodingDefinition.Parse("UTF8")
	if err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	detached.Mu.Lock()
	s.Mu.Lock()
	saveLevel := detached.BeginSettingsSaveLocked()
	_, saveErr := detached.ApplyLocked(invocation, core.DomainOf(e), e.Settings, encodingDefinition, &utf8Value, nil, session.SettingSave)
	s.Mu.Unlock()
	detached.Mu.Unlock()
	e.Mu.Unlock()
	if saveErr != nil || detached.ClientEncoding().ID() != 6 {
		t.Fatal("actual routine SAVE entry", saveErr)
	}
	// Mark contains the encoding ID, not an obsolete provider generation.
	run(s, "BEGIN")
	run(s, "SAVEPOINT entry")
	run(s, "SET LOCAL client_encoding='UTF8'")
	dropPair()
	createPair()
	run(s, "SET LOCAL client_encoding='LATIN1'")
	newest := s.Settings.ClientEncoding()
	newIdentity := newest.DefaultIdentity()
	if newIdentity[0] == oldIdentity[0] || newIdentity[1] == oldIdentity[1] {
		t.Fatal("actual recreated defaults did not advance identity", oldIdentity, newIdentity)
	}
	run(s, "ROLLBACK TO entry")
	if s.Settings.ClientEncoding().DefaultIdentity() != newIdentity {
		t.Fatal("rollback froze obsolete generation")
	}
	e.Mu.Lock()
	detached.Mu.Lock()
	s.Mu.Lock()
	detached.EndSettingsSaveLocked(saveLevel, true)
	s.Mu.Unlock()
	detached.Mu.Unlock()
	e.Mu.Unlock()
	if detached.TypedValue("client_encoding").ClientEncoding != 8 {
		t.Fatal("SAVE restored wrong typed ID")
	}
	if detached.ClientEncoding().DefaultIdentity() != newIdentity {
		t.Fatal("invocation copied stale cache authority")
	}
	mark.Restore()
	if invocation.EffectiveSettings().ClientEncoding().DefaultIdentity() != newIdentity {
		t.Fatal("invocation checkpoint restored old generation")
	}
	invocation.Restricted = nil
	run(s, "ROLLBACK")
	dropPair()
	for _, q := range []string{"RESET client_encoding", "SET client_encoding=DEFAULT", "RESET ALL", "DISCARD ALL"} {
		run(s, q)
		if s.Settings.ClientEncoding().DefaultIdentity() != newIdentity || s.Settings.TypedValue("client_encoding").ClientEncoding != 8 {
			t.Fatal("reset relooked up or restored obsolete authority", q)
		}
	}
	// Inspect actual COPY capability selection after retirement, not just
	// equivalent row bytes from a fresh provider.
	copyOp, _, err := s.AcquireCommand(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	copyScope, err := e.StartStatement(copyOp)
	if err != nil {
		copyOp.Done()
		t.Fatal(err)
	}
	defer copyOp.Done()
	defer copyScope.Finish(context.Canceled)
	if err = copyScope.Gate(enginetest.Statement(t, "SELECT 1")); err != nil {
		_ = copyScope.Finish(err)
		copyOp.Done()
		t.Fatal(err)
	}
	for _, explicit := range []bool{false, true} {
		options := copyfmt.Options{Encoding: 8, EncodingSet: explicit}
		if err := core.PrepareCopyEncodingForTest(copyScope, &options, false); err != nil ||
			!reflect.DeepEqual(options.Conversion, newest.Outgoing()) {
			t.Fatal("same-ID COPY TO discarded admitted capability", explicit, options.Conversion, err)
		}
	}
	options := copyfmt.Options{Encoding: 8, EncodingSet: true}
	copyErr := core.PrepareCopyEncodingForTest(copyScope, &options, true)
	var copyDiagnostic *sqlstate.Error
	if !errors.As(copyErr, &copyDiagnostic) || copyDiagnostic.Code != "42883" || options.Conversion.Available() {
		t.Error("same-ID COPY FROM reused cached incoming authority", copyErr, options.Conversion)
	}
	if err := copyScope.Finish(nil); err != nil {
		t.Error(err)
	}
	copyOp.Done()
	if provider.calls != 1 {
		t.Fatal("restoration reentered default provider", provider.calls)
	}
	for _, p := range []sqltype.ClientEncodingPolicy{old, newest, s.Settings.ClientEncoding()} {
		got, err := p.Decode(context.Background(), "\xe9")
		if err != nil || got != "é" {
			t.Fatalf("retired pair input %x %v", got, err)
		}
		got, err = p.Encode(context.Background(), "é")
		if err != nil || got != "\xe9" {
			t.Fatalf("retired pair output %x %v", got, err)
		}
	}
	beforeValues, beforeTyped, beforeActions := maps.Clone(s.Settings.Values), maps.Clone(s.Settings.Typed), maps.Clone(s.Settings.Actions)
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Execute(op, enginetest.Statement(t, "SET client_encoding='LATIN1'"), 0, nil)
	op.Done()
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != `invalid value for parameter "client_encoding": "LATIN1"` || diagnostic.Detail != "Conversion between LATIN1 and UTF8 is not supported." {
		t.Fatal("missing-pair admission", err)
	}
	if !reflect.DeepEqual(s.Settings.Values, beforeValues) || !reflect.DeepEqual(s.Settings.Typed, beforeTyped) || !reflect.DeepEqual(s.Settings.Actions, beforeActions) || s.Settings.ClientEncoding().DefaultIdentity() != newIdentity {
		t.Fatal("rejected candidate published text/typed/journal/cache state")
	}
	latin1, mule := "LATIN1", "MULE_INTERNAL"
	for _, candidate := range []*string{nil, &latin1, &mule} {
		checkOp, err := s.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		_, _, checkErr := e.ValidatePersistedSetting(checkOp, "client_encoding", candidate)
		checkOp.Done()
		if candidate == nil {
			if checkErr != nil {
				t.Fatal("removing a persisted value checked retired providers", checkErr)
			}
		} else if !errors.As(checkErr, &diagnostic) || diagnostic.Code != "0A000" {
			t.Fatal("persisted candidate bypassed actual pair validation", *candidate, checkErr)
		}
		if !reflect.DeepEqual(s.Settings.Values, beforeValues) || !reflect.DeepEqual(s.Settings.Typed, beforeTyped) ||
			!reflect.DeepEqual(s.Settings.Actions, beforeActions) || s.Settings.ClientEncoding().DefaultIdentity() != newIdentity {
			t.Fatal("persisted validation changed issuer state")
		}
	}
	failed, err := enginetest.OpenSession(context.Background(), e, session.SessionStart{ID: ids.SessionID(900001), Defaults: &clientEncodingDefaults{e: e}})
	if failed != nil || !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != "conversion between LATIN1 and UTF8 is not supported" {
		t.Fatal("failed startup admitted a session", failed, err)
	}
	createPair()
	// Check-only persisted admission must resolve the newly recreated pair,
	// without refreshing the issuer's old accepted generation or reset image.
	checkOp, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	persisted := "LATIN1"
	name, text, checkErr := e.ValidatePersistedSetting(checkOp, "client_encoding", &persisted)
	checkOp.Done()
	if checkErr != nil || name != "client_encoding" || text != persisted {
		t.Fatal("persisted pair validation", name, text, checkErr)
	}
	// The routine declaration owner is also check-only. Resolve a genuinely
	// newer pair through its existing host, then restore the accepted ID: a
	// speculative cache refresh would incorrectly adopt the newer generation.
	routineOp, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	clause := enginetest.Statement(t, "SET client_encoding='LATIN1'").(*parser.Set)
	e.Mu.Lock()
	auth, routineErr := e.AuthorizationLocked(routineOp)
	var routineName, routineText string
	var remove bool
	if routineErr == nil {
		routineName, routineText, remove, routineErr = (core.DDLHost{E: e, Op: routineOp}).ValidateRoutineSetting(auth, clause)
	}
	e.Mu.Unlock()
	routineOp.Done()
	if routineErr != nil || routineName != "client_encoding" || routineText != "LATIN1" || remove {
		t.Fatal("routine check-only pair validation", routineName, routineText, remove, routineErr)
	}
	if s.Settings.ClientEncoding().DefaultIdentity() != newIdentity ||
		!reflect.DeepEqual(s.Settings.Values, beforeValues) || !reflect.DeepEqual(s.Settings.Typed, beforeTyped) ||
		!reflect.DeepEqual(s.Settings.Actions, beforeActions) {
		t.Fatal("routine check-only validation installed issuer state")
	}
	run(s, "RESET client_encoding")
	if s.Settings.ClientEncoding().DefaultIdentity() != newIdentity ||
		!reflect.DeepEqual(s.Settings.Values, beforeValues) || !reflect.DeepEqual(s.Settings.Typed, beforeTyped) ||
		s.Settings.DefaultTyped["client_encoding"].ClientEncoding != 8 {
		t.Fatal("check-only admission installed live/reset/cache authority")
	}
	// Prepare a real current pair, then cancel publication. The actual install
	// must reject before Touch; the already accepted latest pair remains intact.
	ctx, cancel := context.WithCancel(context.Background())
	op, err = s.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	e.Mu.Lock()
	candidate, err := core.PrepareClientEncodingForTest(e, op, 8)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if candidate.DefaultIdentity() == newIdentity {
		t.Fatal("candidate did not capture actual current defaults")
	}
	definition, _ := e.Settings.Lookup("client_encoding")
	value, err := definition.Parse("LATIN1")
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	e.Mu.Lock()
	s.Settings.Mu.Lock()
	s.Mu.Lock()
	_, err = s.Settings.ApplyLocked(op, core.DomainOf(e), e.Settings, definition, &value, nil, session.SettingLocal)
	if err == nil {
		s.Settings.AcceptClientEncodingLocked(candidate)
	}
	s.Mu.Unlock()
	s.Settings.Mu.Unlock()
	e.Mu.Unlock()
	if !errors.Is(err, context.Canceled) || s.Settings.ClientEncoding().DefaultIdentity() != newIdentity {
		t.Fatal("canceled candidate entered cache", err)
	}
	op.Done()
	run(s, "SET client_encoding='LATIN1'")
	if s.Settings.ClientEncoding().DefaultIdentity() != candidate.DefaultIdentity() {
		t.Fatal("fresh admission did not select current generation")
	}
	// Canceled provider initialization leaves no usable returned session.
	ctx, cancel = context.WithCancel(context.Background())
	failed, err = enginetest.OpenSession(ctx, e, session.SessionStart{ID: ids.SessionID(900002), Defaults: &clientEncodingDefaults{e: e, cancel: cancel}})
	if failed != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled startup publication", failed, err)
	}
}
