package engine

import (
	"context"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestSessionIdentityMembershipClosure(t *testing.T) {
	e := New()
	for _, q := range []string{"CREATE USER a", "CREATE ROLE b", "CREATE ROLE c", "CREATE ROLE d"} {
		run(t, e, q)
	}
	roles := e.Roles()
	a, _ := roles.Lookup("a")
	b, _ := roles.Lookup("b")
	c, _ := roles.Lookup("c")
	d, _ := roles.Lookup("d")
	admin, _ := roles.Lookup("postgres")
	edge := func(role, member PrincipalRef, set, inherit, adminOption bool) {
		t.Helper()
		if err := e.AddMembership(context.Background(), Membership{Role: role, Member: member, Grantor: admin.Ref, Set: set, Inherit: inherit, Admin: adminOption}); err != nil {
			t.Fatal(err)
		}
	}
	edge(b.Ref, a.Ref, true, false, false)
	edge(c.Ref, b.Ref, true, false, false)
	edge(a.Ref, c.Ref, true, false, false)
	edge(d.Ref, a.Ref, false, true, true)
	for _, tc := range []struct {
		from, to PrincipalRef
		want     bool
	}{{a.Ref, a.Ref, true}, {a.Ref, b.Ref, true}, {a.Ref, c.Ref, true}, {a.Ref, d.Ref, false}, {d.Ref, c.Ref, false}, {admin.Ref, d.Ref, true}} {
		got, err := e.Roles().canSetRole(tc.from, tc.to)
		if err != nil || got != tc.want {
			t.Fatal(tc, got, err)
		}
	}
	run(t, e, "DROP ROLE d")
	if _, err := e.Roles().canSetRole(a.Ref, d.Ref); err == nil {
		t.Fatal("stale role adopted")
	}
}

func TestSessionIdentityTypedStableBinding(t *testing.T) {
	e := New()
	op, cleanup, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	desc, err := e.Describe(op, statement(t, "SELECT current_catalog,current_user,system_user"))
	if err != nil {
		t.Fatal(err)
	}
	for i, id := range []sqltype.ID{sqltype.Name, sqltype.Name, sqltype.Text} {
		c := desc.Results[i]
		if c.Usage.Type.ID() != id || c.Usage.Typmod != -1 || c.OriginTable != 0 || c.OriginAttribute != 0 || c.NullabilityKnown {
			t.Fatal(c)
		}
	}
	e.mu.Lock()
	binder := newExpressionBinder(context.Background(), e, sqltype.InputEnvironment{}, op, nil)
	expr, err := binder.bind(parser.IdentityExpr{Name: "current_user", Pos: parser.Position{Character: 8}}, expressionPolicy{context: projectionContext})
	e.mu.Unlock()
	if err != nil || expr.Properties() != (ExprProperties{Volatility: Stable}) {
		t.Fatal(expr, err)
	}
}

func TestSessionIdentityRolePublicationAndSessionAuthority(t *testing.T) {
	e := New()
	for _, q := range []string{"CREATE USER a", "CREATE ROLE b", "CREATE ROLE c"} {
		run(t, e, q)
	}
	op, cleanup, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	execute := func(q string) {
		t.Helper()
		if _, err := e.Execute(op, statement(t, q), 0, nil); err != nil {
			t.Fatal(q, err)
		}
	}
	before := op.RoleState()
	revision := op.session.authorization.revision
	catalogRevision := e.Roles().state.revision
	execute("SET ROLE b")
	after := op.RoleState()
	b, _ := e.Roles().Lookup("b")
	if after.CurrentUser != b.Ref || after.SessionUser != before.SessionUser || after.Authenticated != before.Authenticated || after.ResetRole != before.ResetRole || op.session.authorization.revision != revision+1 || e.Roles().state.revision != catalogRevision {
		t.Fatal(before, after)
	}
	// A session superuser is still authoritative while CurrentUser is ordinary.
	execute("SET ROLE c")
	execute("SET SESSION AUTHORIZATION a")
	before = op.RoleState()
	revision = op.session.authorization.revision
	_, err = e.Execute(op, statement(t, "SET ROLE b"), 0, nil)
	state(t, err, "42501")
	if op.RoleState() != before || op.session.authorization.revision != revision {
		t.Fatal("denied mutation")
	}
	_, err = e.Execute(op, statement(t, "SET ROLE absent_identity_role"), 0, nil)
	state(t, err, "22023")
	if op.RoleState() != before || op.session.authorization.revision != revision {
		t.Fatal("missing mutation")
	}
	a, _ := e.Roles().Lookup("a")
	admin, _ := e.Roles().Lookup("postgres")
	if err := e.AddMembership(context.Background(), Membership{Member: a.Ref, Role: b.Ref, Grantor: admin.Ref, Set: true}); err != nil {
		t.Fatal(err)
	}
	execute("SET ROLE b")
	execute("RESET ROLE")
	if op.RoleState().CurrentUser != a.Ref {
		t.Fatal(op.RoleState())
	}
	execute("RESET SESSION AUTHORIZATION")
	if op.RoleState() != (RoleState{Authenticated: admin.Ref, SessionUser: admin.Ref, CurrentUser: admin.Ref}) {
		t.Fatal(op.RoleState())
	}
}

func TestSessionIdentityNamespaceKeywordIndependence(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE USER identity_namespace_reader", "CREATE SCHEMA identity_hidden", "CREATE SCHEMA identity_visible"} {
		roleExecute(t, e, op, q)
	}
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION identity_namespace_reader")
	for _, q := range []string{"SELECT identity_hidden.current_database()", "SELECT identity_hidden.current_user()"} {
		_, err := e.Describe(op, statement(t, q))
		state(t, err, "42501")
	}
	for _, q := range []string{"SELECT missing_identity_schema.current_database()", "SELECT missing_identity_schema.current_user()"} {
		_, err := e.Describe(op, statement(t, q))
		state(t, err, "3F000")
	}
	roleExecute(t, e, op, "SELECT current_user,session_user,current_catalog,system_user")
}

