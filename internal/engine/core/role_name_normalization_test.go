package core_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/sqlstate"
)

func roleNameNormalizationEngine(t *testing.T, name string) (*core.Engine, authorization.PrincipalRef, authorization.PrincipalRef) {
	t.Helper()
	e := engine.New()
	login := e.RoleStore.Domain.Principal(20000)
	target := e.RoleStore.Domain.Principal(20001)
	e.Mu.Lock()
	defer e.Mu.Unlock()
	root, err := e.RoleStore.Published.Root.PutDescriptor(authorization.RoleDescriptor{Ref: login, Name: "normalization_login", Login: true, Inherit: true, ConnectionLimit: -1})
	if err == nil {
		root, err = root.PutDescriptor(authorization.RoleDescriptor{Ref: target, Name: name, Inherit: true, ConnectionLimit: -1})
	}
	if err != nil {
		t.Fatal(err)
	}
	e.RoleStore.PublishLocked(e.RoleStore.Published.WithRoot(root))
	return e, login, target
}

func roleNameClippingNotice(raw, accepted string) sqlstate.Error {
	return sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42622", Message: `identifier "` + raw + `" will be truncated to "` + accepted + `"`}
}

func TestRoleSettingsPersistedNameNormalization(t *testing.T) {
	for _, accepted := range []string{strings.Repeat("X", 63), strings.Repeat("X", 62)} {
		raw := accepted + "é_suffix"
		for _, mode := range []string{"valid", "missing", "denied", "restricted"} {
			t.Run(mode+"/"+accepted, func(t *testing.T) {
				name := accepted
				if mode == "missing" {
					name = "other_target"
				}
				e, login, _ := roleNameNormalizationEngine(t, name)
				if mode != "denied" {
					login = e.RoleStore.Domain.Principal(authorization.BootstrapPrincipalID)
				}
				s := roleDefaultsStart(t, e, session.SessionStart{}, login)
				op, err := s.Acquire(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				defer op.Done()
				before, record, mark := op.RoleState(), e.RoleStore.Published, s.Settings.Savepoint()
				if mode == "restricted" {
					op.EffectiveUser = &session.EffectiveUserInvocation{Owner: login}
				}
				canonical, stored, err := e.ValidatePersistedSetting(op, "ROLE", &raw)
				want := []sqlstate.Error{roleNameClippingNotice(raw, accepted)}
				if mode == "restricted" {
					var diagnostic *sqlstate.Error
					if !errors.As(err, &diagnostic) || diagnostic.Code != "42501" || diagnostic.Message != `cannot set parameter "role" within security-definer function` {
						t.Fatal("restricted admission", err)
					}
					want = nil
				} else {
					if err != nil || canonical != "role" || stored != raw {
						t.Fatal("original catalog input lost", canonical, stored, err)
					}
					if mode == "missing" {
						want = append(want, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42704", Message: `role "` + accepted + `" does not exist`})
					} else if mode == "denied" {
						want = append(want, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42501", Message: `permission will be denied to set role "` + accepted + `"`})
					}
				}
				if !reflect.DeepEqual(op.DetachNotices(), want) || op.RoleState() != before || e.RoleStore.Published != record || !reflect.DeepEqual(s.Settings.Savepoint(), mark) {
					t.Fatal("validation notice order or immutable state changed", want)
				}
			})
		}
	}
}

func TestRoleSettingsStartupNameNormalization(t *testing.T) {
	for _, accepted := range []string{strings.Repeat("X", 63), strings.Repeat("X", 62)} {
		raw := accepted + "é_suffix"
		for _, mode := range []string{"valid", "missing", "denied", "provider", "override", "none"} {
			t.Run(mode+"/"+accepted, func(t *testing.T) {
				name := accepted
				if mode == "missing" {
					name = "other_target"
				}
				e, login, target := roleNameNormalizationEngine(t, name)
				if mode != "denied" {
					login = e.RoleStore.Domain.Principal(authorization.BootstrapPrincipalID)
				}
				stored := raw
				if mode == "provider" {
					stored = "overridden_missing_role"
				}
				key := access.RoleSettingKey{Role: login}
				record := roleDefaultsInstall(e, key, []access.RoleSetting{{Name: "role", Value: stored}})
				var provider settings.SessionDefaultProvider
				calls := 0
				if mode == "provider" || mode == "override" || mode == "none" {
					provider = roleDefaultsProvider(func(_ context.Context, d settings.Definition) (string, bool, error) {
						if d.Name != "role" {
							return "", false, nil
						}
						calls++
						if !e.Mu.TryLock() {
							return "", false, errors.New("role provider under cluster mutex")
						}
						e.Mu.Unlock()
						if mode == "none" {
							return "none", true, nil
						}
						if mode == "override" {
							return accepted, true, nil
						}
						return raw, true, nil
					})
				}
				var notices []sqlstate.Error
				s := roleDefaultsStart(t, e, session.SessionStart{Defaults: provider, NoticeHandler: func(n sqlstate.Error) {
					if !e.Mu.TryLock() {
						t.Error("startup notice under cluster mutex")
						return
					}
					e.Mu.Unlock()
					notices = append(notices, n)
				}}, login)
				want := []sqlstate.Error{roleNameClippingNotice(raw, accepted)}
				if mode == "override" || mode == "none" {
					want = nil
				} else if mode == "missing" {
					want = append(want, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: `role "` + accepted + `" does not exist`})
				} else if mode == "denied" {
					want = append(want, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "42501", Message: `permission denied to set role "` + accepted + `"`})
				}
				expected := authorization.RoleState{Authenticated: login, SessionUser: login, CurrentUser: login}
				if mode != "missing" && mode != "denied" && mode != "none" {
					expected.CurrentUser, expected.ResetRole, expected.ActiveRole = target, target, true
					expected.RoleText, expected.ResetRoleText = accepted, accepted
				}
				if !reflect.DeepEqual(notices, want) || s.Authorization.State != expected || record.Settings.Lookup(key)[0].Value != stored || e.RoleStore.Published != record || (provider != nil && calls != 1) {
					t.Fatal("startup normalization/capture", notices, want, s.Authorization.State, expected, calls)
				}
			})
		}
	}
}
