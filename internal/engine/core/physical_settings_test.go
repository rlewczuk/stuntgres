package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestPhysicalSettingsReadAccess(t *testing.T) {
	domain := &authorization.Domain{}
	root := authorization.Seed(domain)
	bootstrap := domain.Principal(authorization.BootstrapPrincipalID)
	for i, name := range []string{"reader", "bridge", "inherited", "setonly", "supermember"} {
		var err error
		root, err = root.PutDescriptor(authorization.RoleDescriptor{Ref: domain.Principal(uint64(20000 + i)), Name: name, Inherit: true})
		if err != nil {
			t.Fatal(err)
		}
	}
	state := authorization.RoleState{Authenticated: bootstrap, SessionUser: bootstrap, CurrentUser: bootstrap}
	for _, edge := range []struct {
		target, member string
		inherit        bool
	}{
		{"pg_read_all_settings", "reader", true},
		{"reader", "bridge", true},
		{"bridge", "inherited", true},
		{"reader", "setonly", false},
		{"postgres", "supermember", true},
	} {
		request := &parser.RoleMembership{
			Targets: []parser.RoleSpec{{Name: edge.target}}, Members: []parser.RoleSpec{{Name: edge.member}},
			Options: []parser.MembershipOption{{Name: "inherit", Value: edge.inherit}, {Name: "set", Value: true}},
		}
		next, _, _, err := authorization.MembershipCandidate(t.Context(), root.Read(bootstrap), state, request, nil, true, true)
		if err != nil {
			t.Fatal(err)
		}
		root = next
	}
	roles := root.Read(bootstrap)
	registry := settings.NewRegistry(nil, false)
	restrictedNames := map[string]bool{
		"config_file": true, "data_directory": true, "external_pid_file": true,
		"hba_file": true, "ident_file": true, "dynamic_library_path": true,
		"extension_control_path": true, "jit_provider": true,
		"session_preload_libraries": true, "shared_preload_libraries": true,
	}
	for name := range restrictedNames {
		d, err := registry.Lookup(name)
		if err != nil || !d.ReadRestricted {
			t.Fatal("missing restricted builtin", name, d, err)
		}
	}
	for name, d := range registry.Definitions() {
		if d.ReadRestricted != restrictedNames[name] {
			t.Fatal("unexpected restricted-read policy", name, d.ReadRestricted)
		}
	}
	store := &session.SettingsStore{Defaults: make(map[string]string), Values: make(map[string]string)}
	if err := store.Initialize(t.Context(), registry.Definitions(), registry.Order(), nil); err != nil {
		t.Fatal(err)
	}
	var text *sqltype.Descriptor
	for _, typ := range sqltype.Builtins() {
		if typ.ID() == sqltype.Text {
			text = typ
		}
	}
	for _, tc := range []struct {
		role    string
		allowed bool
	}{
		{"postgres", true}, {"pg_read_all_settings", true}, {"pg_monitor", true},
		{"reader", true}, {"inherited", true}, {"setonly", false}, {"supermember", false},
	} {
		principal, ok := roles.Lookup(tc.role)
		if !ok {
			t.Fatal(tc.role)
		}
		current := state
		current.CurrentUser = principal.Ref
		view := core.AuthorizationView{View: access.View{Roles: roles, State: current}}
		allowed, err := core.AuthorizationCanReadRestrictedSettings(view)
		if err != nil || allowed != tc.allowed {
			t.Fatal(tc.role, allowed, err)
		}
		rows, err := store.ShowAllRowsLocked(t.Context(), registry, text, allowed)
		if err != nil {
			t.Fatal(err)
		}
		count := len(registry.Order())
		if !allowed {
			count -= len(restrictedNames)
		}
		if len(rows) != count {
			t.Fatal(tc.role, "filtered registry size", len(rows), count)
		}
		seen := make(map[string]bool, len(rows))
		for _, row := range rows {
			name, err := row[0].Text()
			if err != nil {
				t.Fatal(tc.role, "aggregate name", err)
			}
			name = settings.FoldName(name)
			if _, err := registry.Lookup(name); err != nil || seen[name] || restrictedNames[name] && !allowed {
				t.Fatal(tc.role, "unexpected aggregate member", name, err)
			}
			seen[name] = true
		}
		for _, name := range registry.Order() {
			if seen[name] != (allowed || !restrictedNames[name]) {
				t.Fatal(tc.role, "aggregate membership", name, seen[name])
			}
		}
		for name := range restrictedNames {
			value, err := store.CurrentSettingLocked(t.Context(), registry, text, sqltype.String(text, name), allowed)
			if allowed {
				if err != nil {
					t.Fatal(err)
				}
				if got, err := value.Text(); err != nil || got != store.Defaults[name] {
					t.Fatal(name, got, err)
				}
			} else {
				want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: `permission denied to examine "` + name + `"`, Detail: `Only roles with privileges of the "pg_read_all_settings" role may examine this parameter.`}
				var got *sqlstate.Error
				if !errors.As(err, &got) || *got != *want {
					t.Fatal(tc.role, name, err)
				}
			}
		}
	}
	for _, current := range []authorization.PrincipalRef{{}, domain.Principal(99999), (&authorization.Domain{}).Principal(10)} {
		view := core.AuthorizationView{View: access.View{Roles: roles, State: authorization.RoleState{CurrentUser: current}}}
		if _, err := core.AuthorizationCanReadRestrictedSettings(view); err == nil {
			t.Fatal("invalid authority accepted", current)
		}
	}
	if allowed, err := core.AuthorizationCanReadRestrictedSettings((core.AuthorizationView{})); allowed || err == nil {
		t.Fatal("absent authority must not grant restricted reads", allowed, err)
	}
	restricted, ok := roles.Lookup("pg_read_all_settings")
	if !ok {
		t.Fatal("fixture missing restricted-read role")
	}
	incomplete := root.Retire(map[authorization.PrincipalRef]authorization.RoleDescriptor{restricted.Ref: restricted})
	broken := core.AuthorizationView{View: access.View{Roles: incomplete.Read(bootstrap), State: state}}
	if allowed, err := core.AuthorizationCanReadRestrictedSettings(broken); allowed || err == nil {
		t.Fatal("missing restricted-read role must remain an authority error", allowed, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if rows, err := store.ShowAllRowsLocked(ctx, registry, text, false); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled filtered registry", rows, err)
	}
	if value, err := store.CurrentSettingLocked(t.Context(), registry, text, sqltype.Null(text), false); err != nil || !value.IsNull() {
		t.Fatal("NULL current_setting changed", value, err)
	}
}
