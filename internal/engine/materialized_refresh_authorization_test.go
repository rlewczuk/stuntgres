package engine

import (
	"errors"
	"slices"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func refreshUnitPrincipals(e *Engine) (authorization.PrincipalRef, authorization.PrincipalRef) {
	owner, caller := e.roles.domain.Principal(100001), e.roles.domain.Principal(100002)
	for _, role := range []authorization.RoleDescriptor{{Ref: owner, Name: "refresh_unit_owner", Login: true, Inherit: true, ConnectionLimit: -1}, {Ref: caller, Name: "refresh_unit_caller", Login: true, Inherit: true, ConnectionLimit: -1}} {
		putFixtureRole(e.roles.published, role)

	}
	return owner, caller
}

func TestMaterializedRefreshAuthorization(t *testing.T) {
	e := New()
	d := refreshUnitRelation(t, e)
	owner, caller := refreshUnitPrincipals(e)
	d.Owner = owner
	ref := e.catalog.Domain.Ref(e.catalog.state.Epoch(), d.Address())
	auth := AuthorizationView{View: access.View{Owner: e.catalog.owner(), PairedCatalog: e.CatalogRead(), RoleRecord: e.roles.published, Catalog: e.CatalogRead(), Roles: e.Roles(), State: authorization.RoleState{CurrentUser: caller, SessionUser: caller, Authenticated: caller}}, engine: e}
	for _, privilege := range []authorization.Privileges{authorization.SelectPrivilege, authorization.MaintainPrivilege} {
		d.Security.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{objectACLUnitEntry(owner, caller, privilege, 0)}}
		err := materializedRefreshAccess(auth, ref)
		if privilege == authorization.MaintainPrivilege {
			if err != nil {
				t.Fatal(err)
			}
		} else {
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "42501" || diagnostic.Message != "permission denied for materialized view unit_mv" {
				t.Fatal(err)
			}
		}
	}
	for _, kind := range []catalog.RelationKind{catalog.ViewRelation, catalog.SequenceRelation, catalog.IndexRelation} {
		d.RelationKind = kind
		var diagnostic *sqlstate.Error
		if err := materializedRefreshAccess(auth, ref); !errors.As(err, &diagnostic) || diagnostic.Code != "42809" {
			t.Fatal(kind, err)
		}
	}
	d.RelationKind = catalog.TableRelation
	var diagnostic *sqlstate.Error
	if err := materializedRefreshAccess(auth, ref); !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" {
		t.Fatal(err)
	}
}

