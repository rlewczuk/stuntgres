package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestQuerySetStoredInheritedMarks(t *testing.T) {
	left, right := &sourceNode{occurrence: 0}, &sourceNode{occurrence: 1}
	set := &setNode{left: &projectNode{child: left}, right: &projectNode{child: right}}
	wrapped := &projectNode{child: &limitNode{child: &sortNode{child: &evaluationNode{child: set}}}}
	local := queryRowMark{mode: lock.ForKeyShare, wait: parser.RowLockNowait, diagnosticMode: lock.ForShare, order: 2}
	view := &viewDefinition{query: &BoundQuery{Root: wrapped}, sources: []viewLeaf{{}, {mark: local}}}
	outer := queryRowMark{mode: lock.ForUpdate, wait: parser.RowLockSkip, diagnosticMode: lock.ForNoKeyUpdate, order: 1}
	for i, want := range []queryRowMark{{}, local} {
		if got := viewLeafRowMark(view, i, outer); got != want {
			t.Fatalf("set source %d inherited mark: got %+v want %+v", i, got, want)
		}
		if !queryHasPhysicalInput([]LogicalNode{left, right}[i]) {
			t.Fatal("fixture source lost")
		}
	}
	// General input/dependency traversal must retain both children; only the
	// mark-specific traversal has a boundary. Admission still sees all sources.
	l, r := queryInputs(set)
	if l != set.left || r != set.right || !queryHasPhysicalInput(wrapped) {
		t.Fatal("set barrier pruned general source traversal")
	}
	ordinary := *view
	ordinary.query = &BoundQuery{Root: &projectNode{child: &joinNode{left: left, right: right}}}
	for i, want := range []queryRowMark{outer, mergeQueryRowMark(outer, local)} {
		if got := viewLeafRowMark(&ordinary, i, outer); got != want {
			t.Fatalf("ordinary source %d inherited mark: got %+v want %+v", i, got, want)
		}
	}
	// Encountered sets also stop inherited marks inside an ordinary mixed tree,
	// without clearing marks on the independent physical contributor.
	mixed := *view
	mixed.sources = []viewLeaf{{}, {}, {}}
	mixed.query = &BoundQuery{Root: &projectNode{child: &joinNode{left: set, right: &sourceNode{occurrence: 2}}}}
	for i, want := range []queryRowMark{{}, {}, outer} {
		if got := viewLeafRowMark(&mixed, i, outer); got != want {
			t.Fatalf("mixed source %d got %+v want %+v", i, got, want)
		}
	}
}

