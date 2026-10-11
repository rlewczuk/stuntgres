package engine

import (
	"errors"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func refreshUnitPrincipals(e *Engine) (PrincipalRef, PrincipalRef) {
	owner, caller := PrincipalRef{owner: e, id: 100001}, PrincipalRef{owner: e, id: 100002}
	for _, role := range []RoleDescriptor{{Ref: owner, Name: "refresh_unit_owner", Login: true, Inherit: true, ConnectionLimit: -1}, {Ref: caller, Name: "refresh_unit_caller", Login: true, Inherit: true, ConnectionLimit: -1}} {
		e.roles.roles[role.Ref] = role
		e.roles.names[role.Name] = role.Ref
	}
	return owner, caller
}

func TestMaterializedRefreshAuthorization(t *testing.T) {
	e := New()
	d := refreshUnitRelation(t, e)
	owner, caller := refreshUnitPrincipals(e)
	d.owner = owner
	ref := ObjectRef{owner: e.owner, Epoch: e.state.epoch, Address: d.address}
	auth := AuthorizationView{engine: e, catalog: e.CatalogRead(), roles: e.Roles(), state: RoleState{CurrentUser: caller, SessionUser: caller, Authenticated: caller}}
	for _, privilege := range []Privileges{SelectPrivilege, MaintainPrivilege} {
		d.acl = ACLValue{Explicit: true, Entries: []ACLEntry{grantUnitEntry(owner, caller, privilege, 0)}}
		err := materializedRefreshAccess(auth, ref)
		if privilege == MaintainPrivilege {
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
	for _, kind := range []relationKind{viewRelation, sequenceRelation, indexRelation} {
		d.relationKind = kind
		var diagnostic *sqlstate.Error
		if err := materializedRefreshAccess(auth, ref); !errors.As(err, &diagnostic) || diagnostic.Code != "42809" {
			t.Fatal(kind, err)
		}
	}
	d.relationKind = tableRelation
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
		d.owner = owner
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
		}
		e.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		beforeIdentity, beforeRole := scope.identity, op.RoleState()
		name, _ := e.CatalogRead().LookupType(sqltype.Name)
		scope.currentSchemaResult = sqltype.String(name, "sentinel")
		beforeSchema := scope.currentSchemaResult
		sentinel := errors.New("producer failed")
		usage := d.columns[0].Usage
		probe := queryProbe(usage, func(c *EvalContext) (sqltype.Value, error) {
			e.mu.Lock()
			auth, err := e.authorizationLocked(op)
			e.mu.Unlock()
			if err != nil {
				return sqltype.Value{}, err
			}
			if auth.state.CurrentUser != owner || auth.state.SessionUser != caller || !auth.materializedPopulation {
				t.Fatal("producer did not run as owner", auth.state)
			}
			current, _ := scope.identity.current.Text()
			session, _ := scope.identity.session.Text()
			if current != "refresh_unit_owner" || session != "refresh_unit_caller" {
				t.Fatal(current, session)
			}
			value, err := (identityExpression{expressionInfo: expressionInfo{usage: name.Usage()}, kind: identityCurrentSchema}).Eval(c)
			if err != nil {
				return sqltype.Value{}, err
			}
			schema, _ := value.Text()
			if schema != "pg_catalog" {
				t.Fatal(schema)
			}
			if fail {
				return sqltype.Value{}, sentinel
			}
			return sqltype.Integer(usage.Type, 7)
		})
		definition := *d.view
		definition.query = &BoundQuery{Root: &projectNode{querySchema: d.columns, child: &singletonNode{}, expressions: []BoundExpr{probe}}}
		d.view = &definition
		e.mu.Lock()
		rows, err := e.materializedRefreshRowsLocked(scope, ObjectRef{owner: e.owner, Epoch: e.state.epoch, Address: d.address}, d)
		if fail {
			if !errors.Is(err, sentinel) || rows != nil {
				t.Fatal(rows, err)
			}
		} else if err != nil || len(rows) != 1 {
			t.Fatal(rows, err)
		}
		if op.materializedPopulation || op.materializedOwner != (PrincipalRef{}) || op.RoleState() != beforeRole || scope.identity != beforeIdentity || !sqltype.SameDatum(scope.currentSchemaResult, beforeSchema) {
			t.Fatal("population leaked owner context")
		}
		if err := scope.Finish(err); !fail && err != nil {
			t.Fatal(err)
		}
		done()
	}
}
