package enginetests_test

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestQueryLateralGraft(t *testing.T) {
	t.Run("nonlinear_occurrences_and_executable_descendants", lateralGraftOccurrences)
	t.Run("unchanged_capture_backing", lateralGraftUnchanged)
}

func lateralGraftCatalog(t *testing.T) (catalog.Read, []*catalog.Relation) {
	t.Helper()
	state := catalog.NewState(1)
	var integer *sqltype.Descriptor
	for _, typ := range sqltype.Builtins() {
		state.SetDescriptor(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.ID())}, &catalog.Type{SQLType: typ, Array: typ.AssociatedArray()})
		if typ.ID() == sqltype.Int4 {
			integer = typ
		}
	}
	if integer == nil {
		t.Fatal("missing integer type")
	}
	namespace := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 900}, ObjectName: "graft", ObjectRevision: 1}
	state.SetDescriptor(namespace.Address(), namespace)
	var relations []*catalog.Relation
	for i, name := range []string{"first", "second", "third"} {
		relation := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(901 + i)}, ObjectNamespace: namespace.Address(), ObjectName: name, ObjectRevision: 1, Columns: []catalog.Column{{Name: "v", Usage: integer.Usage()}}, Attributes: []int32{1}}
		state.SetDescriptor(relation.Address(), relation)
		relations = append(relations, relation)
	}
	return catalog.NewRead(&catalog.Owner{Domain: new(ids.Domain), Principals: new(authorization.Domain)}, state), relations
}

