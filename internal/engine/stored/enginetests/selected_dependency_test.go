package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

// Keep the analyzed lifetime manifest as well as the graph: pg_depend and
// executable authority intentionally answer different questions.
func selectedDependencyBinding(t *testing.T, e *core.Engine, s *session.Session, sql string) *analyze.Binding {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := analyze.Describe(op.Context(), e.AnalysisHost(op), enginetest.Statement(t, sql), core.AuthorityOf(auth), ids.ObjectRef{}, sqltype.InputEnvironment{}, analyze.Expected{}, nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	return &binding
}

func selectedDependencySet(t *testing.T, got []ids.ObjectAddress, err error, want ...ids.ObjectAddress) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	expected := make(map[ids.ObjectAddress]bool)
	for _, address := range want {
		expected[address] = true
	}
	if len(got) != len(expected) {
		t.Fatalf("dependencies = %v, want exact %v", got, expected)
	}
	for _, address := range got {
		if !expected[address] {
			t.Fatalf("unexpected dependency %v in %v; want %v", address, got, expected)
		}
	}
}

func selectedDependencyProcedure(t *testing.T, node expr.Expr) *catalog.Procedure {
	t.Helper()
	p, err := exec.ExpressionRoutineDescriptor(node)
	if err != nil || p == nil {
		t.Fatalf("missing real selected procedure for %T: %v", node, err)
	}
	return p
}

func selectedDependencyManifest(t *testing.T, binding *analyze.Binding, p *catalog.Procedure) {
	t.Helper()
	for _, dep := range binding.Dependencies {
		if dep.Ref.Address == p.Address() && dep.Revision == p.Revision() {
			return
		}
	}
	t.Fatalf("backing procedure %v absent from independent executable manifest", p.Address())
}

func TestSelectedOperatorSQLDependencySets(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, sql := range []string{
		"CREATE EXTENSION ltree", "CREATE EXTENSION hstore", "CREATE EXTENSION citext",
		"CREATE TYPE sd_pair AS (i integer,n text)",
		"CREATE TABLE sd_owner(paths ltree[],a ltree,b ltree,q lquery,h hstore,k text,r sd_pair,c citext,d citext,h2 hstore,x ltxtquery)",
	} {
		enginetest.SessionSQL(t, s, sql)
	}
	view := e.CatalogRead()
	relation := view.MustRelation(view.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "sd_owner"}))
	column := func(slot int) ids.ObjectAddress {
		a := relation.Address()
		a.SubID = relation.Attributes[slot-1]
		return a
	}
	for _, tc := range []struct {
		name, operator, direct string
		columns                []int
	}{
		{"ltree_array", "paths @> a", "public._ltree_isparent(paths,a)", []int{1, 2}},
		{"ltree_path", "a || b", "public.ltree_addltree(a,b)", []int{2, 3}},
		{"ltree_query", "a ~ q", "public.ltq_regex(a,q)", []int{2, 4}},
		{"ltree_ltxtquery", "a @ x", "public.ltxtq_exec(a,x)", []int{2, 11}},
		{"ltree_ltxtquery_reverse", "x @ a", "public.ltxtq_rexec(x,a)", []int{11, 2}},
		{"ltree_hierarchy", "a @> b", "public.ltree_isparent(a,b)", []int{2, 3}},
		{"ltree_comparison", "a = b", "", []int{2, 3}},
		{"hstore_array", "%% h", "public.hstore_to_array(h)", []int{5}},
		{"hstore_construct", "h -> k", "public.fetchval(h,k)", []int{5, 6}},
		{"hstore_record", "r #= h", "public.populate_record(r,h)", []int{5, 7}},
		{"hstore_comparison", "h = h2", "public.hstore_eq(h,h2)", []int{5, 10}},
		{"citext_comparison", "c = d", "public.citext_eq(c,d)", []int{8, 9}},
		{"citext_pattern", "c LIKE d", "public.texticlike(c,d)", []int{8, 9}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			projection := tc.operator
			if tc.direct != "" {
				projection += "," + tc.direct
			}
			binding := selectedDependencyBinding(t, e, s, "SELECT "+projection+" FROM sd_owner")
			project := binding.Query.Root.(*plan.ProjectNode)
			selected, ok := project.Expressions[0].(plan.SelectedOperatorExpr)
			if !ok {
				t.Fatalf("not a selected operator: %T", project.Expressions[0])
			}
			backer := selectedDependencyProcedure(t, selected.Provider)
			if tc.direct != "" {
				p := selectedDependencyProcedure(t, project.Expressions[1])
				if backer.Address() != p.Address() {
					t.Fatal("direct call and operator selected different procedures")
				}
			}
			selectedDependencyManifest(t, binding, backer)
			before := append([]catalog.ExpressionDependency(nil), binding.Dependencies...)
			var operands []ids.ObjectAddress
			for _, slot := range tc.columns {
				operands = append(operands, column(slot))
			}
			got, err := stored.ExpressionDependRefs(context.Background(), view, project.Expressions[0])
			selectedDependencySet(t, got, err, append(append([]ids.ObjectAddress(nil), operands...), selected.Ref.Address)...)
			queryWant := append(append([]ids.ObjectAddress(nil), operands...), selected.Ref.Address)
			if tc.direct != "" {
				p := selectedDependencyProcedure(t, project.Expressions[1])
				got, err = stored.ExpressionDependRefs(context.Background(), view, project.Expressions[1])
				selectedDependencySet(t, got, err, append(append([]ids.ObjectAddress(nil), operands...), p.Address())...)
				queryWant = append(queryWant, p.Address())
			}
			got, err = stored.QueryDependRefs(context.Background(), view, nil, binding.Query)
			selectedDependencySet(t, got, err, queryWant...)
			// Exercise the index definition reader with the same real analyzed key.
			if project.Expressions[0].ResultUsage().Type.ID() == sqltype.Bool {
				class, ok, err := view.DefaultIndexClass(403, project.Expressions[0].ResultUsage().Type)
				if err != nil || !ok {
					t.Fatalf("missing real btree class: %v", err)
				}
				definition := &catalog.IndexDefinition{Keys: []catalog.IndexKey{{Derived: &catalog.DerivedIndexKey{Definition: project.Expressions[0]}, Class: class}}}
				indexWant := append(append([]ids.ObjectAddress(nil), operands...), selected.Ref.Address)
				classAddress := ids.ObjectAddress{ClassID: ids.OperatorClassCatalogID, ObjectID: ids.OID(class.OID())}
				if !stored.DependPinned(view, nil, classAddress) {
					indexWant = append(indexWant, classAddress)
				}
				got, err = stored.IndexDependRefs(context.Background(), view, definition)
				selectedDependencySet(t, got, err, indexWant...)
			}
			if !reflect.DeepEqual(before, binding.Dependencies) {
				t.Fatal("SQL dependency traversal changed lifetime manifest")
			}
		})
	}
}

