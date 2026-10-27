package rolecmds_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/engine/rolecmds"
	"stuntgres/internal/parser"
)

func TestRoleSettingsRetirementAtomicity(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	base := e.RoleStore.Published
	first, free := roleTransactionalCandidate(t, e, base, "settings_free")
	e.RoleStore.PublishLocked(first)
	e.RoleStore.InstallPrincipalFrontierLocked(free.Ref.ID() + 1)
	second, blocked := roleTransactionalCandidate(t, e, first, "settings_blocked")
	global := access.RoleSettingKey{}
	freeOnly := access.RoleSettingKey{Role: free.Ref}
	freeDatabase := access.RoleSettingKey{Role: free.Ref, Database: e.Catalog.DatabaseAddress()}
	blockedOnly := access.RoleSettingKey{Role: blocked.Ref}
	settings := second.Settings
	for _, key := range []access.RoleSettingKey{global, freeOnly, freeDatabase, blockedOnly} {
		settings = settings.WithRow(key, []access.RoleSetting{{Name: "application_name", Value: "retained"}})
	}
	original := second.WithSettings(settings)
	e.RoleStore.PublishLocked(original)
	e.RoleStore.InstallPrincipalFrontierLocked(blocked.Ref.ID() + 1)
	state := catalog.CloneState(e.Catalog.State)
	ns := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 30000}, ObjectRevision: 1, ObjectName: "settings_dependency", Owner: blocked.Ref}
	state.SetDescriptor(ns.Address(), ns)
	state.BindName(catalog.DescriptorNameKey(ns), ns.Address())
	e.Catalog.PublishLocked(state)
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		t.Fatal(err)
	}
	host := rolecmds.RoleHost{DDLHost: core.DDLHost{E: e, Op: op}}
	if _, err := role.PlanDropRoles(host, auth, &parser.DropRole{Names: []string{free.Name, blocked.Name}}); err == nil {
		t.Fatal("dependent later target accepted")
	}
	if e.RoleStore.Published != original || original.Settings != settings || len(settings.Lookup(freeOnly)) != 1 || len(settings.Lookup(freeDatabase)) != 1 {
		t.Fatal("batch failure removed a prefix of defaults")
	}
	plan, err := role.PlanDropRoles(host, auth, &parser.DropRole{Names: []string{free.Name}})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Roles.Settings.Lookup(freeOnly) != nil || plan.Roles.Settings.Lookup(freeDatabase) != nil || &plan.Roles.Settings.Lookup(global)[0] != &settings.Lookup(global)[0] || &plan.Roles.Settings.Lookup(blockedOnly)[0] != &settings.Lookup(blockedOnly)[0] {
		t.Fatal("retirement lost exact scope cleanup or surviving immutable backing")
	}
	if len(settings.Lookup(freeOnly)) != 1 || e.RoleStore.Published != original {
		t.Fatal("pure retirement candidate mutated committed authority")
	}
}