func lateralGraftOccurrences(t *testing.T) {
	read, relations := lateralGraftCatalog(t)
	integer, _ := read.LookupType(sqltype.Int4)
	oid, _ := read.LookupType(sqltype.Oid)
	boolean, _ := read.LookupType(sqltype.Bool)
	refs := make([]ids.ObjectRef, len(relations))
	leaves := make([]stored.ViewLeaf, len(relations))
	for i, relation := range relations {
		refs[i] = read.Domain().Ref(read.Epoch(), relation.Address())
		leaves[i] = stored.ViewLeaf{Ref: refs[i], Attributes: relation.Attributes, Reads: []int32{1}, SystemAttributeRead: true, SystemColumns: []int32{-6}}
	}
	system := func(frame uint32, occurrence int) plan.SystemAttributeExpr {
		return plan.SystemAttributeExpr{Info: expr.Info{Usage: oid.Usage()}, Owner: refs[occurrence-1], Scope: 1, Frame: frame, QueryOccurrence: occurrence, SubID: -6}
	}
	cast := func(child expr.Expr) plan.CoercionExpr {
		coercion, err := sqltype.PlanCoercion(child.ResultUsage(), integer.Usage(), sqltype.Explicit)
		if err != nil {
			t.Fatal(err)
		}
		return plan.CoercionExpr{Info: expr.Info{Usage: integer.Usage()}, Child: child, Plan: coercion, Mode: sqltype.Explicit}
	}
	capture := func(frame uint32, occurrence int) plan.FunctionCorrelation {
		return plan.FunctionCorrelation{Frame: frame, QueryOccurrence: occurrence, Name: "identity", Pos: parser.Position{Character: 17}}
	}
	captures := []plan.FunctionCorrelation{capture(10, 1), capture(10, 3), {Frame: 10, Slot: 2, Name: "ordinary"}, {Frame: 10, PresenceSlot: 7, Name: "presence"}, capture(99, 2)}
	functionCaptures := append([]plan.FunctionCorrelation(nil), captures...)
	functionCaptures = append(functionCaptures, capture(20, 2), plan.FunctionCorrelation{Frame: 20, Slot: 0}, plan.FunctionCorrelation{Frame: 20, PresenceSlot: 7})
	call := plan.SeriesExpr{Info: expr.Info{Usage: integer.Usage()}, Args: []expr.Expr{cast(system(10, 1)), cast(system(20, 2))}}
	function := &plan.FunctionFromNode{Frame: 20, Correlated: true, Captures: functionCaptures, Calls: []expr.Expr{call}, PresenceSlot: 8}
	sample := &plan.TableSample{Argument: cast(system(20, 2)), Captures: []plan.FunctionCorrelation{capture(20, 2), {Frame: 20, PresenceSlot: 7}}}
	localSource := &plan.SourceNode{Ref: refs[1], Occurrence: 1, Sample: sample, PresenceSlot: 7}
	predicate := plan.NullTestExpr{Info: expr.Info{Usage: boolean.Usage()}, Child: system(10, 3), Not: true}
	join := &plan.JoinNode{Frame: 20, Left: localSource, Right: function, Predicate: predicate, Merges: []expr.Expr{system(10, 1)}, Captures: captures, CorrelatedRight: true, CorrelationIdentityPrefix: true, PresenceSlot: 9}
	ordinary := plan.ColumnExpr{Info: expr.Info{Usage: integer.Usage()}, Origin: refs[0], Scope: 1, Frame: 10, Slot: 2, QueryOccurrence: 1}
	local := plan.ColumnExpr{Info: ordinary.Info, Origin: refs[1], Scope: 1, Frame: 20, Slot: 0}
	ownerRecipe := system(10, 1)
	ownerRecipe.Scope, ownerRecipe.Frame, ownerRecipe.QueryOccurrence = 0, 0, 0
	virtual := plan.ColumnExpr{Info: expr.Info{Usage: oid.Usage()}, Origin: refs[0], Scope: 1, Frame: 10, Slot: 2, QueryOccurrence: 1, Virtual: ownerRecipe, VirtualOwner: relations[0], VirtualAttribute: 1, RowWidth: 1}
	record, err := sqltype.RecordShape(nil)
	if err != nil {
		t.Fatal(err)
	}
	presence := plan.RowExpr{Info: expr.Info{Usage: record.Usage()}, PresenceFrame: 10, PresenceSlot: 7}
	localPresence := presence
	localPresence.PresenceFrame = 20
	scalarCaptures := []plan.FunctionCorrelation{capture(10, 1), capture(20, 2), capture(40, 2), {Frame: 40, Slot: 0}, {Frame: 40, PresenceSlot: 7}, capture(99, 2)}
	nestedCaptures := append(append([]plan.FunctionCorrelation(nil), scalarCaptures...), capture(50, 3))
	nestedCall := plan.SeriesExpr{Info: expr.Info{Usage: integer.Usage()}, Args: []expr.Expr{cast(system(10, 1)), cast(system(20, 2)), cast(system(40, 2)), cast(system(50, 3))}}
	nestedFunction := &plan.FunctionFromNode{Frame: 50, Captures: nestedCaptures, Calls: []expr.Expr{nestedCall}}
	nestedSource := &plan.SourceNode{Ref: refs[2], Occurrence: 2, PresenceSlot: 7}
	nestedJoin := &plan.JoinNode{Frame: 50, Left: nestedSource, Right: nestedFunction, Captures: nestedCaptures}
	nestedProject := &plan.ProjectNode{Frame: 50, Child: nestedJoin, Expressions: []expr.Expr{system(10, 1), system(20, 2), system(40, 2), system(50, 3), ordinary, presence, virtual, ownerRecipe, system(99, 2)}}
	nested := plan.SubqueryExpr{Info: expr.Info{Usage: oid.Usage()}, QueryPlan: &plan.Query{Root: nestedProject}}
	scalarSample := &plan.TableSample{Argument: cast(system(10, 1)), Captures: scalarCaptures}
	scalarSource := &plan.SourceNode{Ref: refs[1], Occurrence: 1, Sample: scalarSample, PresenceSlot: 7}
	scalarChild := &plan.ProjectNode{Frame: 41, Expressions: []expr.Expr{system(10, 1), system(20, 2), system(40, 2)}}
	scalarDerived := &plan.DerivedNode{ScanFrame: 40, Query: &plan.Query{Root: scalarChild}, Lateral: true, Captures: scalarCaptures}
	scalarJoin := &plan.JoinNode{Frame: 40, Left: scalarSource, Right: scalarDerived, Captures: scalarCaptures, CorrelatedRight: true}
	scalarProject := &plan.ProjectNode{Frame: 40, Child: scalarJoin, Expressions: []expr.Expr{system(10, 1), system(20, 2), system(40, 2), nested}}
	scalar := plan.SubqueryExpr{Info: expr.Info{Usage: oid.Usage()}, QueryPlan: &plan.Query{Root: scalarProject}}
	project := &plan.ProjectNode{Frame: 20, Child: join, Expressions: []expr.Expr{system(10, 1), system(10, 3), ordinary, local, system(99, 2), ownerRecipe, presence, localPresence, virtual, scalar}}
	derived := &plan.DerivedNode{ScanFrame: 10, Query: &plan.Query{Root: project}, Lateral: true, Captures: captures, PresenceSlot: 11}
	firstSource := &plan.SourceNode{Ref: refs[0], Occurrence: 0, PresenceSlot: 12}
	from := &plan.JoinNode{Frame: 10, Left: firstSource, Right: derived, CorrelatedRight: true}
	root := &plan.ProjectNode{Frame: 10, Child: from}
	definition := stored.NewView(stored.ViewParts{Query: &plan.Query{Root: root}, Sources: leaves})
	oldCaptures := append([]plan.FunctionCorrelation(nil), captures...)
	oldFunctionCaptures := append([]plan.FunctionCorrelation(nil), functionCaptures...)
	oldExpressions := append([]expr.Expr(nil), project.Expressions...)
	oldArguments := append([]expr.Expr(nil), call.Args...)
	oldSampleArgument := sample.Argument
	oldScalarCaptures := append([]plan.FunctionCorrelation(nil), scalarCaptures...)
	oldNestedCaptures := append([]plan.FunctionCorrelation(nil), nestedCaptures...)
	oldScalarExpressions := append([]expr.Expr(nil), scalarProject.Expressions...)
	oldNestedExpressions := append([]expr.Expr(nil), nestedProject.Expressions...)
	oldNestedArguments := append([]expr.Expr(nil), nestedCall.Args...)

	var importer view.StoredQueryImporter
	preceding := exec.Leaf{Ref: refs[1], Relation: relations[1], Uses: []int{0}}
	binding := analyze.Binding{QuerySources: []exec.Leaf{preceding}}
	identity := func(n expr.Expr) (expr.Expr, bool, error) { return n, false, nil }
	// Admit the last frozen occurrence first. The full import therefore cannot
	// translate identities by adding one constant prefix offset.
	seed := &plan.ProjectNode{Frame: 30, Child: &plan.SourceNode{Ref: refs[2], Occurrence: 2}}
	seeded, err := importer.GraftQuery(&binding, read, seed, definition, nil, identity)
	if err != nil {
		t.Fatal(err)
	}
	if seeded.(*plan.ProjectNode).Child.(*plan.SourceNode).Occurrence != 1 {
		t.Fatal("preimport did not follow the preceding source")
	}
	// Use the production expression adapter and its recursive graft callback,
	// rather than an identity rewriter which never imports scalar-local owners.
	rewritten, _, err := stored.RewriteViewExpression(read, plan.SubqueryExpr{Info: scalar.Info, QueryPlan: &plan.Query{Root: root}}, nil, identity, func(query plan.Node, rewrite func(expr.Expr) (expr.Expr, bool, error)) (plan.Node, error) {
		return importer.GraftQuery(&binding, read, query, definition, nil, rewrite)
	})
	if err != nil {
		t.Fatal(err)
	}
	mapped := rewritten.(plan.SubqueryExpr).QueryPlan.Root
	if len(binding.QuerySources) != 4 || !binding.SubqueryDemand || !reflect.DeepEqual(binding.QuerySources[0], preceding) {
		t.Fatal("import changed preceding source or duplicated occurrences", binding.QuerySources)
	}
	for slot, old := range []int{2, 0, 1} {
		leaf := binding.QuerySources[slot+1]
		if leaf.Ref != refs[old] || leaf.Relation != relations[old] || !reflect.DeepEqual(leaf.Uses, []int{0}) || !reflect.DeepEqual(leaf.FrozenAttributes, leaves[old].Attributes) || !leaf.SystemAttributeRead || !reflect.DeepEqual(leaf.SystemColumns, []int32{-6}) {
			t.Fatal("frozen source authority or demand changed", slot+1, leaf)
		}
	}
	occurrences := map[int]int{1: 3, 2: 4, 3: 2}
	checkCaptures := func(got, old []plan.FunctionCorrelation) {
		t.Helper()
		if len(got) != len(old) {
			t.Fatal("capture count changed", got)
		}
		for i, want := range old {
			if want.QueryOccurrence != 0 && (want.Frame == 10 || want.Frame == 20 || want.Frame == 40 || want.Frame == 50) {
				want.QueryOccurrence = occurrences[want.QueryOccurrence]
			}
			if got[i] != want {
				t.Fatalf("capture %d = %#v; want %#v", i, got[i], want)
			}
		}
	}
	checkSystem := func(got expr.Expr, frame uint32, old int) {
		t.Helper()
		want := system(frame, old)
		if frame == 10 || frame == 20 || frame == 40 || frame == 50 {
			want.QueryOccurrence = occurrences[old]
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("system leaf = %#v; want %#v", got, want)
		}
	}
	mappedFrom := mapped.(*plan.ProjectNode).Child.(*plan.JoinNode)
	mappedDerived := mappedFrom.Right.(*plan.DerivedNode)
	mappedProject := mappedDerived.Query.Root.(*plan.ProjectNode)
	mappedJoin := mappedProject.Child.(*plan.JoinNode)
	mappedFunction := mappedJoin.Right.(*plan.FunctionFromNode)
	mappedLocalSource := mappedJoin.Left.(*plan.SourceNode)
	checkCaptures(mappedDerived.Captures, captures)
	checkCaptures(mappedJoin.Captures, captures)
	checkCaptures(mappedFunction.Captures, functionCaptures)
	checkCaptures(mappedLocalSource.Sample.Captures, sample.Captures)
	checkSystem(mappedProject.Expressions[0], 10, 1)
	checkSystem(mappedProject.Expressions[1], 10, 3)
	checkSystem(mappedProject.Expressions[4], 99, 2)
	checkSystem(mappedJoin.Predicate.(plan.NullTestExpr).Child, 10, 3)
	checkSystem(mappedJoin.Merges[0], 10, 1)
	mappedCall := mappedFunction.Calls[0].(plan.SeriesExpr)
	checkSystem(mappedCall.Args[0].(plan.CoercionExpr).Child, 10, 1)
	checkSystem(mappedCall.Args[1].(plan.CoercionExpr).Child, 20, 2)
	checkSystem(mappedLocalSource.Sample.Argument.(plan.CoercionExpr).Child, 20, 2)
	if mappedDerived.Captures[0].QueryOccurrence != mappedProject.Expressions[0].(plan.SystemAttributeExpr).QueryOccurrence || mappedDerived.Captures[1].QueryOccurrence != mappedProject.Expressions[1].(plan.SystemAttributeExpr).QueryOccurrence {
		t.Fatal("derived capture no longer identifies its free leaf")
	}
	if mappedFrom.Left.(*plan.SourceNode).Occurrence != 2 || mappedFrom.Left.(*plan.SourceNode).Ref != refs[0] || mappedLocalSource.Occurrence != 3 || mappedLocalSource.Ref != refs[1] {
		t.Fatal("source and expression occurrences diverged")
	}
	mappedOrdinary := mappedProject.Expressions[2].(plan.ColumnExpr)
	wantOrdinary := ordinary
	wantOrdinary.QueryOccurrence = 3
	if !reflect.DeepEqual(mappedOrdinary, wantOrdinary) || !reflect.DeepEqual(mappedProject.Expressions[3], local) || !reflect.DeepEqual(mappedProject.Expressions[5], ownerRecipe) || !reflect.DeepEqual(mappedProject.Expressions[6], presence) || !reflect.DeepEqual(mappedProject.Expressions[7], localPresence) {
		t.Fatal("ordinary slot, owner recipe, local frame or presence identity rebased")
	}
	wantVirtual := virtual
	wantVirtual.QueryOccurrence = 3
	if !reflect.DeepEqual(mappedProject.Expressions[8], wantVirtual) || mappedProject.Expressions[8].(plan.ColumnExpr).VirtualOwner != virtual.VirtualOwner {
		t.Fatal("virtual owner recipe or authority changed")
	}
	if mapped.(*plan.ProjectNode).Frame != 10 || mappedProject.Frame != 20 || mappedDerived.ScanFrame != 10 || !mappedDerived.Lateral || mappedDerived.PresenceSlot != 11 || mappedJoin.Frame != 20 || mappedJoin.PresenceSlot != 9 || !mappedJoin.CorrelationIdentityPrefix || mappedFunction.Frame != 20 || mappedFunction.PresenceSlot != 8 || mappedLocalSource.PresenceSlot != 7 || mappedFrom.Left.(*plan.SourceNode).PresenceSlot != 12 {
		t.Fatal("lexical frame or presence metadata changed")
	}
	mappedScalar := mappedProject.Expressions[9].(plan.SubqueryExpr).QueryPlan.Root.(*plan.ProjectNode)
	mappedScalarJoin := mappedScalar.Child.(*plan.JoinNode)
	mappedScalarSource := mappedScalarJoin.Left.(*plan.SourceNode)
	mappedScalarDerived := mappedScalarJoin.Right.(*plan.DerivedNode)
	mappedScalarChild := mappedScalarDerived.Query.Root.(*plan.ProjectNode)
	mappedNested := mappedScalar.Expressions[3].(plan.SubqueryExpr).QueryPlan.Root.(*plan.ProjectNode)
	mappedNestedJoin := mappedNested.Child.(*plan.JoinNode)
	mappedNestedFunction := mappedNestedJoin.Right.(*plan.FunctionFromNode)
	mappedNestedCall := mappedNestedFunction.Calls[0].(plan.SeriesExpr)
	checkCaptures(mappedScalarJoin.Captures, scalarCaptures)
	checkCaptures(mappedScalarDerived.Captures, scalarCaptures)
	checkCaptures(mappedScalarSource.Sample.Captures, scalarCaptures)
	checkCaptures(mappedNestedJoin.Captures, nestedCaptures)
	checkCaptures(mappedNestedFunction.Captures, nestedCaptures)
	for _, expressions := range [][]expr.Expr{mappedScalar.Expressions, mappedScalarChild.Expressions, mappedNested.Expressions} {
		checkSystem(expressions[0], 10, 1)
		checkSystem(expressions[1], 20, 2)
		checkSystem(expressions[2], 40, 2)
	}
	checkSystem(mappedNested.Expressions[3], 50, 3)
	checkSystem(mappedNestedCall.Args[0].(plan.CoercionExpr).Child, 10, 1)
	checkSystem(mappedNestedCall.Args[1].(plan.CoercionExpr).Child, 20, 2)
	checkSystem(mappedNestedCall.Args[2].(plan.CoercionExpr).Child, 40, 2)
	checkSystem(mappedNestedCall.Args[3].(plan.CoercionExpr).Child, 50, 3)
	checkSystem(mappedScalarSource.Sample.Argument.(plan.CoercionExpr).Child, 10, 1)
	checkSystem(mappedNested.Expressions[8], 99, 2)
	if mappedScalarSource.Occurrence != 3 || mappedScalarSource.Ref != refs[1] || mappedNestedJoin.Left.(*plan.SourceNode).Occurrence != 1 || mappedNestedJoin.Left.(*plan.SourceNode).Ref != refs[2] {
		t.Fatal("scalar-local sources were skipped or mapped twice")
	}
	if mappedScalar.Frame != 40 || mappedScalarDerived.ScanFrame != 40 || mappedScalarChild.Frame != 41 || mappedNested.Frame != 50 || mappedNestedFunction.Frame != 50 || mappedScalarSource.PresenceSlot != 7 || mappedNestedJoin.Left.(*plan.SourceNode).PresenceSlot != 7 {
		t.Fatal("scalar lexical frame or presence identity changed")
	}
	if !reflect.DeepEqual(mappedNested.Expressions[4], wantOrdinary) || !reflect.DeepEqual(mappedNested.Expressions[5], presence) || !reflect.DeepEqual(mappedNested.Expressions[6], wantVirtual) || mappedNested.Expressions[6].(plan.ColumnExpr).VirtualOwner != virtual.VirtualOwner || !reflect.DeepEqual(mappedNested.Expressions[7], ownerRecipe) {
		t.Fatal("scalar ordinary slots, presence identity or virtual owner bounds changed")
	}
	if mappedDerived.Captures[0].QueryOccurrence != mappedNested.Expressions[0].(plan.SystemAttributeExpr).QueryOccurrence || mappedScalarDerived.Captures[2].QueryOccurrence != mappedScalarChild.Expressions[2].(plan.SystemAttributeExpr).QueryOccurrence || mappedNestedFunction.Captures[len(nestedCaptures)-1].QueryOccurrence != mappedNested.Expressions[3].(plan.SystemAttributeExpr).QueryOccurrence {
		t.Fatal("scalar capture metadata diverged from its owning executable leaf")
	}
	if scalar.QueryPlan.Root != scalarProject || scalarProject.Child != scalarJoin || scalarJoin.Right != scalarDerived || scalarDerived.Query.Root != scalarChild || scalarJoin.Left != scalarSource || scalarSource.Sample != scalarSample || nested.QueryPlan.Root != nestedProject || nestedProject.Child != nestedJoin || nestedJoin.Right != nestedFunction || nestedJoin.Left != nestedSource || scalarSource.Occurrence != 1 || nestedSource.Occurrence != 2 {
		t.Fatal("grafting mutated retained scalar graph edges or source identities")
	}
	if !reflect.DeepEqual(scalarProject.Expressions, oldScalarExpressions) || !reflect.DeepEqual(nestedProject.Expressions, oldNestedExpressions) || !reflect.DeepEqual(nestedFunction.Calls[0].(plan.SeriesExpr).Args, oldNestedArguments) || !reflect.DeepEqual(scalarCaptures, oldScalarCaptures) || !reflect.DeepEqual(nestedCaptures, oldNestedCaptures) {
		t.Fatal("grafting mutated retained scalar expressions or captures")
	}
	mappedScalarJoin.Captures[0].QueryOccurrence = 99
	mappedScalarDerived.Captures[1].QueryOccurrence = 99
	mappedScalarSource.Sample.Captures[2].QueryOccurrence = 99
	mappedNestedJoin.Captures[0].QueryOccurrence = 99
	mappedNestedFunction.Captures[len(nestedCaptures)-1].QueryOccurrence = 99
	if !reflect.DeepEqual(scalarCaptures, oldScalarCaptures) || !reflect.DeepEqual(nestedCaptures, oldNestedCaptures) {
		t.Fatal("translated scalar capture backing aliases the retained graph")
	}
	if root.Child != from || from.Right != derived || derived.Query.Root != project || project.Child != join || join.Right != function || join.Left != localSource || localSource.Sample != sample || firstSource.Occurrence != 0 || localSource.Occurrence != 1 {
		t.Fatal("grafting mutated retained graph edges or source identities")
	}
	if &derived.Captures[0] != &captures[0] || &join.Captures[0] != &captures[0] || &function.Captures[0] != &functionCaptures[0] || !reflect.DeepEqual(captures, oldCaptures) || !reflect.DeepEqual(functionCaptures, oldFunctionCaptures) {
		t.Fatal("grafting replaced retained capture metadata or backing")
	}
	if !reflect.DeepEqual(project.Expressions, oldExpressions) || !reflect.DeepEqual(function.Calls[0].(plan.SeriesExpr).Args, oldArguments) || !reflect.DeepEqual(sample.Argument, oldSampleArgument) || !reflect.DeepEqual(join.Predicate, predicate) {
		t.Fatal("grafting mutated retained expressions")
	}
	mappedDerived.Captures[0].QueryOccurrence = 99
	mappedJoin.Captures[1].QueryOccurrence = 99
	mappedFunction.Captures[0].QueryOccurrence = 99
	mappedLocalSource.Sample.Captures[0].QueryOccurrence = 99
	if !reflect.DeepEqual(captures, oldCaptures) || !reflect.DeepEqual(functionCaptures, oldFunctionCaptures) || sample.Captures[0].QueryOccurrence != 2 {
		t.Fatal("translated capture backing aliases the retained graph")
	}
}

