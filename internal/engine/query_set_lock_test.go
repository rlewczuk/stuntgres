package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestQuerySetStoredInheritedMarks(t *testing.T) {
	left, right := &plan.SourceNode{Occurrence: 0}, &plan.SourceNode{Occurrence: 1}
	set := &plan.SetNode{Left: &plan.ProjectNode{Child: left}, Right: &plan.ProjectNode{Child: right}}
	wrapped := &plan.ProjectNode{Child: &plan.LimitNode{Child: &plan.SortNode{Child: &plan.EvaluationNode{Child: set}}}}
	local := plan.RowMark{Mode: lock.ForKeyShare, Wait: parser.RowLockNowait, DiagnosticMode: lock.ForShare, Order: 2}
	view := stored.NewView(stored.ViewParts{Query: &plan.Query{Root: wrapped}, Sources: []viewLeaf{{}, {Mark: local}}})
	outer := plan.RowMark{Mode: lock.ForUpdate, Wait: parser.RowLockSkip, DiagnosticMode: lock.ForNoKeyUpdate, Order: 1}
	for i, want := range []plan.RowMark{{}, local} {
		if got := viewLeafRowMark(view, i, outer); got != want {
			t.Fatalf("set source %d inherited mark: got %+v want %+v", i, got, want)
		}
		if !exec.QueryHasPhysicalInput([]plan.Node{left, right}[i]) {
			t.Fatal("fixture source lost")
		}
	}
	// General input/dependency traversal must retain both children; only the
	// mark-specific traversal has a boundary. Admission still sees all sources.
	l, r := plan.QueryInputs(set)
	if l != set.Left || r != set.Right || !exec.QueryHasPhysicalInput(wrapped) {
		t.Fatal("set barrier pruned general source traversal")
	}
	ordinary := view.With(func(p *stored.ViewParts) {
		p.Query = &plan.Query{Root: &plan.ProjectNode{Child: &plan.JoinNode{Left: left, Right: right}}}
	})
	for i, want := range []plan.RowMark{outer, exec.MergeQueryRowMark(outer, local)} {
		if got := viewLeafRowMark(ordinary, i, outer); got != want {
			t.Fatalf("ordinary source %d inherited mark: got %+v want %+v", i, got, want)
		}
	}
	// Encountered sets also stop inherited marks inside an ordinary mixed tree,
	// without clearing marks on the independent physical contributor.
	mixed := view.With(func(p *stored.ViewParts) {
		p.Sources = []viewLeaf{{}, {}, {}}
		p.Query = &plan.Query{Root: &plan.ProjectNode{Child: &plan.JoinNode{Left: set, Right: &plan.SourceNode{Occurrence: 2}}}}
	})
	for i, want := range []plan.RowMark{{}, {}, outer} {
		if got := viewLeafRowMark(mixed, i, outer); got != want {
			t.Fatalf("mixed source %d got %+v want %+v", i, got, want)
		}
	}
}

