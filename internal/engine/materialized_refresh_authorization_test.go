package engine

import (
	"errors"
	"slices"
	"testing"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func refreshUnitPrincipals(e *core.Engine) (authorization.PrincipalRef, authorization.PrincipalRef) {
	owner, caller := e.RoleStore.Domain.Principal(100001), e.RoleStore.Domain.Principal(100002)
	for _, role := range []authorization.RoleDescriptor{{Ref: owner, Name: "refresh_unit_owner", Login: true, Inherit: true, ConnectionLimit: -1}, {Ref: caller, Name: "refresh_unit_caller", Login: true, Inherit: true, ConnectionLimit: -1}} {
		putFixtureRole(e.RoleStore.Published, role)

	}
	return owner, caller
}

func TestMaterializedRefreshAuthorization(t *testing.T) {
	e := New()
	d := refreshUnitRelation(t, e)
	owner, caller := refreshUnitPrincipals(e)
	d.Owner = owner
	ref := e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), d.Address())
	auth := e.AuthorityView(access.View{Owner: &e.Catalog.Owner, PairedCatalog: e.CatalogRead(), RoleRecord: e.RoleStore.Published, Catalog: e.CatalogRead(), Roles: e.Roles(), State: authorization.RoleState{CurrentUser: caller, SessionUser: caller, Authenticated: caller}})
	for _, privilege := range []authorization.Privileges{authorization.SelectPrivilege, authorization.MaintainPrivilege} {
		d.Security.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{objectACLUnitEntry(owner, caller, privilege, 0)}}
		err := view.MaterializedRefreshAccess(auth.View, auth, ref)
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
		if err := view.MaterializedRefreshAccess(auth.View, auth, ref); !errors.As(err, &diagnostic) || diagnostic.Code != "42809" {
			t.Fatal(kind, err)
		}
	}
	d.RelationKind = catalog.TableRelation
	var diagnostic *sqlstate.Error
	if err := view.MaterializedRefreshAccess(auth.View, auth, ref); !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" {
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
		if err := scope.Gate(stmt); err != nil {
			t.Fatal(err)
		}
		if err := core.ScopeSnapshotPhase(scope, stmt); err != nil {
			t.Fatal(err)
		}
		e.Mu.Lock()
		original, err := e.AuthorizationLocked(op)
		if err == nil {
			err = scope.CaptureIdentityLocked(original)
			if err == nil {
				err = e.CaptureElementInput(original, op, &scope.Input)
			}
		}
		e.Mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		beforeIdentity, beforeRole := scope.Identity, op.RoleState()
		name, _ := e.CatalogRead().LookupType(sqltype.Name)
		scope.Inquiry.CurrentSchemaResult = sqltype.String(name, "sentinel")
		beforeSchema := scope.Inquiry.CurrentSchemaResult
		scope.Input.TypeDiagnosticName = func(sqltype.ID) string { return "caller-naming" }
		scope.Input.ExactSnapshot = true
		beforeInput := scope.Input
		sentinel := errors.New("producer failed")
		routine, _ := e.CatalogRead().LookupType(sqltype.Regprocedure)
		text, _ := e.CatalogRead().LookupType(sqltype.Text)
		columns := append([]catalog.Column(nil), d.Columns...)
		columns[0].Usage = routine.Usage()
		d.Columns = columns
		probe := queryProbe(text.Usage(), func(c *exec.Context) (sqltype.Value, error) {
			e.Mu.Lock()
			auth, err := e.AuthorizationLocked(op)
			e.Mu.Unlock()
			if err != nil {
				return sqltype.Value{}, err
			}
			if auth.State.CurrentUser != owner || auth.State.SessionUser != caller || !core.AuthorizationMaterializedPopulation(&auth) {
				t.Fatal("producer did not run as owner", auth.State)
			}
			current, _ := scope.Identity.Current.Text()
			session, _ := scope.Identity.Session.Text()
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
			captured, ok := c.Input.Regtype.(regobj.TypeInput)
			env, _ := captured.Authority().Environment.(core.AuthorizationView)
			if !ok || captured.Authority().View.State.CurrentUser != owner || captured.Authority().View.State.SessionUser != caller || !core.AuthorizationMaterializedPopulation(&env) ||
				!slices.Equal(captured.Path(), []ids.ObjectAddress{{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}}) ||
				c.Input.Regclass == nil || c.Input.Regrole == nil || c.Input.Operator == nil || c.Input.Regproc == nil ||
				c.Input.TypeDiagnosticName != nil || !c.Input.ExactSnapshot {
				t.Fatal("population did not recapture coherent restricted input", c.Input)
			}
			if fail {
				return sqltype.Value{}, sentinel
			}
			return sqltype.String(text, "abs(integer)"), nil
		})
		coercionPlan, err := sqltype.PlanCoercion(text.Usage(), routine.Usage(), sqltype.Explicit)
		if err != nil {
			t.Fatal(err)
		}
		converted := plan.CoercionExpr{Info: expr.Info{Usage: routine.Usage()}, Child: probe, Plan: coercionPlan, Mode: sqltype.Explicit}
		d.View = stored.ViewOf(d).With(func(p *stored.ViewParts) {
			p.Query = &plan.Query{Root: &plan.ProjectNode{QuerySchema: d.Columns, Child: &plan.SingletonNode{}, Expressions: []expr.Expr{converted}}}
		})
		e.Mu.Lock()
		rows, err := materializedRefreshRowsLocked(e, scope, e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), d.Address()), d)
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
		restored := scope.Input.Regtype.(regobj.TypeInput)
		before := beforeInput.Regtype.(regobj.TypeInput)
		if restored.Authority().View.State != before.Authority().View.State || !slices.Equal(restored.Path(), before.Path()) ||
			scope.Input.TypeDiagnosticName == nil || scope.Input.TypeDiagnosticName(sqltype.Regprocedure) != "caller-naming" ||
			scope.Input.ExactSnapshot != beforeInput.ExactSnapshot {
			t.Fatal("population leaked input context")
		}
		if core.OperationReplayOrZero(op).MaterializedPopulation || core.OperationReplayOrZero(op).MaterializedOwner != (authorization.PrincipalRef{}) || op.RoleState() != beforeRole || scope.Identity != beforeIdentity || !sqltype.SameDatum(scope.Inquiry.CurrentSchemaResult, beforeSchema) {
			t.Fatal("population leaked owner context")
		}
		if err := scope.Finish(err); !fail && err != nil {
			t.Fatal(err)
		}
		done()
	}
}
