package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func randomPlanningUnitDependency(t *testing.T, e *core.Engine, dependencies []catalog.ExpressionDependency, name string) catalog.ExpressionDependency {
	t.Helper()
	for _, dependency := range dependencies {
		if dependency.Ref.Address.ClassID == ids.ProcedureClassID && e.CatalogRead().MustProcedure(dependency.Ref.Address).Name() == name {
			return dependency
		}
	}
	t.Fatal("missing original routine dependency", name)
	return catalog.ExpressionDependency{}
}

func eMustJSONBRoutineAuthority(t *testing.T, e *core.Engine, op *session.Operation) *core.AuthorizationView {
	t.Helper()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		t.Fatal(err)
	}
	return &auth
}

func objectACLUnitEntry(source, target authorization.PrincipalRef, use, options authorization.Privileges) authorization.ACLEntry {
	return authorization.ACLEntry{Grantor: source, Grantee: authorization.ACLGrantee{Role: target}, Privileges: use, GrantOptions: options}
}

func ownershipUnitPublish(t *testing.T, e *core.Engine, stage *core.CatalogStage) {
	t.Helper()
	p, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = enginetest.PublishCatalog(e, p, core.PublicationToken{Epoch: stage.Base.Epoch()}); err != nil {
		t.Fatal(err)
	}
}

func roleAttributeCreate(t *testing.T, e *core.Engine, op *session.Operation, name string, login bool, o parser.RoleOptions) authorization.RoleDescriptor {
	t.Helper()
	roleAttributeApply(t, e, op, &parser.CreateRole{Name: parser.Identifier{Text: name}, DefaultLogin: login, Options: o})
	d, ok := e.Roles().Lookup(name)
	if !ok {
		t.Fatal("missing role", name)
	}
	return d
}

func roleAttributeApply(t *testing.T, e *core.Engine, op *session.Operation, s parser.Statement) {
	t.Helper()
	r, err := e.Execute(op, s, 0, nil)
	if err != nil || r.Count != 0 || r.Rows != nil {
		t.Fatalf("attribute utility: result=%#v error=%v", r, err)
	}
}
