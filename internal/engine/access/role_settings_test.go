package access_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
)

func TestRoleSettingsMerge(t *testing.T) {
	var domain authorization.Domain
	a := access.RoleSettingKey{Role: domain.Principal(100)}
	b := access.RoleSettingKey{Role: domain.Principal(101), Database: ids.ObjectAddress{ClassID: ids.DatabaseClassID, ObjectID: 200}}
	input := []access.RoleSetting{{Name: "application_name", Value: "base"}, {Name: "search_path", Value: "public"}}
	var empty *access.RoleSettings
	base := empty.WithRow(a, input)
	input[0].Value = "caller mutated"
	old := base.Lookup(a)
	if old[0].Value != "base" {
		t.Fatal("row retained writable caller backing")
	}
	if base.WithRow(a, old) != base {
		t.Fatal("equal replacement allocated a new root")
	}
	left := base.WithRow(a, []access.RoleSetting{{Name: "application_name", Value: "left"}})
	right := base.WithRow(b, []access.RoleSetting{{Name: "application_name", Value: "right"}})
	if &right.Lookup(a)[0] != &old[0] || &left.Lookup(a)[0] == &old[0] {
		t.Fatal("unchanged/changed row ownership")
	}
	merged, err := access.MergeRoleSettings(context.Background(), base, left, right)
	if err != nil || merged.Lookup(a)[0].Value != "left" || merged.Lookup(b)[0].Value != "right" {
		t.Fatal("disjoint merge", err)
	}
	if &merged.Lookup(a)[0] != &left.Lookup(a)[0] || &merged.Lookup(b)[0] != &right.Lookup(b)[0] {
		t.Fatal("merge copied immutable row backing")
	}
	if _, err := access.MergeRoleSettings(context.Background(), base, left, base.WithRow(a, []access.RoleSetting{{Name: "application_name", Value: "peer"}})); err == nil {
		t.Fatal("same predecessor conflict accepted")
	}
	aba := left.WithRow(a, old)
	if _, err := access.MergeRoleSettings(context.Background(), base, left, aba); err == nil {
		t.Fatal("ABA predecessor accepted")
	}
	removed := merged.WithoutRoles([]authorization.PrincipalRef{a.Role, b.Role})
	count := 0
	for range removed.Rows() {
		count++
	}
	if count != 0 || len(base.Lookup(a)) != 2 || len(merged.Lookup(b)) != 1 {
		t.Fatal("retirement mutated retained authority")
	}
	deleted, err := access.MergeRoleSettings(context.Background(), base, base.WithRow(a, nil), right)
	if err != nil || deleted.Lookup(a) != nil || len(deleted.Lookup(b)) != 1 {
		t.Fatal("row deletion merge", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := access.MergeRoleSettings(ctx, base, left, right); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled merge", err)
	}
}

func TestRoleSettingsRecordOwnership(t *testing.T) {
	var store access.RoleStore
	store.Initialize()
	base := store.Published
	key := access.RoleSettingKey{Role: store.Domain.Principal(10)}
	next := base.WithSettings(base.Settings.WithRow(key, []access.RoleSetting{{Name: "application_name", Value: "owned"}}))
	advanced := next.Advance()
	changed := next.WithRoot(next.Root)
	if next.Revision != base.Revision+1 || advanced.Revision != next.Revision+1 || !next.SameAuthority(base) || !advanced.SameAuthority(base) || changed.SameAuthority(base) || changed.Settings != next.Settings || advanced.Settings != next.Settings || base.Settings != nil {
		t.Fatal("complete record lineage/revision retention")
	}
	store.PublishLocked(next)
	capture := store.CaptureLocked()
	var stage access.RoleStore
	stage.Initialize()
	mapping := stage.InstallCapture(capture)
	if stage.Published.Settings != nil || store.Published != next || next.Settings.Lookup(key)[0].Value != "owned" || stage.Published.Revision != next.Revision {
		t.Fatal("Restore imported defaults or replaced source")
	}
	stageRef := stage.Domain.Principal(key.Role.ID())
	if stageRef == key.Role || mapping[stageRef] != key.Role {
		t.Fatal("Restore graph provenance not reminted")
	}
}
