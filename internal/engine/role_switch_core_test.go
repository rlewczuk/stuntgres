package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
)

func TestRoleSwitchInitializationFailures(t *testing.T) {
	for _, tc := range []struct {
		name, value, code string
		providerErr       error
		cancel            bool
	}{
		{name: "missing", value: "absent", code: "22023"},
		{name: "denied", value: "postgres", code: "42501"},
		{name: "provider", value: "switch_a", providerErr: errors.New("role default failed")},
		{name: "cancel", value: "switch_a", cancel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, _ := roleSwitchUnitSession(t)
			login, _ := e.Roles().Lookup("switch_login")
			before := len(EngineSessions(e))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			provider := roleSwitchDefaultProvider{value: tc.value, err: tc.providerErr, hook: func(context.Context, settings.Definition) {
				if tc.cancel {
					cancel()
				}
			}}
			s, err := e.StartSession(ctx, session.SessionStart{ID: ids.SessionID(testSessionID.Add(1)), Authenticated: login.Ref, Defaults: provider})
			if s != nil || err == nil || len(EngineSessions(e)) != before {
				t.Fatal("failed startup published", s, err)
			}
			if tc.code != "" {
				state(t, err, tc.code)
			}
			if tc.providerErr != nil && !errors.Is(err, tc.providerErr) {
				t.Fatal(err)
			}
			if tc.cancel && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		})
	}
	for _, value := range []string{"none"} {
		e, _ := roleSwitchUnitSession(t)
		login, _ := e.Roles().Lookup("switch_login")
		s, err := e.StartSession(context.Background(), session.SessionStart{ID: ids.SessionID(testSessionID.Add(1)), Authenticated: login.Ref, Defaults: roleSwitchDefaultProvider{value: value}})
		if err != nil {
			t.Fatal(err)
		}
		if s.Authorization.State.CurrentUser != login.Ref || s.Authorization.State.ResetRole != (authorization.PrincipalRef{}) {
			t.Fatal(s.Authorization.State)
		}
		s.RequestEnd(session.CleanClose)
		if err := s.WaitEnd(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}
