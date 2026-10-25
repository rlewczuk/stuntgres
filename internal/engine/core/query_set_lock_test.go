package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Reuse the existing bound-table fixture, then construct immutable typed view
// recipes and actual private ACLs. Public SQL witnesses live in reference only.
func querySetLockAuthorityFixture(t *testing.T) (*core.Engine, authorization.PrincipalRef, authorization.PrincipalRef, ids.ObjectRef, ids.ObjectRef, map[string]ids.ObjectRef) {
	t.Helper()
	e, query, source := queryCoreFixture(t, "SELECT v FROM t")
	base := source.Ref
	owner := e.RoleStore.Domain.Principal(1000000)
	reader := e.RoleStore.Domain.Principal(1000001)
	roles := cloneRoleFixture(e.RoleStore.Published)
	for _, role := range []authorization.RoleDescriptor{{Ref: owner, Name: "set_owner"}, {Ref: reader, Name: "set_reader"}} {
		putFixtureRole(roles, role)
	}
	e.RoleStore.PublishLocked(roles)
	e.Catalog.PublishLocked(catalog.CloneState(e.Catalog.State))
	original := e.Catalog.State.MustRelation(base.Address)
	right := base
	right.Address.ObjectID = 1000000
	copy := *original
	copy.ObjectAddress, copy.ObjectName = right.Address, "t_right"
	e.Catalog.State.SetDescriptor(right.Address, &copy)
	refs := make(map[string]ids.ObjectRef)
	next := ids.OID(1000001)
	add := func(name string, invoker bool, root *plan.ProjectNode, leaves []stored.ViewLeaf) ids.ObjectRef {
		ref := e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: next})
		next++
		d := *original
		d.ObjectAddress, d.ObjectName, d.Owner, d.RelationKind = ref.Address, name, owner, catalog.ViewRelation
		d.Columns, d.Attributes, d.Security.ColumnACL = query.Root.Schema(), []int32{1}, nil
		d.View = stored.NewView(stored.ViewParts{Query: &plan.Query{Root: root}, Sources: leaves, Invoker: invoker})
		e.Catalog.State.SetDescriptor(ref.Address, &d)
		refs[name] = ref
		return ref
	}
	arm := func(i int) plan.Node { return &plan.SourceNode{Occurrence: i} }
	setRoot := func() *plan.ProjectNode {
		return &plan.ProjectNode{Syntax: &parser.Select{Set: &parser.SetQuery{}}, Child: &plan.SetNode{Left: &plan.ProjectNode{Child: arm(0)}, Right: &plan.ProjectNode{Child: arm(1)}}}
	}
	leaves := []stored.ViewLeaf{{Ref: base, Reads: []int32{2}}, {Ref: right, Reads: []int32{2}}}
	add("owned", false, setRoot(), leaves)
	add("invoker", true, setRoot(), leaves)
	for _, name := range []string{"owned", "invoker"} {
		add(name+"_nested", false, &plan.ProjectNode{Syntax: &parser.Select{}, Child: arm(0)}, []stored.ViewLeaf{{Ref: refs[name], Reads: []int32{1}}})
	}
	add("ordinary", true, &plan.ProjectNode{Syntax: &parser.Select{}, Child: arm(0)}, leaves[:1])
	localMark := plan.RowMark{Mode: lock.ForUpdate, DiagnosticMode: lock.ForUpdate, Order: 1}
	local := add("local", true, &plan.ProjectNode{Syntax: &parser.Select{}, Child: &plan.RowLockNode{Child: arm(0), FirstMode: lock.ForUpdate, Marks: []plan.RowMark{localMark}}}, []stored.ViewLeaf{{Ref: base, Reads: []int32{2}, Mark: localMark}})
	add("local_set", true, setRoot(), []stored.ViewLeaf{{Ref: local, Reads: []int32{1}}, {Ref: right, Reads: []int32{2}}})
	zero, err := sqltype.Integer(query.Root.Schema()[0].Usage.Type, 0)
	if err != nil {
		t.Fatal(err)
	}
	limited := setRoot()
	limited.Child = &plan.LimitNode{Child: limited.Child, Count: plan.LiteralExpr{Info: expr.InfoOf(query.Root.(*plan.ProjectNode).Expressions[0]), Value: zero}}
	add("invoker_zero", true, limited, leaves)
	grant := func(ref ids.ObjectRef, mask authorization.Privileges) {
		t.Helper()
		entries := []authorization.ACLEntry{
			{Grantor: original.Owner, Grantee: authorization.ACLGrantee{Role: owner}, Privileges: mask},
			{Grantor: original.Owner, Grantee: authorization.ACLGrantee{Role: reader}, Privileges: mask},
		}
		if err := enginetest.SetObjectACL(e, context.Background(), ref, authorization.ACLValue{Explicit: true, Entries: entries}); err != nil {
			t.Fatal(err)
		}
	}
	grant(base, authorization.SelectPrivilege)
	grant(right, authorization.SelectPrivilege)
	for _, ref := range refs {
		grant(ref, authorization.SelectPrivilege|authorization.UpdatePrivilege)
	}
	return e, owner, reader, base, right, refs
}

