package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
)

type developerStartupProvider struct {
	values map[string]string
	cancel context.CancelFunc
}

func (p *developerStartupProvider) SessionDefault(_ context.Context, d settings.Definition) (string, bool, error) {
	value, ok := p.values[d.Name]
	if ok && p.cancel != nil {
		p.cancel()
	}
	return value, ok, nil
}

func TestDeveloperSettingsProviderLifecycle(t *testing.T) {
	e := engine.New()
	provider := &developerStartupProvider{values: map[string]string{"ignore_system_indexes": "on", "jit_debugging_support": "on", "jit_profiling_support": "on", "post_auth_delay": "60s"}}
	s := enginetest.StartSession(t, e, provider)
	check := func(s *session.Session, changed bool) {
		t.Helper()
		facts, err := settingsRuntimeForTest(t, e, s).SessionSettings(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, fact := range facts {
			d := fact.Definition
			if _, ok := provider.values[d.Name]; !ok {
				continue
			}
			count++
			want, source := "on", "client"
			boot := "off"
			if d.Name == "post_auth_delay" {
				want, boot = "60", "0"
			}
			if !changed {
				want = boot
			}
			if d.Raw(fact.Value) != want || d.Raw(fact.Reset) != want || d.Raw(d.BootValue()) != boot || fact.Source != source {
				t.Fatal("startup metadata", d.Name, fact, want)
			}
		}
		if count != 4 {
			t.Fatal("missing startup facts", count)
		}
	}
	check(s, true)
	for name := range provider.values {
		provider.values[name] = "off"
		if name == "post_auth_delay" {
			provider.values[name] = "0"
		}
	}
	s.Settings.Mu.Lock()
	s.Mu.Lock()
	s.Settings.ResetAllLocked(e.Settings)
	s.Mu.Unlock()
	s.Settings.Mu.Unlock()
	check(s, true)
	replacement := enginetest.StartSession(t, e, provider)
	check(replacement, false)
	check(s, true)
	for _, mode := range []string{"invalid", "canceled"} {
		p := &developerStartupProvider{values: map[string]string{"post_auth_delay": "2148"}}
		ctx := t.Context()
		cancel := func() {}
		if mode == "canceled" {
			ctx, cancel = context.WithCancel(ctx)
			p.values["post_auth_delay"] = "60s"
			p.cancel = cancel
		}
		failed, err := enginetest.OpenSession(ctx, e, session.SessionStart{ID: ids.SessionID(900001), Defaults: p})
		cancel()
		if failed != nil || err == nil {
			t.Fatal("failed startup published", mode, failed, err)
		}
		if mode == "canceled" && !errors.Is(err, context.Canceled) {
			t.Fatal("lost cancellation", err)
		}
		core.EngineSessionMuRef(e).Lock()
		count := len(core.EngineSessions(e))
		core.EngineSessionMuRef(e).Unlock()
		if count != 2 {
			t.Fatal("failed startup leaked owner", mode, count)
		}
	}
}
