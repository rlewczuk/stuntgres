package engine

import (
	"context"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"testing"
)

func TestRLSBaseOwnerPublicationFreshness(t *testing.T) {
	e, op, done, d := policyUnitFixture(t)
	defer done()
	roleExecute(t, e, op, "CREATE ROLE rls_publication_reader")
	roleExecute(t, e, op, "CREATE ROLE rls_publication_bypass BYPASSRLS")
	reader, bypass := e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("rls_publication_reader"), e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("rls_publication_bypass")
	publish := func(next *catalog.Relation) {
		t.Helper()
		base := e.CatalogRead()
		stage := e.BeginCatalogStage(base)
		defer e.DiscardCatalogStage(stage)
		if err := stage.Replace(next); err != nil {
			t.Fatal(err)
		}
		prepared, err := e.ValidateCatalogStage(context.Background(), stage)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.PublishCatalog(prepared, PublicationToken{Epoch: base.Epoch()}); err != nil {
			t.Fatal(err)
		}
	}
	enabled := *d
	enabled.Security.RLSEnabled = true
	enabled.ObjectRevision++
	publish(&enabled)
	retained := e.CatalogRead()
	retainedDescriptor := retained.MustRelation(d.Address())
	decision := func(principal authorization.PrincipalRef, denied bool) {
		t.Helper()
		e.mu.Lock()
		auth, err := e.authorizationLocked(op)
		current := e.catalog.state.MustRelation(d.Address())
		e.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		got, err := access.SelectRowSecurity(auth.Roles, current, principal, catalog.PolicySelect, false, false)
		if err != nil || got.Denied() != denied {
			t.Fatal("fresh descriptor authority", got, err, denied)
		}
	}
	decision(reader, true)
	owned := enabled
	owned.Owner = reader
	owned.ObjectRevision++
	publish(&owned)
	decision(reader, false)
	decision(bypass, false)
	if retained.Get(d.Address()) != retainedDescriptor || retainedDescriptor.Owner != d.Owner || !retainedDescriptor.Security.RLSEnabled || enabled.Owner != d.Owner || d.Security.RLSEnabled {
		t.Fatal("publication mutated retained root/descriptor")
	}
	forced := owned
	forced.Security.RLSForced = true
	forced.ObjectRevision++
	publish(&forced)
	decision(reader, true)
	decision(bypass, false)
	decision(d.Owner, false)
	if owned.Security.RLSForced || retained.MustRelation(d.Address()).Security.RLSForced {
		t.Fatal("FORCE mutated older roots")
	}
}
