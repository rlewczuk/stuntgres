package schemacmds_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/engine/rolecmds"
	"stuntgres/internal/parser"
)

func TestDomainIdentityGrantStamp(t *testing.T) {
	e := engine.New()
	aclUnitRoles(e, "identity_acl_reader")
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.d AS integer"))
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "ALTER DOMAIN public.d RENAME TO renamed"))
	old := domainUnitType(t, e.CatalogRead(), "renamed")
	stamp := old.EffectiveDomainPlanStamp()
	raw := enginetest.Statement(t, "GRANT USAGE ON DOMAIN public.renamed TO identity_acl_reader").(*parser.ObjectGrant)
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		t.Fatal(err)
	}
	host := rolecmds.RoleHost{core.DDLHost{E: e, Op: op}}
	req, err := role.ObjectGrantRequest(host, auth, raw)
	if err != nil {
		t.Fatal(err)
	}
	allocated := 0
	revision := func() (ids.DefinitionRevision, error) { allocated++; return e.Catalog.RevisionLocked() }
	plan, warnings, err := role.ObjectGrantPlan(context.Background(), auth.View, raw, req, revision)
	if err != nil || len(warnings) != 0 || allocated != 1 {
		t.Fatal("real changed ACL did not allocate ordinary revision", err, warnings, allocated)
	}
	next := plan.Candidate.MustType(old.Address())
	if plan.Changed[old.Address()].Descriptor != next || next.Revision() == old.Revision() || next.EffectiveDomainPlanStamp() == stamp || next.DomainPlanStamp != 0 || old.EffectiveDomainPlanStamp() != stamp {
		t.Fatal("candidate/TX changes disagree or copied identity stamp survived ACL mutation")
	}
	// Repeating the exact grant must not allocate another stamp or mutate state.
	auth.Recapture(e.Catalog.Read(plan.Candidate))
	req, err = role.ObjectGrantRequest(host, auth, raw)
	if err != nil {
		t.Fatal(err)
	}
	allocated = 0
	unchanged, warnings, err := role.ObjectGrantPlan(context.Background(), auth.View, raw, req, revision)
	if err != nil || len(warnings) != 0 || allocated != 0 || unchanged.Candidate != nil || len(unchanged.Changed) != 0 {
		t.Fatal("no-op ACL advanced definition", err, warnings, allocated)
	}
}