func TestSelectedDependencyNestedCallsAndNull(t *testing.T) {
	e := engine.New()
	s := ltreeArrayOwnershipSetup(t, e)
	view := e.CatalogRead()
	relation := view.MustRelation(view.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "la_owner"}))
	for _, tc := range []struct {
		name, sql string
		columns   []int
	}{
		{"array", "SELECT paths @> subpath(target,0),subpath(target,0) FROM la_owner", []int{1, 3}},
		{"path", "SELECT subpath(target,0) || target,subpath(target,0) FROM la_owner", []int{3}},
		{"query", "SELECT subpath(target,0) ~ q,subpath(target,0) FROM la_owner", []int{3, 4}},
		{"ltxtquery", "SELECT subpath(target,0) @ 'Top'::ltxtquery,subpath(target,0) FROM la_owner", []int{3}},
		{"ltxtquery_reverse", "SELECT 'Top'::ltxtquery @ subpath(target,0),subpath(target,0) FROM la_owner", []int{3}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			binding := selectedDependencyBinding(t, e, s, tc.sql)
			project := binding.Query.Root.(*plan.ProjectNode)
			selected := project.Expressions[0].(plan.SelectedOperatorExpr)
			p := selectedDependencyProcedure(t, project.Expressions[1])
			want := []ids.ObjectAddress{selected.Ref.Address, p.Address()}
			if tc.name == "ltxtquery" || tc.name == "ltxtquery_reverse" {
				want = append(want, view.NameAddress(ids.NameKey{ClassID: ids.TypeClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "ltxtquery"}))
			}
			for _, slot := range tc.columns {
				address := relation.Address()
				address.SubID = relation.Attributes[slot-1]
				want = append(want, address)
			}
			got, err := stored.ExpressionDependRefs(context.Background(), view, selected)
			selectedDependencySet(t, got, err, want...)
			selectedDependencyManifest(t, binding, p)
			selectedDependencyManifest(t, binding, selectedDependencyProcedure(t, selected.Provider))
		})
	}
	for _, tc := range []struct {
		name, sql string
		types     []string
	}{
		{"array_null", "SELECT public._ltree_isparent(NULL::ltree[],NULL::ltree),NULL::ltree[] @> NULL::ltree", []string{"_ltree", "ltree"}},
		{"path_null", "SELECT public.ltree_addltree(NULL::ltree,NULL::ltree),NULL::ltree || NULL::ltree", []string{"ltree"}},
		{"query_null", "SELECT public.ltq_regex(NULL::ltree,NULL::lquery),NULL::ltree ~ NULL::lquery", []string{"ltree", "lquery"}},
		{"ltxtquery_null", "SELECT public.ltxtq_exec(NULL::ltree,NULL::ltxtquery),NULL::ltree @ NULL::ltxtquery", []string{"ltree", "ltxtquery"}},
		{"ltxtquery_reverse_null", "SELECT public.ltxtq_rexec(NULL::ltxtquery,NULL::ltree),NULL::ltxtquery @ NULL::ltree", []string{"ltxtquery", "ltree"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			binding := selectedDependencyBinding(t, e, s, tc.sql)
			project := binding.Query.Root.(*plan.ProjectNode)
			p := selectedDependencyProcedure(t, project.Expressions[0])
			selected := project.Expressions[1].(plan.SelectedOperatorExpr)
			var types []ids.ObjectAddress
			for _, name := range tc.types {
				address := view.NameAddress(ids.NameKey{ClassID: ids.TypeClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: name})
				if address.ObjectID == 0 {
					t.Fatalf("missing installed type %s", name)
				}
				types = append(types, address)
			}
			got, err := stored.ExpressionDependRefs(context.Background(), view, project.Expressions[0])
			selectedDependencySet(t, got, err, append(append([]ids.ObjectAddress(nil), types...), p.Address())...)
			got, err = stored.ExpressionDependRefs(context.Background(), view, selected)
			selectedDependencySet(t, got, err, append(types, selected.Ref.Address)...)
			selectedDependencyManifest(t, binding, p)
		})
	}
}

