package engine

import (
	"context"
	"stuntgres/internal/engine/authorization"
	"testing"
)

func TestRLSBaseOwnerPublicationFreshness(t *testing.T) {
	e, op, done, d := policyUnitFixture(t)
	defer done()
	roleExecute(t, e, op, "CREATE ROLE rls_publication_reader")
	roleExecute(t, e, op, "CREATE ROLE rls_publication_bypass BYPASSRLS")
	reader, bypass := e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("rls_publication_reader"), e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("rls_publication_bypass")
	publish := func(next *relationDescriptor) {
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
	enabled.rlsEnabled = true
	enabled.revision++
	publish(&enabled)
	retained := e.CatalogRead()
	retainedDescriptor := retained.MustRelation(d.address)
	decision := func(principal authorization.PrincipalRef, want rlsDisposition) {
		t.Helper()
		e.mu.Lock()
		auth, err := e.authorizationLocked(op)
		current := e.catalog.state.MustRelation(d.address)
		e.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		got, err := rowSecurityDecision(auth, current, principal, policySelect, false)
		if err != nil || got != want {
			t.Fatal("fresh descriptor authority", got, err, want)
		}
	}
	decision(reader, rlsDefaultDeny)
	owned := enabled
	owned.owner = reader
	owned.revision++
	publish(&owned)
	decision(reader, rlsUnrestricted)
	decision(bypass, rlsUnrestricted)
	if retained.Get(d.address) != retainedDescriptor || retainedDescriptor.owner != d.owner || !retainedDescriptor.rlsEnabled || enabled.owner != d.owner || d.rlsEnabled {
		t.Fatal("publication mutated retained root/descriptor")
	}
	forced := owned
	forced.rlsForced = true
	forced.revision++
	publish(&forced)
	decision(reader, rlsDefaultDeny)
	decision(bypass, rlsUnrestricted)
	decision(d.owner, rlsUnrestricted)
	if owned.rlsForced || retained.MustRelation(d.address).rlsForced {
		t.Fatal("FORCE mutated older roots")
	}
}
