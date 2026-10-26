package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
)

// settingsRuntimeForTest captures the same validated operation authority as the
// production pg_settings source. The returned view retains immutable roots only.
func settingsRuntimeForTest(t *testing.T, e *core.Engine, s *session.Session) core.EngineCatalogRuntime {
	t.Helper()
	op, err := s.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return core.NewCatalogRuntime(core.CatalogRuntimeFields{Auth: auth, Settings: s.Settings, Registry: e.Settings})
}

func TestPGSettingsProviderCapture(t *testing.T) {
	defs := []settings.Definition{
		{Name: "metadata.flag", Type: settings.Boolean, BootDefault: "off"},
		{Name: "metadata.text", Type: settings.String, BootDefault: "seed"},
		{Name: "metadata.duration", Type: settings.Integer, Unit: settings.Milliseconds, BootDefault: "0", IntegerMax: 10000},
		{Name: "metadata.real", Type: settings.Floating, BootDefault: "1", FloatMax: 10},
		{Name: "metadata.mode", Type: settings.Enum, BootDefault: "safe", EnumValues: []string{"safe", "fast"}},
	}
	for i := range defs {
		var err error
		defs[i], err = settings.Validate(defs[i])
		if err != nil {
			t.Fatal(err)
		}
	}
	e := engine.NewWithSettings(defs, "", false)
	provider := &settingsDefaultProviderForTest{values: map[string]string{
		"application_name": "", "statement_timeout": "2s",
		"metadata.flag": "off", "metadata.text": "", "metadata.duration": "2s",
		"metadata.real": "1.23456789", "metadata.mode": "FAST",
	}, calls: map[string]int{}}
	a := sessionStart(t, e, provider)
	capture := func(s *session.Session) map[string]string {
		t.Helper()
		facts, err := settingsRuntimeForTest(t, e, s).SessionSettings(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		out := make(map[string]string)
		for _, f := range facts {
			out[f.Definition.Name] = f.Definition.Raw(f.Value) + "/" + f.Source + "/" + f.Definition.Raw(f.Reset)
		}
		return out
	}
	initial := capture(a)
	if initial["application_name"] != "/client/" || initial["statement_timeout"] != "2000/client/2000" || initial["row_security"] != "on/default/on" {
		t.Fatal(initial)
	}
	wantTyped := map[string]settings.Value{
		"metadata.flag": {Boolean: false},
		"metadata.text": {Text: ""},
		"metadata.duration": {Number: 2000},
		"metadata.real": {Number: 1.23456789},
		"metadata.mode": {Text: "fast"},
	}
	// Compare typed values as well as rendered metadata: real formatting alone
	// would hide lost precision, and provider-equals-boot still has client source.
	checkTyped := func(s *session.Session, want map[string]settings.Value) {
		t.Helper()
		facts, err := settingsRuntimeForTest(t, e, s).SessionSettings(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		matched := 0
		for _, f := range facts {
			if value, ok := want[f.Definition.Name]; ok {
				matched++
				if f.Value.Text != value.Text || f.Value.Number != value.Number || f.Value.Boolean != value.Boolean || f.Reset.Text != value.Text || f.Reset.Number != value.Number || f.Reset.Boolean != value.Boolean || f.Source != "client" {
					t.Fatalf("%s current=%+v reset=%+v source=%s want=%+v", f.Definition.Name, f.Value, f.Reset, f.Source, value)
				}
			}
		}
		if matched != len(want) {
			t.Fatalf("captured %d of %d registered provider facts", matched, len(want))
		}
	}
	checkTyped(a, wantTyped)
	for name, value := range map[string]string{"metadata.flag": "on", "metadata.text": "replacement", "metadata.duration": "3s", "metadata.real": "2.34567891", "metadata.mode": "safe"} {
		provider.values[name] = value
	}
	checkTyped(a, wantTyped)
	for _, tc := range []struct {
		name, value string
		want settings.Value
	}{
		{"metadata.flag", "on", settings.Value{Boolean: true}},
		{"metadata.text", "changed", settings.Value{Text: "changed"}},
		{"metadata.duration", "4s", settings.Value{Number: 4000}},
		{"metadata.real", "9.87654321", settings.Value{Number: 9.87654321}},
		{"metadata.mode", "safe", settings.Value{Text: "safe"}},
	} {
		op, err := a.Acquire(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		d := e.Settings.Definitions()[tc.name]
		_, err = core.EngineApplySetting(e, op, a.Settings, d, &tc.value, false)
		if err == nil {
			a.Settings.Mu.Lock()
			current := a.Settings.Typed[tc.name]
			reset := a.Settings.DefaultTyped[tc.name]
			a.Settings.Mu.Unlock()
			wantReset := wantTyped[tc.name]
			if current.Boolean != tc.want.Boolean || current.Text != tc.want.Text || current.Number != tc.want.Number || reset.Boolean != wantReset.Boolean || reset.Text != wantReset.Text || reset.Number != wantReset.Number {
				op.Done()
				t.Fatalf("%s typed SET=%+v reset=%+v", tc.name, current, reset)
			}
			_, err = core.EngineApplySetting(e, op, a.Settings, d, nil, false)
		}
		op.Done()
		if err != nil {
			t.Fatal(err)
		}
		if provider.calls[tc.name] != 1 {
			t.Fatalf("RESET reran provider for %s", tc.name)
		}
	}
	checkTyped(a, wantTyped)
	provider.values["application_name"] = "replacement"
	provider.values["statement_timeout"] = "3s"
	apply := func(value *string) {
		t.Helper()
		op, err := a.Acquire(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer op.Done()
		if _, err := core.EngineApplySetting(e, op, a.Settings, e.Settings.Definitions()["application_name"], value, false); err != nil {
			t.Fatal(err)
		}
	}
	empty := ""
	apply(&empty)
	a.Settings.Mu.Lock()
	a.Settings.CurrentIsolation = "repeatable read"
	a.Settings.CurrentReadOnly = true
	a.Settings.CurrentDeferrable = true
	a.Settings.Mu.Unlock()
	current := capture(a)
	if current["application_name"] != "/session/" || current["transaction_isolation"] != "repeatable read/default/read committed" || current["transaction_read_only"] != "on/default/off" || current["transaction_deferrable"] != "on/default/off" {
		t.Fatal(current)
	}
	apply(nil)
	a.Settings.FinishTransaction(true)
	if got := capture(a); got["application_name"] != initial["application_name"] || got["statement_timeout"] != initial["statement_timeout"] {
		t.Fatal(got)
	}
	b := sessionStart(t, e, provider)
	if got := capture(b); got["application_name"] != "replacement/client/replacement" || got["statement_timeout"] != "3000/client/3000" {
		t.Fatal(got)
	}
	checkTyped(b, map[string]settings.Value{
		"metadata.flag": {Boolean: true}, "metadata.text": {Text: "replacement"},
		"metadata.duration": {Number: 3000}, "metadata.real": {Number: 2.34567891},
		"metadata.mode": {Text: "safe"},
	})
	checkTyped(a, wantTyped)
	for _, d := range defs {
		registered := e.Settings.Definitions()[d.Name]
		if registered.BootDefault != d.BootDefault || registered.BootValue().Number != d.BootValue().Number || registered.BootValue().Text != d.BootValue().Text {
			t.Fatal("provider changed shared definition", d.Name)
		}
	}
	if capture(a)["application_name"] != initial["application_name"] {
		t.Fatal("peer changed captured defaults")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if facts, err := settingsRuntimeForTest(t, e, a).SessionSettings(ctx); facts != nil || err != context.Canceled {
		t.Fatal(facts, err)
	}
	store := &session.SettingsStore{Defaults: make(map[string]string), Values: make(map[string]string)}
	if err := store.Initialize(t.Context(), e.Settings.Definitions(), e.Settings.Order(), provider); err != nil {
		t.Fatal(err)
	}
	if err := store.EndSession(ids.SessionToken{}, session.CleanClose); err != nil || store.ProviderDefaults != nil || store.DefaultTyped != nil {
		t.Fatal(store, err)
	}
}

func TestPGSettingsCoherentCapture(t *testing.T) {
	e := engine.New()
	s := sessionStart(t, e, nil)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	runtime := settingsRuntimeForTest(t, e, s)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 1000 {
			s.Settings.Mu.Lock()
			value := "even"
			if i%2 != 0 {
				value = "odd"
			}
			s.Settings.Typed["application_name"] = settings.Value{Text: value}
			s.Settings.Typed["lc_messages"] = settings.Value{Text: value}
			s.Settings.Mu.Unlock()
		}
	}()
	defer func() { <-done }()
	for range 1000 {
		facts, err := runtime.SessionSettings(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var application, locale string
		for _, f := range facts {
			if f.Definition.Name == "application_name" {
				application = f.Value.Text
			}
			if f.Definition.Name == "lc_messages" {
				locale = f.Value.Text
			}
		}
		if application != "" && application != locale {
			t.Errorf("torn capture %s/%s", application, locale)
			break
		}
	}
	<-done
}
