package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func catalogRelationsUnitBind(t *testing.T, e *core.Engine, name string, args ...parser.Expr) expr.Expr {
	t.Helper()
	binder := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	binder.Clause.AggregateClause = "SELECT"
	raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}, Args: args}
	node, err := binder.BindFunctionProvider(raw)
	if err != nil {
		t.Fatal(err)
	}
	return node
}

func TestCatalogRelationsCapture(t *testing.T) {
	e := engine.New()
	// Publish real immutable authorization roots; no SQL, enumeration cache or
	// mocked lookup is involved. The statement keeps the original root frontier.
	e.Mu.Lock()
	ref := e.RoleStore.Domain.Principal(50000)
	first := cloneRoleFixture(e.RoleStore.Published)
	putFixtureRole(first, authorization.RoleDescriptor{Ref: ref, Name: "captured é\"role", ConnectionLimit: -1})
	e.RoleStore.PublishLocked(first)
	e.Mu.Unlock()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(context.Canceled)
	e.Mu.Lock()
	err = e.CaptureCommentSnapshotLocked(scope)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	original := core.InquiryCommentRoles(&scope.Inquiry)
	e.Mu.Lock()
	next := cloneRoleFixture(e.RoleStore.Published)
	retireFixtureRole(next, ref)
	replacement := e.RoleStore.Domain.Principal(50001)
	putFixtureRole(next, authorization.RoleDescriptor{Ref: replacement, Name: "captured é\"role", ConnectionLimit: -1})
	e.RoleStore.PublishLocked(next)
	e.Mu.Unlock()
	oidType, _ := e.CatalogRead().LookupType(sqltype.Oid)
	nameType, _ := e.CatalogRead().LookupType(sqltype.Name)
	lookup := func(scope *core.StatementScope, oid uint32, want string) {
		t.Helper()
		input, err := sqltype.OIDValue(oidType, oid)
		if err != nil {
			t.Fatal(err)
		}
		node := functions.CatalogUserName{Info: expr.Info{Usage: nameType.Usage()}, Arg: plan.LiteralExpr{Info: expr.Info{Usage: oidType.Usage()}, Value: input}}
		value, err := node.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Host: core.HostOf(scope)}})
		if err != nil {
			t.Fatal(err)
		}
		text, err := value.Text()
		if err != nil || text != want {
			t.Fatal("captured authority", text, want, err)
		}
	}
	lookup(scope, 50000, "captured é\"role")
	lookup(scope, 50001, "unknown (OID=50001)")
	e.Mu.Lock()
	err = e.CaptureCommentSnapshotLocked(scope)
	e.Mu.Unlock()
	if err != nil || core.InquiryCommentRoles(&scope.Inquiry) != original {
		t.Fatal("statement snapshot recaptured", err)
	}
	newer, newDone := enginetest.RoleOperation(t, e, "postgres")
	defer newDone()
	current, err := e.StartStatement(newer)
	if err != nil {
		t.Fatal(err)
	}
	defer current.Finish(context.Canceled)
	e.Mu.Lock()
	err = e.CaptureCommentSnapshotLocked(current)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	lookup(current, 50000, "unknown (OID=50000)")
	lookup(current, 50001, "captured é\"role")
	lookup(current, 0, "unknown (OID=0)")
	foreign := engine.New()
	saved := core.InquiryCommentRoles(&current.Inquiry)
	core.SetInquiryCommentRoles(&current.Inquiry, foreign.RoleStore.Published.Root.Read(authorization.PrincipalRef{}))
	input, _ := sqltype.OIDValue(oidType, 50001)
	node := functions.CatalogUserName{Info: expr.Info{Usage: nameType.Usage()}, Arg: plan.LiteralExpr{Info: expr.Info{Usage: oidType.Usage()}, Value: input}}
	if _, err := node.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Host: core.HostOf(current)}}); err == nil {
		t.Fatal("foreign authorization snapshot trusted")
	}
	core.SetInquiryCommentRoles(&current.Inquiry, saved)
}