func TestQuerySetStoredLockAuthority(t *testing.T) {
	e, _, reader, base, right, refs := querySetLockAuthorityFixture(t)
	check := func(name string, inherited plan.RowMark, denied string) {
		t.Helper()
		auth := enginetest.CatalogAuthorization(e, e.CatalogRead())
		auth.State.CurrentUser = reader
		d := e.Catalog.State.MustRelation(refs[name].Address)
		err := view.CheckRowMarks(auth.View, auth, d, inherited, reader)
		if denied == "" {
			if err != nil {
				t.Fatal(name, err)
			}
			return
		}
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42501" || diagnostic.Message != "permission denied for table "+denied {
			t.Fatal(name, err)
		}
	}
	for _, mode := range []lock.Mode{lock.ForKeyShare, lock.ForShare, lock.ForNoKeyUpdate, lock.ForUpdate} {
		outer := plan.RowMark{Mode: mode}
		for _, name := range []string{"owned", "invoker", "owned_nested", "invoker_nested", "invoker_zero"} {
			check(name, outer, "")
		}
		check("ordinary", outer, "t")
		check("local_set", outer, "t")
	}
	// The local stored mark applies even with no inherited outer mark.
	check("local_set", plan.RowMark{}, "t")
	// Revoke one arm independently: mark suppression must not prune either arm's
	// ordinary used-column SELECT checks, including LIMIT0 and nested wrappers.
	for _, tc := range []struct {
		ref  ids.ObjectRef
		name string
	}{{right, "t_right"}, {base, "t"}} {
		if err := enginetest.SetObjectACL(e, context.Background(), tc.ref, authorization.ACLValue{Explicit: true}); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"invoker", "invoker_nested", "invoker_zero"} {
			check(name, plan.RowMark{Mode: lock.ForUpdate}, tc.name)
		}
		// Restore this arm before checking the other arm's independent denial.
		if err := enginetest.SetObjectACL(e, context.Background(), tc.ref, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), Grantee: authorization.ACLGrantee{Role: reader}, Privileges: authorization.SelectPrivilege}}}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestQuerySetStoredReferenceKinds(t *testing.T) {
	e, _, _, base, _, refs := querySetLockAuthorityFixture(t)
	e.Catalog.PublishLocked(catalog.CloneState(e.Catalog.State))
	next := ids.OID(1000100)
	add := func(name string, definition *stored.View, kind catalog.RelationKind) ids.ObjectRef {
		ref := e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: next})
		next++
		d := *e.Catalog.State.MustRelation(base.Address)
		d.ObjectAddress, d.ObjectName, d.View, d.RelationKind = ref.Address, name, stored.ViewPayload(definition), kind
		e.Catalog.State.SetDescriptor(ref.Address, &d)
		return ref
	}
	sequence := add("kind_sequence", nil, catalog.SequenceRelation)
	catalogKindRef := add("kind_catalog", nil, catalog.CatalogRelation)
	plainRoot := func() *plan.ProjectNode {
		return &plan.ProjectNode{Syntax: &parser.Select{}, Child: &plan.SourceNode{Occurrence: 0}}
	}
	setRoot := func() *plan.ProjectNode {
		return &plan.ProjectNode{Syntax: &parser.Select{Set: &parser.SetQuery{}}, Child: &plan.LimitNode{Child: &plan.SortNode{Child: &plan.EvaluationNode{Child: &plan.FilterNode{Child: &plan.SetNode{Left: plainRoot(), Right: &plan.ProjectNode{Child: &plan.SourceNode{Occurrence: 1}}}}}}}}
	}
	set := add("kind_set", stored.NewView(stored.ViewParts{Query: &plan.Query{Root: setRoot()}, Sources: []stored.ViewLeaf{{Ref: sequence}, {Ref: catalogKindRef}}}), catalog.ViewRelation)
	nested := add("kind_nested", stored.NewView(stored.ViewParts{Query: &plan.Query{Root: plainRoot()}, Sources: []stored.ViewLeaf{{Ref: set}}}), catalog.ViewRelation)
	ordinary := add("kind_ordinary", stored.NewView(stored.ViewParts{Query: &plan.Query{Root: plainRoot()}, Sources: []stored.ViewLeaf{{Ref: sequence}}}), catalog.ViewRelation)
	localMark := plan.RowMark{Mode: lock.ForUpdate}
	localRoot := plainRoot()
	localRoot.Child = &plan.RowLockNode{Child: &plan.JoinNode{Left: &plan.SourceNode{Occurrence: 0}, Right: &plan.SourceNode{Occurrence: 1}}, FirstMode: lock.ForUpdate}
	// The sequence is unselected: only the local reference-kind context, not a
	// physical source mark, must reject this sibling beneath a nested set.
	local := add("kind_local", stored.NewView(stored.ViewParts{Query: &plan.Query{Root: localRoot}, Sources: []stored.ViewLeaf{{Ref: base, Mark: localMark}, {Ref: sequence}}}), catalog.ViewRelation)
	localSet := add("kind_local_set", stored.NewView(stored.ViewParts{Query: &plan.Query{Root: setRoot()}, Sources: []stored.ViewLeaf{{Ref: local}, {Ref: catalogKindRef}}}), catalog.ViewRelation)
	localCatalog := add("kind_local_catalog", stored.NewView(stored.ViewParts{Query: &plan.Query{Root: plainRoot()}, Sources: []stored.ViewLeaf{{Ref: catalogKindRef, Mark: localMark}}}), catalog.ViewRelation)
	localCatalogSet := add("kind_local_catalog_set", stored.NewView(stored.ViewParts{Query: &plan.Query{Root: setRoot()}, Sources: []stored.ViewLeaf{{Ref: localCatalog}, {Ref: base}}}), catalog.ViewRelation)
	for _, tc := range []struct {
		ref           ids.ObjectRef
		mark          plan.RowMark
		code, message string
	}{
		{set, localMark, "", ""},
		{nested, localMark, "", ""},
		// Kind-only outer context must also stop, even when this view's mark is 0.
		{set, plan.RowMark{}, "", ""},
		{nested, plan.RowMark{}, "", ""},
		{ordinary, plan.RowMark{}, "42809", `cannot lock rows in sequence "kind_sequence"`},
		{localSet, plan.RowMark{}, "42809", `cannot lock rows in sequence "kind_sequence"`},
		{localCatalogSet, plan.RowMark{}, "0A000", "row locking is not supported for virtual catalog relations"},
	} {
		d := e.Catalog.State.MustRelation(tc.ref.Address)
		scope := &core.StatementScope{Owner: &session.TransactionOwner{}}
		leaf := exec.Leaf{Ref: tc.ref, Relation: d, Mark: tc.mark, Query: &parser.Select{Locking: []parser.RowLockClause{{}}}}
		e.Mu.Lock()
		err := e.InitializeQueryLocksLocked(scope, nil, []exec.Leaf{leaf})
		e.Mu.Unlock()
		if tc.code == "" {
			if err != nil {
				t.Fatal(d.Name(), err)
			}
		} else {
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code || diagnostic.Message != tc.message {
				t.Fatal(d.Name(), err)
			}
		}
	}
	// This predicate is about stored set output, not any encountered descendant.
	for _, root := range []plan.Node{
		&plan.ProjectNode{Child: &plan.JoinNode{Left: setRoot(), Right: &plan.SourceNode{}}},
		&plan.ProjectNode{Child: &plan.DerivedNode{Query: &plan.Query{Root: setRoot()}}},
		&plan.ProjectNode{Child: &plan.DistinctNode{Child: setRoot()}},
		&plan.ProjectNode{Child: &plan.AggregateNode{Child: setRoot()}},
	} {
		if analyze.QueryHasSetOutput(root) {
			t.Fatal("ordinary output acquired a set-entry kind exemption")
		}
	}
	// Every stored arm is still admitted, including one whose identity expired.
	d := *e.Catalog.State.MustRelation(set.Address)
	sources := append([]stored.ViewLeaf(nil), stored.ViewOf(&d).Sources()...)
	sources[1].Ref.Epoch++
	d.View = stored.ViewOf(&d).With(func(p *stored.ViewParts) { p.Sources = sources })
	e.Catalog.State.SetDescriptor(set.Address, &d)
	scope := &core.StatementScope{Owner: &session.TransactionOwner{}}
	e.Mu.Lock()
	err := e.InitializeQueryLocksLocked(scope, nil, []exec.Leaf{{Ref: set, Relation: &d, Mark: localMark}})
	e.Mu.Unlock()
	if !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("set boundary skipped stale arm admission", err)
	}
	// First-mode discovery still finds locally stored marks beneath a set when
	// there is no inherited outer diagnostic, without granting marks to arms.
	e.Mu.Lock()
	mode, err := view.FirstQueryRowMode(e.PrivateCatalogLocked(nil), nil, []exec.Leaf{{Ref: refs["local_set"], Relation: e.Catalog.State.MustRelation(refs["local_set"].Address)}})
	e.Mu.Unlock()
	if err != nil || mode != lock.ForUpdate {
		t.Fatal("local first diagnostic lost", mode, err)
	}
}

