package engine

import (
	"errors"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func TestTypeLifecycleRestoreLookupAuthority(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE SCHEMA tl_private", "CREATE TYPE tl_private.previous AS ENUM ('a')", "ALTER TYPE tl_private.previous RENAME TO current_name"} {
		domainUnitPublish(t, e, domainUnitStage(t, e, op, q))
	}
	named, ok := e.Roles().Lookup("pg_signal_backend")
	if !ok {
		t.Fatal("missing released role fixture")
	}
	before := e.CatalogRead()
	namespace := before.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "tl_private"})
	address := before.NameAddress(ids.NameKey{ClassID: ids.TypeClassID, Namespace: namespace, Name: "current_name"})
	provider := before.MustType(address)
	e.mu.Lock()
	op.Session.Mu.Lock()
	original := op.Session.Authorization.State
	op.Session.Authorization.State.SessionUser = named.Ref
	op.Session.Authorization.State.CurrentUser = named.Ref
	op.Session.Mu.Unlock()
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		op.Session.Mu.Lock()
		op.Session.Authorization.State = original
		op.Session.Mu.Unlock()
		operationReplay(op).tableDestination = false
		e.importTargets = nil
		e.mu.Unlock()
	}()
	checkCode := func(err error, code string) {
		t.Helper()
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != code {
			t.Fatal(err, code)
		}
	}
	raw := parser.TypeName{Name: "current_name", Schema: &parser.Identifier{Text: "tl_private"}}
	resolve := func(raw parser.TypeName) error {
		t.Helper()
		e.mu.Lock()
		defer e.mu.Unlock()
		usage, err := e.resolveAnnotationType(raw, op, false)
		if err == nil && usage.Type != provider.SQLType {
			t.Fatal("lookup substituted a provider")
		}
		return err
	}
	checkCode(resolve(raw), "42501")
	operationReplay(op).tableDestination = true
	e.mu.Lock()
	_, err := e.authorizationLocked(op)
	e.mu.Unlock()
	if err == nil {
		t.Fatal("replay marker alone granted lookup authority")
	}
	e.mu.Lock()
	e.importTargets = make(map[authorization.PrincipalRef]authorization.PrincipalRef)
	trusted, err := e.authorizationLocked(op)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if trusted.State.CurrentUser != named.Ref || trusted.State.SessionUser != named.Ref || trusted.State.Authenticated != original.Authenticated {
		t.Fatal("trusted lookup changed named ownership/session identity")
	}
	input := pathInput(trusted, false)
	if input.View.State.CurrentUser != original.Authenticated || input.NamespaceUser != named.Ref || input.Env.State.CurrentUser != named.Ref {
		t.Fatal("lookup permission and semantic principals not separated")
	}
	if err := resolve(raw); err != nil {
		t.Fatal("current provider lookup denied", err)
	}
	checkCode(trusted.CheckCreate(e.catalog.Domain.Ref(before.Epoch(), namespace)), "42501")
	old := raw
	old.Name = "previous"
	checkCode(resolve(old), "42704")
	operationReplay(op).tableDestination = false
	checkCode(resolve(raw), "42501")
	operationReplay(op).tableDestination = true
	// Both the isolated marker and a live authenticated bootstrap are required.
	e.mu.Lock()
	op.Session.Mu.Lock()
	op.Session.Authorization.State.Authenticated = named.Ref
	op.Session.Mu.Unlock()
	_, err = e.authorizationLocked(op)
	e.mu.Unlock()
	if err == nil {
		t.Fatal("non-bootstrap authenticated principal granted trusted lookup")
	}
	foreign := New()
	foreignBootstrap, _ := foreign.Roles().Lookup("postgres")
	e.mu.Lock()
	op.Session.Mu.Lock()
	op.Session.Authorization.State.Authenticated = foreignBootstrap.Ref
	op.Session.Mu.Unlock()
	_, err = e.authorizationLocked(op)
	op.Session.Mu.Lock()
	op.Session.Authorization.State.Authenticated = original.Authenticated
	op.Session.Mu.Unlock()
	e.mu.Unlock()
	if err == nil {
		t.Fatal("foreign principal with matching numeric identity granted trusted lookup")
	}
	if e.CatalogRead().State() != before.State() || e.CatalogRead().MustType(address) != provider {
		t.Fatal("definition lookup changed catalog ownership or providers")
	}
}
