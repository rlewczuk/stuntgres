package extension_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestIsnValidationInputLifecycle(t *testing.T) {
	e := engine.New()
	installer, done := enginetest.RoleOperation(t, e, "postgres")
	if _, err := e.Execute(installer, &parser.CreateExtension{Name: parser.Identifier{Text: "isn"}}, 0, nil); err != nil { done(); t.Fatal(err) }
	done()
	s := enginetest.StartSession(t, e, nil)
	op, err := s.Acquire(context.Background())
	if err != nil { t.Fatal(err) }
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil { t.Fatal(err) }
	defer scope.Finish(nil)
	view := e.CatalogRead()
	typ := view.MustType(view.NameAddress(ids.NameKey{ClassID: ids.TypeClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "ean13"})).SQLType
	foreign, err := sqltype.Ean13(typ.ID(), "ean13", "public.ean13")
	if err != nil { t.Fatal(err) }
	if _, err := exec.PrepareIsnInput(op.Ctx, core.HostOf(scope), view, foreign); err == nil { t.Fatal("foreign equal-ID input activated module") }
	if s.Settings.IsnLoaded() { t.Fatal("rejected input changed recognition") }
	canceled, cancel := context.WithCancel(op.Ctx)
	cancel()
	if _, err := exec.PrepareIsnInput(canceled, core.HostOf(scope), view, typ); !errors.Is(err, context.Canceled) { t.Fatal("canceled input admission", err) }
	if s.Settings.IsnLoaded() { t.Fatal("canceled input initialized module") }
	weak, err := exec.PrepareIsnInput(op.Ctx, core.HostOf(scope), view, typ)
	if err != nil || weak || !s.Settings.IsnLoaded() { t.Fatal("genuine first input", weak, err) }
	boolean, _ := view.LookupType(sqltype.Bool)
	if _, err := core.HostOf(scope).InvokeIsnWeak(true, true, boolean); err != nil { t.Fatal(err) }
	environment := scope.Input
	environment.IsnWeak = false // A stale detached snapshot must not win at runtime.
	environment.DomainChecks = exec.InputDomainChecks(scope, view)
	marked, err := typ.InputWithEnvironment(op.Ctx, "2222222222221", -1, environment)
	if err != nil { t.Fatal("wrapped live input ignored setter", err) }
	truth, err := sqltype.IsValidISN(marked, typ.Usage(), boolean)
	if err != nil { t.Fatal(err) }
	valid, err := truth.Boolean()
	if err != nil || valid { t.Fatal("weak input lost invalid bit", err) }
	lifecycle := environment.DomainChecks.(sqltype.ISNInputLifecycle)
	if _, err := lifecycle.PrepareIsnInput(canceled, typ); !errors.Is(err, context.Canceled) { t.Fatal("wrapper softened initialization error", err) }
	if _, err := typ.InputWithEnvironment(op.Ctx, "2222222222221", -1, sqltype.InputEnvironment{IsnWeak: false}); err == nil { t.Fatal("isolated/closed input borrowed live weak state") }
}