func TestQuerySetStoredRelationModes(t *testing.T) {
	e, _, _, base, right, refs := querySetLockAuthorityFixture(t)
	op, done, err := enginetest.Operation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	for _, tc := range []struct {
		name                string
		baseMode, rightMode lock.Mode
	}{
		{"invoker", lock.AccessShare, lock.AccessShare},
		{"invoker_nested", lock.AccessShare, lock.AccessShare},
		{"ordinary", lock.RowShare, 0},
		{"local_set", lock.RowShare, lock.AccessShare},
	} {
		scope, err := e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		if err := scope.Gate(&parser.Select{}); err != nil {
			t.Fatal(err)
		}
		ref := refs[tc.name]
		scope.LockedRef = ref
		scope.QuerySources = []exec.Leaf{{Ref: ref, Relation: e.Catalog.State.MustRelation(ref.Address), Mark: plan.RowMark{Mode: lock.ForUpdate}}}
		_, err = scope.AcquireViewSources(false)
		if err == nil {
			for _, want := range []struct {
				ref  ids.ObjectRef
				mode lock.Mode
			}{{base, tc.baseMode}, {right, tc.rightMode}} {
				lockState := e.Locks.Inspect()
				var got lock.Mode
				if entry := lockState.Resources[lock.Resource{ObjectRef: want.ref}]; entry != nil {
					got = entry.Holders[scope.LockTicket].Mode
				}
				if got != want.mode {
					t.Errorf("%s arm %v got relation mode %v want %v", tc.name, want.ref.Address, got, want.mode)
				}
			}
		}
		if err := scope.Finish(err); err != nil {
			t.Fatal(tc.name, err)
		}
	}
}

func cloneRoleFixture(r *access.RoleRecord) *access.RoleRecord {
	return &access.RoleRecord{Root: r.Root, Revision: r.Revision + 1}
}

func putFixtureRole(r *access.RoleRecord, d authorization.RoleDescriptor) {
	root, err := r.Root.PutDescriptor(d)
	if err != nil {
		panic(err)
	}
	r.Root = root
}