func TestSelectedDependencyCollectorFailures(t *testing.T) {
	e := engine.New()
	s := ltreeArrayOwnershipSetup(t, e)
	binding := ltreeArrayOwnershipBinding(t, e, s, false)
	node := binding.Expressions[0].(plan.SelectedOperatorExpr)
	view := e.CatalogRead()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := stored.ExpressionDependRefs(ctx, view, node)
	if !errors.Is(err, context.Canceled) || got != nil {
		t.Fatalf("canceled collection published %v: %v", got, err)
	}
	got, err = stored.QueryDependRefs(ctx, view, nil, &plan.Query{Root: binding})
	if !errors.Is(err, context.Canceled) || got != nil {
		t.Fatalf("canceled query published %v: %v", got, err)
	}
	index := &catalog.IndexDefinition{Keys: []catalog.IndexKey{{Derived: &catalog.DerivedIndexKey{Definition: node}}}}
	got, err = stored.IndexDependRefs(ctx, view, index)
	if !errors.Is(err, context.Canceled) || got != nil {
		t.Fatalf("canceled index published %v: %v", got, err)
	}
	state := catalog.CloneState(view.State())
	state.RemoveDescriptor(node.Ref.Address)
	got, err = stored.ExpressionDependRefs(context.Background(), view.WithState(state), node)
	if err == nil || got != nil {
		t.Fatalf("missing operator published %v: %v", got, err)
	}
	// Missing genuine operands must still fail after accepting the operator.
	call := node.Provider.(functions.LtreeArray)
	column := call.Args[0].(plan.ColumnExpr).Origin.Address
	column.SubID = 0
	state = catalog.CloneState(view.State())
	state.RemoveDescriptor(column)
	got, err = stored.ExpressionDependRefs(context.Background(), view.WithState(state), node)
	if err == nil || got != nil {
		t.Fatalf("missing operand published %v: %v", got, err)
	}
	direct := ltreeArrayOwnershipBinding(t, e, s, true).Expressions[0]
	procedure := selectedDependencyProcedure(t, direct)
	state = catalog.CloneState(view.State())
	state.RemoveDescriptor(procedure.Address())
	got, err = stored.ExpressionDependRefs(context.Background(), view.WithState(state), direct)
	if err == nil || got != nil {
		t.Fatalf("missing direct procedure published %v: %v", got, err)
	}
	failure := errors.New("prior dependency failure")
	refs := stored.DependRefs{Context: context.Background(), Catalog: view, Err: failure}
	refs.Expression(node)
	got, err = refs.Finish()
	if !errors.Is(err, failure) || got != nil {
		t.Fatal("collector replaced prior error or published partial set")
	}
}

