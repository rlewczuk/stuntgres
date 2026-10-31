package core_test

import (
	"context"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
)

func TestForeignKeyTrustedReplayNamespacePrincipal(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE ROLE fk_namespace_alpha")
	enginetest.SessionSQL(t, s, "CREATE SCHEMA fk_namespace_alpha AUTHORIZATION fk_namespace_alpha")
	enginetest.SessionSQL(t, s, "SET ROLE fk_namespace_alpha")
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	core.OperationReplay(op).InsertAuthority = true
	e.Mu.Lock()
	originalTargets := e.ImportTargets
	e.ImportTargets = make(map[authorization.PrincipalRef]authorization.PrincipalRef)
	ordinary, ordinaryErr := e.AuthorizationLocked(op)
	trusted, trustedErr := e.StatementAuthorizationLocked(op, enginetest.Statement(t, "INSERT INTO nowhere VALUES(1)"))
	var path []ids.ObjectAddress
	var pathErr error
	if trustedErr == nil {
		path, _, pathErr = core.ExplicitPath(trusted)
	}
	e.ImportTargets = originalTargets
	e.Mu.Unlock()
	if ordinaryErr != nil || trustedErr != nil || pathErr != nil {
		t.Fatal(ordinaryErr, trustedErr, pathErr)
	}
	alpha := ordinary.Catalog.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "fk_namespace_alpha"})
	if ordinary.State.CurrentUser == ordinary.State.Authenticated || trusted.State.CurrentUser != ordinary.State.Authenticated || core.AuthorizationNamespaceUser(&trusted) != ordinary.State.CurrentUser || !slices.Contains(path, alpha) || trusted.SessionRoleState != ordinary.SessionRoleState {
		t.Fatal("permission elevation replaced semantic namespace principal", ordinary.State, trusted.State, core.AuthorizationNamespaceUser(&trusted), path)
	}
	after, err := core.OperationAuthorization(op)
	if err != nil || after.State != ordinary.State || core.AuthorizationNamespaceUser(&after) != (authorization.PrincipalRef{}) {
		t.Fatal("transient replay authority changed public session identity", after.State, err)
	}
}

func TestForeignKeyCreateOutcomeIdentity(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	created := enginetest.SessionSQL(t, s, "CREATE TABLE outcome(pid INT)").Created
	view := e.CatalogRead()
	ref := enginetest.CatalogRef(t, e, view, "outcome")
	if created != ref {
		t.Fatal("creator did not return its exact minted reference", created, ref)
	}
	skipped := enginetest.SessionSQL(t, s, "CREATE TABLE IF NOT EXISTS outcome(pid INT REFERENCES missing_schema.missing_parent)").Created
	if skipped != (ids.ObjectRef{}) || e.CatalogRead().State() != view.State() {
		t.Fatal("no-op masqueraded as a successful creation", skipped)
	}
	if len(view.MustRelation(ref.Address).Constraints.ForeignKeys) != 0 {
		t.Fatal("no-op attached ignored foreign-key metadata")
	}
}
