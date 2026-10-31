package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestQueryWindowExcludeIdentityAndMetadata(t *testing.T) {
	query, source := enginetest.QueryFixture(t, engine.New(), "SELECT count(*) OVER w,sum(v) OVER w,sum(v) OVER w,sum(v+1) OVER w,sum(v::bigint) OVER w,row_number() OVER w FROM t WINDOW w AS(ORDER BY id ROWS CURRENT ROW EXCLUDE GROUP)")
	defer source.Close()
	node := query.Root.(*plan.ProjectNode).Child.(*plan.WindowNode)
	if len(node.Calls) != 6 || len(node.Specs) != 1 {
		t.Fatal(node)
	}
	for i, column := range query.Root.Schema() {
		if column.Nullable != (i >= 1 && i <= 4) || !column.NullabilityKnown || column.OriginTable != 0 {
			t.Fatal(i, column)
		}
	}
	if node.Calls[4].Expression.ResultUsage().Type.ID() != sqltype.Numeric {
		t.Fatal("bigint SUM result")
	}
	binder := &analyze.Binder{}
	a, b := node.Specs[0], node.Specs[0]
	b.Frame.Exclusion = parser.ExcludeTies
	if binder.SameWindowSpec(a, b) || !binder.SameWindowSort(a, b) || !binder.SameWindowCall(node.Calls[1], node.Calls[2]) {
		t.Fatal("semantic frame/call identity")
	}
	if node.Calls[1].Expression.Slot == node.Calls[2].Expression.Slot {
		t.Fatal("equal SUM targets shared execution ownership")
	}
	for _, other := range node.Calls[3:5] {
		if binder.SameWindowCall(node.Calls[1], other) {
			t.Fatal("distinct typed SUM arguments compared equal")
		}
	}
	left, right := node.Calls[1], node.Calls[2]
	left.Spec, right.Spec = a, b
	if binder.SameWindowCall(left, right) {
		t.Fatal("distinct exclusion modes compared equal")
	}
	left.Raw, right.Raw = &parser.WindowSpec{}, &parser.WindowSpec{}
	if !binder.SameWindowCall(left, right) {
		t.Fatal("identical unresolved SUM targets compared unequal")
	}
	for _, call := range node.Calls[1:5] {
		state, err := call.Provider.Init(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		value, err := call.Provider.Finalize(context.Background(), state)
		if err != nil || !value.IsNull() || value.Type() != call.Expression.ResultUsage().Type {
			t.Fatal(value, err)
		}
	}
}

func TestQueryWindowExcludeGraphRemap(t *testing.T) {
	query, source := enginetest.QueryFixture(t, engine.New(), "SELECT sum(v+1) OVER(ORDER BY id ROWS CURRENT ROW EXCLUDE TIES) FROM t")
	defer source.Close()
	node := query.Root.(*plan.ProjectNode).Child.(*plan.WindowNode)
	argument := node.Calls[0].Argument
	found := false
	plan.QueryExpressions(node, func(expr expr.Expr) { found = found || expr == argument })
	if !found {
		t.Fatal("argument absent from graph")
	}
	next, err := plan.RewriteQueryExpressions(node, func(expr expr.Expr) (expr.Expr, bool, error) {
		if expr == argument {
			return node.Specs[0].Order[0].Expression, true, nil
		}
		return expr, false, nil
	})
	if err != nil || next == node || next.(*plan.WindowNode).Calls[0].Argument == argument || node.Calls[0].Argument != argument {
		t.Fatal("immutable argument rewrite", next, err)
	}
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, typ := range sqltype.Builtins() {
		types[typ.ID()] = typ
	}
	mapped, err := stored.RemapViewQuery(engine.New().CatalogRead(), query.Root, func(ref ids.ObjectRef) ids.ObjectRef { ref.Address.ObjectID += 100; return ref }, func(old *sqltype.Descriptor) *sqltype.Descriptor { return types[old.ID()] })
	if err != nil {
		t.Fatal(err)
	}
	call := mapped.(*plan.ProjectNode).Child.(*plan.WindowNode).Calls[0]
	provider := call.Provider.(functions.IntegralSum)
	if call.Argument == argument || call.Argument.ResultUsage().Type != types[sqltype.Int4] || provider.InputType() != types[sqltype.Int4] || provider.ResultUsage().Type != types[sqltype.Int8] || call.Expression == node.Calls[0].Expression {
		t.Fatal("argument/provider ownership", call)
	}
}

func TestQueryWindowExcludePeers(t *testing.T) {
	query, source := enginetest.QueryFixture(t, engine.New(), "SELECT count(*) OVER(ORDER BY v DESC NULLS FIRST,id) FROM t")
	defer source.Close()
	keys := query.Root.(*plan.ProjectNode).Child.(*plan.WindowNode).Specs[0].Order
	typ := keys[0].Expression.ResultUsage().Type
	one, err := sqltype.Integer(typ, 1)
	if err != nil {
		t.Fatal(err)
	}
	two, err := sqltype.Integer(typ, 2)
	if err != nil {
		t.Fatal(err)
	}
	values := []sqltype.Value{sqltype.Null(typ), one, sqltype.Null(typ), one, sqltype.Null(typ), two, two, one, one, one}
	for _, tc := range []struct{ a, b, want int }{
		{0, 1, 0}, {0, 2, -1}, {2, 0, 1}, {0, 3, -1}, {3, 4, -1}, {4, 3, 1},
	} {
		got, err := exec.CompareWindowRows(context.Background(), keys, values, tc.a, tc.b, sqltype.InputEnvironment{})
		if err != nil || got != tc.want {
			t.Fatal(tc, got, err)
		}
	}
	got, err := exec.CompareWindowRows(context.Background(), nil, nil, 0, 4, sqltype.InputEnvironment{})
	if err != nil || got != 0 {
		t.Fatal("orderless partition must be peers", got, err)
	}
}

func TestQueryWindowExcludeLiteralCapture(t *testing.T) {
	sql := "SELECT sum(v+1) OVER(ROWS CURRENT ROW EXCLUDE TIES) FROM t"
	e := engine.New()
	query, source := enginetest.QueryFixture(t, e, sql)
	defer source.Close()
	op, done, err := enginetest.Operation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	binder := core.NewExpressionBinder(context.Background(), e, sqltype.InputEnvironment{}, op, nil)
	project := query.Root.(*plan.ProjectNode)
	window := project.Child.(*plan.WindowNode)
	binder.Grouping.WindowCalls = append([]plan.WindowCall(nil), window.Calls...)
	binder.Grouping.WindowCalls[0].Spec = window.Specs[0]
	raw := enginetest.Statement(t, sql).(*parser.Select).Targets[0]
	if err := binder.CaptureViewLiterals(raw, project.Expressions[0], analyze.ViewTargetLiteral, 0); err != nil {
		t.Fatal(err)
	}
	if len(binder.Capture.Literals) != 1 || len(binder.Capture.Columns) != 1 {
		t.Fatal("SUM argument children not captured", binder.Capture.Literals, binder.Capture.Columns)
	}
	for _, literal := range binder.Capture.Literals {
		value, err := literal.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
		n, integerErr := value.Integer()
		if err != nil || integerErr != nil || n != 1 {
			t.Fatal(value, err, integerErr)
		}
	}
}

func TestQueryWindowExcludeDomainRemap(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE DOMAIN exclude_arg AS INT")
	enginetest.Run(t, e, "CREATE TABLE t(id INT)")
	ast := enginetest.Statement(t, "SELECT sum(id::exclude_arg) OVER(ROWS CURRENT ROW EXCLUDE GROUP) FROM t")
	ref := enginetest.RoleTableRef(t, e, "t")
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	description, err := core.Describe(op.Ctx, e.AnalysisHost(op), ast, auth, ref, sqltype.InputEnvironment{}, nil, nil)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	root := description.Query.Root
	original := root.(*plan.ProjectNode).Child.(*plan.WindowNode)
	var domain *sqltype.Descriptor
	plan.WalkExpression(original.Calls[0].Argument, func(expr expr.Expr) {
		if expr.ResultUsage().Type.IsDomain() {
			domain = expr.ResultUsage().Type
		}
	})
	if domain == nil {
		t.Fatal("missing coerced SUM domain child")
	}
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, typ := range sqltype.Builtins() {
		types[typ.ID()] = typ
	}
	types[domain.ID()], err = sqltype.Domain(domain.ID(), domain.DiagnosticName(), domain.SQLName(), types[domain.DomainBase().Type.ID()].Usage())
	if err != nil {
		t.Fatal(err)
	}
	lookup := func(old *sqltype.Descriptor) *sqltype.Descriptor { return types[old.ID()] }
	mapped, err := stored.RemapViewQuery(engine.New().CatalogRead(), root, func(ref ids.ObjectRef) ids.ObjectRef { ref.Address.ObjectID += 100; return ref }, lookup)
	if err != nil {
		t.Fatal(err)
	}
	next := mapped.(*plan.ProjectNode).Child.(*plan.WindowNode)
	var mappedDomain *sqltype.Descriptor
	plan.WalkExpression(next.Calls[0].Argument, func(expr expr.Expr) {
		if expr.ResultUsage().Type.IsDomain() {
			mappedDomain = expr.ResultUsage().Type
		}
	})
	if mappedDomain != types[domain.ID()] || mappedDomain == domain || !analyze.QueryDescriptorsChanged(root, lookup) || analyze.QueryDescriptorsChanged(mapped, lookup) || next.Specs[0].Frame.Exclusion != parser.ExcludeGroup {
		t.Fatal("SUM domain ownership", domain, mappedDomain)
	}
}
