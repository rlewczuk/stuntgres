package core_test

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestQueryLockNullableNormalizedDiagnostics(t *testing.T) {
	for _, tc := range []struct{ q, mode string }{
		{"SELECT a.k FROM a LEFT JOIN b USING(k) FOR KEY SHARE OF b FOR UPDATE OF b", "FOR UPDATE"},
		{"SELECT a.k FROM a FULL JOIN b USING(k) FOR SHARE OF a FOR UPDATE OF b", "FOR SHARE"},
		{"SELECT a.k FROM a FULL JOIN b USING(k) FOR UPDATE OF b FOR SHARE OF a", "FOR UPDATE"},
		{"SELECT a.k FROM a FULL JOIN b USING(k) FOR KEY SHARE OF b,a FOR UPDATE OF b", "FOR UPDATE"},
	} {
		t.Run(tc.q, func(t *testing.T) {
			e, query, _ := queryJoinsFixture(t, tc.q)
			evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
			root, err := view.PreflightQuery(&evaluation, query.Root)
			if err != nil {
				t.Fatal(err)
			}
			waitsDiagnostic(t, view.ValidateQueryJoinExecution(&evaluation, root, e.CatalogRead(), 0), "0A000", tc.mode+" cannot be applied to the nullable side of an outer join")
		})
	}
}

func TestQueryLockSelectedACLAndViewPrincipals(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE USER mark_reader", "CREATE TABLE t(id INT,v INT)", "CREATE TABLE u(id INT,v INT)", "CREATE VIEW owned AS SELECT a.id FROM t a JOIN u b USING(id) FOR UPDATE OF a NOWAIT", "CREATE VIEW invoker WITH(security_invoker=true) AS SELECT a.id FROM t a JOIN u b USING(id) FOR UPDATE OF a NOWAIT"} {
		enginetest.RoleExecute(t, e, op, q)
	}
	enginetest.RoleExecute(t, e, op, "CREATE SEQUENCE s")
	reader, _ := e.Roles().Lookup("mark_reader")
	grantor := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	grant := func(ref ids.ObjectRef, rights authorization.Privileges) {
		t.Helper()
		acl := authorization.ACLValue{Explicit: true}
		if rights != 0 {
			acl.Entries = []authorization.ACLEntry{{Grantor: grantor, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: rights}}
		}
		if err := enginetest.SetObjectACL(e, context.Background(), ref, acl); err != nil {
			t.Fatal(err)
		}
	}
	table, other := enginetest.RoleTableRef(t, e, "t"), enginetest.RoleTableRef(t, e, "u")
	grant(table, authorization.SelectPrivilege)
	grant(other, authorization.SelectPrivilege)
	grant(enginetest.RoleTableRef(t, e, "s"), authorization.SelectPrivilege)
	column := table
	column.Address.SubID = 2
	grant(column, authorization.UpdatePrivilege)
	grant(enginetest.RoleTableRef(t, e, "owned"), authorization.SelectPrivilege)
	grant(enginetest.RoleTableRef(t, e, "invoker"), authorization.SelectPrivilege)
	check := func(q string) error {
		t.Helper()
		e.Mu.Lock()
		defer e.Mu.Unlock()
		auth, err := e.AuthorizationLocked(op)
		if err != nil {
			return err
		}
		auth.State.CurrentUser = reader.Ref
		binder := engine.NewExpressionBinder(op.Context(), e, sqltype.InputEnvironment{}, op, nil)
		binder.Query.Auth = &auth
		raw := enginetest.Statement(t, q).(*parser.Select)
		if _, err = binder.BindSelect(raw, false); err != nil {
			t.Fatal("executor ACL leaked into binding", q, err)
		}
		return analyze.CheckQuerySources(auth, binder.Query.Sources)
	}
	for _, q := range []string{"SELECT b.v FROM t a JOIN u b USING(id) FOR UPDATE OF a NOWAIT", "SELECT id FROM owned", "SELECT id FROM invoker"} {
		if err := check(q); err != nil {
			t.Fatal(q, err)
		}
	}
	// Reference-kind rejection is separate from physical-target UPDATE authority.
	if err := check("SELECT a.id FROM t a CROSS JOIN s FOR UPDATE OF a"); err != nil {
		t.Fatal("unselected sequence acquired invented UPDATE requirement", err)
	}
	waitsDiagnostic(t, check("SELECT a.id FROM t a JOIN u b USING(id) FOR UPDATE OF b"), "42501", "permission denied for table u")
	grant(column, 0)
	waitsDiagnostic(t, check("SELECT id FROM invoker"), "42501", "permission denied for table t")
	if err := check("SELECT id FROM owned"); err != nil {
		t.Fatal("owner view inherited invoker ACL", err)
	}
	grant(column, authorization.UpdatePrivilege)
	if err := check("SELECT id FROM invoker"); err != nil {
		t.Fatal("regrant retained stale executor ACL", err)
	}
}

func TestPGSequencesEffectiveRowMarks(t *testing.T) {
	e := engine.New()
	view := e.Catalog.State.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 12048})
	table := &catalog.Relation{RelationKind: catalog.TableRelation}
	requested := plan.RowMark{Mode: lock.ForUpdate, Wait: parser.RowLockNowait, DiagnosticMode: lock.ForUpdate, Order: 1}
	locks := &exec.LockContext{Leaves: []exec.QueryLockLeaf{
		{Leaf: exec.Leaf{Relation: view, Mark: requested}, Mark: exec.EffectiveQueryRowMark(view, requested)},
		{Leaf: exec.Leaf{Relation: table, Mark: requested}, Mark: exec.EffectiveQueryRowMark(table, requested)},
	}}
	// A transparent stored/derived expansion may contain both the SQL-only
	// catalog occurrence and a genuinely marked table. Preserve its identities.
	expanded := &exec.Source{Materialized: true, Occurrences: []int{0, 1}}
	inputs := &exec.Source{Sources: []*exec.Source{expanded}, Locks: locks}
	pending := exec.AppendPendingRowMarks(nil, []plan.RowMark{requested}, inputs)
	if len(pending) != 1 || !reflect.DeepEqual(pending[0].Occurrences, []int{1}) || pending[0].Mark != requested {
		t.Fatal("effective expansion changed ordinary physical mark", pending)
	}
	if !reflect.DeepEqual(expanded.Occurrences, []int{0, 1}) || locks.Leaves[0].Leaf.Mark != requested || locks.Leaves[0].Mark != (plan.RowMark{}) {
		t.Fatal("effective decision mutated SQL marks or source identities")
	}
	inputs.Sources[0] = &exec.Source{Occurrence: 0}
	if pending := exec.AppendPendingRowMarks(nil, []plan.RowMark{requested}, inputs); len(pending) != 0 {
		t.Fatal("SQL-only catalog mark reached physical arbitration", pending)
	}
}