func TestMaterializedRefreshOwnerContext(t *testing.T) {
	for _, fail := range []bool{false, true} {
		e := New()
		d := refreshUnitRelation(t, e)
		owner, caller := refreshUnitPrincipals(e)
		d.Owner = owner
		op, done := roleOperation(t, e, "refresh_unit_caller")
		t.Cleanup(done)
		scope, err := e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { scope.Finish(nil) })
		stmt := &parser.RefreshMaterializedView{}
		if err := scope.gate(stmt); err != nil {
			t.Fatal(err)
		}
		if err := scope.snapshotPhase(stmt); err != nil {
			t.Fatal(err)
		}
		e.mu.Lock()
		original, err := e.authorizationLocked(op)
		if err == nil {
			err = scope.captureIdentityLocked(original)
			if err == nil {
				err = e.captureElementInput(original, op, &scope.input)
			}
		}
		e.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		beforeIdentity, beforeRole := scope.identity, op.RoleState()
		name, _ := e.CatalogRead().LookupType(sqltype.Name)
		scope.inquiry.currentSchemaResult = sqltype.String(name, "sentinel")
		beforeSchema := scope.inquiry.currentSchemaResult
		scope.input.TypeDiagnosticName = func(sqltype.ID) string { return "caller-naming" }
		scope.input.ExactSnapshot = true
		beforeInput := scope.input
		sentinel := errors.New("producer failed")
		routine, _ := e.CatalogRead().LookupType(sqltype.Regprocedure)
		text, _ := e.CatalogRead().LookupType(sqltype.Text)
		columns := append([]catalog.Column(nil), d.Columns...)
		columns[0].Usage = routine.Usage()
		d.Columns = columns
		probe := queryProbe(text.Usage(), func(c *EvalContext) (sqltype.Value, error) {
			e.mu.Lock()
			auth, err := e.authorizationLocked(op)
			e.mu.Unlock()
			if err != nil {
				return sqltype.Value{}, err
			}
			if auth.State.CurrentUser != owner || auth.State.SessionUser != caller || !auth.materializedPopulation {
				t.Fatal("producer did not run as owner", auth.State)
			}
			current, _ := scope.identity.current.Text()
			session, _ := scope.identity.session.Text()
			if current != "refresh_unit_owner" || session != "refresh_unit_caller" {
				t.Fatal(current, session)
			}
			value, err := (functions.Identity{Info: expr.Info{Usage: name.Usage()}, Kind: functions.IdentityCurrentSchema}).Eval(c)
			if err != nil {
				return sqltype.Value{}, err
			}
			schema, _ := value.Text()
			if schema != "pg_catalog" {
				t.Fatal(schema)
			}
			captured, ok := c.Input.Regtype.(capturedRegtypeInput)
			if !ok || captured.auth.State.CurrentUser != owner || captured.auth.State.SessionUser != caller || !captured.auth.materializedPopulation ||
				!slices.Equal(captured.path, []ids.ObjectAddress{{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}}) ||
				c.Input.Regclass == nil || c.Input.Regrole == nil || c.Input.Operator == nil || c.Input.Regproc == nil ||
				c.Input.TypeDiagnosticName != nil || !c.Input.ExactSnapshot {
				t.Fatal("population did not recapture coherent restricted input", c.Input)
			}
			if fail {
				return sqltype.Value{}, sentinel
			}
			return sqltype.String(text, "abs(integer)"), nil
		})
		plan, err := sqltype.PlanCoercion(text.Usage(), routine.Usage(), sqltype.Explicit)
		if err != nil {
			t.Fatal(err)
		}
		converted := coercionExpression{Info: expr.Info{Usage: routine.Usage()}, Child: probe, Plan: plan, Mode: sqltype.Explicit}
		definition := *viewOf(d)
		definition.query = &BoundQuery{Root: &projectNode{querySchema: d.Columns, Child: &singletonNode{}, Expressions: []expr.Expr{converted}}}
		d.View = &definition
		e.mu.Lock()
		rows, err := e.materializedRefreshRowsLocked(scope, e.catalog.Domain.Ref(e.catalog.state.Epoch(), d.Address()), d)
		if fail {
			if !errors.Is(err, sentinel) || rows != nil {
				t.Fatal(rows, err)
			}
		} else if err != nil || len(rows) != 1 {
			t.Fatal(rows, err)
		}
		if !fail {
			id, err := rows[0][0].ProcedureOID()
			if err != nil || id != 1397 {
				t.Fatal("population did not evaluate retained routine coercion", id, err)
			}
		}
		restored := scope.input.Regtype.(capturedRegtypeInput)
		before := beforeInput.Regtype.(capturedRegtypeInput)
		if restored.auth.State != before.auth.State || !slices.Equal(restored.path, before.path) ||
			scope.input.TypeDiagnosticName == nil || scope.input.TypeDiagnosticName(sqltype.Regprocedure) != "caller-naming" ||
			scope.input.ExactSnapshot != beforeInput.ExactSnapshot {
			t.Fatal("population leaked input context")
		}
		if op.materializedPopulation || op.materializedOwner != (authorization.PrincipalRef{}) || op.RoleState() != beforeRole || scope.identity != beforeIdentity || !sqltype.SameDatum(scope.inquiry.currentSchemaResult, beforeSchema) {
			t.Fatal("population leaked owner context")
		}
		if err := scope.Finish(err); !fail && err != nil {
			t.Fatal(err)
		}
		done()
	}
}