func lateralGraftUnchanged(t *testing.T) {
	read, relations := lateralGraftCatalog(t)
	ref := read.Domain().Ref(read.Epoch(), relations[0].Address())
	captures := []plan.FunctionCorrelation{{Frame: 10, QueryOccurrence: 1}, {Frame: 10, Slot: 0}, {Frame: 10, PresenceSlot: 1}, {Frame: 99, QueryOccurrence: 1}}
	function := &plan.FunctionFromNode{Frame: 20, Captures: captures}
	project := &plan.ProjectNode{Frame: 20, Child: function}
	scalarProject := &plan.ProjectNode{Frame: 30, Child: &plan.FunctionFromNode{Frame: 30, Captures: captures}}
	scalar := plan.SubqueryExpr{QueryPlan: &plan.Query{Root: scalarProject}}
	project.Expressions = []expr.Expr{scalar}
	derived := &plan.DerivedNode{ScanFrame: 10, Lateral: true, Captures: captures, Query: &plan.Query{Root: project}}
	join := &plan.JoinNode{Frame: 10, Left: &plan.SourceNode{Ref: ref}, Right: derived, Captures: captures}
	root := &plan.ProjectNode{Frame: 10, Child: join}
	definition := stored.NewView(stored.ViewParts{Query: &plan.Query{Root: root}, Sources: []stored.ViewLeaf{{Ref: ref, Attributes: relations[0].Attributes}}})
	var importer view.StoredQueryImporter
	var binding analyze.Binding
	rewritten, changed, err := stored.RewriteViewExpression(read, plan.SubqueryExpr{QueryPlan: &plan.Query{Root: root}}, nil, func(n expr.Expr) (expr.Expr, bool, error) { return n, false, nil }, func(query plan.Node, rewrite func(expr.Expr) (expr.Expr, bool, error)) (plan.Node, error) {
		return importer.GraftQuery(&binding, read, query, definition, nil, rewrite)
	})
	if err != nil {
		t.Fatal(err)
	}
	mapped := rewritten.(plan.SubqueryExpr).QueryPlan.Root
	if changed || mapped != root || &derived.Captures[0] != &captures[0] || &join.Captures[0] != &captures[0] || &function.Captures[0] != &captures[0] || project.Expressions[0].(plan.SubqueryExpr).QueryPlan != scalar.QueryPlan || &scalarProject.Child.(*plan.FunctionFromNode).Captures[0] != &captures[0] {
		t.Fatal("identity import copied unchanged graph or capture backing")
	}
	if len(binding.QuerySources) != 1 || binding.QuerySources[0].Ref != ref || binding.QuerySources[0].Uses == nil || len(binding.QuerySources[0].Uses) != 0 {
		t.Fatal("presence-only occurrence gained full column demand", binding.QuerySources)
	}
}
