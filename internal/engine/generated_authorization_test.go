package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// The fixture publishes a normally cooked generation through the catalog stage,
// then grants only column privileges through the immutable internal ACL seam.
// No SQL parser, public execution API, or substitute authorization is involved.
func generatedAuthorizationFixture(t *testing.T) (*Engine, *Operation, *Operation, ObjectRef) {
	t.Helper()
	e, view, d := generatedUnitDescriptor(t)
	admin, adminDone := roleOperation(t, e, "postgres")
	t.Cleanup(adminDone)
	if _, err := e.executeRole(admin, &parser.CreateRole{Name: parser.Identifier{Text: "generated_actor"}, Login: true}, 0); err != nil {
		t.Fatal(err)
	}
	d.owner = admin.RoleState().CurrentUser
	generation := &parser.GenerationDefinition{Mode: parser.IdentityAlways, Kind: "stored", Expression: parser.OperatorExpr{
		Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "+"}}},
		Args: []parser.Expr{
			parser.ColumnExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "a"}}}},
			parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 1}},
		},
	}}
	e.mu.Lock()
	auth, err := e.authorizationLocked(admin)
	if err == nil {
		err = e.bindCreateGenerations(admin, auth, d, []createColumnDeclaration{{}, {generation: generation}}, sqltype.InputEnvironment{})
	}
	if err == nil {
		err = e.assignDefinitionOIDsLocked(d)
	}
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	stage := e.BeginCatalogStage(view)
	if err := stage.Create(d); err != nil {
		t.Fatal(err)
	}
	publication, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.PublishCatalog(publication, PublicationToken{Epoch: view.Epoch()}); err != nil {
		t.Fatal(err)
	}
	ref := ObjectRef{owner: e.owner, Epoch: e.CatalogRead().Epoch(), Address: d.address}
	actor, actorDone := roleOperation(t, e, "generated_actor")
	t.Cleanup(actorDone)
	generatedAuthorizationGrant(t, e, admin, actor, ref, 1, InsertPrivilege|UpdatePrivilege)
	generatedAuthorizationGrant(t, e, admin, actor, ref, 2, SelectPrivilege)
	return e, admin, actor, ref
}

func generatedAuthorizationGrant(t *testing.T, e *Engine, admin, actor *Operation, ref ObjectRef, attribute int32, privileges Privileges) {
	t.Helper()
	ref.Address.SubID = attribute
	acl := ACLValue{Explicit: true, Entries: []ACLEntry{{
		Grantor:    admin.RoleState().CurrentUser,
		Grantee:    ACLGrantee{Role: actor.RoleState().CurrentUser},
		Privileges: privileges,
	}}}
	if err := e.SetObjectACL(context.Background(), ref, acl); err != nil {
		t.Fatal(err)
	}
}

func TestGeneratedPartialColumnAuthorization(t *testing.T) {
	name := parser.ObjectName{Parts: []parser.Identifier{{Text: "g"}}}
	column := func(label string) parser.Expr {
		return parser.ColumnExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: label}}}}
	}
	literal := parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 8}}
	selectColumn := func(label string) parser.Statement {
		return &parser.Select{From: &parser.TableReference{Name: name}, Targets: []parser.Expr{column(label)}}
	}
	update := func(label string, rhs parser.Expr, where parser.Expr) parser.Statement {
		return &parser.Update{Name: name, Assignments: []parser.UpdateAssignment{{Target: parser.Identifier{Text: label}, Value: rhs}}, Where: where}
	}
	readPredicate := parser.OperatorExpr{
		Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "="}}},
		Args: []parser.Expr{column("a"), literal},
	}
	for _, tc := range []struct {
		name       string
		stmt       parser.Statement
		targets    []int
		reads      []int
		sourceUses [][]int
		denied     bool
		grantOn    int32
		grant      Privileges
	}{
		{name: "select_stored_b_without_select_a", stmt: selectColumn("b"), sourceUses: [][]int{{1}}},
		{name: "select_input_a_requires_select_a", stmt: selectColumn("a"), sourceUses: [][]int{{0}}, denied: true, grantOn: 1, grant: InsertPrivilege | UpdatePrivilege | SelectPrivilege},
		{name: "insert_literal_a_without_hidden_select", stmt: &parser.Insert{Name: name, Targets: []parser.InsertTarget{{Name: "a"}}, Rows: [][]parser.Expr{{literal}}}, targets: []int{0}},
		{name: "update_literal_a_without_hidden_select", stmt: update("a", literal, nil), targets: []int{0}},
		{name: "update_rhs_a_requires_select_a", stmt: update("a", column("a"), nil), targets: []int{0}, reads: []int{0}, denied: true, grantOn: 1, grant: InsertPrivilege | UpdatePrivilege | SelectPrivilege},
		{name: "update_where_a_requires_select_a", stmt: update("a", literal, readPredicate), targets: []int{0}, reads: []int{0}, denied: true, grantOn: 1, grant: InsertPrivilege | UpdatePrivilege | SelectPrivilege},
		{name: "generated_default_requires_update_b", stmt: update("b", parser.DefaultExpr{}, nil), targets: []int{1}, denied: true, grantOn: 2, grant: SelectPrivilege | UpdatePrivilege},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, admin, actor, ref := generatedAuthorizationFixture(t)
			// Bind through the normal caller path and consume exactly its manifests.
			// Private generation dependencies must not become caller SELECT demands.
			e.mu.Lock()
			auth, err := e.authorizationLocked(actor)
			var bound binding
			if err == nil {
				bound, err = e.describe(context.Background(), tc.stmt, auth, ref, sqltype.InputEnvironment{}, actor, nil)
			}
			e.mu.Unlock()
			if err != nil {
				t.Fatal("binding", err)
			}
			if !reflect.DeepEqual(bound.targets, tc.targets) || !reflect.DeepEqual(bound.usedColumns, tc.reads) {
				t.Fatalf("caller manifests targets=%v reads=%v; want targets=%v reads=%v", bound.targets, bound.usedColumns, tc.targets, tc.reads)
			}
			if len(bound.querySources) != len(tc.sourceUses) {
				t.Fatalf("source manifest count=%d; want %d", len(bound.querySources), len(tc.sourceUses))
			}
			for i, source := range bound.querySources {
				if !reflect.DeepEqual(source.uses, tc.sourceUses[i]) {
					t.Fatalf("source %d reads=%v; want %v", i, source.uses, tc.sourceUses[i])
				}
			}
			check := func(authority AuthorizationView, denied bool) {
				t.Helper()
				err := authority.checkBinding(bound, tc.stmt)
				if !denied {
					if err != nil {
						t.Fatal("permitted binding denied", err)
					}
					return
				}
				var diagnostic *sqlstate.Error
				want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for table g"}
				if !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) {
					t.Fatalf("denial=%#v (%v); want %#v", diagnostic, err, want)
				}
			}
			check(auth, tc.denied)
			ownerAuth, err := admin.Authorization()
			if err != nil {
				t.Fatal(err)
			}
			check(ownerAuth, false)
			if tc.denied {
				before := auth.catalog.state
				generatedAuthorizationGrant(t, e, admin, actor, ref, tc.grantOn, tc.grant)
				granted, err := actor.Authorization()
				if err != nil {
					t.Fatal(err)
				}
				if granted.catalog.state == before {
					t.Fatal("grant did not publish an immutable catalog replacement")
				}
				// The same retained binding is now allowed by current authority;
				// the old immutable authority retains its independently expected denial.
				check(granted, false)
				check(auth, true)
			}
		})
	}
}