func TestSessionIdentityConfiguredResetRole(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE ROLE identity_reset", "CREATE ROLE identity_selected"} {
		roleExecute(t, e, op, q)
	}
	reset, _ := e.Roles().Lookup("identity_reset")
	op.session.mu.Lock()
	op.session.authorization.state.ResetRole = reset.Ref
	op.session.mu.Unlock()
	roleExecute(t, e, op, "SET ROLE identity_selected")
	roleExecute(t, e, op, "RESET ROLE")
	if op.RoleState().CurrentUser != reset.Ref || op.RoleState().ResetRole != reset.Ref {
		t.Fatal(op.RoleState())
	}
	roleExecute(t, e, op, "SET ROLE NONE")
	if op.RoleState().CurrentUser != op.RoleState().SessionUser || op.RoleState().ResetRole != reset.Ref {
		t.Fatal(op.RoleState())
	}
}

func TestSessionIdentityPhysicalNameAndTermination(t *testing.T) {
	ctx := context.Background()
	e := New()
	session, err := e.StartSession(ctx, SessionStart{ID: SessionID(testSessionID.Add(1)), DatabaseName: "unit identity"})
	if err != nil {
		t.Fatal(err)
	}
	op, err := session.Acquire(ctx)
	if err != nil {
		session.RequestEnd(CleanClose)
		t.Fatal(err)
	}
	defer func() {
		op.Done()
		session.RequestEnd(CleanClose)
		if err := session.WaitEnd(ctx); err != nil {
			t.Error(err)
		}
	}()
	result, err := e.Execute(op, statement(t, "SELECT current_catalog,current_database(),system_user"), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	rows := independentFinalRows(result.Rows)
	defer rows.Close()
	values := make([]sqltype.Value, 3)
	present, err := rows.Next(ctx, values)
	if !present || err != nil {
		t.Fatal(present, err)
	}
	for _, v := range values[:2] {
		name, err := v.Text()
		if err != nil || name != "unit identity" || v.Type().ID() != sqltype.Name {
			t.Fatal(v, err)
		}
	}
	if !values[2].IsNull() || values[2].Type().ID() != sqltype.Text {
		t.Fatal(values[2])
	}
	session.RequestEnd(Terminated)
	if _, err := e.Execute(op, statement(t, "SELECT current_user"), 0, nil); err == nil {
		t.Fatal("terminated identity operation admitted")
	}
	if _, err := e.Execute(op, statement(t, "RESET ROLE"), 0, nil); err == nil {
		t.Fatal("terminated role mutation admitted")
	}
}
