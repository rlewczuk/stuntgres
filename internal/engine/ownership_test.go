package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/authorization"
)

func ownershipUnitRoles() (e *Engine, owner, source, target, reader authorization.PrincipalRef) {
	e = New()
	owner = e.RoleStore.Domain.Principal(10)
	refs := []*authorization.PrincipalRef{&source, &target, &reader}
	names := []string{"ownership_source", "ownership_target", "ownership_reader"}
	publication := cloneRoleFixture(e.RoleStore.Published)
	for i, destination := range refs {
		e.RoleStore.InstallPrincipalFrontierLocked(uint64(100001 + i))
		ref, err := e.RoleStore.PrincipalCandidateLocked()
		if err != nil {
			panic(err)
		}
		*destination = ref
		e.RoleStore.InstallPrincipalFrontierLocked(ref.ID() + 1)
		putFixtureRole(publication, authorization.RoleDescriptor{Ref: ref, Name: names[i], Inherit: true})
	}
	e.RoleStore.PublishLocked(publication)
	return
}

func ownershipUnitExec(t *testing.T, e *Engine, q string) {
	t.Helper()
	if _, err := testExecute(e, context.Background(), statement(t, q), nil); err != nil {
		t.Fatal(q, err)
	}
}

func ownershipUnitPublish(t *testing.T, e *Engine, stage *CatalogStage) {
	t.Helper()
	p, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = publishCatalog(e, p, PublicationToken{Epoch: stage.Base.Epoch()}); err != nil {
		t.Fatal(err)
	}
}