func TestInstalledCarrierSQLDependencySets(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, sql := range []string{
		"CREATE EXTENSION hstore", "CREATE EXTENSION citext",
		"CREATE TYPE sd_direct_pair AS (i integer,n text)",
		"CREATE TABLE sd_direct(h hstore,k text,w text,r sd_direct_pair,c citext,d citext)",
	} {
		enginetest.SessionSQL(t, s, sql)
	}
	view := e.CatalogRead()
	relation := view.MustRelation(view.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "sd_direct"}))
	for _, tc := range []struct {
		name, sql string
		carrier   any
		columns   []int
	}{
		{"hstore_array", "hstore_to_array(h)", functions.HstoreArray{}, []int{1}},
		{"hstore_json", "hstore_to_json(h)", functions.HstoreJSON{}, []int{1}},
		{"hstore_jsonb", "hstore_to_jsonb(h)", functions.HstoreJSON{}, []int{1}},
		{"hstore_construct", "hstore(k,w)", functions.HstoreConstruct{}, []int{2, 3}},
		{"hstore_record", "hstore(r)", functions.HstoreRecord{}, []int{4}},
		{"hstore_rows", "skeys(h)", functions.HstoreRows{}, []int{1}},
		{"citext_string", "strpos(c,d)", functions.String{}, []int{5, 6}},
		{"citext_regexp", "regexp_match(c,d)", functions.Regexp{}, []int{5, 6}},
		{"citext_regexp_set", "regexp_matches(c,d)", functions.RegexpSet{}, []int{5, 6}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			binding := selectedDependencyBinding(t, e, s, "SELECT "+tc.sql+" FROM sd_direct")
			var found expr.Expr
			var expression func(expr.Expr)
			expression = func(node expr.Expr) {
				if reflect.TypeOf(node) == reflect.TypeOf(tc.carrier) {
					if found != nil && !reflect.DeepEqual(found, node) {
						t.Fatal("different selected carriers in one-call binding")
					}
					found = node
				}
				plan.ExpressionChildren(node, expression)
			}
			var query func(plan.Node)
			query = func(node plan.Node) {
				if node == nil {
					return
				}
				plan.QueryExpressions(node, expression)
				left, right := plan.QueryInputs(node)
				query(left)
				query(right)
			}
			query(binding.Query.Root)
			if found == nil {
				t.Fatalf("real analyzed %T absent", tc.carrier)
			}
			p := selectedDependencyProcedure(t, found)
			want := []ids.ObjectAddress{p.Address()}
			for _, slot := range tc.columns {
				address := relation.Address()
				address.SubID = relation.Attributes[slot-1]
				want = append(want, address)
			}
			got, err := stored.ExpressionDependRefs(context.Background(), view, found)
			selectedDependencySet(t, got, err, want...)
			got, err = stored.QueryDependRefs(context.Background(), view, nil, binding.Query)
			selectedDependencySet(t, got, err, want...)
			selectedDependencyManifest(t, binding, p)
		})
	}
}

func TestSelectedDependencyNestedHstoreCalls(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE EXTENSION hstore")
	enginetest.SessionSQL(t, s, "CREATE TABLE sd_nested(k text,w text)")
	view := e.CatalogRead()
	relation := view.MustRelation(view.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "sd_nested"}))
	binding := selectedDependencyBinding(t, e, s, "SELECT hstore(k,w) || hstore(w,k),hstore(k,w) FROM sd_nested")
	project := binding.Query.Root.(*plan.ProjectNode)
	selected := project.Expressions[0].(plan.SelectedOperatorExpr)
	constructor := selectedDependencyProcedure(t, project.Expressions[1])
	backer := selectedDependencyProcedure(t, selected.Provider)
	if constructor.Address() == backer.Address() {
		t.Fatal("nested witness does not distinguish explicit constructor from implicit concat")
	}
	want := []ids.ObjectAddress{selected.Ref.Address, constructor.Address()}
	for _, attribute := range relation.Attributes {
		address := relation.Address()
		address.SubID = attribute
		want = append(want, address)
	}
	got, err := stored.ExpressionDependRefs(context.Background(), view, selected)
	selectedDependencySet(t, got, err, want...)
	selectedDependencyManifest(t, binding, constructor)
	selectedDependencyManifest(t, binding, backer)
}