// Reuse the existing bound-table fixture, then construct immutable typed view
// recipes and actual private ACLs. Public SQL witnesses live in reference only.
func querySetLockAuthorityFixture(t *testing.T) (*Engine, authorization.PrincipalRef, authorization.PrincipalRef, ids.ObjectRef, ids.ObjectRef, map[string]ids.ObjectRef) {
	t.Helper()
	e, query, source := queryCoreFixture(t, "SELECT v FROM t")
	base := source.ref
	owner := e.authorizationDomain.Principal(1000000)
	reader := e.authorizationDomain.Principal(1000001)
	roles := cloneRoleFixture(e.roles)
	for _, role := range []authorization.RoleDescriptor{{Ref: owner, Name: "set_owner"}, {Ref: reader, Name: "set_reader"}} {
		putFixtureRole(roles, role)
	}
	e.roles = roles
	e.catalog.state = cloneCatalog(e.catalog.state)
	original := e.catalog.state.MustRelation(base.Address)
	right := base
	right.Address.ObjectID = 1000000
	copy := *original
	copy.address, copy.name = right.Address, "t_right"
	e.catalog.state.setDescriptor(right.Address, &copy)
	refs := make(map[string]ids.ObjectRef)
	next := ids.OID(1000001)
	add := func(name string, invoker bool, root *projectNode, leaves []viewLeaf) ids.ObjectRef {
		ref := e.catalog.Domain.Ref(e.catalog.state.epoch, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: next})
		next++
		d := *original
		d.address, d.name, d.Owner, d.RelationKind = ref.Address, name, owner, viewRelation
		d.Columns, d.Attributes, d.ColumnACL = query.Root.Schema(), []int32{1}, nil
		d.View = &viewDefinition{query: &BoundQuery{Root: root}, sources: leaves, invoker: invoker}
		e.catalog.state.setDescriptor(ref.Address, &d)
		refs[name] = ref
		return ref
	}
	arm := func(i int) LogicalNode { return &sourceNode{occurrence: i} }
	setRoot := func() *projectNode {
		return &projectNode{syntax: &parser.Select{Set: &parser.SetQuery{}}, child: &setNode{left: &projectNode{child: arm(0)}, right: &projectNode{child: arm(1)}}}
	}
	leaves := []viewLeaf{{ref: base, reads: []int32{2}}, {ref: right, reads: []int32{2}}}
	add("owned", false, setRoot(), leaves)
	add("invoker", true, setRoot(), leaves)
	for _, name := range []string{"owned", "invoker"} {
		add(name+"_nested", false, &projectNode{syntax: &parser.Select{}, child: arm(0)}, []viewLeaf{{ref: refs[name], reads: []int32{1}}})
	}
	add("ordinary", true, &projectNode{syntax: &parser.Select{}, child: arm(0)}, leaves[:1])
	localMark := queryRowMark{mode: lock.ForUpdate, diagnosticMode: lock.ForUpdate, order: 1}
	local := add("local", true, &projectNode{syntax: &parser.Select{}, child: &rowLockNode{child: arm(0), firstMode: lock.ForUpdate, marks: []queryRowMark{localMark}}}, []viewLeaf{{ref: base, reads: []int32{2}, mark: localMark}})
	add("local_set", true, setRoot(), []viewLeaf{{ref: local, reads: []int32{1}}, {ref: right, reads: []int32{2}}})
	zero, err := sqltype.Integer(query.Root.Schema()[0].Usage.Type, 0)
	if err != nil {
		t.Fatal(err)
	}
	limited := setRoot()
	limited.child = &limitNode{child: limited.child, count: literalExpression{Info: expr.InfoOf(query.Root.(*projectNode).expressions[0]), value: zero}}
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
	check := func(name string, inherited queryRowMark, denied string) {
		t.Helper()
		auth := catalogAuthorization(e, e.CatalogRead())
		auth.state.CurrentUser = reader
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
		outer := queryRowMark{mode: mode}
		for _, name := range []string{"owned", "invoker", "owned_nested", "invoker_nested", "invoker_zero"} {
			check(name, outer, "")
		}
		check("ordinary", outer, "t")
		check("local_set", outer, "t")
	}
	// The local stored mark applies even with no inherited outer mark.
	check("local_set", queryRowMark{}, "t")
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
			check(name, queryRowMark{mode: lock.ForUpdate}, tc.name)
		}
		// Restore this arm before checking the other arm's independent denial.
		if err := e.SetObjectACL(context.Background(), tc.ref, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), Grantee: authorization.ACLGrantee{Role: reader}, Privileges: authorization.SelectPrivilege}}}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestQuerySetStoredReferenceKinds(t *testing.T) {
	e, _, _, base, _, refs := querySetLockAuthorityFixture(t)
	e.catalog.state = cloneCatalog(e.catalog.state)
	next := ids.OID(1000100)
	add := func(name string, definition *viewDefinition, kind relationKind) ids.ObjectRef {
		ref := e.catalog.Domain.Ref(e.catalog.state.epoch, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: next})
		next++
		d := *e.catalog.state.MustRelation(base.Address)
		d.address, d.name, d.View, d.RelationKind = ref.Address, name, viewPayloadOf(definition), kind
		e.catalog.state.setDescriptor(ref.Address, &d)
		return ref
	}
	sequence := add("kind_sequence", nil, sequenceRelation)
	catalog := add("kind_catalog", nil, catalogRelation)
	plainRoot := func() *projectNode {
		return &projectNode{syntax: &parser.Select{}, child: &sourceNode{occurrence: 0}}
	}
	setRoot := func() *projectNode {
		return &projectNode{syntax: &parser.Select{Set: &parser.SetQuery{}}, child: &limitNode{child: &sortNode{child: &evaluationNode{child: &filterNode{child: &setNode{left: plainRoot(), right: &projectNode{child: &sourceNode{occurrence: 1}}}}}}}}
	}
	set := add("kind_set", &viewDefinition{query: &BoundQuery{Root: setRoot()}, sources: []viewLeaf{{ref: sequence}, {ref: catalog}}}, viewRelation)
	nested := add("kind_nested", &viewDefinition{query: &BoundQuery{Root: plainRoot()}, sources: []viewLeaf{{ref: set}}}, viewRelation)
	ordinary := add("kind_ordinary", &viewDefinition{query: &BoundQuery{Root: plainRoot()}, sources: []viewLeaf{{ref: sequence}}}, viewRelation)
	localMark := queryRowMark{mode: lock.ForUpdate}
	localRoot := plainRoot()
	localRoot.child = &rowLockNode{child: &joinNode{left: &sourceNode{occurrence: 0}, right: &sourceNode{occurrence: 1}}, firstMode: lock.ForUpdate}
	// The sequence is unselected: only the local reference-kind context, not a
	// physical source mark, must reject this sibling beneath a nested set.
	local := add("kind_local", &viewDefinition{query: &BoundQuery{Root: localRoot}, sources: []viewLeaf{{ref: base, mark: localMark}, {ref: sequence}}}, viewRelation)
	localSet := add("kind_local_set", &viewDefinition{query: &BoundQuery{Root: setRoot()}, sources: []viewLeaf{{ref: local}, {ref: catalog}}}, viewRelation)
	localCatalog := add("kind_local_catalog", &viewDefinition{query: &BoundQuery{Root: plainRoot()}, sources: []viewLeaf{{ref: catalog, mark: localMark}}}, viewRelation)
	localCatalogSet := add("kind_local_catalog_set", &viewDefinition{query: &BoundQuery{Root: setRoot()}, sources: []viewLeaf{{ref: localCatalog}, {ref: base}}}, viewRelation)
	for _, tc := range []struct {
		ref           ids.ObjectRef
		mark          queryRowMark
		code, message string
	}{
		{set, localMark, "", ""},
		{nested, localMark, "", ""},
		// Kind-only outer context must also stop, even when this view's mark is 0.
		{set, queryRowMark{}, "", ""},
		{nested, queryRowMark{}, "", ""},
		{ordinary, queryRowMark{}, "42809", `cannot lock rows in sequence "kind_sequence"`},
		{localSet, queryRowMark{}, "42809", `cannot lock rows in sequence "kind_sequence"`},
		{localCatalogSet, queryRowMark{}, "0A000", "row locking is not supported for virtual catalog relations"},
	} {
		d := e.catalog.state.MustRelation(tc.ref.Address)
		scope := &StatementScope{owner: &transactionOwner{}}
		leaf := queryLeaf{ref: tc.ref, relation: d, mark: tc.mark, query: &parser.Select{Locking: []parser.RowLockClause{{}}}}
		e.mu.Lock()
		err := e.initializeQueryLocksLocked(scope, nil, []queryLeaf{leaf})
		e.mu.Unlock()
		if tc.code == "" {
			if err != nil {
				t.Fatal(d.name, err)
			}
		} else {
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code || diagnostic.Message != tc.message {
				t.Fatal(d.name, err)
			}
		}
	}
	// This predicate is about stored set output, not any encountered descendant.
	for _, root := range []LogicalNode{
		&projectNode{child: &joinNode{left: setRoot(), right: &sourceNode{}}},
		&projectNode{child: &derivedNode{query: &BoundQuery{Root: setRoot()}}},
		&projectNode{child: &distinctNode{child: setRoot()}},
		&projectNode{child: &aggregateNode{child: setRoot()}},
	} {
		if queryHasSetOutput(root) {
			t.Fatal("ordinary output acquired a set-entry kind exemption")
		}
	}
	// Every stored arm is still admitted, including one whose identity expired.
	d := *e.catalog.state.MustRelation(set.Address)
	definition := *viewOf(&d)
	definition.sources = append([]viewLeaf(nil), definition.sources...)
	definition.sources[1].ref.Epoch++
	d.View = &definition
	e.catalog.state.setDescriptor(set.Address, &d)
	scope := &StatementScope{owner: &transactionOwner{}}
	e.mu.Lock()
	err := e.initializeQueryLocksLocked(scope, nil, []queryLeaf{{ref: set, relation: &d, mark: localMark}})
	e.mu.Unlock()
	if !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("set boundary skipped stale arm admission", err)
	}
	// First-mode discovery still finds locally stored marks beneath a set when
	// there is no inherited outer diagnostic, without granting marks to arms.
	e.mu.Lock()
	mode, err := e.firstQueryRowModeLocked(&StatementScope{owner: &transactionOwner{}}, nil, []queryLeaf{{ref: refs["local_set"], relation: e.catalog.state.MustRelation(refs["local_set"].Address)}})
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
		scope.querySources = []queryLeaf{{ref: ref, relation: e.catalog.state.MustRelation(ref.Address), mark: queryRowMark{mode: lock.ForUpdate}}}
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