// Reuse the existing bound-table fixture, then construct immutable typed view
// recipes and actual private ACLs. Public SQL witnesses live in reference only.
func querySetLockAuthorityFixture(t *testing.T) (*Engine, authorization.PrincipalRef, authorization.PrincipalRef, ids.ObjectRef, ids.ObjectRef, map[string]ids.ObjectRef) {
	t.Helper()
	e, query, source := queryCoreFixture(t, "SELECT v FROM t")
	base := source.Ref
	owner := e.roles.domain.Principal(1000000)
	reader := e.roles.domain.Principal(1000001)
	roles := cloneRoleFixture(e.roles.published)
	for _, role := range []authorization.RoleDescriptor{{Ref: owner, Name: "set_owner"}, {Ref: reader, Name: "set_reader"}} {
		putFixtureRole(roles, role)
	}
	e.roles.published = roles
	e.catalog.state = catalog.CloneState(e.catalog.state)
	original := e.catalog.state.MustRelation(base.Address)
	right := base
	right.Address.ObjectID = 1000000
	copy := *original
	copy.ObjectAddress, copy.ObjectName = right.Address, "t_right"
	e.catalog.state.SetDescriptor(right.Address, &copy)
	refs := make(map[string]ids.ObjectRef)
	next := ids.OID(1000001)
	add := func(name string, invoker bool, root *plan.ProjectNode, leaves []viewLeaf) ids.ObjectRef {
		ref := e.catalog.Domain.Ref(e.catalog.state.Epoch(), ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: next})
		next++
		d := *original
		d.ObjectAddress, d.ObjectName, d.Owner, d.RelationKind = ref.Address, name, owner, catalog.ViewRelation
		d.Columns, d.Attributes, d.Security.ColumnACL = query.Root.Schema(), []int32{1}, nil
		d.View = stored.NewView(stored.ViewParts{Query: &plan.Query{Root: root}, Sources: leaves, Invoker: invoker})
		e.catalog.state.SetDescriptor(ref.Address, &d)
		refs[name] = ref
		return ref
	}
	arm := func(i int) plan.Node { return &plan.SourceNode{Occurrence: i} }
	setRoot := func() *plan.ProjectNode {
		return &plan.ProjectNode{Syntax: &parser.Select{Set: &parser.SetQuery{}}, Child: &plan.SetNode{Left: &plan.ProjectNode{Child: arm(0)}, Right: &plan.ProjectNode{Child: arm(1)}}}
	}
	leaves := []viewLeaf{{Ref: base, Reads: []int32{2}}, {Ref: right, Reads: []int32{2}}}
	add("owned", false, setRoot(), leaves)
	add("invoker", true, setRoot(), leaves)
	for _, name := range []string{"owned", "invoker"} {
		add(name+"_nested", false, &plan.ProjectNode{Syntax: &parser.Select{}, Child: arm(0)}, []viewLeaf{{Ref: refs[name], Reads: []int32{1}}})
	}
	add("ordinary", true, &plan.ProjectNode{Syntax: &parser.Select{}, Child: arm(0)}, leaves[:1])
	localMark := plan.RowMark{Mode: lock.ForUpdate, DiagnosticMode: lock.ForUpdate, Order: 1}
	local := add("local", true, &plan.ProjectNode{Syntax: &parser.Select{}, Child: &plan.RowLockNode{Child: arm(0), FirstMode: lock.ForUpdate, Marks: []plan.RowMark{localMark}}}, []viewLeaf{{Ref: base, Reads: []int32{2}, Mark: localMark}})
	add("local_set", true, setRoot(), []viewLeaf{{Ref: local, Reads: []int32{1}}, {Ref: right, Reads: []int32{2}}})
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
		if err := e.SetObjectACL(context.Background(), ref, authorization.ACLValue{Explicit: true, Entries: entries}); err != nil {
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
		auth := catalogAuthorization(e, e.CatalogRead())
		auth.State.CurrentUser = reader
		d := e.catalog.state.MustRelation(refs[name].Address)
		err := auth.checkViewRowMarks(d, inherited, reader)
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
		if err := e.SetObjectACL(context.Background(), tc.ref, authorization.ACLValue{Explicit: true}); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"invoker", "invoker_nested", "invoker_zero"} {
			check(name, plan.RowMark{Mode: lock.ForUpdate}, tc.name)
		}
		// Restore this arm before checking the other arm's independent denial.
		if err := e.SetObjectACL(context.Background(), tc.ref, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), Grantee: authorization.ACLGrantee{Role: reader}, Privileges: authorization.SelectPrivilege}}}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestQuerySetStoredReferenceKinds(t *testing.T) {
	e, _, _, base, _, refs := querySetLockAuthorityFixture(t)
	e.catalog.state = catalog.CloneState(e.catalog.state)
	next := ids.OID(1000100)
	add := func(name string, definition *viewDefinition, kind catalog.RelationKind) ids.ObjectRef {
		ref := e.catalog.Domain.Ref(e.catalog.state.Epoch(), ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: next})
		next++
		d := *e.catalog.state.MustRelation(base.Address)
		d.ObjectAddress, d.ObjectName, d.View, d.RelationKind = ref.Address, name, stored.ViewPayload(definition), kind
		e.catalog.state.SetDescriptor(ref.Address, &d)
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
	set := add("kind_set", stored.NewView(stored.ViewParts{Query: &plan.Query{Root: setRoot()}, Sources: []viewLeaf{{Ref: sequence}, {Ref: catalogKindRef}}}), catalog.ViewRelation)
	nested := add("kind_nested", stored.NewView(stored.ViewParts{Query: &plan.Query{Root: plainRoot()}, Sources: []viewLeaf{{Ref: set}}}), catalog.ViewRelation)
	ordinary := add("kind_ordinary", stored.NewView(stored.ViewParts{Query: &plan.Query{Root: plainRoot()}, Sources: []viewLeaf{{Ref: sequence}}}), catalog.ViewRelation)
	localMark := plan.RowMark{Mode: lock.ForUpdate}
	localRoot := plainRoot()
	localRoot.Child = &plan.RowLockNode{Child: &plan.JoinNode{Left: &plan.SourceNode{Occurrence: 0}, Right: &plan.SourceNode{Occurrence: 1}}, FirstMode: lock.ForUpdate}
	// The sequence is unselected: only the local reference-kind context, not a
	// physical source mark, must reject this sibling beneath a nested set.
	local := add("kind_local", stored.NewView(stored.ViewParts{Query: &plan.Query{Root: localRoot}, Sources: []viewLeaf{{Ref: base, Mark: localMark}, {Ref: sequence}}}), catalog.ViewRelation)
	localSet := add("kind_local_set", stored.NewView(stored.ViewParts{Query: &plan.Query{Root: setRoot()}, Sources: []viewLeaf{{Ref: local}, {Ref: catalogKindRef}}}), catalog.ViewRelation)
	localCatalog := add("kind_local_catalog", stored.NewView(stored.ViewParts{Query: &plan.Query{Root: plainRoot()}, Sources: []viewLeaf{{Ref: catalogKindRef, Mark: localMark}}}), catalog.ViewRelation)
	localCatalogSet := add("kind_local_catalog_set", stored.NewView(stored.ViewParts{Query: &plan.Query{Root: setRoot()}, Sources: []viewLeaf{{Ref: localCatalog}, {Ref: base}}}), catalog.ViewRelation)
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
		d := e.catalog.state.MustRelation(tc.ref.Address)
		scope := &StatementScope{owner: &session.TransactionOwner{}}
		leaf := exec.Leaf{Ref: tc.ref, Relation: d, Mark: tc.mark, Query: &parser.Select{Locking: []parser.RowLockClause{{}}}}
		e.mu.Lock()
		err := e.initializeQueryLocksLocked(scope, nil, []exec.Leaf{leaf})
		e.mu.Unlock()
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
	d := *e.catalog.state.MustRelation(set.Address)
	sources := append([]viewLeaf(nil), stored.ViewOf(&d).Sources()...)
	sources[1].Ref.Epoch++
	d.View = stored.ViewOf(&d).With(func(p *stored.ViewParts) { p.Sources = sources })
	e.catalog.state.SetDescriptor(set.Address, &d)
	scope := &StatementScope{owner: &session.TransactionOwner{}}
	e.mu.Lock()
	err := e.initializeQueryLocksLocked(scope, nil, []exec.Leaf{{Ref: set, Relation: &d, Mark: localMark}})
	e.mu.Unlock()
	if !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("set boundary skipped stale arm admission", err)
	}
	// First-mode discovery still finds locally stored marks beneath a set when
	// there is no inherited outer diagnostic, without granting marks to arms.
	e.mu.Lock()
	mode, err := e.firstQueryRowModeLocked(&StatementScope{owner: &session.TransactionOwner{}}, nil, []exec.Leaf{{Ref: refs["local_set"], Relation: e.catalog.state.MustRelation(refs["local_set"].Address)}})
	e.mu.Unlock()
	if err != nil || mode != lock.ForUpdate {
		t.Fatal("local first diagnostic lost", mode, err)
	}
}

func TestQuerySetStoredRelationModes(t *testing.T) {
	e, _, _, base, right, refs := querySetLockAuthorityFixture(t)
	op, done, err := testOperation(e, context.Background())
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
		if err := scope.gate(&parser.Select{}); err != nil {
			t.Fatal(err)
		}
		ref := refs[tc.name]
		scope.lockedRef = ref
		scope.querySources = []exec.Leaf{{Ref: ref, Relation: e.catalog.state.MustRelation(ref.Address), Mark: plan.RowMark{Mode: lock.ForUpdate}}}
		err = scope.acquireViewSources()
		if err == nil {
			for _, want := range []struct {
				ref  ids.ObjectRef
				mode lock.Mode
			}{{base, tc.baseMode}, {right, tc.rightMode}} {
				lockState := e.locks.Inspect()
				var got lock.Mode
				if entry := lockState.Resources[lock.Resource{ObjectRef: want.ref}]; entry != nil {
					got = entry.Holders[scope.ticket].Mode
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
