package schemacmds_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/engine/session"
)

// Decide both final-publication orderings explicitly rather than relying on a
// scheduler to arrange a role retirement against a newly admitted historical ACL.
func TestPGInitPrivsRolePublication(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE ROLE ip_race")
	principal, _ := e.Roles().Lookup("ip_race")
	base := e.CatalogRead()
	address := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	owner := base.MustNamespace(address).Owner
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	stage.RecordInitialPrivilege(address, 'e', authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
		{Grantor: owner, Grantee: authorization.ACLGrantee{Role: principal.Ref}, Privileges: authorization.UsagePrivilege},
	}})
	retirement := session.NewJournal()
	retirement.RetiredRoles = map[authorization.PrincipalRef]struct{}{principal.Ref: {}}
	retirement.Roles = e.RoleStore.Published.WithRoot(e.RoleStore.Published.Root.Retire(map[authorization.PrincipalRef]authorization.RoleDescriptor{principal.Ref: principal}))
	// No live descriptor or initial record in the captured root references the
	// role. The retirement was legal before the competing snapshot addition.
	e.Mu.Lock()
	err := role.RetirementPreflightLocked(retirement, &e.Catalog.Owner, &e.RoleStore.Domain, base.State())
	e.Mu.Unlock()
	if err != nil {
		t.Fatal("unreferenced retirement was rejected", err)
	}
	// Addition wins: the same captured retirement must reject at final preflight.
	e.Mu.Lock()
	err = role.RetirementPreflightLocked(retirement, &e.Catalog.Owner, &e.RoleStore.Domain, stage.State())
	e.Mu.Unlock()
	if !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("late historical dependency escaped retirement preflight", err)
	}
	if e.CatalogRead().State() != base.State() {
		t.Fatal("failed retirement preflight published its private input")
	}
	// Retirement wins: preparing the already-captured addition must reject the
	// retired incarnation rather than accepting its old name or numerical ID.
	enginetest.Run(t, e, "DROP ROLE ip_race")
	enginetest.Run(t, e, "CREATE ROLE ip_race")
	replacement, _ := e.Roles().Lookup("ip_race")
	if replacement.Ref == principal.Ref {
		t.Fatal("role name reuse retained the retired identity")
	}
	current := e.CatalogRead().State()
	if publication, err := e.ValidateCatalogStage(context.Background(), stage); err == nil || publication != nil {
		t.Fatal("stale initial ACL principal admitted after retirement", publication, err)
	}
	if e.CatalogRead().State() != current || e.CatalogRead().State().InitialPrivilege(address) != base.State().InitialPrivilege(address) {
		t.Fatal("rejected historical addition changed published roots")
	}
}
